package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/profile"
	"github.com/coullworks/keel/internal/project"
)

// isolateConfig points KEEL_CONFIG_DIR at a fresh temp dir so profile/project/
// pack state never touches the real user config. Returns the dir.
func isolateConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("KEEL_CONFIG_DIR", dir)
	return dir
}

// trackProject puts dir in the project registry. Exec and SQL only run inside
// tracked projects, so a test that drives them has to register its temp dir the
// way a real user would (keel new / keel adopt / Add in studio).
// Requires isolateConfig so the real registry is never touched.
func trackProject(t *testing.T, dir string) {
	t.Helper()
	reg, err := project.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Add(dir); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
}

// decodeJSON parses a recorder body into a generic map, failing on invalid JSON.
func decodeJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("body is not JSON object: %v\n%s", err, w.Body.String())
	}
	return m
}

// writeManifest drops a minimal .keel/manifest.yaml into dir so ReadManifest and
// the "is a keel project" checks succeed.
func writeManifest(t *testing.T, dir, env string, recipes []string) {
	t.Helper()
	kdir := filepath.Join(dir, ".keel")
	if err := os.MkdirAll(kdir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "framework: laravel\nenv: " + env + "\nrecipes:\n"
	for _, r := range recipes {
		body += "  - " + r + "\n"
	}
	if err := os.WriteFile(filepath.Join(kdir, "manifest.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- loopbackOnly ------------------------------------------------------------

// TestLoopbackAllowsLocalhosts pins loopbackOnly's REAL behaviour. It strips a
// port with a naive LastIndex(host, ":"), so:
//   - 127.0.0.1 / localhost (with or without a port) are allowed;
//   - a bracketed IPv6 host WITH a port ("[::1]:7373") survives the strip and is
//     allowed;
//   - a bare bracketed IPv6 host ("[::1]") is MANGLED to "[::" by the strip and
//     is therefore rejected. That is the loopback guard's actual behaviour, not a
//     bug this test masks — the studio always listens on 127.0.0.1 anyway.
func TestLoopbackAllowsLocalhosts(t *testing.T) {
	allowed := []string{"127.0.0.1", "127.0.0.1:7373", "localhost", "localhost:7373", "[::1]:7373"}
	for _, host := range allowed {
		req := httptest.NewRequest("GET", "http://x/api/recipes", nil)
		req.Host = host
		w := httptest.NewRecorder()
		called := false
		loopbackOnly(func(w http.ResponseWriter, r *http.Request) { called = true })(w, req)
		if !called || w.Code == http.StatusForbidden {
			t.Fatalf("host %q should be allowed (called=%v code=%d)", host, called, w.Code)
		}
	}
	// Bare "[::1]" is rejected by the naive port strip (mangled to "[::").
	req := httptest.NewRequest("GET", "http://x/api/recipes", nil)
	req.Host = "[::1]"
	w := httptest.NewRecorder()
	called := false
	loopbackOnly(func(w http.ResponseWriter, r *http.Request) { called = true })(w, req)
	if called || w.Code != http.StatusForbidden {
		t.Fatalf("bare %q is mangled by the port strip and should be rejected (called=%v code=%d)", "[::1]", called, w.Code)
	}
}

func TestLoopbackRejectsRemote(t *testing.T) {
	for _, host := range []string{"evil.com", "evil.com:80", "10.0.0.5:7373", "example.org"} {
		req := httptest.NewRequest("GET", "http://x/", nil)
		req.Host = host
		w := httptest.NewRecorder()
		called := false
		loopbackOnly(func(w http.ResponseWriter, r *http.Request) { called = true })(w, req)
		if called || w.Code != http.StatusForbidden {
			t.Fatalf("host %q should be rejected (called=%v code=%d)", host, called, w.Code)
		}
	}
}

// --- toDTO -------------------------------------------------------------------

func TestToDTODefaultsSourceToBuiltin(t *testing.T) {
	reg, err := catalog.Registry()
	if err != nil {
		t.Fatal(err)
	}
	rc, ok := reg.Get("laravel")
	if !ok {
		t.Fatal("expected a laravel recipe in the catalog")
	}
	rc.Source = ""
	if d := toDTO(rc); d.Source != "builtin" {
		t.Fatalf("empty source should default to builtin, got %q", d.Source)
	}
	if d := toDTO(rc); d.ID != "laravel" || d.Kind == "" {
		t.Fatalf("toDTO should copy core recipe fields: %+v", d)
	}
	rc.Source = "user"
	if d := toDTO(rc); d.Source != "user" {
		t.Fatalf("explicit source should be preserved, got %q", d.Source)
	}
}

// --- handleResolve bad body --------------------------------------------------

func TestHandleResolveBadBody(t *testing.T) {
	w := httptest.NewRecorder()
	handleResolve(w, httptest.NewRequest("POST", "http://127.0.0.1/api/resolve", strings.NewReader("not json")))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("malformed body should be a 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"error":"bad request"`) {
		t.Fatalf("malformed body should yield bad request error: %s", w.Body.String())
	}
}

// Malformed JSON on a mutating route is a 400, not a 200 with an {error} body.
// A 200 read as success to anything checking the status; keeping the JSON error
// body means the SPA still shows the message. This locks the consistency fix
// across a representative set of the routes that used to answer 200.
func TestMalformedJSONReturns400(t *testing.T) {
	isolateConfig(t)
	cases := map[string]http.HandlerFunc{
		"/api/resolve":   handleResolve,
		"/api/profile":   handleProfile,
		"/api/projects":  handleProjects,
		"/api/run/tasks": handleRunTasks,
	}
	for path, h := range cases {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			h(w, httptest.NewRequest("POST", "http://127.0.0.1"+path, strings.NewReader("}not json{")))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s: malformed JSON should be 400, got %d: %s", path, w.Code, w.Body.String())
			}
			if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
				t.Errorf("%s: 400 should still carry a JSON body, got Content-Type %q", path, ct)
			}
		})
	}
}

