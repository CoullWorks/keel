package studio

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/brand"
	"github.com/coullworks/keel/internal/engine"
)

// writeTailwindV4 drops a Tailwind v4 CSS entry into dir so brand.Apply detects
// it and writes the @theme block.
func writeTailwindV4(t *testing.T, dir string) {
	t.Helper()
	css := filepath.Join(dir, "app.css")
	if err := os.WriteFile(css, []byte(`@import "tailwindcss";`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHandleBrandAppliesColours(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "laravel-docker", []string{"laravel", "postgres"})
	writeTailwindV4(t, dir)
	trackProject(t, dir)

	w := httptest.NewRecorder()
	handleBrand(w, httptest.NewRequest("POST", "http://127.0.0.1/api/brand",
		strings.NewReader(`{"dir":`+strconvQuote(dir)+`,"primary":"#5b21b6","accent":"#3ab7bf"}`)))
	m := decodeJSON(t, w)
	if err, ok := m["error"]; ok {
		t.Fatalf("brand apply should succeed: %v (%s)", err, w.Body.String())
	}
	if m["stack"] != "tailwind4" {
		t.Fatalf("stack = %v, want tailwind4", m["stack"])
	}
	// The colours must land in the CSS entry, not just be reported.
	out, err := os.ReadFile(filepath.Join(dir, "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "#5b21b6") || !strings.Contains(string(out), "#3ab7bf") {
		t.Fatalf("brand colours not written to CSS: %s", out)
	}
}

func TestHandleBrandRejectsBadColour(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "laravel-docker", []string{"laravel", "postgres"})
	writeTailwindV4(t, dir)
	trackProject(t, dir)

	w := httptest.NewRecorder()
	handleBrand(w, httptest.NewRequest("POST", "http://127.0.0.1/api/brand",
		strings.NewReader(`{"dir":`+strconvQuote(dir)+`,"primary":"purple"}`)))
	if err, _ := decodeJSON(t, w)["error"].(string); !strings.Contains(err, "hex colour") {
		t.Fatalf("a non-hex primary must be refused: %s", w.Body.String())
	}
}

func TestHandleBrandRejectsUntrackedDir(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir() // never tracked
	writeTailwindV4(t, dir)

	w := httptest.NewRecorder()
	handleBrand(w, httptest.NewRequest("POST", "http://127.0.0.1/api/brand",
		strings.NewReader(`{"dir":`+strconvQuote(dir)+`,"primary":"#5b21b6"}`)))
	if err, _ := decodeJSON(t, w)["error"].(string); !strings.Contains(err, "not a tracked keel project") {
		t.Fatalf("an untracked dir must be refused: %s", w.Body.String())
	}
}

func TestHandleBrandBadBody(t *testing.T) {
	w := httptest.NewRecorder()
	handleBrand(w, httptest.NewRequest("POST", "http://127.0.0.1/api/brand", strings.NewReader(`not json`)))
	if err, _ := decodeJSON(t, w)["error"].(string); err != "bad request" {
		t.Fatalf("malformed body should be a bad request, got %s", w.Body.String())
	}
}

// --- global default editor (Settings → Brand) --------------------------------

// TestBrandGlobalGetNoDefault: with no saved default, GET returns exists:false
// and a non-blank preview token set so the editor renders swatches immediately.
func TestBrandGlobalGetNoDefault(t *testing.T) {
	isolateConfig(t)
	w := httptest.NewRecorder()
	handleBrandGlobal(w, httptest.NewRequest("GET", "http://127.0.0.1/api/brand/global", nil))
	m := decodeJSON(t, w)
	if m["exists"] != false {
		t.Fatalf("exists should be false when no default saved: %s", w.Body.String())
	}
	if _, ok := m["tokens"].(map[string]any); !ok {
		t.Fatalf("a preview token set should still be returned: %s", w.Body.String())
	}
}

// TestBrandGlobalSaveThenGet: a POST persists the default (via brand.SaveGlobal),
// and a subsequent GET reports exists:true with the same seed.
func TestBrandGlobalSaveThenGet(t *testing.T) {
	isolateConfig(t)
	w := httptest.NewRecorder()
	handleBrandGlobal(w, httptest.NewRequest("POST", "http://127.0.0.1/api/brand/global",
		strings.NewReader(`{"primary":"#5b21b6","accent":"#f97316"}`)))
	m := decodeJSON(t, w)
	if m["saved"] != true || m["exists"] != true {
		t.Fatalf("POST should save and report exists: %s", w.Body.String())
	}
	if !brand.GlobalExists() {
		t.Fatal("brand.SaveGlobal was not called — no global file on disk")
	}
	// GET now reflects the saved default.
	w2 := httptest.NewRecorder()
	handleBrandGlobal(w2, httptest.NewRequest("GET", "http://127.0.0.1/api/brand/global", nil))
	m2 := decodeJSON(t, w2)
	if m2["exists"] != true {
		t.Fatalf("GET after save should report exists:true: %s", w2.Body.String())
	}
	tok, _ := m2["tokens"].(map[string]any)
	if tok["primary"] != "#5b21b6" {
		t.Fatalf("saved primary seed should round-trip, got %v", tok["primary"])
	}
}

// TestBrandGlobalPreviewDoesNotSave: preview:true generates swatches without
// touching ~/.config/keel/brand.yaml.
func TestBrandGlobalPreviewDoesNotSave(t *testing.T) {
	isolateConfig(t)
	w := httptest.NewRecorder()
	handleBrandGlobal(w, httptest.NewRequest("POST", "http://127.0.0.1/api/brand/global",
		strings.NewReader(`{"primary":"#123456","preview":true}`)))
	m := decodeJSON(t, w)
	if m["preview"] != true {
		t.Fatalf("preview flag should echo: %s", w.Body.String())
	}
	if brand.GlobalExists() {
		t.Fatal("a preview must not persist the global default")
	}
}

// TestBrandGlobalRejectsBadSeed: a non-hex primary is refused, nothing saved.
func TestBrandGlobalRejectsBadSeed(t *testing.T) {
	isolateConfig(t)
	w := httptest.NewRecorder()
	handleBrandGlobal(w, httptest.NewRequest("POST", "http://127.0.0.1/api/brand/global",
		strings.NewReader(`{"primary":"purple"}`)))
	if err, _ := decodeJSON(t, w)["error"].(string); !strings.Contains(err, "hex colour") {
		t.Fatalf("a non-hex primary must be refused: %s", w.Body.String())
	}
	if brand.GlobalExists() {
		t.Fatal("a rejected seed must not save a default")
	}
}

func TestBrandGlobalBadBody(t *testing.T) {
	isolateConfig(t)
	w := httptest.NewRecorder()
	handleBrandGlobal(w, httptest.NewRequest("POST", "http://127.0.0.1/api/brand/global", strings.NewReader(`nope`)))
	if err, _ := decodeJSON(t, w)["error"].(string); err != "bad request" {
		t.Fatalf("malformed body should be a bad request: %s", w.Body.String())
	}
}

// --- per-project override (project view → Brand tab) -------------------------

// TestBrandProjectGetResolvesGlobal: a tracked project with no override, when a
// global default exists, resolves to source:global and reports the detected kit.
func TestBrandProjectGetResolvesGlobal(t *testing.T) {
	isolateConfig(t)
	// A saved global default so resolution has a global layer to fall to.
	if err := brand.SaveGlobal(mustGen(t, "#0ea5e9", "")); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeManifest(t, dir, "laravel-docker", []string{"laravel", "postgres"})
	writeTailwindV4(t, dir)
	trackProject(t, dir)

	w := httptest.NewRecorder()
	handleBrandProject(w, httptest.NewRequest("GET",
		"http://127.0.0.1/api/brand/project?dir="+urlq(dir), nil))
	m := decodeJSON(t, w)
	if m["source"] != string(brand.SourceGlobal) {
		t.Fatalf("no override + a global default should resolve to global, got %v (%s)", m["source"], w.Body.String())
	}
	if m["stack"] != "tailwind4" {
		t.Fatalf("the tailwind v4 kit should be detected, got %v", m["stack"])
	}
	if _, ok := m["override"]; ok {
		t.Fatalf("a project with no override must not report one: %s", w.Body.String())
	}
}

// TestBrandProjectSetAppliesAndPersists: a POST writes the manifest brand block,
// resolves to source:project, and applies the theme to the CSS (colours land).
func TestBrandProjectSetAppliesAndPersists(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "laravel-docker", []string{"laravel", "postgres"})
	writeTailwindV4(t, dir)
	trackProject(t, dir)

	w := httptest.NewRecorder()
	handleBrandProject(w, httptest.NewRequest("POST", "http://127.0.0.1/api/brand/project",
		strings.NewReader(`{"dir":`+strconvQuote(dir)+`,"primary":"#5b21b6","accent":"#3ab7bf"}`)))
	m := decodeJSON(t, w)
	if err, ok := m["error"]; ok {
		t.Fatalf("setting an override should succeed: %v (%s)", err, w.Body.String())
	}
	if m["source"] != string(brand.SourceProject) {
		t.Fatalf("after setting an override the source must be project, got %v", m["source"])
	}
	if m["stack"] != "tailwind4" {
		t.Fatalf("stack = %v, want tailwind4 (%s)", m["stack"], w.Body.String())
	}
	// The override is recorded in the manifest.
	man, err := engine.ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if man.Brand == nil || man.Brand.Primary != "#5b21b6" {
		t.Fatalf("manifest brand override not written: %+v", man.Brand)
	}
	// The generated brand colour lands in the CSS (not just reported).
	out, err := os.ReadFile(filepath.Join(dir, "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "#5b21b6") {
		t.Fatalf("brand colour not written to CSS: %s", out)
	}
}

// TestBrandProjectClearReinheritsGlobal: after clearing, the manifest override is
// gone and resolution falls back to the global default.
func TestBrandProjectClearReinheritsGlobal(t *testing.T) {
	isolateConfig(t)
	if err := brand.SaveGlobal(mustGen(t, "#0ea5e9", "")); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeManifest(t, dir, "laravel-docker", []string{"laravel", "postgres"})
	writeTailwindV4(t, dir)
	trackProject(t, dir)
	// Set an override first.
	man, _ := engine.ReadManifest(dir)
	man.Brand = &engine.BrandRef{Primary: "#5b21b6"}
	if err := engine.WriteManifestFile(dir, man); err != nil {
		t.Fatal(err)
	}
	// Now clear it.
	w := httptest.NewRecorder()
	handleBrandProject(w, httptest.NewRequest("POST", "http://127.0.0.1/api/brand/project",
		strings.NewReader(`{"dir":`+strconvQuote(dir)+`,"clear":true}`)))
	m := decodeJSON(t, w)
	if err, ok := m["error"]; ok {
		t.Fatalf("clearing should succeed: %v (%s)", err, w.Body.String())
	}
	if m["source"] != string(brand.SourceGlobal) {
		t.Fatalf("after clearing, resolution must fall back to global, got %v", m["source"])
	}
	man2, _ := engine.ReadManifest(dir)
	if man2.Brand != nil {
		t.Fatalf("clear must remove the manifest override, got %+v", man2.Brand)
	}
}

// TestBrandProjectRejectsBadSeed: a non-hex primary is refused and nothing is
// written to the manifest.
func TestBrandProjectRejectsBadSeed(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "laravel-docker", []string{"laravel", "postgres"})
	writeTailwindV4(t, dir)
	trackProject(t, dir)

	w := httptest.NewRecorder()
	handleBrandProject(w, httptest.NewRequest("POST", "http://127.0.0.1/api/brand/project",
		strings.NewReader(`{"dir":`+strconvQuote(dir)+`,"primary":"nope"}`)))
	if err, _ := decodeJSON(t, w)["error"].(string); !strings.Contains(err, "hex colour") {
		t.Fatalf("a non-hex seed must be refused: %s", w.Body.String())
	}
	man, _ := engine.ReadManifest(dir)
	if man.Brand != nil {
		t.Fatalf("a rejected seed must not write the manifest: %+v", man.Brand)
	}
}

func TestBrandProjectRejectsUntrackedDir(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir() // never tracked
	w := httptest.NewRecorder()
	handleBrandProject(w, httptest.NewRequest("POST", "http://127.0.0.1/api/brand/project",
		strings.NewReader(`{"dir":`+strconvQuote(dir)+`,"primary":"#5b21b6"}`)))
	if err, _ := decodeJSON(t, w)["error"].(string); !strings.Contains(err, "not a tracked keel project") {
		t.Fatalf("an untracked dir must be refused: %s", w.Body.String())
	}
}

func TestBrandProjectBadBody(t *testing.T) {
	w := httptest.NewRecorder()
	handleBrandProject(w, httptest.NewRequest("POST", "http://127.0.0.1/api/brand/project", strings.NewReader(`bad`)))
	if err, _ := decodeJSON(t, w)["error"].(string); err != "bad request" {
		t.Fatalf("malformed body should be a bad request: %s", w.Body.String())
	}
}

// TestBrandProjectGetSurfacesMagentoThemes: a Magento project's Brand tab GET
// carries a `magento` block with every frontend theme's resolved swatches, so the
// UI can render a theme picker.
func TestBrandProjectGetSurfacesMagentoThemes(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "magento", []string{"magento"})
	trackProject(t, dir)
	// A theme with a real brand var.
	base := filepath.Join(dir, "app", "design", "frontend", "Acme", "theme")
	if err := os.MkdirAll(filepath.Join(base, "web", "css", "source"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "theme.xml"),
		[]byte("<theme><title>Acme</title><parent>Magento/luma</parent></theme>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "web", "css", "source", "_theme.less"),
		[]byte("@primary__color: #5b21b6;"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	handleBrandProject(w, httptest.NewRequest("GET",
		"http://127.0.0.1/api/brand/project?dir="+urlq(dir), nil))
	m := decodeJSON(t, w)
	mg, ok := m["magento"].(map[string]any)
	if !ok {
		t.Fatalf("a Magento project should carry a magento block: %s", w.Body.String())
	}
	themes, ok := mg["themes"].([]any)
	if !ok || len(themes) < 1 {
		t.Fatalf("expected at least the Acme magento theme, got %v", mg["themes"])
	}
	// A theme inheriting Magento/luma with no vendor/ present also surfaces the
	// Luma fallback theme, so find Acme by path rather than assume it is the only one.
	var th map[string]any
	for _, t0 := range themes {
		tm := t0.(map[string]any)
		if tm["path"] == "Acme/theme" {
			th = tm
			break
		}
	}
	if th == nil {
		t.Fatalf("Acme/theme should be among the magento themes, got %v", mg["themes"])
	}
	roles := th["roles"].(map[string]any)
	brandRamp := roles["brand"].([]any)
	if len(brandRamp) == 0 {
		t.Fatalf("brand var should resolve to a swatch, got %v", roles["brand"])
	}
	stop := brandRamp[0].(map[string]any)
	if stop["hex"] != "#5b21b6" {
		t.Fatalf("@primary__color should resolve to #5b21b6, got %v", stop["hex"])
	}
}

// TestBrandProjectGetNoMagentoBlockForNonMagento: a non-Magento project's GET
// must not carry a magento block, so the picker only appears where it makes sense.
func TestBrandProjectGetNoMagentoBlockForNonMagento(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "laravel-docker", []string{"laravel", "postgres"})
	writeTailwindV4(t, dir)
	trackProject(t, dir)

	w := httptest.NewRecorder()
	handleBrandProject(w, httptest.NewRequest("GET",
		"http://127.0.0.1/api/brand/project?dir="+urlq(dir), nil))
	m := decodeJSON(t, w)
	if _, ok := m["magento"]; ok {
		t.Fatalf("a non-Magento project must not carry a magento block: %s", w.Body.String())
	}
}

// mustGen builds a token set or fails the test — a helper for seeding a global
// default in tests.
func mustGen(t *testing.T, primary, accent string) brand.BrandTokens {
	t.Helper()
	tk, err := brand.Generate(primary, accent)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

// urlq is a tiny query-escape helper so the ?dir= tests don't drag in net/url
// everywhere.
func urlq(s string) string {
	r := strings.NewReplacer("/", "%2F", " ", "%20")
	return r.Replace(s)
}
