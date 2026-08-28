package studio

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/creds"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

// mutating lists every route that can change something on the developer's
// machine: a build, a shell command, SQL, or stored settings. These are the
// routes a cross-site request would aim at.
var mutating = []struct {
	path, method, body string
}{
	{"/api/build", "POST", `{"ids":["laravel","ddev","postgres"],"name":"x","run":true}`},
	{"/api/exec", "POST", `{"dir":".","args":["doctor"]}`},
	{"/api/db/query", "POST", `{"dir":".","sql":"SELECT 1"}`},
	{"/api/db/tables", "POST", `{"dir":"."}`},
	{"/api/projects", "POST", `{"path":"/tmp"}`},
	{"/api/projects", "DELETE", `{"path":"/tmp"}`},
	{"/api/profile", "POST", `{"name":"x"}`},
	{"/api/resolve", "POST", `{"ids":["laravel"]}`},
	// Installing a plugin reaches git and the filesystem, so it belongs in this
	// table rather than being treated as a harmless read.
	{"/api/plugins", "POST", `{"action":"add","source":"evil/repo"}`},
}

// TestCrossSitePostIsRefused is the regression test for the studio CSRF hole:
// any page the developer had open could fire a no-preflight text/plain POST at
// the studio and trigger a real build, shell command or SQL. The Host header
// looks local on such a request, so the loopback guard alone let it through.
func TestCrossSitePostIsRefused(t *testing.T) {
	isolateConfig(t)
	mux := testMux()
	for _, m := range mutating {
		// The exact shape of the attack: text/plain avoids a preflight, and the
		// browser sets a loopback Host for the attacker.
		req := httptest.NewRequest(m.method, "http://127.0.0.1"+m.path, strings.NewReader(m.body))
		req.Host = "127.0.0.1:7373"
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("Origin", "https://evil.example")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s %s from another site must be 403, got %d: %s", m.method, m.path, w.Code, w.Body.String())
		}
	}
}

// A cross-site request that guesses nothing but omits Sec-Fetch-Site (an older
// browser, or a hand-rolled client) is still refused: it has no session token.
func TestRequestWithoutTokenIsRefused(t *testing.T) {
	isolateConfig(t)
	mux := testMux()
	for _, m := range mutating {
		req := httptest.NewRequest(m.method, "http://127.0.0.1"+m.path, strings.NewReader(m.body))
		req.Host = "127.0.0.1:7373"
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s %s without a session token must be 403, got %d", m.method, m.path, w.Code)
		}
	}
}

func TestWrongTokenIsRefused(t *testing.T) {
	isolateConfig(t)
	req := httptest.NewRequest("POST", "http://127.0.0.1/api/exec", strings.NewReader(`{"dir":".","args":["doctor"]}`))
	req.Host = "127.0.0.1"
	req.Header.Set(tokenHeader, testTok+"x")
	w := httptest.NewRecorder()
	testMux().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("a wrong token must be 403, got %d", w.Code)
	}
}

// The UI's own request — same origin, correct token — still works. Without this
// the guards could "pass" by refusing everything.
func TestSameOriginWithTokenIsAllowed(t *testing.T) {
	isolateConfig(t)
	req := httptest.NewRequest("POST", "http://127.0.0.1/api/resolve", strings.NewReader(`{"ids":["laravel"]}`))
	req.Host = "127.0.0.1:7373"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://127.0.0.1:7373")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set(tokenHeader, testTok)
	w := httptest.NewRecorder()
	testMux().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("the studio's own request should be served, got %d: %s", w.Code, w.Body.String())
	}
}

func TestWrongMethodIsRefused(t *testing.T) {
	isolateConfig(t)
	// /api/build only accepts POST: a GET (or a form-shaped request) must not
	// reach the builder.
	req := httptest.NewRequest("GET", "http://127.0.0.1/api/build", nil)
	req.Host = "127.0.0.1"
	req.Header.Set(tokenHeader, testTok)
	w := httptest.NewRecorder()
	testMux().ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/build must be 405, got %d", w.Code)
	}
	if allow := w.Header().Get("Allow"); allow != "POST" {
		t.Fatalf("405 should advertise the allowed method, got %q", allow)
	}
}