// --- handleProjects ----------------------------------------------------------

func TestHandleProjectsGETEmpty(t *testing.T) {
	isolateConfig(t)
	w := httptest.NewRecorder()
	handleProjects(w, httptest.NewRequest("GET", "http://127.0.0.1/api/projects", nil))
	m := decodeJSON(t, w)
	projects, ok := m["projects"]
	if !ok {
		t.Fatalf("GET should return a projects key: %s", w.Body.String())
	}
	// A fresh registry has no tracked projects (null or empty slice).
	if arr, isArr := projects.([]any); isArr && len(arr) != 0 {
		t.Fatalf("fresh registry should be empty, got %v", arr)
	}
}

func TestHandleProjectsPOSTAddsAndDELETEUntracks(t *testing.T) {
	isolateConfig(t)
	proj := t.TempDir()

	// POST a real directory -> it is added and echoed back.
	w := httptest.NewRecorder()
	body := `{"path":` + strconvQuote(proj) + `}`
	handleProjects(w, httptest.NewRequest("POST", "http://127.0.0.1/api/projects", strings.NewReader(body)))
	m := decodeJSON(t, w)
	added, ok := m["added"].(map[string]any)
	if !ok {
		t.Fatalf("POST should return the added project: %s", w.Body.String())
	}
	if added["path"] != proj {
		t.Fatalf("added path mismatch: got %v want %v", added["path"], proj)
	}

	// GET now lists it.
	wg := httptest.NewRecorder()
	handleProjects(wg, httptest.NewRequest("GET", "http://127.0.0.1/api/projects", nil))
	if !strings.Contains(wg.Body.String(), proj) {
		t.Fatalf("added project should appear in GET listing: %s", wg.Body.String())
	}

	// DELETE untracks it.
	wd := httptest.NewRecorder()
	handleProjects(wd, httptest.NewRequest("DELETE", "http://127.0.0.1/api/projects", strings.NewReader(body)))
	md := decodeJSON(t, wd)
	if md["removed"] != proj {
		t.Fatalf("DELETE should echo the removed path: %s", wd.Body.String())
	}

	// GET no longer lists it.
	wg2 := httptest.NewRecorder()
	handleProjects(wg2, httptest.NewRequest("GET", "http://127.0.0.1/api/projects", nil))
	if strings.Contains(wg2.Body.String(), proj) {
		t.Fatalf("untracked project should be gone: %s", wg2.Body.String())
	}
}

func TestHandleProjectsPOSTMissingPath(t *testing.T) {
	isolateConfig(t)
	w := httptest.NewRecorder()
	handleProjects(w, httptest.NewRequest("POST", "http://127.0.0.1/api/projects", strings.NewReader(`{"path":"  "}`)))
	if !strings.Contains(w.Body.String(), "a project path is required") {
		t.Fatalf("blank path should be rejected: %s", w.Body.String())
	}
}

func TestHandleProjectsPOSTBadBody(t *testing.T) {
	isolateConfig(t)
	w := httptest.NewRecorder()
	handleProjects(w, httptest.NewRequest("POST", "http://127.0.0.1/api/projects", strings.NewReader("nope")))
	if !strings.Contains(w.Body.String(), "a project path is required") {
		t.Fatalf("malformed body should be rejected: %s", w.Body.String())
	}
}

func TestHandleProjectsPOSTNonexistentDir(t *testing.T) {
	isolateConfig(t)
	w := httptest.NewRecorder()
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	handleProjects(w, httptest.NewRequest("POST", "http://127.0.0.1/api/projects", strings.NewReader(`{"path":`+strconvQuote(missing)+`}`)))
	if !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("adding a missing dir should error: %s", w.Body.String())
	}
}

// --- handleProfile -----------------------------------------------------------

func TestHandleProfileGETDefault(t *testing.T) {
	isolateConfig(t)
	w := httptest.NewRecorder()
	handleProfile(w, httptest.NewRequest("GET", "http://127.0.0.1/api/profile", nil))
	m := decodeJSON(t, w)
	if m["exists"] != false {
		t.Fatalf("no profile saved yet -> exists should be false: %s", w.Body.String())
	}
	if _, ok := m["hostingOptions"]; !ok {
		t.Fatalf("profile GET should include hostingOptions: %s", w.Body.String())
	}
}

func TestHandleProfilePOSTThenGET(t *testing.T) {
	isolateConfig(t)
	post := `{"Name":"Dan","ProjectsDir":"~/code","Hosting":"vercel","Framework":"laravel","Env":"ddev","Database":"postgres","Editor":"code","Frontend":"react","Services":"redis","Addons":"","Extras":""}`
	w := httptest.NewRecorder()
	handleProfile(w, httptest.NewRequest("POST", "http://127.0.0.1/api/profile", strings.NewReader(post)))
	if !strings.Contains(w.Body.String(), `"saved":true`) {
		t.Fatalf("POST should save the profile: %s", w.Body.String())
	}
	// GET reflects the saved values.
	wg := httptest.NewRecorder()
	handleProfile(wg, httptest.NewRequest("GET", "http://127.0.0.1/api/profile", nil))
	m := decodeJSON(t, wg)
	if m["exists"] != true {
		t.Fatalf("after save, exists should be true: %s", wg.Body.String())
	}
	if m["name"] != "Dan" || m["hosting"] != "vercel" || m["frontend"] != "react" {
		t.Fatalf("saved values not reflected: %s", wg.Body.String())
	}
}

