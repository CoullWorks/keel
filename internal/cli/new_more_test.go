package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A profile whose default framework matches seeds its saved frontend + services
// + add-ons + extras into the plan (the profile-driven seedKind/frontend paths).
func TestNewSeedsProfileLayers(t *testing.T) {
	isolate(t)
	cfg := os.Getenv("KEEL_CONFIG_DIR")
	prof := "" +
		"defaults:\n" +
		"  framework: laravel\n" +
		"  services: redis\n" +
		"  addons: pest\n" +
		"  extras: git\n"
	if err := os.WriteFile(filepath.Join(cfg, "profile.yaml"), []byte(prof), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "new", "laravel", "--dry-run")
	if err != nil {
		t.Fatalf("new laravel (profile layers) --dry-run: %v", err)
	}
	mustContain(t, out, "laravel")
}

// A bare project name (no path separator) is placed under the profile's
// projects_dir. Dry-run keeps it inert but still resolves the target dir.
func TestNewBareNameUsesProjectsDir(t *testing.T) {
	wd := isolate(t)
	cfg := os.Getenv("KEEL_CONFIG_DIR")
	base := filepath.Join(wd, "code")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	prof := "defaults:\n  projects_dir: " + base + "\n"
	if err := os.WriteFile(filepath.Join(cfg, "profile.yaml"), []byte(prof), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "new", "laravel", "myshop", "--dry-run")
	if err != nil {
		t.Fatalf("new laravel myshop --dry-run: %v", err)
	}
	mustContain(t, out, "laravel")
}

// An invalid --with recipe id fails resolution before any build work.
func TestNewResolveError(t *testing.T) {
	isolate(t)
	_, err := runRoot(t, "new", "laravel", "--with", "no-such-recipe-xyz", "--dry-run")
	if err == nil {
		t.Fatal("expected a resolve error for an unknown --with recipe")
	}
}

// new refuses a directory name that would be unsafe as a path or working
// directory: traversal, shell metacharacters, and whitespace are rejected before
// anything is built, while a plain name and a normal path are accepted.
func TestNewRejectsUnsafeDirName(t *testing.T) {
	bad := []string{"../escape", "a;b", "my shop", "a|b", "a$(x)", "a&b", "a>b", "a\nb", "..", "sub/../..", "a`b`"}
	for _, d := range bad {
		t.Run(d, func(t *testing.T) {
			isolate(t)
			_, err := runRoot(t, "new", "laravel", d, "--dry-run", "--yes")
			if err == nil {
				t.Fatalf("new laravel %q should be rejected", d)
			}
			if !strings.Contains(err.Error(), "invalid project directory") {
				t.Errorf("error should name the invalid directory, got: %v", err)
			}
		})
	}
}

// validateProjectDir accepts legitimate names and paths and rejects the unsafe
// ones — the pure-function contract behind the command-level guard.
func TestValidateProjectDir(t *testing.T) {
	ok := []string{"", "myshop", "my-shop", "my_shop.v2", "~/code/myshop", "/abs/path/app", "a/b/c"}
	for _, d := range ok {
		if err := validateProjectDir(d); err != nil {
			t.Errorf("validateProjectDir(%q) = %v, want nil", d, err)
		}
	}
	bad := []string{"../escape", "..", "a;b", "my shop", "a|b", "a$x", "a/../b", "a\tb"}
	for _, d := range bad {
		if err := validateProjectDir(d); err == nil {
			t.Errorf("validateProjectDir(%q) = nil, want an error", d)
		}
	}
}

// new --with adds recipes to the plan (dry-run).
func TestNewWithAddons(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "new", "laravel", "--with", "pest", "--dry-run")
	if err != nil {
		t.Fatalf("new laravel --with pest --dry-run: %v", err)
	}
	mustContain(t, out, "laravel")
}

// new --env / --db flags feed the plan; an env that doesn't apply falls back to
// the framework default without erroring.
func TestNewEnvDbFlags(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "new", "laravel", "--env", "sail", "--db", "postgres", "--dry-run")
	if err != nil {
		t.Fatalf("new laravel --env sail --db postgres --dry-run: %v", err)
	}
	mustContain(t, out, "laravel")
}

// Every built-in framework must compose + preview under --dry-run without
// touching the network or Docker. This also exercises the framework-default
// frontend seeding (e.g. Magento → Hyvä, WooCommerce variants).
func TestNewDryRunAllFrameworks(t *testing.T) {
	reg, err := loadCatalog(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, fw := range frameworkIDs(reg) {
		t.Run(fw, func(t *testing.T) {
			isolate(t)
			out, err := runRoot(t, "new", fw, "--dry-run")
			if err != nil {
				t.Fatalf("new %s --dry-run: %v\n%s", fw, err, out)
			}
			mustContain(t, out, fw)
			if _, err := os.Stat(fw + "-app"); err == nil {
				t.Errorf("dry-run created %s-app", fw)
			}
		})
	}
}

// A full, real (non-dry-run) build of a trivial installed pack runs entirely
// offline: the pack's only install step is `echo`, and it needs no env tools, so
// this exercises build()'s write path end-to-end — preflight, ensureTools (noop),
// engine.Build, the post-build done message and open hooks — plus the --trust
// consent branch, without touching Docker or the network.
func TestNewBuildTrivialPack(t *testing.T) {
	wd := isolate(t)
	src := filepath.Join(t.TempDir(), "echopack")
	if err := scaffoldPack(io.Discard, src, "echopack"); err != nil {
		t.Fatalf("scaffoldPack: %v", err)
	}
	if _, err := runRoot(t, "recipes", "add", src); err != nil {
		t.Fatalf("recipes add: %v", err)
	}
	proj := filepath.Join(wd, "built")
	out, err := runRoot(t, "new", "echopack", "--yes", "--trust", "-o", proj)
	if err != nil {
		t.Fatalf("new echopack --yes --trust: %v\n%s", err, out)
	}
	mustContain(t, out, "echopack", "scaffolding echopack", "built")
	// The recipe drops a NOTES file into the project.
	if _, err := os.Stat(filepath.Join(proj, "NOTES-echopack.md")); err != nil {
		t.Errorf("recipe file not written: %v", err)
	}
	// A .keel manifest should exist (project is now keel-managed).
	if _, err := os.Stat(filepath.Join(proj, ".keel", "manifest.yaml")); err != nil {
		t.Errorf("manifest not written: %v", err)
	}
}

// A real build (--yes, not dry-run) blocks in pre-flight when a required host
// tool is missing — before any installer runs. Emptying PATH removes git (always
// required), so build() returns the pre-flight error. Covers build's pre-flight
// gate without ever running an installer.
func TestNewBuildPreflightBlocks(t *testing.T) {
	isolate(t)
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	_, err := runRoot(t, "new", "fastapi", "--yes", "-o", filepath.Join(t.TempDir(), "x"))
	if err == nil {
		t.Fatal("expected a pre-flight block on empty PATH")
	}
	mustContain(t, err.Error(), "missing required tool")
}

// contains helper.
func TestContains(t *testing.T) {
	if !contains([]string{"a", "b"}, "b") {
		t.Error("contains should find b")
	}
	if contains([]string{"a"}, "z") {
		t.Error("contains should not find z")
	}
	if contains(nil, "x") {
		t.Error("contains(nil) should be false")
	}
}

// first returns the first element of a wizard answer, "" for empty.
func TestFirst(t *testing.T) {
	if first([]string{"a", "b"}) != "a" {
		t.Error("first should return a")
	}
	if first(nil) != "" {
		t.Error("first(nil) should be empty")
	}
}
