package brand

import (
	"os"
	"path/filepath"
	"testing"
)

// writeManifest drops a minimal .keel/manifest.yaml with an optional brand block
// so the resolver has a project override to read.
func writeManifestWithBrand(t *testing.T, dir, primary, accent string) {
	t.Helper()
	kd := filepath.Join(dir, ".keel")
	if err := os.MkdirAll(kd, 0o755); err != nil {
		t.Fatal(err)
	}
	// Hex values start with '#', which is a YAML comment unless quoted — the real
	// manifest writer (yaml.Marshal) quotes them; the hand-written fixture must too.
	body := "framework: nextjs\nenv: local\nrecipes:\n  - nextjs\n"
	if primary != "" {
		body += "brand:\n  primary: \"" + primary + "\"\n"
		if accent != "" {
			body += "  accent: \"" + accent + "\"\n"
		}
	}
	if err := os.WriteFile(filepath.Join(kd, "manifest.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveProjectWinsOverGlobal(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	// A global default exists…
	g, _ := Generate("#111111", "")
	if err := SaveGlobal(g); err != nil {
		t.Fatal(err)
	}
	// …but the project sets its own override, which must win.
	dir := t.TempDir()
	writeManifestWithBrand(t, dir, "#5b21b6", "#3ab7bf")

	r, err := Resolve(dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.Source != SourceProject {
		t.Fatalf("source = %q, want project", r.Source)
	}
	if r.Tokens.Roles.Brand[500] != "#5b21b6" {
		t.Fatalf("project override not used, brand 500 = %q", r.Tokens.Roles.Brand[500])
	}
	if r.Tokens.Roles.Accent[500] != "#3ab7bf" {
		t.Fatalf("project accent not used, accent 500 = %q", r.Tokens.Roles.Accent[500])
	}
}

func TestResolveFallsBackToGlobal(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	g, _ := Generate("#5b21b6", "")
	if err := SaveGlobal(g); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeManifestWithBrand(t, dir, "", "") // manifest present, no brand block

	r, err := Resolve(dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.Source != SourceGlobal {
		t.Fatalf("source = %q, want global", r.Source)
	}
	if r.Tokens.Roles.Brand[500] != "#5b21b6" {
		t.Fatalf("global default not used, brand 500 = %q", r.Tokens.Roles.Brand[500])
	}
}

func TestResolveKitWhenNothingSet(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	dir := t.TempDir() // no manifest, no global
	r, err := Resolve(dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.Source != SourceKit || r.HasTokens {
		t.Fatalf("with nothing set, want SourceKit/HasTokens=false, got %q/%v", r.Source, r.HasTokens)
	}
}

func TestProjectOverride(t *testing.T) {
	dir := t.TempDir()
	if _, ok := ProjectOverride(dir); ok {
		t.Fatal("no manifest -> no override")
	}
	writeManifestWithBrand(t, dir, "#5b21b6", "")
	seed, ok := ProjectOverride(dir)
	if !ok || seed.Primary != "#5b21b6" {
		t.Fatalf("ProjectOverride = %+v ok=%v", seed, ok)
	}
	if seed.Accent != "" {
		t.Fatalf("accent should be empty when the manifest omits it, got %q", seed.Accent)
	}
}
