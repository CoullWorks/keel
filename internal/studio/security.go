package studio

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coullworks/keel/internal/project"
	"github.com/coullworks/keel/internal/resolver"
)

// tokenHeader carries the studio's per-process session token. It is a custom
// header on purpose: a custom header makes a cross-origin request non-simple, so
// the browser must send a CORS preflight first — and the studio answers no
// preflight at all, so the real request is never sent.
const tokenHeader = "X-Keel-Token"

// tokenPlaceholder is substituted for the live session token when the UI is
// served, so the page can speak to the API and no other page can.
const tokenPlaceholder = "__KEEL_SESSION_TOKEN__"

// newToken returns a cryptographically random hex token. Go's crypto/rand does
// not fail on any supported platform (it panics internally if the OS source is
// broken), so there is no error path that could silently yield a weak token.
func newToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// loopbackOrigin reports whether an Origin header names a loopback host. The
// studio's own page is the only such origin in practice.
func loopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

// crossSite reports whether a request came from another site. Host checks alone
// do not catch this: a malicious page's request carries Host: localhost:7373 and
// passes loopbackOnly, which is why the studio needs the browser's own view of
// where the request came from.
func crossSite(r *http.Request) bool {
	// Fetch metadata is set by the browser and cannot be forged by page script.
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return false
	case "":
		// Older browser or a non-browser client: fall through to Origin.
	default:
		// "cross-site" and "same-site" are both foreign to us.
		return true
	}
	if o := r.Header.Get("Origin"); o != "" {
		return !loopbackOrigin(o)
	}
	return false
}

// guardAPI wraps an API handler with the studio's request guards: a loopback
// Host, a same-origin request, a session token no other page can read, and a
// method allowlist. Any one layer can be argued around; together they close the
// cross-site path to build, exec and SQL.
func guardAPI(tok string, methods []string, next http.HandlerFunc) http.HandlerFunc {
	allowed := make(map[string]bool, len(methods))
	for _, m := range methods {
		allowed[m] = true
	}
	allow := strings.Join(methods, ", ")
	return loopbackOnly(func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.Method] {
			w.Header().Set("Allow", allow)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if crossSite(r) {
			http.Error(w, "keel studio refuses cross-site requests", http.StatusForbidden)
			return
		}
		if subtle.ConstantTimeCompare([]byte(tok), []byte(r.Header.Get(tokenHeader))) != 1 {
			http.Error(w, "missing or invalid "+tokenHeader, http.StatusForbidden)
			return
		}
		next(w, r)
	})
}

// requireLoopbackAddr refuses to bind anywhere but loopback. The studio runs
// real builds, shells and SQL for whoever can reach it, and the Host guard is no
// defence against someone on the same network reaching a 0.0.0.0 bind directly.
func requireLoopbackAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("bad address %q: %w", addr, err)
	}
	switch host {
	case "127.0.0.1", "localhost", "::1", "":
		if host == "" {
			return fmt.Errorf("refusing to listen on every interface (%q): keel studio is loopback-only, use 127.0.0.1", addr)
		}
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("refusing to listen on %q: keel studio is loopback-only, use 127.0.0.1", addr)
}

// --- consent for untrusted (pack) recipes ------------------------------------

// consentGrant is a server-issued, single-use permit to run the untrusted
// recipes of one specific plan.
type consentGrant struct {
	plan    string // fingerprint of the resolved recipe ids
	expires time.Time
}

// consentStore holds outstanding grants. The studio never accepts "trusted" as
// a client-supplied boolean: the server decides a plan needs consent, streams
// back the exact commands it would run plus a grant, and only a user who saw
// that round trip can send the grant back. This mirrors the CLI's interactive
// gate rather than trusting a checkbox in the request body.
type consentStore struct {
	mu     sync.Mutex
	grants map[string]consentGrant
}

const consentTTL = 5 * time.Minute

var consents = &consentStore{grants: map[string]consentGrant{}}

func (c *consentStore) issue(plan string) string {
	tok := newToken()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sweepLocked()
	c.grants[tok] = consentGrant{plan: plan, expires: time.Now().Add(consentTTL)}
	return tok
}

// redeem consumes a grant. It must exist, be unexpired, and belong to this exact
// plan, so a grant issued for a harmless plan can never authorise another one.
func (c *consentStore) redeem(tok, plan string) bool {
	if tok == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	g, ok := c.grants[tok]
	delete(c.grants, tok) // single use, whether or not it matches
	return ok && time.Now().Before(g.expires) && g.plan == plan
}

func (c *consentStore) sweepLocked() {
	now := time.Now()
	for k, g := range c.grants {
		if now.After(g.expires) {
			delete(c.grants, k)
		}
	}
}

// planFingerprint identifies a resolved plan by its recipe ids, binding a
// consent grant to the plan the user was actually shown.
func planFingerprint(plan *resolver.Plan) string {
	h := sha256.New()
	for _, rc := range plan.Recipes {
		h.Write([]byte(rc.ID))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// --- path constraints --------------------------------------------------------

// safeProjectName validates a new project's directory name. It is joined onto
// the projects folder, so it must be a single plain segment: "../../.ssh" must
// never resolve outside the base.
func safeProjectName(name string) error {
	switch {
	case name == "":
		return nil // caller substitutes a default
	case strings.ContainsRune(name, '/'), strings.ContainsRune(name, '\\'):
		return fmt.Errorf("project name must not contain a path separator: %q", name)
	case name == "." || name == "..":
		return fmt.Errorf("invalid project name: %q", name)
	case strings.HasPrefix(name, "-"):
		return fmt.Errorf("project name must not start with a dash: %q", name)
	case filepath.Clean(name) != name:
		return fmt.Errorf("invalid project name: %q", name)
	}
	return nil
}

// projectDir resolves a directory the studio was asked to act inside, and
// refuses anything that is not a tracked project (or a member of one). Exec and
// SQL run real commands, so the target must be a directory the user already put
// under keel's management, not an arbitrary path from the request body.
func projectDir(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	abs, err := filepath.Abs(project.Expand(dir))
	if err != nil {
		return "", fmt.Errorf("bad directory: %w", err)
	}
	abs = filepath.Clean(abs)
	// The studio's own working directory is always addressable: it is where a
	// CLI-less user starts, and it is not attacker-chosen.
	if cwd, err := os.Getwd(); err == nil && filepath.Clean(cwd) == abs {
		return abs, nil
	}
	reg, err := project.Load()
	if err != nil {
		return "", fmt.Errorf("could not read the project list: %w", err)
	}
	for _, p := range reg.Projects {
		if filepath.Clean(p.Path) == abs {
			return abs, nil
		}
		for _, m := range project.Members(p.Path) {
			if filepath.Clean(m.Path) == abs {
				return abs, nil
			}
		}
	}
	return "", fmt.Errorf("not a tracked keel project: %s", abs)
}
