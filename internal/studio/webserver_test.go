package studio

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestRecipesExposeProvides: the page groups the web-server question by
// capability rather than by a hardcoded list of ids, so that a pack shipping its
// own web server asks the right question without the page knowing its name.
// That only works if the API sends provides.
func TestRecipesExposeProvides(t *testing.T) {
	mux := newMux("tok", nil)
	req := httptest.NewRequest("GET", "/api/recipes", nil)
	req.Host = "127.0.0.1:7777"
	req.Header.Set("X-Keel-Token", "tok")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		Recipes []struct {
			ID       string   `json:"id"`
			Default  bool     `json:"default"`
			Provides []string `json:"provides"`
		} `json:"recipes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	var webServers, defaulted int
	for _, r := range got.Recipes {
		for _, p := range r.Provides {
			if p == "webserver" {
				webServers++
				if r.Default {
					defaulted++
				}
			}
		}
	}
	if webServers < 2 {
		t.Errorf("want NGINX and Apache both offered, found %d web servers", webServers)
	}
	// Exactly one, or the studio pre-selects a pair that cannot coexist.
	if defaulted != 1 {
		t.Errorf("want exactly one web server selected by default, got %d", defaulted)
	}
}

// TestThePageAsksForAWebServer guards the studio half of CLI/studio parity.
// The CLI wizard grew a Web server step; if the page does not have the matching
// one, the two front doors stop being the same tool.
func TestThePageAsksForAWebServer(t *testing.T) {
	for _, want := range []string{
		`key:"web",title:"Web server"`,        // the step exists
		`provides||[]).includes("webserver")`, // chosen by capability, not by id
		`svcFor(fw)`,                          // and the Services list no longer offers them too
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("the build page is missing %q", want)
		}
	}
}

// TestThePageParses syntax-checks the studio's inline script.
//
// index.html carries one ~53KB inline <script>, and every Go test around it
// only greps for substrings - which a file with a syntax error passes happily
// while the page itself renders nothing at all. Editing that block is exactly
// the sort of change that can break it silently.
//
// Skipped where node is not installed, so it hardens a developer machine and CI
// without becoming a new dependency.
func TestThePageParses(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed: cannot syntax-check the inline script")
	}
	re := regexp.MustCompile(`(?s)<script(?:(?:[^>]*?))>(.*?)</script>`)
	var js strings.Builder
	for _, m := range re.FindAllStringSubmatch(indexHTML, -1) {
		if strings.Contains(m[0][:strings.Index(m[0], ">")], "src=") {
			continue // an external script has no body to check
		}
		js.WriteString(m[1])
		js.WriteString("\n;\n")
	}
	if js.Len() == 0 {
		t.Fatal("no inline script found: the extraction is wrong, not the page")
	}
	f := filepath.Join(t.TempDir(), "inline.js")
	if err := os.WriteFile(f, []byte(js.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, "--check", f).CombinedOutput(); err != nil {
		t.Errorf("the studio's inline script does not parse, so the page renders nothing:\n%s", out)
	}
}
