package brand

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGlobalSaveLoadRoundTrip(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())

	// No file yet.
	if GlobalExists() {
		t.Fatal("no global default should exist yet")
	}
	if _, ok, err := LoadGlobal(); err != nil || ok {
		t.Fatalf("LoadGlobal on a fresh config: ok=%v err=%v, want false/nil", ok, err)
	}

	tk, _ := Generate("#5b21b6", "#3ab7bf")
	if err := SaveGlobal(tk); err != nil {
		t.Fatalf("SaveGlobal: %v", err)
	}
	if !GlobalExists() {
		t.Fatal("GlobalExists should be true after save")
	}

	got, ok, err := LoadGlobal()
	if err != nil || !ok {
		t.Fatalf("LoadGlobal after save: ok=%v err=%v", ok, err)
	}
	if got.Roles.Brand[500] != "#5b21b6" || got.Roles.Accent[500] != "#3ab7bf" {
		t.Fatalf("round-tripped tokens lost colours: %v", got.Roles.Brand)
	}
	if len(got.Roles.Brand) != len(scaleSteps) {
		t.Fatalf("round-tripped brand ramp truncated: %d stops", len(got.Roles.Brand))
	}
	if got.Seed.Primary != "#5b21b6" {
		t.Fatalf("seed not persisted: %+v", got.Seed)
	}
}

func TestGlobalPathNextToProfile(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("KEEL_CONFIG_DIR", cfg)
	if GlobalPath() != filepath.Join(cfg, "brand.yaml") {
		t.Fatalf("GlobalPath = %q, want %s/brand.yaml", GlobalPath(), cfg)
	}
}

func TestLoadGlobalCorruptFileErrors(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("KEEL_CONFIG_DIR", cfg)
	if err := os.WriteFile(filepath.Join(cfg, "brand.yaml"), []byte("::not yaml::\n\t- ["), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadGlobal(); err == nil {
		t.Fatal("a corrupt global brand file should error, not be silently ignored")
	}
}
