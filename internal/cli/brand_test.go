package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// brand applies the primary (+ optional accent) into the project's CSS framework.
// A Tailwind v4 entry (@import "tailwindcss") is the simplest detection path.
func TestBrandApplyTailwindV4(t *testing.T) {
	wd := isolate(t)
	css := filepath.Join(wd, "app.css")
	if err := os.WriteFile(css, []byte(`@import "tailwindcss";`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "brand", "#5b21b6", "#3ab7bf")
	if err != nil {
		t.Fatalf("brand: %v", err)
	}
	mustContain(t, out, "tailwind4", "app.css")

	b, _ := os.ReadFile(css)
	if !strings.Contains(string(b), "#5b21b6") || !strings.Contains(string(b), "#3ab7bf") {
		t.Errorf("brand colours not written: %q", b)
	}
}

// brand rejects a non-hex colour (Args pass, Apply validation fails).
func TestBrandBadHex(t *testing.T) {
	wd := isolate(t)
	if err := os.WriteFile(filepath.Join(wd, "app.css"), []byte(`@import "tailwindcss";`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := runRoot(t, "brand", "purple")
	if err == nil {
		t.Fatal("expected an error for a non-hex primary colour")
	}
	mustContain(t, err.Error(), "hex colour")
}

// brand errors clearly when the project has no Tailwind or Bootstrap setup.
func TestBrandNoUIKit(t *testing.T) {
	isolate(t)
	_, err := runRoot(t, "brand", "#5b21b6")
	if err == nil {
		t.Fatal("expected an error with no UI kit present")
	}
	mustContain(t, err.Error(), "no Tailwind or Bootstrap")
}

// `keel brand` now writes a full 50-950 scale, not a single shade.
func TestBrandWritesFullScale(t *testing.T) {
	wd := isolate(t)
	css := filepath.Join(wd, "app.css")
	if err := os.WriteFile(css, []byte(`@import "tailwindcss";`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runRoot(t, "brand", "#5b21b6"); err != nil {
		t.Fatalf("brand: %v", err)
	}
	b, _ := os.ReadFile(css)
	s := string(b)
	for _, want := range []string{"--color-brand-50:", "--color-brand-500: #5b21b6;", "--color-brand-950:", ".dark {"} {
		if !strings.Contains(s, want) {
			t.Errorf("full-scale output missing %q:\n%s", want, s)
		}
	}
}

// `keel brand set` writes the global default; `keel brand show` reads it back.
func TestBrandSetAndShowGlobal(t *testing.T) {
	isolate(t) // sets KEEL_CONFIG_DIR to a temp dir
	out, err := runRoot(t, "brand", "set", "#5b21b6", "#3ab7bf")
	if err != nil {
		t.Fatalf("brand set: %v", err)
	}
	mustContain(t, out, "global brand default saved", "primary=#5b21b6")

	// `brand show` (no project override) resolves to the global default.
	show, err := runRoot(t, "brand", "show")
	if err != nil {
		t.Fatalf("brand show: %v", err)
	}
	mustContain(t, show, "global default", "brand:", "500=#5b21b6")
}

// `keel brand` with no args shows the resolved brand (here: none set).
func TestBrandShowNoneSet(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "brand")
	if err != nil {
		t.Fatalf("brand: %v", err)
	}
	mustContain(t, out, "brand: none", "keel brand set")
}

// `keel brand apply --global` errors clearly when no global default is set.
func TestBrandApplyGlobalNoneSet(t *testing.T) {
	wd := isolate(t)
	if err := os.WriteFile(filepath.Join(wd, "app.css"), []byte(`@import "tailwindcss";`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := runRoot(t, "brand", "apply", "--global")
	if err == nil {
		t.Fatal("expected an error applying a non-existent global default")
	}
	mustContain(t, err.Error(), "no global brand default")
}

// `keel brand apply --global` applies the saved global default to the project.
func TestBrandApplyGlobal(t *testing.T) {
	wd := isolate(t)
	css := filepath.Join(wd, "app.css")
	if err := os.WriteFile(css, []byte(`@import "tailwindcss";`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runRoot(t, "brand", "set", "#5b21b6"); err != nil {
		t.Fatalf("brand set: %v", err)
	}
	out, err := runRoot(t, "brand", "apply", "--global")
	if err != nil {
		t.Fatalf("brand apply --global: %v", err)
	}
	mustContain(t, out, "brand applied", "global")
	b, _ := os.ReadFile(css)
	if !strings.Contains(string(b), "--color-brand-500: #5b21b6;") {
		t.Errorf("global brand not applied to CSS:\n%s", b)
	}
}

// --global and --project cannot be combined.
func TestBrandApplyMutuallyExclusive(t *testing.T) {
	isolate(t)
	_, err := runRoot(t, "brand", "apply", "--global", "--project")
	if err == nil {
		t.Fatal("expected an error combining --global and --project")
	}
	mustContain(t, err.Error(), "mutually exclusive")
}

// `keel brand apply --project` records the resolved seed as the project's
// override in the manifest, then applies it; `brand show` then reports it as a
// project override.
func TestBrandApplyProjectRecordsOverride(t *testing.T) {
	wd := isolate(t)
	css := filepath.Join(wd, "app.css")
	if err := os.WriteFile(css, []byte(`@import "tailwindcss";`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A tracked project with a manifest is required for --project to persist into.
	if err := os.MkdirAll(filepath.Join(wd, ".keel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, ".keel", "manifest.yaml"),
		[]byte("framework: nextjs\nenv: local\nrecipes:\n  - nextjs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Set a global default first — --project resolves it, then pins it locally.
	if _, err := runRoot(t, "brand", "set", "#5b21b6"); err != nil {
		t.Fatalf("brand set: %v", err)
	}
	out, err := runRoot(t, "brand", "apply", "--project")
	if err != nil {
		t.Fatalf("brand apply --project: %v", err)
	}
	mustContain(t, out, "brand applied", "project")

	// The manifest now carries the brand override (hex quoted by yaml.Marshal).
	m, _ := os.ReadFile(filepath.Join(wd, ".keel", "manifest.yaml"))
	mustContain(t, string(m), "brand:", "#5b21b6")

	// `brand show` now reports the source as the project override.
	show, err := runRoot(t, "brand", "show")
	if err != nil {
		t.Fatalf("brand show: %v", err)
	}
	mustContain(t, show, "project override")
}
