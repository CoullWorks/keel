package brand

import (
	"os"
	"path/filepath"
	"testing"
)

// ApplyAssets copies a logo + favicon into a project's public dir, and also to
// app/favicon.ico for a Next App Router project.
func TestApplyAssets(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	logo := filepath.Join(dir, "src-logo.svg")
	fav := filepath.Join(dir, "src-fav.ico")
	if err := os.WriteFile(logo, []byte("<svg/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fav, []byte("ICO"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := ApplyAssets(dir, logo, fav)
	if err != nil {
		t.Fatalf("ApplyAssets: %v", err)
	}
	for _, want := range []string{"public/brand-logo.svg", "public/favicon.ico", "app/favicon.ico"} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("missing %s: %v", want, err)
		}
	}
	if len(res.Written) != 3 {
		t.Errorf("expected 3 files written, got %v", res.Written)
	}
}

// With no public/static dir, ApplyAssets creates public/ rather than failing.
func TestApplyAssetsCreatesPublic(t *testing.T) {
	dir := t.TempDir()
	logo := filepath.Join(dir, "l.png")
	os.WriteFile(logo, []byte("PNG"), 0o644)
	if _, err := ApplyAssets(dir, logo, ""); err != nil {
		t.Fatalf("ApplyAssets: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "public", "brand-logo.png")); err != nil {
		t.Errorf("logo not placed in created public/: %v", err)
	}
}