// The web server has its own profile key, and the studio has to carry it in
// both directions. If the POST dropped it, the page's answer would never reach
// disk and `keel new` would read the profile as never having been asked - which
// is the state that built stacks with nothing listening on them.
func TestHandleProfileCarriesTheWebServerBothWays(t *testing.T) {
	for _, want := range []string{"apache", profile.NoWebServer} {
		t.Run(want, func(t *testing.T) {
			isolateConfig(t)
			post := `{"Name":"Dan","Framework":"laravel","Env":"ddev","Database":"postgres",` +
				`"Webserver":"` + want + `","Services":"redis"}`
			w := httptest.NewRecorder()
			handleProfile(w, httptest.NewRequest("POST", "http://127.0.0.1/api/profile", strings.NewReader(post)))
			if !strings.Contains(w.Body.String(), `"saved":true`) {
				t.Fatalf("POST should save the profile: %s", w.Body.String())
			}
			wg := httptest.NewRecorder()
			handleProfile(wg, httptest.NewRequest("GET", "http://127.0.0.1/api/profile", nil))
			if got := decodeJSON(t, wg)["webserver"]; got != want {
				t.Errorf("webserver = %v, want %q: %s", got, want, wg.Body.String())
			}
			// And it must not have been folded back into the services list,
			// where the two could disagree about what fronts a build.
			p, err := profile.Load()
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(p.Defaults["services"], "apache") {
				t.Errorf("services = %q, which should not carry the web server", p.Defaults["services"])
			}
		})
	}
}

func TestHandleProfilePOSTBadBody(t *testing.T) {
	isolateConfig(t)
	w := httptest.NewRecorder()
	handleProfile(w, httptest.NewRequest("POST", "http://127.0.0.1/api/profile", strings.NewReader("nope")))
	if !strings.Contains(w.Body.String(), `"error":"bad request"`) {
		t.Fatalf("malformed body should be rejected: %s", w.Body.String())
	}
}

// --- handleExec branches -----------------------------------------------------

func TestHandleExecBadBody(t *testing.T) {
	w := httptest.NewRecorder()
	handleExec(w, httptest.NewRequest("POST", "http://127.0.0.1/api/exec", strings.NewReader("{}")))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty args should be a 400, got %d", w.Code)
	}
}

func TestHandleExecCommandNotAllowed(t *testing.T) {
	w := httptest.NewRecorder()
	handleExec(w, httptest.NewRequest("POST", "http://127.0.0.1/api/exec", strings.NewReader(`{"dir":".","args":["rm","-rf"]}`)))
	body := w.Body.String()
	if !strings.Contains(body, "command not allowed: rm") || !strings.Contains(body, "event: done") {
		t.Fatalf("non-whitelisted command should be refused with a done frame: %s", body)
	}
}

func TestHandleExecNotAKeelProject(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir() // tracked, but no .keel/manifest.yaml
	trackProject(t, dir)
	w := httptest.NewRecorder()
	handleExec(w, httptest.NewRequest("POST", "http://127.0.0.1/api/exec", strings.NewReader(`{"dir":`+strconvQuote(dir)+`,"args":["db"]}`)))
	body := w.Body.String()
	if !strings.Contains(body, "not a keel project") || !strings.Contains(body, "event: done") {
		t.Fatalf("a project command outside a keel project should be refused: %s", body)
	}
}

func TestHandleExecStreamingUnsupported(t *testing.T) {
	w := newNoFlushWriter()
	handleExec(w, httptest.NewRequest("POST", "http://127.0.0.1/api/exec", strings.NewReader(`{"args":["doctor"]}`)))
	if w.rec.Code != http.StatusInternalServerError {
		t.Fatalf("a non-flushing writer should yield 500, got %d", w.rec.Code)
	}
	if !strings.Contains(w.rec.Body.String(), "streaming unsupported") {
		t.Fatalf("should return the streaming-unsupported message without running anything: %s", w.rec.Body.String())
	}
}

// --- handleBuild branches ----------------------------------------------------

func TestHandleBuildBadBody(t *testing.T) {
	w := httptest.NewRecorder()
	handleBuild(w, httptest.NewRequest("POST", "http://127.0.0.1/api/build", strings.NewReader("nope")))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("malformed body should be a 400, got %d", w.Code)
	}
}

func TestHandleBuildInvalidRecipesEmitsError(t *testing.T) {
	isolateConfig(t)
	w := httptest.NewRecorder()
	// An incompatible combo fails resolution -> error is emitted, then done.
	handleBuild(w, httptest.NewRequest("POST", "http://127.0.0.1/api/build", strings.NewReader(`{"ids":["laravel","magento"],"run":false}`)))
	body := w.Body.String()
	if !strings.Contains(body, "✗") || !strings.Contains(body, "event: done") {
		t.Fatalf("invalid plan should emit an error + done frame: %s", body)
	}
}

func TestHandleBuildDryRunStreams(t *testing.T) {
	isolateConfig(t)
	w := httptest.NewRecorder()
	// A valid plan, dry-run: streams the mode header and completes with done.
	// Dry-run changes nothing on disk and runs no real commands.
	handleBuild(w, httptest.NewRequest("POST", "http://127.0.0.1/api/build",
		strings.NewReader(`{"ids":["laravel","ddev","mysql"],"name":"demo-app","run":false}`)))
	body := w.Body.String()
	if !strings.Contains(body, "keel preview → ./demo-app") {
		t.Fatalf("dry-run should announce preview mode: %s", body)
	}
	if !strings.Contains(body, "event: done") {
		t.Fatalf("build should finish with a done frame: %s", body)
	}
}

