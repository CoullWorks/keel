package studio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The studio's page and the studio's server are written in different languages
// and checked by different things: the handler tests call the API directly, and
// nothing at all read index.html. A URL the page asks for and the server does
// not serve was invisible to both.
//
// This is the same gap that let the console ship a menu of commands that did not
// run. It is closed the same way: read what the front end asks for, and check it
// against what the back end answers. It is not a browser test and does not
// pretend to be one; it proves the two halves agree about what exists.

// apiCall matches the /api paths index.html references, whether they are passed
// to the api()/apiJSON() helpers or written inline.
var apiCall = regexp.MustCompile("[\"'`](/api/[a-zA-Z0-9/_-]*)")

// pageEndpoints is every /api path index.html references.
func pageEndpoints(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	for _, m := range apiCall.FindAllStringSubmatch(indexHTML, -1) {
		seen[m[1]] = true
	}
	if len(seen) == 0 {
		t.Fatal("found no /api calls in index.html, so this test is checking nothing")
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Every endpoint the page calls is one the server serves.
func TestPageOnlyCallsEndpointsTheServerServes(t *testing.T) {
	isolateConfig(t)
	// This sweep GETs every endpoint the page names, including the GET-guarded
	// /api/deploy/targets, which shells `keel deploy --json`. Under test
	// os.Executable() is the test binary, so a real shell-out would re-run the
	// whole suite recursively (a 10-minute hang). Stub the runCapture seam — the
	// same seam console_test uses — so the sweep proves the route exists without
	// spawning a subprocess.
	restore := runCapture
	runCapture = func(_ context.Context, _ string, _ ...string) (string, error) {
		return `{"framework":"","targets":[]}`, nil
	}
	defer func() { runCapture = restore }()
	mux := testMux()
	for _, path := range pageEndpoints(t) {
		w := muxGet(mux, path)
		// 404 means the page asks for something that is not there. Anything
		// else - including 405 for a POST-only route and 400 for a missing
		// parameter - means the route exists and the request simply was not the
		// one it wanted, which is this test's limit.
		if w.Code == http.StatusNotFound {
			t.Errorf("index.html calls %s, which the server does not serve", path)
		}
	}
}

// Every endpoint the page calls is behind the session token.
//
// The page sends a per-session token on every call through its api() helper and
// the server refuses anything without one. A route added to the server but left
// out of that check is reachable from any other page the user has open, so the
// check is asserted across the whole surface rather than route by route.
func TestEveryPageEndpointRefusesACrossSitePost(t *testing.T) {
	isolateConfig(t)
	mux := testMux()
	for _, path := range pageEndpoints(t) {
		// What another page in the browser can send with no preflight: a
		// text/plain POST, cross-site, carrying no token.
		req := httptest.NewRequest("POST", "http://127.0.0.1"+path, strings.NewReader("{}"))
		req.Host = "127.0.0.1"
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("Origin", "https://evil.example")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code == http.StatusOK {
			t.Errorf("%s accepted a cross-site POST with no token", path)
		}
	}
}

// The page carries the token the server issued. Without the meta tag every call
// the page makes is refused, which is a blank studio rather than an error
// anybody can read.
func TestPageCarriesTheSessionToken(t *testing.T) {
	isolateConfig(t)
	body := muxGet(testMux(), "/").Body.String()
	if !strings.Contains(body, `name="keel-token"`) {
		t.Fatal("the page has no keel-token meta tag, so every API call it makes will be refused")
	}
	if !strings.Contains(body, testTok) {
		t.Error("the page carries a token that is not this session's")
	}
	if strings.Contains(body, tokenPlaceholder) {
		t.Error("the token placeholder was not substituted")
	}
}

// The studio never reaches a third party.
//
// Every framework and service option used to carry an <img> from a public icon
// CDN, so opening the build screen made about thirty requests that together
// spell out which stacks the developer is looking at. The sidebar says "zero
// telemetry" three lines from where those tags were built, and keel is a local
// tool that has to work with no network at all. One of the slugs had also been
// withdrawn from that CDN, so a 404 was logged in every session.
//
// github.com and coullworks.com are allowed: those are links a person clicks
// (the sponsor button, the "powered by COULLWORKS" credit), not resources the
// page loads. Nothing is fetched; the studio still works fully offline.
func TestPageLoadsNothingFromTheInternet(t *testing.T) {
	remote := regexp.MustCompile(`(?i)(src|href)\s*=\s*["']?(https?:)?//([a-z0-9.-]+)`)
	allowed := map[string]bool{"github.com": true, "coullworks.com": true}

	for _, m := range remote.FindAllStringSubmatch(indexHTML, -1) {
		attr, host := strings.ToLower(m[1]), strings.ToLower(m[3])
		if attr == "href" && allowed[host] {
			continue // a link, not a load
		}
		t.Errorf("index.html loads %s=%s from %s; the studio must work offline and tell nobody what you are building",
			attr, m[0], host)
	}
	// And nothing fetches across origins at runtime either.
	for _, host := range regexp.MustCompile(`(?i)fetch\(\s*["'](https?:)?//([a-z0-9.-]+)`).FindAllStringSubmatch(indexHTML, -1) {
		t.Errorf("the page fetches from %s at runtime", host[2])
	}
}
