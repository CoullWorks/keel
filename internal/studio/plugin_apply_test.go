package studio

import (
	"net/url"
	"strconv"
	"testing"
)

// The plugin-state endpoint reports, per option step, what a project has applied
// so the interactive form pre-checks from installed state. A fresh project has
// applied nothing, so it reports an empty per-step map (not an error).
func TestPluginStateEndpointFreshProject(t *testing.T) {
	dir := trackedLaravel(t)
	mux := testMux()

	w := muxGet(mux, "/api/plugin-state?dir="+url.QueryEscape(dir))
	if w.Code != 200 {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	got := decodeJSON(t, w)
	if _, ok := got["steps"]; !ok {
		t.Errorf("plugin-state must return a steps key, got %v", got)
	}
}

// Applying the interactive form reconciles a project's plugin selection: a chosen
// value is written, and it then shows up as installed state so the form re-checks
// it. This is the two-way sync the read-only tab never had, proven end to end
// against a real discovered plugin whose apply persists state its screen reads
// back — the generic mechanism, with no plugin the studio imports.
func TestPluginApplyReconcilesAndStateReflectsIt(t *testing.T) {
	dir, f := trackedLaravelWithPlugin(t)
	mux := testMux()

	// The plugin's step and a concrete choice, from the same schema the form draws.
	opts := decodeJSON(t, muxGet(mux, "/api/plugin-options?framework=laravel"))
	schemas, _ := opts["schemas"].([]any)
	if len(schemas) == 0 {
		t.Fatalf("the trusted plugin should offer option schemas for laravel, got %v", opts)
	}
	stepID := f.Step()
	var choice string
	for _, s := range schemas {
		m := s.(map[string]any)
		if id, _ := m["id"].(string); id == stepID {
			if ch, _ := m["choices"].([]any); len(ch) > 0 {
				choice, _ = ch[0].(map[string]any)["value"].(string)
			}
		}
	}
	if choice == "" {
		t.Fatalf("the plugin's step %q offered no choice to apply, schemas=%v", stepID, schemas)
	}

	// Apply exactly one value, nothing else — a reconciling write.
	body := `{"dir":` + strconv.Quote(dir) + `,"options":{` + strconv.Quote(stepID) + `:[` + strconv.Quote(choice) + `]}}`
	w := muxPost(mux, "/api/plugin/apply", body)
	if w.Code != 200 {
		t.Fatalf("apply want 200, got %d: %s", w.Code, w.Body)
	}
	res := decodeJSON(t, w)
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("apply should report ok, got %v", res)
	}

	// The state now reflects the applied selection, so the form pre-checks it.
	st := decodeJSON(t, muxGet(mux, "/api/plugin-state?dir="+url.QueryEscape(dir)))
	steps, _ := st["steps"].(map[string]any)
	vals, _ := steps[stepID].([]any)
	found := false
	for _, v := range vals {
		if v == choice {
			found = true
		}
	}
	if !found {
		t.Errorf("after applying %q, plugin-state should list it installed for step %q, got %v", choice, stepID, steps)
	}
}

// Applying with an empty step means "chose none", which reconciles that step to
// nothing — the removal path a read-only tab could never offer. It must not fall
// back to the step's defaults.
func TestPluginApplyEmptyStepRemovesAll(t *testing.T) {
	dir, f := trackedLaravelWithPlugin(t)
	mux := testMux()

	opts := decodeJSON(t, muxGet(mux, "/api/plugin-options?framework=laravel"))
	schemas, _ := opts["schemas"].([]any)
	if len(schemas) == 0 {
		t.Fatalf("the trusted plugin should offer option schemas for laravel, got %v", opts)
	}
	stepID := f.Step()
	var choice string
	for _, s := range schemas {
		m := s.(map[string]any)
		if id, _ := m["id"].(string); id == stepID {
			if ch, _ := m["choices"].([]any); len(ch) > 0 {
				choice, _ = ch[0].(map[string]any)["value"].(string)
			}
		}
	}
	if choice == "" {
		t.Fatalf("the plugin's step %q offered no choice to apply, schemas=%v", stepID, schemas)
	}

	// First install one, then reconcile to none.
	muxPost(mux, "/api/plugin/apply", `{"dir":`+strconv.Quote(dir)+`,"options":{`+strconv.Quote(stepID)+`:[`+strconv.Quote(choice)+`]}}`)
	w := muxPost(mux, "/api/plugin/apply", `{"dir":`+strconv.Quote(dir)+`,"options":{`+strconv.Quote(stepID)+`:[]}}`)
	if w.Code != 200 {
		t.Fatalf("apply(none) want 200, got %d: %s", w.Code, w.Body)
	}

	st := decodeJSON(t, muxGet(mux, "/api/plugin-state?dir="+url.QueryEscape(dir)))
	steps, _ := st["steps"].(map[string]any)
	if vals, _ := steps[stepID].([]any); len(vals) != 0 {
		t.Errorf("reconciling %q to none should leave it empty, got %v", stepID, vals)
	}
}

// Apply refuses an untracked directory, the same front door every mutating route
// uses — a plugin reconcile only ever touches a tracked project.
func TestPluginApplyRefusesUntracked(t *testing.T) {
	isolateConfig(t)
	reloadRegistry()
	mux := testMux()
	w := muxPost(mux, "/api/plugin/apply", `{"dir":"/tmp/not-tracked","options":{}}`)
	got := decodeJSON(t, w)
	if ok, _ := got["ok"].(bool); ok {
		t.Errorf("apply on an untracked dir should not report ok, got %v", got)
	}
}