func TestHandleBuildStreamingUnsupported(t *testing.T) {
	w := newNoFlushWriter()
	handleBuild(w, httptest.NewRequest("POST", "http://127.0.0.1/api/build", strings.NewReader(`{"ids":["laravel"]}`)))
	if w.rec.Code != http.StatusInternalServerError {
		t.Fatalf("a non-flushing writer should yield 500, got %d", w.rec.Code)
	}
	if !strings.Contains(w.rec.Body.String(), "streaming unsupported") {
		t.Fatalf("should return the streaming-unsupported message without running a build: %s", w.rec.Body.String())
	}
}

// --- handleDBTables ----------------------------------------------------------

func TestHandleDBTablesBadBody(t *testing.T) {
	w := httptest.NewRecorder()
	handleDBTables(w, httptest.NewRequest("POST", "http://127.0.0.1/api/db/tables", strings.NewReader("nope")))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("malformed body should be a 400, got %d", w.Code)
	}
}

func TestHandleDBTablesNotAKeelProject(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir() // tracked, but no manifest
	trackProject(t, dir)
	w := httptest.NewRecorder()
	handleDBTables(w, httptest.NewRequest("POST", "http://127.0.0.1/api/db/tables", strings.NewReader(`{"dir":`+strconvQuote(dir)+`}`)))
	m := decodeJSON(t, w)
	if err, _ := m["error"].(string); !strings.Contains(err, "not a keel project") {
		t.Fatalf("no manifest -> not a keel project error: %s", w.Body.String())
	}
	if _, ok := m["tables"]; !ok {
		t.Fatalf("response should still include an (empty) tables key: %s", w.Body.String())
	}
}

// An untracked directory is refused before any manifest is read: the studio only
// acts inside projects the user already put under keel's management.
func TestHandleDBTablesRefusesUntrackedDir(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "ddev", []string{"laravel", "postgres"})
	w := httptest.NewRecorder()
	handleDBTables(w, httptest.NewRequest("POST", "http://127.0.0.1/api/db/tables", strings.NewReader(`{"dir":`+strconvQuote(dir)+`}`)))
	m := decodeJSON(t, w)
	if err, _ := m["error"].(string); !strings.Contains(err, "not a tracked keel project") {
		t.Fatalf("an untracked dir must be refused even with a valid manifest: %s", w.Body.String())
	}
}

func TestHandleDBTablesDBUnreachableReturnsTablesKeyAndError(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "laravel-docker", []string{"laravel", "postgres"})
	trackProject(t, dir)
	// Simulate the database not being up: the handler must degrade to an empty
	// tables list with a connection error, never a not-a-keel-project error.
	restore := stubOpenDBErr(t, fmt.Errorf("dial tcp: connection refused"))
	defer restore()
	w := httptest.NewRecorder()
	handleDBTables(w, httptest.NewRequest("POST", "http://127.0.0.1/api/db/tables", strings.NewReader(`{"dir":`+strconvQuote(dir)+`}`)))
	m := decodeJSON(t, w)
	if _, ok := m["tables"]; !ok {
		t.Fatalf("valid project should return a tables key: %s", w.Body.String())
	}
	if err, _ := m["error"].(string); !strings.Contains(err, "could not connect") {
		t.Fatalf("an unreachable DB should surface a connect error: %s", w.Body.String())
	}
}

// --- handleDBQuery -----------------------------------------------------------

func TestHandleDBQueryBadBody(t *testing.T) {
	w := httptest.NewRecorder()
	handleDBQuery(w, httptest.NewRequest("POST", "http://127.0.0.1/api/db/query", strings.NewReader("nope")))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("malformed body should be a 400, got %d", w.Code)
	}
}