// The served page carries the live token, never the placeholder.
func TestIndexCarriesSessionToken(t *testing.T) {
	w := muxGet(testMux(), "/")
	body := w.Body.String()
	if strings.Contains(body, tokenPlaceholder) {
		t.Fatalf("the served page still contains the token placeholder")
	}
	if !strings.Contains(body, testTok) {
		t.Fatalf("the served page should carry this session's token")
	}
}

func TestNewTokenIsRandomAndLongEnough(t *testing.T) {
	a, b := newToken(), newToken()
	if a == b {
		t.Fatalf("tokens must not repeat")
	}
	if len(a) != 64 {
		t.Fatalf("token should be 32 random bytes hex-encoded, got %d chars", len(a))
	}
}

// --- crossSite ---------------------------------------------------------------

func TestCrossSiteClassification(t *testing.T) {
	cases := []struct {
		name, fetchSite, origin string
		want                    bool
	}{
		{"same-origin fetch metadata", "same-origin", "", false},
		{"direct navigation", "none", "", false},
		{"cross-site fetch metadata", "cross-site", "", true},
		{"same-site is still foreign", "same-site", "", true},
		{"loopback origin, no metadata", "", "http://127.0.0.1:7373", false},
		{"localhost origin, no metadata", "", "http://localhost:7373", false},
		{"remote origin, no metadata", "", "https://evil.example", true},
		{"unparseable origin", "", "://nonsense", true},
		{"no headers at all (curl)", "", "", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "http://127.0.0.1/api/build", nil)
		if c.fetchSite != "" {
			r.Header.Set("Sec-Fetch-Site", c.fetchSite)
		}
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if got := crossSite(r); got != c.want {
			t.Fatalf("%s: crossSite = %v, want %v", c.name, got, c.want)
		}
	}
}

// --- bind address ------------------------------------------------------------

func TestRequireLoopbackAddr(t *testing.T) {
	for _, ok := range []string{"127.0.0.1:7373", "localhost:7373", "[::1]:7373", "127.0.0.2:7373"} {
		if err := requireLoopbackAddr(ok); err != nil {
			t.Fatalf("%s should be allowed: %v", ok, err)
		}
	}
	for _, bad := range []string{"0.0.0.0:7373", ":7373", "192.168.1.10:7373", "example.com:7373", "nonsense"} {
		if err := requireLoopbackAddr(bad); err == nil {
			t.Fatalf("%s should be refused", bad)
		}
	}
}

// Serve refuses a non-loopback bind before it ever listens, so --addr cannot
// expose builds and shells to the network.
func TestServeRefusesNonLoopbackAddr(t *testing.T) {
	err := Serve(io.Discard, "0.0.0.0:7373", false, true)
	if err == nil || !strings.Contains(err.Error(), "loopback-only") {
		t.Fatalf("Serve should refuse a public bind, got %v", err)
	}
}

// --- consent -----------------------------------------------------------------

func planOf(ids ...string) *resolver.Plan {
	p := &resolver.Plan{}
	for _, id := range ids {
		p.Recipes = append(p.Recipes, recipe.Recipe{ID: id})
	}
	return p
}

func TestConsentGrantIsSingleUseAndPlanBound(t *testing.T) {
	store := &consentStore{grants: map[string]consentGrant{}}
	planA := planFingerprint(planOf("laravel", "evil-pack-recipe"))
	planB := planFingerprint(planOf("laravel", "something-else"))

	g := store.issue(planA)
	if store.redeem(g, planB) {
		t.Fatalf("a grant must not authorise a different plan")
	}
	// That failed attempt also burned the grant, so it cannot be retried.
	if store.redeem(g, planA) {
		t.Fatalf("a grant must be consumed even by a failed redemption")
	}

	g2 := store.issue(planA)
	if !store.redeem(g2, planA) {
		t.Fatalf("a fresh grant for the right plan should be accepted")
	}
	if store.redeem(g2, planA) {
		t.Fatalf("a grant must not be reusable")
	}
	if store.redeem("", planA) || store.redeem("made-up", planA) {
		t.Fatalf("an invented grant must never be accepted")
	}
}

