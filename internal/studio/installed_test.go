package studio

// Tests for the Issue-3 fix: the Manage-services tab must show the recipes THIS
// project already has (from the real manifest) so each can be removed. These
// drive /api/installed end to end through the guarded mux, and check the
// removable / non-removable split.

import (
	"net/http"
	"testing"
)

// A project with recipes in its manifest lists them, each resolved to a label +
// kind, with the removable flag set — a service is removable, the env is not.
func TestHandleInstalledListsManifestRecipes(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	// writeManifest records env: django-docker + recipes: [redis, django-docker].
	writeManifest(t, dir, "django-docker", []string{"redis", "django-docker"})
	trackProject(t, dir)

	w := muxGet(testMux(), "/api/installed?dir="+dir)
	if w.Code != http.StatusOK {
		t.Fatalf("installed should be 200, got %d: %s", w.Code, w.Body)
	}
	m := decodeJSON(t, w)
	list, _ := m["installed"].([]any)
	if len(list) == 0 {
		t.Fatalf("the installed list must read the manifest recipes: %s", w.Body)
	}
	byID := map[string]map[string]any{}
	for _, r := range list {
		if o, ok := r.(map[string]any); ok {
			byID[o["id"].(string)] = o
		}
	}
	redis, ok := byID["redis"]
	if !ok {
		t.Fatalf("redis (a service recipe) must appear as installed: %s", w.Body)
	}
	if redis["removable"] != true {
		t.Errorf("a service recipe must be removable: %+v", redis)
	}
	if redis["kind"] != "service" {
		t.Errorf("redis should resolve to kind=service: %+v", redis)
	}
	if env, ok := byID["django-docker"]; ok {
		if env["removable"] != false {
			t.Errorf("the env recipe defines the project and must NOT be removable: %+v", env)
		}
	}
}

// A project with no added recipes returns an empty list (200) — the "none added
// yet" case the UI renders gracefully, not an error.
func TestHandleInstalledNoneAdded(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "django-docker", nil)
	trackProject(t, dir)

	w := muxGet(testMux(), "/api/installed?dir="+dir)
	if w.Code != http.StatusOK {
		t.Fatalf("installed should be 200, got %d: %s", w.Code, w.Body)
	}
	m := decodeJSON(t, w)
	list, _ := m["installed"].([]any)
	if len(list) != 0 {
		t.Errorf("a project with no recipes has an empty installed list: %s", w.Body)
	}
}

// A non-project dir is a normal answer (200 + error field the UI shows), not a 500.
func TestHandleInstalledUntracked(t *testing.T) {
	isolateConfig(t)
	w := muxGet(testMux(), "/api/installed?dir="+t.TempDir())
	if w.Code != http.StatusOK {
		t.Fatalf("an untracked dir is a normal 200, got %d", w.Code)
	}
	m := decodeJSON(t, w)
	if s, _ := m["error"].(string); s == "" {
		t.Errorf("an untracked dir must carry an error field: %s", w.Body)
	}
	if list, _ := m["installed"].([]any); list == nil {
		t.Errorf("installed must still be an array, not null: %s", w.Body)
	}
}
