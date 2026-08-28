package studio

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeManifestFW drops a minimal .keel/manifest.yaml for an arbitrary framework
// into dir, so the framework-aware Generate endpoints resolve the right stack.
// writeManifest (studio_more_test.go) is Laravel-only; this parametrises it.
func writeManifestFW(t *testing.T, dir, framework, env string, recipes []string) {
	t.Helper()
	kdir := filepath.Join(dir, ".keel")
	if err := os.MkdirAll(kdir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "framework: " + framework + "\nenv: " + env + "\nrecipes:\n"
	for _, r := range recipes {
		body += "  - " + r + "\n"
	}
	if err := os.WriteFile(filepath.Join(kdir, "manifest.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// trackedProject sets up an isolated config, a temp project dir with a manifest
// for framework, and tracks it — the shape every /api/generators + /api/addable
// test needs. Returns the dir.
func trackedProject(t *testing.T, framework string, recipes []string) string {
	t.Helper()
	isolateConfig(t)
	dir := t.TempDir()
	writeManifestFW(t, dir, framework, framework+"-docker", recipes)
	trackProject(t, dir)
	return dir
}

// genList decodes the generatables array from an /api/generators response.
func genList(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	raw, _ := m["generatables"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		if g, ok := r.(map[string]any); ok {
			out = append(out, g)
		}
	}
	return out
}

func findGen(gens []map[string]any, key string) (map[string]any, bool) {
	for _, g := range gens {
		if g["key"] == key {
			return g, true
		}
	}
	return nil, false
}

func strSlice(v any) []string {
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))
	for _, x := range raw {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// --- Fix 1: framework-aware Generate menu (/api/generators) ------------------

// A Laravel project is offered Laravel's own generators: a "model" code-block
// with a typed "fields" input (the mage2gen primitive the fields table renders),
// and an auth stack that applies the real Breeze recipe. This is the endpoint the
// hardcoded Laravel noun list is replaced by.
func TestGeneratorsLaravelHasModelWithFieldsAndAuthStack(t *testing.T) {
	dir := trackedProject(t, "laravel", []string{"laravel", "laravel-docker"})

	w := muxGet(testMux(), "/api/generators?dir="+dir)
	if w.Code != http.StatusOK {
		t.Fatalf("generators should be 200, got %d: %s", w.Code, w.Body)
	}
	m := decodeJSON(t, w)
	if m["framework"] != "laravel" {
		t.Fatalf("framework should be laravel, got %v", m["framework"])
	}
	gens := genList(t, m)
	if len(gens) == 0 {
		t.Fatal("laravel must offer generatables")
	}

	model, ok := findGen(gens, "model")
	if !ok {
		t.Fatalf("laravel should offer a model generator: %s", w.Body)
	}
	// The fields table is driven by an input of type "fields" on the model.
	inputs, _ := model["inputs"].([]any)
	hasFields := false
	for _, in := range inputs {
		if im, ok := in.(map[string]any); ok && im["type"] == "fields" {
			hasFields = true
		}
	}
	if !hasFields {
		t.Errorf("the model generator must carry a fields input so the studio renders the fields table: %v", model["inputs"])
	}

	// Auth is a stack generatable that applies the real recipe — the fix for the
	// broken `gen auth` button.
	var auth map[string]any
	for _, g := range gens {
		if g["level"] == "stack" && contains(strSlice(g["provides"]), "auth") {
			auth = g
		}
	}
	if auth == nil {
		t.Fatalf("laravel should offer an auth stack generatable: %s", w.Body)
	}
	if applies := strSlice(auth["applies"]); !contains(applies, "laravel-breeze") {
		t.Errorf("the laravel auth stack should apply laravel-breeze, got %v", applies)
	}
}

// The whole point of the fix: the menu is scoped to the project's framework. A
// Next project shows its OWN auth stack (Auth.js) and must NOT show Laravel's
// nouns — the universal-Laravel-list bug this replaces.
func TestGeneratorsAreFrameworkScoped(t *testing.T) {
	dir := trackedProject(t, "nextjs", []string{"nextjs", "nextjs-docker"})

	w := muxGet(testMux(), "/api/generators?dir="+dir)
	if w.Code != http.StatusOK {
		t.Fatalf("generators should be 200, got %d: %s", w.Code, w.Body)
	}
	m := decodeJSON(t, w)
	if m["framework"] != "nextjs" {
		t.Fatalf("framework should be nextjs, got %v", m["framework"])
	}
	gens := genList(t, m)

	// Laravel's artisan nouns must not leak into a Next project.
	for _, laravelNoun := range []string{"controller", "migration", "filament-resource"} {
		if _, ok := findGen(gens, laravelNoun); ok {
			t.Errorf("a next project must not be offered the Laravel %q generator", laravelNoun)
		}
	}
	// It gets its own auth stack instead.
	var auth map[string]any
	for _, g := range gens {
		if g["level"] == "stack" && contains(strSlice(g["provides"]), "auth") {
			auth = g
		}
	}
	if auth == nil {
		t.Fatalf("nextjs should offer its own auth stack: %s", w.Body)
	}
	if applies := strSlice(auth["applies"]); !contains(applies, "nextjs-authjs") {
		t.Errorf("the nextjs auth stack should apply nextjs-authjs, got %v", applies)
	}
}

// A directory that is not a tracked keel project is refused with a calm error and
// an empty list — never a panic, never Laravel's list as a fallback.
func TestGeneratorsRejectsUntrackedDir(t *testing.T) {
	isolateConfig(t)
	w := muxGet(testMux(), "/api/generators?dir="+t.TempDir())
	if w.Code != http.StatusOK {
		t.Fatalf("an untracked dir is a normal answer, got %d", w.Code)
	}
	m := decodeJSON(t, w)
	if _, ok := m["error"]; !ok {
		t.Errorf("an untracked dir should carry an error: %s", w.Body)
	}
	if gens := genList(t, m); len(gens) != 0 {
		t.Errorf("an untracked dir must offer no generatables, got %d", len(gens))
	}
}

// --- Fix 3: add-service picker (/api/addable) --------------------------------

// The Add-service picker lists recipes compatible with the project's framework,
// grouped by kind, excluding what's already installed. This is the studio surface
// of `keel add`.
func TestAddableListsFrameworkRecipesExcludingInstalled(t *testing.T) {
	// A Laravel project that already has redis: redis must be excluded, other
	// laravel-compatible services must appear.
	dir := trackedProject(t, "laravel", []string{"laravel", "laravel-docker", "redis"})

	w := muxGet(testMux(), "/api/addable?dir="+dir)
	if w.Code != http.StatusOK {
		t.Fatalf("addable should be 200, got %d: %s", w.Code, w.Body)
	}
	m := decodeJSON(t, w)
	if m["framework"] != "laravel" {
		t.Fatalf("framework should be laravel, got %v", m["framework"])
	}
	raw, _ := m["addable"].([]any)
	if len(raw) == 0 {
		t.Fatalf("a laravel project should have addable recipes: %s", w.Body)
	}
	for _, r := range raw {
		rm, _ := r.(map[string]any)
		if rm["id"] == "redis" {
			t.Errorf("redis is already installed and must not be offered to add again")
		}
		// The framework and env define the project — they are never addable.
		if k := rm["kind"]; k == "framework" || k == "env" {
			t.Errorf("a %v recipe must not be addable: %v", k, rm["id"])
		}
	}
}

// Add-service is framework-filtered: a recipe that does not apply to the project's
// framework never appears (a Magento-only service is not offered to a Django app).
func TestAddableIsFrameworkFiltered(t *testing.T) {
	laravelDir := trackedProject(t, "laravel", []string{"laravel", "laravel-docker"})
	lw := decodeJSON(t, muxGet(testMux(), "/api/addable?dir="+laravelDir))
	laravelIDs := map[string]bool{}
	for _, r := range asMaps(lw["addable"]) {
		if id, ok := r["id"].(string); ok {
			laravelIDs[id] = true
		}
	}

	djangoDir := trackedProject(t, "django", []string{"django", "django-docker"})
	dw := decodeJSON(t, muxGet(testMux(), "/api/addable?dir="+djangoDir))
	djangoIDs := map[string]bool{}
	for _, r := range asMaps(dw["addable"]) {
		if id, ok := r["id"].(string); ok {
			djangoIDs[id] = true
		}
	}

	// The two frameworks must not offer an identical menu — the whole point is
	// that Add-service is scoped, not universal.
	if len(laravelIDs) == 0 || len(djangoIDs) == 0 {
		t.Skip("no addable recipes for one framework in this catalogue; nothing to compare")
	}
	same := len(laravelIDs) == len(djangoIDs)
	if same {
		for id := range laravelIDs {
			if !djangoIDs[id] {
				same = false
				break
			}
		}
	}
	if same {
		t.Errorf("laravel and django were offered the same add-service menu; the picker is not framework-scoped")
	}
}

// An untracked dir is refused for add too, with an empty list.
func TestAddableRejectsUntrackedDir(t *testing.T) {
	isolateConfig(t)
	w := muxGet(testMux(), "/api/addable?dir="+t.TempDir())
	m := decodeJSON(t, w)
	if _, ok := m["error"]; !ok {
		t.Errorf("an untracked dir should carry an error: %s", w.Body)
	}
	if raw, _ := m["addable"].([]any); len(raw) != 0 {
		t.Errorf("an untracked dir must offer nothing to add")
	}
}

// --- exec allowlist additions ------------------------------------------------

// The studio's exec allowlist must include `add` (the add-service + auth-stack
// path) and `gen` (the generator path). Anything else stays refused — the
// allowlist is the studio's exec safety boundary.
func TestExecAllowlistPermitsAddAndGen(t *testing.T) {
	for _, cmd := range []string{"add", "gen"} {
		if !execAllowed[cmd] {
			t.Errorf("the studio exec allowlist must permit %q so the Generate/Add-service surfaces work", cmd)
		}
	}
	for _, cmd := range []string{"rm", "new", "sh", "bash", "remove"} {
		if execAllowed[cmd] {
			t.Errorf("the studio exec allowlist must NOT permit %q", cmd)
		}
	}
}

// A disallowed command is refused by the live exec handler, streaming a "not
// allowed" line rather than running anything — the allowlist enforced end to end.
func TestExecHandlerRefusesDisallowedCommand(t *testing.T) {
	isolateConfig(t)
	w := muxPost(testMux(), "/api/exec", `{"dir":".","args":["rm","-rf","/"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("exec streams over SSE (200), got %d", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, "command not allowed") {
		t.Errorf("a disallowed command must be refused in the stream: %q", body)
	}
}

// asMaps decodes a JSON array of objects into []map[string]any.
func asMaps(v any) []map[string]any {
	raw, _ := v.([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		if m, ok := r.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
