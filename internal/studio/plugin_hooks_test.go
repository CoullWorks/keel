package studio

import (
	"strconv"
	"testing"

	"github.com/coullworks/keel/internal/plugintest"
)

// trackedLaravel makes an isolated, tracked laravel project the studio can act on,
// returning its directory. The plugin hook endpoints all take a project, so a test
// needs a real tracked one — an untracked path is refused.
func trackedLaravel(t *testing.T) string {
	t.Helper()
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "ddev", []string{"laravel"})
	trackProject(t, dir)
	reloadRegistry()
	return dir
}

// trackedLaravelWithPlugin is trackedLaravel plus a discovered, trusted plugin
// fixture. keel ships zero built-ins, so a test that needs a plugin to overview,
// act on or offer options for a project installs a real one on disk and trusts it
// (its overview/action/options run executables, so they need trust). The fixture
// applies to every framework, so its contributions reach a laravel project.
func trackedLaravelWithPlugin(t *testing.T) (string, *plugintest.Fixture) {
	t.Helper()
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "ddev", []string{"laravel"})
	trackProject(t, dir)
	f := plugintest.Install(t, "demo")
	f.Trust()
	reloadRegistry()
	return dir, f
}

// The overview endpoint gathers the applicable plugins' sections for a project. A
// trusted discovered plugin is an Overviewer, so a laravel project has sections.
func TestPluginOverviewEndpoint(t *testing.T) {
	dir, _ := trackedLaravelWithPlugin(t)
	mux := testMux()

	w := muxPost(mux, "/api/plugin-overview", `{"dir":`+strconv.Quote(dir)+`}`)
	if w.Code != 200 {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	got := decodeJSON(t, w)
	secs, ok := got["sections"].([]any)
	if !ok || len(secs) == 0 {
		t.Errorf("expected overview sections from the plugin, got %v", got["sections"])
	}
}

// The actions endpoint lists what plugins can do to a project, each tagged with
// its owning plugin. The fixture contributes a capability-free action.
func TestPluginActionsEndpoint(t *testing.T) {
	dir, _ := trackedLaravelWithPlugin(t)
	mux := testMux()

	w := muxPost(mux, "/api/plugin-actions", `{"dir":`+strconv.Quote(dir)+`}`)
	if w.Code != 200 {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	got := decodeJSON(t, w)
	actions, ok := got["actions"].([]any)
	if !ok || len(actions) == 0 {
		t.Fatalf("expected at least the plugin's action, got %v", got["actions"])
	}
	first := actions[0].(map[string]any)
	if first["plugin"] == "" {
		t.Error("an action must be tagged with its owning plugin")
	}
}

// Running a capability-free action succeeds and returns its output.
func TestPluginActionRunsCapabilityFree(t *testing.T) {
	dir, f := trackedLaravelWithPlugin(t)
	mux := testMux()

	body := `{"plugin":"` + f.Name + `","id":"` + f.Action() + `","dir":` + strconv.Quote(dir) + `}`
	w := muxPost(mux, "/api/plugin/action", body)
	if w.Code != 200 {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	got := decodeJSON(t, w)
	if ok, _ := got["ok"].(bool); !ok {
		t.Errorf("a capability-free action should succeed, got %v", got)
	}
}

// An action for an unknown plugin is a clean error, not a panic.
func TestPluginActionUnknownPlugin(t *testing.T) {
	dir := trackedLaravel(t)
	mux := testMux()
	body := `{"plugin":"nope","id":"x","dir":` + strconv.Quote(dir) + `}`
	w := muxPost(mux, "/api/plugin/action", body)
	got := decodeJSON(t, w)
	if ok, _ := got["ok"].(bool); ok {
		t.Error("an unknown plugin should not report ok")
	}
}

// The pages endpoint returns an empty list when no plugin contributes a page,
// rather than erroring — the studio draws nothing, which is correct.
func TestPluginPagesEndpointEmpty(t *testing.T) {
	isolateConfig(t)
	reloadRegistry()
	w := muxGet(testMux(), "/api/plugin-pages")
	if w.Code != 200 {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	got := decodeJSON(t, w)
	if _, ok := got["pages"]; !ok {
		t.Error("the pages endpoint should return a pages key even when empty")
	}
}