func TestHandleDBQueryEmptySQL(t *testing.T) {
	w := httptest.NewRecorder()
	handleDBQuery(w, httptest.NewRequest("POST", "http://127.0.0.1/api/db/query", strings.NewReader(`{"dir":".","sql":"   "}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty SQL should be a 400, got %d", w.Code)
	}
}

func TestHandleDBQueryNotAKeelProject(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir() // tracked, but no manifest
	trackProject(t, dir)
	w := httptest.NewRecorder()
	handleDBQuery(w, httptest.NewRequest("POST", "http://127.0.0.1/api/db/query", strings.NewReader(`{"dir":`+strconvQuote(dir)+`,"sql":"SELECT 1"}`)))
	body := w.Body.String()
	if !strings.Contains(body, "not a keel project") || !strings.Contains(body, "event: done") {
		t.Fatalf("no manifest -> not a keel project + done frame: %s", body)
	}
}

func TestHandleDBQueryStreamingUnsupported(t *testing.T) {
	w := newNoFlushWriter()
	handleDBQuery(w, httptest.NewRequest("POST", "http://127.0.0.1/api/db/query", strings.NewReader(`{"dir":".","sql":"SELECT 1"}`)))
	if w.rec.Code != http.StatusInternalServerError {
		t.Fatalf("a non-flushing writer should yield 500, got %d", w.rec.Code)
	}
	if !strings.Contains(w.rec.Body.String(), "streaming unsupported") {
		t.Fatalf("should return the streaming-unsupported message without querying: %s", w.rec.Body.String())
	}
}

func TestHandleDBTablesReturnsJSONNotStream(t *testing.T) {
	// handleDBTables does not require a flusher (it returns JSON), so a plain
	// recorder is enough — this guards against a regression that adds one. An
	// untracked dir is refused before any connection is attempted.
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "laravel-docker", []string{"laravel", "mysql"})
	w := httptest.NewRecorder()
	handleDBTables(w, httptest.NewRequest("POST", "http://127.0.0.1/api/db/tables", strings.NewReader(`{"dir":`+strconvQuote(dir)+`}`)))
	if _, ok := decodeJSON(t, w)["tables"]; !ok {
		t.Fatalf("response should return a tables key: %s", w.Body.String())
	}
}

// --- sseWriter ---------------------------------------------------------------

func TestSSEWriterFramesLines(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &sseWriter{w: rec, f: rec}
	// Two complete lines in one write; the trailing partial stays buffered.
	n, err := sw.Write([]byte("alpha\nbeta\npart"))
	if err != nil || n != len("alpha\nbeta\npart") {
		t.Fatalf("Write should report all bytes consumed: n=%d err=%v", n, err)
	}
	out := rec.Body.String()
	if !strings.Contains(out, "data: alpha\n\n") || !strings.Contains(out, "data: beta\n\n") {
		t.Fatalf("each complete line should be an SSE frame: %q", out)
	}
	if strings.Contains(out, "part") {
		t.Fatalf("a partial line should stay buffered, not emitted: %q", out)
	}
	// flushRemainder emits the buffered partial line.
	sw.flushRemainder()
	if !strings.Contains(rec.Body.String(), "data: part\n\n") {
		t.Fatalf("flushRemainder should emit the buffered tail: %q", rec.Body.String())
	}
	// A second flushRemainder with an empty buffer is a no-op.
	before := rec.Body.Len()
	sw.flushRemainder()
	if rec.Body.Len() != before {
		t.Fatalf("flushRemainder on an empty buffer should emit nothing")
	}
}

func TestSSEWriterStripsCarriageReturns(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &sseWriter{w: rec, f: rec}
	sw.emit([]byte("hello\rworld"))
	if got := rec.Body.String(); got != "data: helloworld\n\n" {
		t.Fatalf("emit should strip \\r: %q", got)
	}
}

func TestSSEWriterMultipleWritesReassemble(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &sseWriter{w: rec, f: rec}
	// A logical line split across writes is only emitted once its newline arrives.
	sw.Write([]byte("split "))
	if strings.Contains(rec.Body.String(), "data:") {
		t.Fatalf("no complete line yet -> nothing emitted")
	}
	sw.Write([]byte("line\n"))
	if !strings.Contains(rec.Body.String(), "data: split line\n\n") {
		t.Fatalf("the reassembled line should be emitted once complete: %q", rec.Body.String())
	}
}

// --- dbClientCmd -------------------------------------------------------------

func TestDBClientCmd(t *testing.T) {
	pg := &engine.Manifest{Recipes: []string{"laravel", "postgres"}}
	my := &engine.Manifest{Recipes: []string{"laravel", "mysql"}}

	cases := []struct {
		name, exec string
		m          *engine.Manifest
		want       string
	}{
		{"ddev postgres", "ddev exec", pg, "ddev psql -c 'SELECT 1'"},
		{"ddev mysql", "ddev exec", my, "ddev mysql -e 'SELECT 1'"},
		{"docker postgres", "docker compose exec -T app", pg, "docker compose exec -T app psql -U db -d db -c 'SELECT 1'"},
		{"docker mysql", "docker compose exec -T app", my, "docker compose exec -T app mysql -u db -pdb db -e 'SELECT 1'"},
	}
	for _, c := range cases {
		if got := dbClientCmd(c.exec, c.m, "SELECT 1"); got != c.want {
			t.Fatalf("%s: dbClientCmd = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDBClientCmdEscapesQuotes(t *testing.T) {
	m := &engine.Manifest{Recipes: []string{"mysql"}}
	got := dbClientCmd("ddev exec", m, "SELECT 'x'")
	if !strings.Contains(got, `'\''`) {
		t.Fatalf("single quotes in SQL should be shell-escaped: %q", got)
	}
}

// --- modeLabel ---------------------------------------------------------------

func TestModeLabel(t *testing.T) {
	if modeLabel(true) != "build" {
		t.Fatalf("run=true should be build")
	}
	if modeLabel(false) != "preview" {
		t.Fatalf("run=false should be preview")
	}
}

// --- writeJSON ---------------------------------------------------------------

func TestWriteJSONSetsContentType(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, map[string]int{"n": 1})
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("writeJSON should set a JSON content type, got %q", ct)
	}
	if strings.TrimSpace(w.Body.String()) != `{"n":1}` {
		t.Fatalf("writeJSON body wrong: %q", w.Body.String())
	}
}

// --- newMux routing (httptest only, no port bind) ----------------------------

// testTok is the session token the test mux is built with; requests that should
// succeed must present it exactly as the served UI does.
const testTok = "test-session-token"

// testMux builds the studio mux with a known session token and MCP auto-started,
// exactly as `keel studio` does by default, so the test surface exercises the
// live /mcp mount too.
func testMux() *http.ServeMux {
	o := mcpOptions()
	return newMux(testTok, &o)
}

// muxGet serves a loopback GET (Host 127.0.0.1, valid session token) through the
// studio mux and returns the recorder — the whole request path incl. the guards.
func muxGet(mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "http://127.0.0.1"+path, nil)
	req.Host = "127.0.0.1"
	req.Header.Set(tokenHeader, testTok)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestMuxServesIndexAtRoot(t *testing.T) {
	w := muxGet(testMux(), "/")
	if w.Code != http.StatusOK {
		t.Fatalf("root should be 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("root should serve HTML, got %q", ct)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("the UI must not be cached: %q", cc)
	}
	if w.Body.Len() == 0 {
		t.Fatalf("root should serve the embedded index.html")
	}
}

func TestMuxUnknownPathIs404(t *testing.T) {
	// "/" catches everything not matched by a more specific route; a non-root
	// path under it must 404 rather than serve the index.
	w := muxGet(testMux(), "/definitely-not-a-page")
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown path should 404, got %d", w.Code)
	}
}

func TestMuxServesFavicon(t *testing.T) {
	w := muxGet(testMux(), "/favicon.ico")
	if w.Code != http.StatusOK {
		t.Fatalf("favicon should be 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("favicon should be a PNG, got %q", ct)
	}
	if w.Body.Len() == 0 {
		t.Fatalf("favicon body should be the embedded PNG")
	}
}

func TestMuxServesEmbeddedAsset(t *testing.T) {
	w := muxGet(testMux(), "/assets/anchor.png")
	if w.Code != http.StatusOK {
		t.Fatalf("embedded asset should be 200, got %d", w.Code)
	}
	if w.Body.Len() == 0 {
		t.Fatalf("asset body should be the embedded PNG")
	}
}

func TestMuxRecipesRouteWorks(t *testing.T) {
	w := muxGet(testMux(), "/api/recipes")
	if w.Code != http.StatusOK {
		t.Fatalf("recipes route should be 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"recipes"`) {
		t.Fatalf("recipes route should return the recipe list: %s", w.Body.String())
	}
}

func TestMuxLoopbackGuardBlocksRemoteHost(t *testing.T) {
	// A remote Host is rejected at the mux level (every route is loopback-only).
	req := httptest.NewRequest("GET", "http://evil.com/api/recipes", nil)
	req.Host = "evil.com"
	req.Header.Set(tokenHeader, testTok) // a valid token must not rescue a remote Host
	w := httptest.NewRecorder()
	testMux().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("remote host should be 403 through the mux, got %d", w.Code)
	}
}

// --- Serve error path (no port bind, no blocking) ----------------------------

func TestServeListenError(t *testing.T) {
	// An unbindable address makes net.Listen fail, so Serve returns the wrapped
	// error immediately WITHOUT ever blocking in Serve(). open=false -> no browser.
	err := Serve(io.Discard, "256.256.256.256:99999", false, true)
	if err == nil {
		t.Fatalf("Serve should return an error for an unbindable address")
	}
	if !strings.Contains(err.Error(), "keel studio") {
		t.Fatalf("Serve should wrap the listen error with a keel studio prefix: %v", err)
	}
}

// TestServeAddressInUseIsClear: binding an address a studio already holds must
// say so plainly ("already in use — is a studio already running?") rather than
// leaking a raw "bind: address already in use" syscall string. A person reading
// it should know to stop the other studio or pick another port.
func TestServeAddressInUseIsClear(t *testing.T) {
	// Hold a loopback port so the next bind to it fails with EADDRINUSE.
	ln := loopbackListener(t)
	defer ln.Close()
	addr := ln.Addr().String()

	err := Serve(io.Discard, addr, false, true) // open=false -> no browser; noMCP=true
	if err == nil {
		t.Fatalf("Serve should fail when %s is already bound", addr)
	}
	msg := err.Error()
	if !strings.Contains(msg, "already in use") || !strings.Contains(msg, "already running") {
		t.Fatalf("busy-port error must be human-readable, got: %v", err)
	}
	if strings.Contains(msg, "bind:") {
		t.Errorf("busy-port error should not leak the raw syscall string: %v", err)
	}
}

// --- serveOn (loopback listener only, no signals, no public port) -------------

// loopbackListener returns a listener bound to 127.0.0.1:0 (an OS-chosen free
// loopback port — never a public interface). The caller closes it.
func loopbackListener(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not bind a loopback listener: %v", err)
	}
	return ln
}