func TestConsentGrantExpires(t *testing.T) {
	store := &consentStore{grants: map[string]consentGrant{}}
	fp := planFingerprint(planOf("laravel"))
	g := store.issue(fp)
	store.mu.Lock()
	store.grants[g] = consentGrant{plan: fp, expires: time.Now().Add(-time.Second)}
	store.mu.Unlock()
	if store.redeem(g, fp) {
		t.Fatalf("an expired grant must be refused")
	}
}

func TestPlanFingerprintDistinguishesPlans(t *testing.T) {
	if planFingerprint(planOf("a", "b")) == planFingerprint(planOf("a", "c")) {
		t.Fatalf("different recipe sets must fingerprint differently")
	}
	if planFingerprint(planOf("a", "b")) != planFingerprint(planOf("a", "b")) {
		t.Fatalf("the same recipe set must fingerprint identically")
	}
	// Concatenation must not collide: {"ab"} and {"a","b"} are different plans.
	if planFingerprint(planOf("ab")) == planFingerprint(planOf("a", "b")) {
		t.Fatalf("recipe boundaries must be part of the fingerprint")
	}
}

// --- consent, end to end through handleBuild ---------------------------------

// installUntrustedPack drops a pack recipe (with a post_build hook) into the
// isolated config dir, so a plan using it resolves as untrusted exactly like a
// third-party pack a user fetched.
func installUntrustedPack(t *testing.T) {
	t.Helper()
	dir := filepath.Join(os.Getenv("KEEL_CONFIG_DIR"), "recipes", "evilpack")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keel.pack.yaml"), []byte("name: evilpack\nversion: 0.0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "" +
		"id: evil-extra\n" +
		"kind: extra\n" +
		"label: Untrusted extra\n" +
		"appliesTo: [\"*\"]\n" +
		"hooks:\n" +
		"  post_build:\n" +
		"    - run: echo pwned\n"
	if err := os.WriteFile(filepath.Join(dir, "extra.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// buildReq drives handleBuild and returns the SSE body.
func buildReq(t *testing.T, jsonBody string) string {
	t.Helper()
	w := httptest.NewRecorder()
	handleBuild(w, httptest.NewRequest("POST", "http://127.0.0.1/api/build", strings.NewReader(jsonBody)))
	return w.Body.String()
}

// TestUntrustedBuildRequiresServerIssuedConsent is the second half of the CSRF
// fix: the studio used to take "trust":true straight from the request body, so
// one forged POST could run a third-party pack's hooks. Consent is now something
// the server hands out after showing what would run.
func TestUntrustedBuildRequiresServerIssuedConsent(t *testing.T) {
	isolateConfig(t)
	installUntrustedPack(t)
	base := t.TempDir()
	t.Chdir(base) // builds land under the cwd; keep them in a temp dir

	// Block the target path with a file. Once consent IS accepted the build gets
	// as far as creating its directory and stops there, so the test proves the
	// gate opened without running a real scaffold.
	if err := os.WriteFile(filepath.Join(base, "consent-app"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := `{"ids":["nextjs","nextjs-local","evil-extra"],"name":"consent-app","run":true`

	// A client-supplied trust flag is not a thing any more: it is ignored, and
	// the server answers with a consent request instead of building.
	body := buildReq(t, plan+`,"trust":true}`)
	if !strings.Contains(body, "event: consent") {
		t.Fatalf("an untrusted plan must ask for consent, not build: %s", body)
	}
	if !strings.Contains(body, "untrusted pack") {
		t.Fatalf("the consent request should say why: %s", body)
	}
	if strings.Contains(body, "✓ done") {
		t.Fatalf("nothing may be built before consent: %s", body)
	}

	// An invented grant is refused the same way.
	if b := buildReq(t, plan+`,"consent":"made-up-grant"}`); !strings.Contains(b, "event: consent") {
		t.Fatalf("a forged grant must not authorise the build: %s", b)
	}

	// The grant the server issued does authorise this exact plan: the build is
	// attempted (and stops on the blocked directory) instead of asking again.
	grant := grantFrom(t, body)
	accepted := buildReq(t, plan+`,"consent":`+strconvQuote(grant)+`}`)
	if strings.Contains(accepted, "event: consent") {
		t.Fatalf("a server-issued grant should let the build proceed: %s", accepted)
	}
	if !strings.Contains(accepted, "keel build → ./consent-app") {
		t.Fatalf("the build should have started: %s", accepted)
	}
	// And it is spent: replaying it asks again.
	if b := buildReq(t, plan+`,"consent":`+strconvQuote(grant)+`}`); !strings.Contains(b, "event: consent") {
		t.Fatalf("a grant must not be replayable: %s", b)
	}
}

// A dry run executes nothing, so it needs no consent even for an untrusted plan.
func TestDryRunNeedsNoConsent(t *testing.T) {
	isolateConfig(t)
	installUntrustedPack(t)
	t.Chdir(t.TempDir())
	body := buildReq(t, `{"ids":["nextjs","nextjs-local","evil-extra"],"name":"preview-app","run":false}`)
	if strings.Contains(body, "event: consent") {
		t.Fatalf("a preview should not ask for consent: %s", body)
	}
}

// grantFrom pulls the grant out of a consent SSE frame.
func grantFrom(t *testing.T, sse string) string {
	t.Helper()
	for _, frame := range strings.Split(sse, "\n\n") {
		if !strings.HasPrefix(frame, "event: consent") {
			continue
		}
		_, data, ok := strings.Cut(frame, "data: ")
		if !ok {
			continue
		}
		var payload struct {
			Grant string `json:"grant"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			t.Fatalf("consent frame is not JSON: %v (%s)", err, data)
		}
		return payload.Grant
	}
	t.Fatalf("no consent frame in stream: %s", sse)
	return ""
}

// --- path constraints --------------------------------------------------------

func TestSafeProjectName(t *testing.T) {
	for _, ok := range []string{"", "myapp", "my-app", "my_app.v2"} {
		if err := safeProjectName(ok); err != nil {
			t.Fatalf("%q should be accepted: %v", ok, err)
		}
	}
	// The name is joined onto the projects folder, so anything that could climb
	// out of it, or be read as a flag, is refused.
	for _, bad := range []string{"../escape", "../../tmp/x", "a/b", `a\b`, ".", "..", "-rf", "./x"} {
		if err := safeProjectName(bad); err == nil {
			t.Fatalf("%q should be refused", bad)
		}
	}
}

func TestHandleBuildRefusesTraversingName(t *testing.T) {
	isolateConfig(t)
	w := httptest.NewRecorder()
	handleBuild(w, httptest.NewRequest("POST", "http://127.0.0.1/api/build",
		strings.NewReader(`{"ids":["laravel"],"name":"../../escaped","run":true}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a traversing project name must be a 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestProjectDirAcceptsTrackedRejectsOthers(t *testing.T) {
	isolateConfig(t)
	tracked := t.TempDir()
	trackProject(t, tracked)
	got, err := projectDir(tracked)
	if err != nil {
		t.Fatalf("a tracked project should be accepted: %v", err)
	}
	if got != filepath.Clean(tracked) {
		t.Fatalf("projectDir should return the cleaned absolute path, got %q", got)
	}
	if _, err := projectDir(t.TempDir()); err == nil {
		t.Fatalf("an untracked directory must be refused")
	}
	if _, err := projectDir(filepath.Join(tracked, "..")); err == nil {
		t.Fatalf("climbing out of a tracked project must be refused")
	}
	// The studio's own working directory is always addressable.
	cwd, _ := os.Getwd()
	if _, err := projectDir(""); err != nil {
		t.Fatalf("an empty dir should fall back to the cwd (%s): %v", cwd, err)
	}
}

// --- credentials -------------------------------------------------------------

// Credentials sent with a build are written where the environment reads them.
func TestWriteCredentialsPutsThemOnDisk(t *testing.T) {
	isolateConfig(t)
	dir := filepath.Join(t.TempDir(), "shop")
	const secret = "super-secret-private-key"

	reg, err := catalog.Registry()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := resolver.Resolve(reg, []string{"magento", "magento-ddev", "mariadb"})
	if err != nil {
		t.Fatal(err)
	}
	sw := &sseWriter{w: httptest.NewRecorder(), f: nopFlusher{}}
	err = writeCredentials(plan, dir, []creds.Value{
		{ID: "repo.magento.com", Kind: "composer", Username: "pub", Secret: secret, Remember: true},
		{ID: "OPENAI_API_KEY", Kind: "env", Secret: "sk-test"},
	}, sw)
	if err != nil {
		t.Fatal(err)
	}
	authPath := filepath.Join(dir, ".ddev", "homeadditions", ".composer", "auth.json")
	b, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("auth.json not written: %v", err)
	}
	if !strings.Contains(string(b), secret) {
		t.Fatal("auth.json should hold the credential")
	}
	if info, _ := os.Stat(authPath); info.Mode().Perm() != 0o600 {
		t.Errorf("auth.json mode = %o, want 600", info.Mode().Perm())
	}
	envPath := filepath.Join(dir, ".env")
	eb, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf(".env not written: %v", err)
	}
	if !strings.Contains(string(eb), "OPENAI_API_KEY=sk-test") {
		t.Errorf(".env missing the key: %s", eb)
	}
	if info, _ := os.Stat(envPath); info.Mode().Perm() != 0o600 {
		t.Errorf(".env mode = %o, want 600", info.Mode().Perm())
	}
	// "Remember" persisted it for next time, in its own 0600 file.
	store, err := creds.Load()
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := store.Get("repo.magento.com"); !ok || v.Secret != secret {
		t.Fatal("a credential marked remember should be saved")
	}
	if _, ok := store.Get("OPENAI_API_KEY"); ok {
		t.Fatal("a credential not marked remember must not be stored")
	}
}

// A credential must never come back out in the build stream: that pane is
// rendered in a browser and gets screenshotted and pasted into issues.
func TestBuildStreamNeverEchoesACredential(t *testing.T) {
	isolateConfig(t)
	base := t.TempDir()
	t.Chdir(base)
	// Block the target so the build stops early; the stream must still be clean.
	if err := os.WriteFile(filepath.Join(base, "shop"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	const secret = "super-secret-private-key"
	body := `{"ids":["magento","magento-ddev","mariadb"],"name":"shop","run":true,"credentials":[
		{"id":"repo.magento.com","kind":"composer","username":"pub","secret":"` + secret + `"}]}`
	w := httptest.NewRecorder()
	handleBuild(w, httptest.NewRequest("POST", "http://127.0.0.1/api/build", strings.NewReader(body)))
	if out := w.Body.String(); strings.Contains(out, secret) {
		t.Fatalf("the build stream echoed a credential back:\n%s", out)
	}
}

// A build that needs a credential and was sent none stops before running
// anything, naming what is missing.
func TestBuildStopsWhenARequiredCredentialIsMissing(t *testing.T) {
	isolateConfig(t)
	base := t.TempDir()
	t.Chdir(base)
	w := httptest.NewRecorder()
	handleBuild(w, httptest.NewRequest("POST", "http://127.0.0.1/api/build",
		strings.NewReader(`{"ids":["magento","magento-ddev","mariadb"],"name":"shop","run":true}`)))
	out := w.Body.String()
	if !strings.Contains(out, "repo.magento.com") {
		t.Fatalf("the missing credential should be named: %s", out)
	}
	if _, err := os.Stat(filepath.Join(base, "shop")); err == nil {
		t.Error("nothing should have been created")
	}
}

// The plan response tells the UI what to ask for, and never carries a value.
func TestResolveExposesCredentialsWithoutValues(t *testing.T) {
	isolateConfig(t)
	store, _ := creds.Load()
	store.Remember(creds.Value{ID: "repo.magento.com", Kind: "composer", Username: "u", Secret: "top-secret", Remember: true})
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handleResolve(w, httptest.NewRequest("POST", "http://127.0.0.1/api/resolve",
		strings.NewReader(`{"ids":["magento","magento-ddev","mariadb"]}`)))
	out := w.Body.String()
	if strings.Contains(out, "top-secret") {
		t.Fatalf("a stored secret must never cross the wire:\n%s", out)
	}
	for _, want := range []string{"repo.magento.com", `"required":true`, `"saved":true`, "GOOGLE_ANALYTICS_ID"} {
		if !strings.Contains(out, want) {
			t.Errorf("resolve response missing %q", want)
		}
	}
}

// nopFlusher stands in for the http.Flusher an SSE writer needs when a test
// drives one directly.
type nopFlusher struct{}

func (nopFlusher) Flush() {}
