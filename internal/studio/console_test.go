package studio

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// errFake stands in for a real command failure in the runCapture seam tests.
var errFake = errors.New("exit status 1")

// --- safeTaskName ------------------------------------------------------------

func TestSafeTaskName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"dev", true},
		{"test", true},
		{"type-check", true},
		{"build2", true},
		{"", false},
		{"  ", false},
		{"-flag", false},    // could be read as a flag
		{"rm -rf /", false}, // spaces / path
		{"../etc", false},   // path traversal shape
		{"Dev", false},      // uppercase is not a task name
		{"a;b", false},      // shell metacharacter
		{strings.Repeat("a", 41), false},
	}
	for _, c := range cases {
		if got := safeTaskName(c.name); got != c.want {
			t.Errorf("safeTaskName(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

// --- handleProjectAction -----------------------------------------------------

func TestHandleProjectActionStreamingUnsupported(t *testing.T) {
	w := newNoFlushWriter()
	handleProjectAction(w, httptest.NewRequest("POST", "http://127.0.0.1/api/project/action",
		strings.NewReader(`{"dir":".","action":"open"}`)))
	if w.rec.Code != http.StatusInternalServerError {
		t.Fatalf("a non-flushing writer should yield 500, got %d", w.rec.Code)
	}
	if !strings.Contains(w.rec.Body.String(), "streaming unsupported") {
		t.Fatalf("should return streaming-unsupported without acting: %s", w.rec.Body.String())
	}
}

func TestHandleProjectActionBadBody(t *testing.T) {
	w := httptest.NewRecorder()
	handleProjectAction(w, httptest.NewRequest("POST", "http://127.0.0.1/api/project/action", strings.NewReader("nope")))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("malformed body should be a 400, got %d", w.Code)
	}
}

func TestHandleProjectActionUnknownActionRefused(t *testing.T) {
	isolateConfig(t)
	w := httptest.NewRecorder()
	handleProjectAction(w, httptest.NewRequest("POST", "http://127.0.0.1/api/project/action",
		strings.NewReader(`{"dir":".","action":"delete"}`)))
	body := w.Body.String()
	if !strings.Contains(body, "action not allowed: delete") || !strings.Contains(body, "event: done") {
		t.Fatalf("an unknown action should be refused with a done frame: %s", body)
	}
}

func TestHandleProjectActionRefusesUntrackedDir(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir() // never tracked
	w := httptest.NewRecorder()
	handleProjectAction(w, httptest.NewRequest("POST", "http://127.0.0.1/api/project/action",
		strings.NewReader(`{"dir":`+strconvQuote(dir)+`,"action":"start"}`)))
	body := w.Body.String()
	if !strings.Contains(body, "not a tracked keel project") || !strings.Contains(body, "event: done") {
		t.Fatalf("an untracked dir must be refused: %s", body)
	}
}

// "open" launches the editor through the openInEditor seam, so it can be
// exercised with no real editor. It reports success and never touches the shell.
func TestHandleProjectActionOpenUsesEditorSeam(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	trackProject(t, dir)

	var gotEditor, gotDir string
	restore := openInEditor
	openInEditor = func(editor, d string) error { gotEditor, gotDir = editor, d; return nil }
	defer func() { openInEditor = restore }()

	// Force a known, present editor so platform.Has passes deterministically:
	// "sh" is on PATH everywhere the tests run.
	t.Setenv("EDITOR", "sh")

	w := httptest.NewRecorder()
	handleProjectAction(w, httptest.NewRequest("POST", "http://127.0.0.1/api/project/action",
		strings.NewReader(`{"dir":`+strconvQuote(dir)+`,"action":"open"}`)))
	body := w.Body.String()
	if !strings.Contains(body, "✓ opened") || !strings.Contains(body, "event: done") {
		t.Fatalf("open should report success and a done frame: %s", body)
	}
	if gotEditor != "sh" || gotDir != dir {
		t.Fatalf("editor seam got (%q,%q), want (sh,%q)", gotEditor, gotDir, dir)
	}
}

// A start on a project whose env recipe has no manifest is refused with a clear
// reason, not run.
func TestHandleProjectActionStartNotAKeelProject(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir() // tracked, but no .keel/manifest.yaml
	trackProject(t, dir)
	w := httptest.NewRecorder()
	handleProjectAction(w, httptest.NewRequest("POST", "http://127.0.0.1/api/project/action",
		strings.NewReader(`{"dir":`+strconvQuote(dir)+`,"action":"start"}`)))
	body := w.Body.String()
	if !strings.Contains(body, "not a keel project") || !strings.Contains(body, "event: done") {
		t.Fatalf("start outside a keel project should be refused: %s", body)
	}
}

// --- envCommandFor -----------------------------------------------------------

func TestEnvCommandForUnknownEnv(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "not-a-real-env", []string{"laravel"})
	if _, err := envCommandFor(dir, "start"); err == nil {
		t.Fatal("an unknown env recipe should be an error, not a silent empty command")
	}
}

// A real docker env recipe defines start/down/restart; envCommandFor resolves
// each from the recipe rather than guessing per environment.
func TestEnvCommandForResolvesRealEnv(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "nextjs-docker", []string{"nextjs"})
	for _, action := range []string{"start", "stop", "restart"} {
		cmd, err := envCommandFor(dir, action)
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if !strings.Contains(cmd, "docker compose") {
			t.Fatalf("%s should resolve to a docker compose command, got %q", action, cmd)
		}
	}
}