func TestServeOnGracefulShutdownOnContextCancel(t *testing.T) {
	ln := loopbackListener(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveOn(ctx, io.Discard, ln, testMux()) }()

	// The server is up; a real loopback request is served through the mux.
	base := "http://" + ln.Addr().String()
	req, err := http.NewRequest("GET", base+"/api/recipes", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(tokenHeader, testTok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("live studio should answer on loopback: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("recipes over the real listener should be 200, got %d", resp.StatusCode)
	}

	// Cancelling the context triggers the graceful-shutdown branch of serveOn.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("graceful shutdown should return nil, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("serveOn did not shut down within 5s")
	}
}

func TestServeOnReturnsServeError(t *testing.T) {
	ln := loopbackListener(t)
	// Close the listener out from under the server: srv.Serve then returns a
	// non-ErrServerClosed error, exercising serveOn's errCh branch.
	ln.Close()
	err := serveOn(context.Background(), io.Discard, ln, testMux())
	if err == nil || err == http.ErrServerClosed {
		t.Fatalf("a closed listener should surface a serve error, got %v", err)
	}
}

// freeLoopbackAddr returns a free 127.0.0.1 port as "host:port" by binding then
// releasing it — so Serve can bind it a moment later without a fixed-port clash.
func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not find a free loopback port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func TestServeEndToEndGracefulShutdownOnSIGTERM(t *testing.T) {
	// Exercise Serve's own body (addr default guard is skipped by passing an addr;
	// listen success, banner print, signal setup, then graceful shutdown).
	//
	// SAFETY: Serve calls signal.NotifyContext(..., SIGTERM) BEFORE we send the
	// signal, so the Go runtime intercepts SIGTERM and cancels Serve's context
	// instead of terminating the test process. No public port (loopback only),
	// no Docker, no build.
	addr := freeLoopbackAddr(t)
	done := make(chan error, 1)
	go func() { done <- Serve(io.Discard, addr, false, true) }()

	// Wait until the server answers, so the signal handler is definitely wired.
	base := "http://" + addr
	deadline := time.Now().Add(5 * time.Second)
	var up bool
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/api/recipes")
		if err == nil {
			resp.Body.Close()
			up = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !up {
		t.Fatalf("Serve did not come up on %s", addr)
	}

	// SIGTERM is handled by Serve's NotifyContext, so this shuts the server down
	// gracefully rather than killing the test binary.
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("could not signal self: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("graceful shutdown should return nil, got %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatalf("Serve did not shut down after SIGTERM")
	}
}

// --- Load-error branches (malformed registry YAML) ---------------------------

func TestHandleProjectsRegistryLoadError(t *testing.T) {
	cfg := isolateConfig(t)
	// Malformed projects.yaml -> project.Load() errors -> handler reports it.
	if err := os.WriteFile(filepath.Join(cfg, "projects.yaml"), []byte("projects: [not: valid: yaml"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handleProjects(w, httptest.NewRequest("GET", "http://127.0.0.1/api/projects", nil))
	if !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("a malformed projects.yaml should surface an error: %s", w.Body.String())
	}
}

func TestHandlePacksRegistryLoadError(t *testing.T) {
	cfg := isolateConfig(t)
	// Malformed packs.yaml -> pack.Load() errors -> handler reports it.
	if err := os.WriteFile(filepath.Join(cfg, "packs.yaml"), []byte("packs: [not: valid: yaml"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handlePacks(w, httptest.NewRequest("GET", "http://127.0.0.1/api/packs", nil))
	if !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("a malformed packs.yaml should surface an error: %s", w.Body.String())
	}
}

// --- handlePacks with an installed pack ---------------------------------------

func TestHandlePacksListsInstalledPackAndRecipes(t *testing.T) {
	cfg := isolateConfig(t)
	// Install a pack on disk: <config>/recipes/<name>/{keel.pack.yaml, recipe.yaml}
	// plus an entry in <config>/packs.yaml. catalog.Registry() then stamps the
	// recipe with Pack=<name>, so handlePacks' inner recipe match fires too.
	const pkg = "demopack"
	rdir := filepath.Join(cfg, "recipes", pkg)
	if err := os.MkdirAll(rdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rdir, "keel.pack.yaml"),
		[]byte("schema_version: 1\nname: "+pkg+"\nversion: 1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rdir, "recipe.yaml"),
		[]byte("id: demo-addon\nkind: addon\nlabel: Demo Addon\nappliesTo: [laravel]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "packs.yaml"),
		[]byte("packs:\n  - name: "+pkg+"\n    version: 1.0.0\n    trusted: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	handlePacks(w, httptest.NewRequest("GET", "http://127.0.0.1/api/packs", nil))
	body := w.Body.String()
	if !strings.Contains(body, `"name":"`+pkg+`"`) {
		t.Fatalf("installed pack should be listed: %s", body)
	}
	if !strings.Contains(body, "demo-addon") {
		t.Fatalf("the pack's recipe should be attached to it: %s", body)
	}
}

// --- extra handler branches (all fail-fast, no build/docker/test-binary) ------

func TestHandleExecRefusesUntrackedDir(t *testing.T) {
	// An arbitrary path from the request body never runs a command: exec is
	// limited to directories the user already tracks. This is what stops a
	// crafted "dir" from reaching a shell somewhere else on the disk.
	isolateConfig(t)
	missing := filepath.Join(t.TempDir(), "no-such-dir")
	w := httptest.NewRecorder()
	handleExec(w, httptest.NewRequest("POST", "http://127.0.0.1/api/exec",
		strings.NewReader(`{"dir":`+strconvQuote(missing)+`,"args":["doctor"]}`)))
	body := w.Body.String()
	if !strings.Contains(body, "not a tracked keel project") {
		t.Fatalf("an untracked dir must be refused: %s", body)
	}
	if strings.Contains(body, "→ keel doctor") {
		t.Fatalf("nothing should be announced or run for a refused dir: %s", body)
	}
	if !strings.Contains(body, "event: done") {
		t.Fatalf("exec should finish with a done frame: %s", body)
	}
}

func TestHandleExecRunFailsFastOnBadDir(t *testing.T) {
	// A no-project command (doctor) skips the manifest check and reaches the exec
	// runner. The dir is tracked (so it passes the path guard) but then removed,
	// making `sh -c` fail on chdir BEFORE the command runs — so the keel binary
	// (here the test binary) is never executed. This exercises handleExec's
	// run + error-emit + done branch safely.
	isolateConfig(t)
	dir := t.TempDir()
	trackProject(t, dir)
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handleExec(w, httptest.NewRequest("POST", "http://127.0.0.1/api/exec",
		strings.NewReader(`{"dir":`+strconvQuote(dir)+`,"args":["doctor"]}`)))
	body := w.Body.String()
	if !strings.Contains(body, "→ keel doctor") {
		t.Fatalf("exec should announce the command before running: %s", body)
	}
	if !strings.Contains(body, "✗") {
		t.Fatalf("a chdir failure should surface an error line: %s", body)
	}
	if !strings.Contains(body, "event: done") {
		t.Fatalf("exec should finish with a done frame: %s", body)
	}
}

func TestHandleExecDefaultsDirToCwd(t *testing.T) {
	// A whitelisted project command with an empty dir defaults to the studio's own
	// working directory, which is always addressable. The test's cwd (the studio
	// package dir) has no .keel/manifest.yaml, so the handler refuses with
	// "not a keel project" WITHOUT ever running keel.
	isolateConfig(t)
	w := httptest.NewRecorder()
	handleExec(w, httptest.NewRequest("POST", "http://127.0.0.1/api/exec", strings.NewReader(`{"args":["db"]}`)))
	body := w.Body.String()
	cwd, _ := os.Getwd()
	if !strings.Contains(body, "not a keel project: "+cwd) || !strings.Contains(body, "event: done") {
		t.Fatalf("empty dir should default to the cwd and be refused as not-a-project: %s", body)
	}
}

// TestHandleExecUsesArgvNoShellHop proves the P0-2 fix: handleExec passes the
// browser's args through as argv (via the execArgv seam → RunArgv), never a
// `sh -c` line. The seam records the argv, and the test asserts a shell
// metacharacter in a later arg arrives as ONE inert argument — not split, not
// quoted, and with no "sh"/"-c" anywhere. Using the seam (instead of really
// running os.Executable, which under `go test` is the test binary) keeps this
// deterministic and avoids re-executing the suite.
func TestHandleExecUsesArgvNoShellHop(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	trackProject(t, dir)

	var gotArgv []string
	old := execArgv
	execArgv = func(ctx context.Context, out io.Writer, d string, argv []string) error {
		gotArgv = argv
		return nil
	}
	defer func() { execArgv = old }()

	// If the args were ever joined into `sh -c`, this ";touch …" would be a second
	// command; under argv it must remain a single, verbatim argument.
	inject := ";touch /tmp/pwned"
	w := httptest.NewRecorder()
	handleExec(w, httptest.NewRequest("POST", "http://127.0.0.1/api/exec",
		strings.NewReader(`{"dir":`+strconvQuote(dir)+`,"args":["optimize",`+strconvQuote(inject)+`]}`)))

	if len(gotArgv) < 2 {
		t.Fatalf("execArgv was not reached with the request args: %v", gotArgv)
	}
	// argv[0] is the keel executable; the rest are the request args, verbatim.
	if gotArgv[1] != "optimize" || gotArgv[len(gotArgv)-1] != inject {
		t.Fatalf("args were not passed through as argv (shell-joined?): %v", gotArgv)
	}
	for _, a := range gotArgv {
		if a == "sh" || a == "-c" {
			t.Fatalf("handleExec still routes through a shell: %v", gotArgv)
		}
	}
	if !strings.Contains(w.Body.String(), "event: done") {
		t.Fatalf("exec should finish with a done frame: %s", w.Body.String())
	}
}

func TestHandleDBQueryRunsClientAndEmitsError(t *testing.T) {
	// A valid manifest on the local (exec="") env makes the handler shell to the
	// bare `psql` client. With no DB server reachable, psql exits non-zero fast —
	// exercising the run + error-emit branch without Docker or a real build.
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "laravel-local", []string{"laravel", "postgres"})
	trackProject(t, dir)
	w := httptest.NewRecorder()
	handleDBQuery(w, httptest.NewRequest("POST", "http://127.0.0.1/api/db/query",
		strings.NewReader(`{"dir":`+strconvQuote(dir)+`,"sql":"SELECT 1"}`)))
	body := w.Body.String()
	// Either the client ran and failed (✗ …) or produced output; either way the
	// stream must terminate with a done frame and never a not-a-project error.
	if strings.Contains(body, "not a keel project") {
		t.Fatalf("a valid manifest should reach the DB client, not the no-project branch: %s", body)
	}
	if !strings.Contains(body, "event: done") {
		t.Fatalf("db query should finish with a done frame: %s", body)
	}
}

func TestHandleBuildDefaultsNameFromFramework(t *testing.T) {
	// No name -> the handler derives "<framework>-app". Dry-run: nothing runs.
	isolateConfig(t)
	w := httptest.NewRecorder()
	handleBuild(w, httptest.NewRequest("POST", "http://127.0.0.1/api/build",
		strings.NewReader(`{"ids":["laravel","ddev","mysql"],"run":false}`)))
	body := w.Body.String()
	if !strings.Contains(body, "keel preview → ./laravel-app") {
		t.Fatalf("a blank name should default to <framework>-app: %s", body)
	}
	if !strings.Contains(body, "event: done") {
		t.Fatalf("build should finish with a done frame: %s", body)
	}
}

func TestHandleBuildUsesProfileProjectsDir(t *testing.T) {
	// With a profile projects_dir set, the dry-run build resolves the target under
	// it (covers the projects_dir branch). Dry-run changes nothing on disk.
	isolateConfig(t)
	projects := t.TempDir()
	// Save a profile whose projects_dir points at a temp folder.
	post := `{"Name":"Dan","ProjectsDir":` + strconvQuote(projects) + `,"Hosting":"","Framework":"","Env":"","Database":"","Editor":"","Frontend":"","Services":"","Addons":"","Extras":""}`
	pw := httptest.NewRecorder()
	handleProfile(pw, httptest.NewRequest("POST", "http://127.0.0.1/api/profile", strings.NewReader(post)))
	if !strings.Contains(pw.Body.String(), `"saved":true`) {
		t.Fatalf("profile should save: %s", pw.Body.String())
	}
	w := httptest.NewRecorder()
	handleBuild(w, httptest.NewRequest("POST", "http://127.0.0.1/api/build",
		strings.NewReader(`{"ids":["laravel","ddev","mysql"],"name":"in-profile-dir","run":false}`)))
	body := w.Body.String()
	if !strings.Contains(body, "keel preview → ./in-profile-dir") {
		t.Fatalf("dry-run should announce the named preview: %s", body)
	}
	if !strings.Contains(body, "event: done") {
		t.Fatalf("build should finish with a done frame: %s", body)
	}
}

// --- helpers -----------------------------------------------------------------

// noFlushWriter is an http.ResponseWriter that is deliberately NOT an
// http.Flusher, so the SSE handlers take their "streaming unsupported" branch
// immediately WITHOUT running any real build/exec/db command.
//
// It embeds only the http.ResponseWriter *interface* (backed by a recorder), so
// the recorder's concrete Flush method is NOT promoted onto noFlushWriter — a
// type assertion to http.Flusher therefore fails, which is the whole point.
// The underlying recorder is kept in `rec` so tests can read Code/Body.
type noFlushWriter struct {
	http.ResponseWriter
	rec *httptest.ResponseRecorder
}

// newNoFlushWriter builds a non-flushing writer over a fresh recorder.
func newNoFlushWriter() *noFlushWriter {
	rec := httptest.NewRecorder()
	return &noFlushWriter{ResponseWriter: rec, rec: rec}
}

// strconvQuote JSON-quotes a string for embedding in a request body literal.
func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