// A missing manifest is a clear "not a keel project", not an empty command.
func TestEnvCommandForNoManifest(t *testing.T) {
	dir := t.TempDir()
	if _, err := envCommandFor(dir, "start"); err == nil || !strings.Contains(err.Error(), "not a keel project") {
		t.Fatalf("no manifest should be a not-a-keel-project error, got %v", err)
	}
}

// --- editorCommand -----------------------------------------------------------

func TestEditorCommandPrefersProfileThenEnv(t *testing.T) {
	isolateConfig(t)
	// No profile, no env vars -> the default.
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if got := editorCommand(); got != "code" {
		t.Fatalf("with nothing configured, editorCommand should default to code, got %q", got)
	}
	// $EDITOR wins over the default, and only the binary name is kept.
	t.Setenv("EDITOR", "nvim --clean")
	if got := editorCommand(); got != "nvim" {
		t.Fatalf("editorCommand should take the binary from $EDITOR, got %q", got)
	}
	// $VISUAL wins over $EDITOR.
	t.Setenv("VISUAL", "code")
	if got := editorCommand(); got != "code" {
		t.Fatalf("$VISUAL should win over $EDITOR, got %q", got)
	}
}

// --- handleRun ---------------------------------------------------------------

func TestHandleRunStreamingUnsupported(t *testing.T) {
	w := newNoFlushWriter()
	handleRun(w, httptest.NewRequest("POST", "http://127.0.0.1/api/run",
		strings.NewReader(`{"dir":".","task":"dev"}`)))
	if w.rec.Code != http.StatusInternalServerError {
		t.Fatalf("a non-flushing writer should yield 500, got %d", w.rec.Code)
	}
	if !strings.Contains(w.rec.Body.String(), "streaming unsupported") {
		t.Fatalf("should return streaming-unsupported without running: %s", w.rec.Body.String())
	}
}

func TestHandleRunBadBody(t *testing.T) {
	w := httptest.NewRecorder()
	handleRun(w, httptest.NewRequest("POST", "http://127.0.0.1/api/run", strings.NewReader("{}")))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty task should be a 400, got %d", w.Code)
	}
}

func TestHandleRunRejectsUnsafeTask(t *testing.T) {
	isolateConfig(t)
	w := httptest.NewRecorder()
	handleRun(w, httptest.NewRequest("POST", "http://127.0.0.1/api/run",
		strings.NewReader(`{"dir":".","task":"dev; rm -rf /"}`)))
	body := w.Body.String()
	if !strings.Contains(body, "invalid task name") || !strings.Contains(body, "event: done") {
		t.Fatalf("an unsafe task name must be refused before anything runs: %s", body)
	}
}

func TestHandleRunNotAKeelProject(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir() // tracked, no manifest
	trackProject(t, dir)
	w := httptest.NewRecorder()
	handleRun(w, httptest.NewRequest("POST", "http://127.0.0.1/api/run",
		strings.NewReader(`{"dir":`+strconvQuote(dir)+`,"task":"test"}`)))
	body := w.Body.String()
	if !strings.Contains(body, "not a keel project") || !strings.Contains(body, "event: done") {
		t.Fatalf("run outside a keel project should be refused: %s", body)
	}
}

// --- handleRunTasks ----------------------------------------------------------

func TestHandleRunTasksBadBody(t *testing.T) {
	w := httptest.NewRecorder()
	handleRunTasks(w, httptest.NewRequest("POST", "http://127.0.0.1/api/run/tasks", strings.NewReader("nope")))
	if err, _ := decodeJSON(t, w)["error"].(string); err != "bad request" {
		t.Fatalf("malformed body should return a bad-request error: %s", w.Body.String())
	}
}

func TestHandleRunTasksNotAKeelProject(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir() // tracked, no manifest
	trackProject(t, dir)
	w := httptest.NewRecorder()
	handleRunTasks(w, httptest.NewRequest("POST", "http://127.0.0.1/api/run/tasks",
		strings.NewReader(`{"dir":`+strconvQuote(dir)+`}`)))
	m := decodeJSON(t, w)
	if err, _ := m["error"].(string); !strings.Contains(err, "not a keel project") {
		t.Fatalf("no manifest -> not a keel project error: %s", w.Body.String())
	}
	if _, ok := m["tasks"]; !ok {
		t.Fatalf("response should still carry a tasks key: %s", w.Body.String())
	}
}

// The tasks list is captured through the runCapture seam, so it is exercised
// without building the real keel binary: a fake returns the run --json shape and
// the handler parses it into a tasks array.
func TestHandleRunTasksParsesTasksViaSeam(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "laravel-docker", []string{"laravel"})
	trackProject(t, dir)

	restore := runCapture
	runCapture = func(_ context.Context, _ string, _ ...string) (string, error) {
		return `{"framework":"laravel","env":"laravel-docker","tasks":[{"name":"dev","command":"php artisan serve"},{"name":"test","command":"php artisan test"}]}`, nil
	}
	defer func() { runCapture = restore }()

	w := httptest.NewRecorder()
	handleRunTasks(w, httptest.NewRequest("POST", "http://127.0.0.1/api/run/tasks",
		strings.NewReader(`{"dir":`+strconvQuote(dir)+`}`)))
	body := w.Body.String()
	if !strings.Contains(body, `"name":"dev"`) || !strings.Contains(body, `"name":"test"`) {
		t.Fatalf("tasks should be parsed from the run --json output: %s", body)
	}
	if !strings.Contains(body, `"framework":"laravel"`) {
		t.Fatalf("the framework should be echoed back: %s", body)
	}
}

func TestHandleRunTasksSurfacesBinaryError(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "laravel-docker", []string{"laravel"})
	trackProject(t, dir)

	restore := runCapture
	runCapture = func(_ context.Context, _ string, _ ...string) (string, error) {
		return "no tasks defined for laravel", errFake
	}
	defer func() { runCapture = restore }()

	w := httptest.NewRecorder()
	handleRunTasks(w, httptest.NewRequest("POST", "http://127.0.0.1/api/run/tasks",
		strings.NewReader(`{"dir":`+strconvQuote(dir)+`}`)))
	m := decodeJSON(t, w)
	if _, ok := m["tasks"]; !ok {
		t.Fatalf("even on error the response should carry a tasks key: %s", w.Body.String())
	}
	if err, _ := m["error"].(string); !strings.Contains(err, "no tasks defined") {
		t.Fatalf("the binary's error should be surfaced: %s", w.Body.String())
	}
}

// --- withEnv (projects listing enrichment) -----------------------------------

// The projects GET response carries each managed project's env so the dashboard
// can label a Start/Stop control without a round trip per row.
func TestHandleProjectsGETCarriesEnv(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "nextjs-docker", []string{"nextjs"})
	trackProject(t, dir)
	w := httptest.NewRecorder()
	handleProjects(w, httptest.NewRequest("GET", "http://127.0.0.1/api/projects", nil))
	if !strings.Contains(w.Body.String(), `"env":"nextjs-docker"`) {
		t.Fatalf("a managed project's env should be in the listing: %s", w.Body.String())
	}
}

// --- route wiring ------------------------------------------------------------

// The three new console routes are behind the same guards as every other
// mutating route: a cross-site, tokenless POST is refused.
func TestNewConsoleRoutesAreGuarded(t *testing.T) {
	isolateConfig(t)
	mux := testMux()
	for _, path := range []string{"/api/project/action", "/api/run", "/api/run/tasks"} {
		req := httptest.NewRequest("POST", "http://127.0.0.1"+path, strings.NewReader("{}"))
		req.Host = "127.0.0.1:7373"
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("Origin", "https://evil.example")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s must refuse a cross-site tokenless POST, got %d", path, w.Code)
		}
	}
}

// The new routes reject a wrong method (they are POST-only), which is what keeps
// a form-shaped GET from reaching them.
func TestNewConsoleRoutesArePostOnly(t *testing.T) {
	isolateConfig(t)
	mux := testMux()
	for _, path := range []string{"/api/project/action", "/api/run", "/api/run/tasks"} {
		req := httptest.NewRequest("GET", "http://127.0.0.1"+path, nil)
		req.Host = "127.0.0.1"
		req.Header.Set(tokenHeader, testTok)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s should be 405, got %d", path, w.Code)
		}
	}
}
