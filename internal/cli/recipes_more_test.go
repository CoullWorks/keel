package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/catalog"
)

// recipes (bare) prints its help/subcommand list.
func TestRecipesBare(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "recipes")
	if err != nil {
		t.Fatalf("recipes: %v", err)
	}
	mustContain(t, out, "list", "add", "validate", "freshness")
}

// recipes list renders the built-in recipe table with source/trust columns.
func TestRecipesListRenders(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "recipes", "list")
	if err != nil {
		t.Fatalf("recipes list: %v", err)
	}
	mustContain(t, out, "laravel", "django")
}

// recipes verify --help documents the smoke-test flow (we never run the real
// installers here — that needs Docker/network).
func TestRecipesVerifyHelp(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "recipes", "verify", "--help")
	if err != nil {
		t.Fatalf("recipes verify --help: %v", err)
	}
	mustContain(t, out, "smoke", "--with", "--env")
}

// recipes verify with a non-framework arg errors before touching the host.
func TestRecipesVerifyNotAFramework(t *testing.T) {
	isolate(t)
	_, err := runRoot(t, "recipes", "verify", "not-a-framework")
	if err == nil {
		t.Fatal("expected error for a non-framework arg")
	}
	mustContain(t, err.Error(), "is not a framework")
}

// recipes search --help documents the GitHub topic search (no network in tests).
func TestRecipesSearchHelp(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "recipes", "search", "--help")
	if err != nil {
		t.Fatalf("recipes search --help: %v", err)
	}
	mustContain(t, out, "keel-recipes")
}

// recipes add with a bogus source errors (a local git clone that can't resolve —
// no network needed).
func TestRecipesAddBadSource(t *testing.T) {
	isolate(t)
	_, err := runRoot(t, "recipes", "add", filepath.Join(t.TempDir(), "nope-not-a-repo"))
	if err == nil {
		t.Fatal("expected error adding a non-existent source")
	}
}

// recipes add on a local path that exists but isn't a pack errors clearly.
func TestRecipesAddNotAPack(t *testing.T) {
	isolate(t)
	// A real git repo with no keel.pack.yaml.
	repo := t.TempDir()
	if _, err := runRoot(t, "recipes", "add", repo); err == nil {
		t.Fatal("expected error for a non-pack source")
	}
}

// validatePackDir rejects a directory with no keel.pack.yaml.
func TestValidatePackDirNotAPack(t *testing.T) {
	isolate(t)
	if err := validatePackDir(io.Discard, t.TempDir()); err == nil {
		t.Fatal("expected error validating a non-pack dir")
	}
}

// recipes validate on a scaffolded pack dir passes end-to-end.
func TestRecipesValidateScaffoldedPack(t *testing.T) {
	wd := isolate(t)
	if _, err := runRoot(t, "new-recipe", "vpack", "--pack"); err != nil {
		t.Fatalf("new-recipe pack: %v", err)
	}
	out, err := runRoot(t, "recipes", "validate", filepath.Join(wd, "vpack"))
	if err != nil {
		t.Fatalf("validate pack: %v", err)
	}
	mustContain(t, out, "pack vpack", "valid")
}

// recipes remove --yes on a missing pack errors (covered) — here confirm the
// remove path with a no answer isn't reachable non-interactively, so we only
// assert the not-installed branch again through the top-level command with rm alias.
func TestRecipesRemoveAliasMissing(t *testing.T) {
	isolate(t)
	_, err := runRoot(t, "recipes", "rm", "ghost-pack", "--yes")
	if err == nil {
		t.Fatal("expected error for a missing pack via rm alias")
	}
	mustContain(t, err.Error(), "not installed")
}

// new-recipe scaffolds a single starter recipe file.
func TestNewRecipeSingle(t *testing.T) {
	wd := isolate(t)
	out, err := runRoot(t, "new-recipe", "mystack")
	if err != nil {
		t.Fatalf("new-recipe: %v", err)
	}
	mustContain(t, out, "starter recipe created", "mystack.yaml")
	if _, err := os.Stat(filepath.Join(wd, "mystack", "mystack.yaml")); err != nil {
		t.Errorf("recipe file not written: %v", err)
	}
}

// new-recipe --pack scaffolds a full pack layout.
func TestNewRecipePack(t *testing.T) {
	wd := isolate(t)
	out, err := runRoot(t, "new-recipe", "mypack", "--pack")
	if err != nil {
		t.Fatalf("new-recipe --pack: %v", err)
	}
	mustContain(t, out, "pack scaffolded")
	for _, f := range []string{"keel.pack.yaml", "recipes/mypack.yaml", "hooks/post_create.sh", "README.md"} {
		if _, err := os.Stat(filepath.Join(wd, "mypack", f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
}

// new-recipe with an explicit target dir (-o) honours it.
func TestNewRecipeDir(t *testing.T) {
	wd := isolate(t)
	target := filepath.Join(wd, "custom")
	if _, err := runRoot(t, "new-recipe", "here", "-o", target); err != nil {
		t.Fatalf("new-recipe -o: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "here.yaml")); err != nil {
		t.Errorf("recipe not written to -o dir: %v", err)
	}
}

// new-recipe --kind rejects a kind outside the closed set, before scaffolding
// anything, rather than writing a recipe with a kind nothing resolves.
func TestNewRecipeRejectsBadKind(t *testing.T) {
	wd := isolate(t)
	_, err := runRoot(t, "new-recipe", "x", "--kind", "notakind")
	if err == nil {
		t.Fatal("new-recipe --kind notakind should be rejected")
	}
	if !strings.Contains(err.Error(), "unknown recipe kind") {
		t.Errorf("error should name the bad kind, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(wd, "x")); statErr == nil {
		t.Error("new-recipe scaffolded despite the invalid kind")
	}
}

// new-recipe --kind accepts every kind in the closed set.
func TestNewRecipeAcceptsValidKinds(t *testing.T) {
	for _, k := range []string{"framework", "addon", "env", "db", "service", "frontend", "extra", "generator"} {
		t.Run(k, func(t *testing.T) {
			isolate(t)
			if _, err := runRoot(t, "new-recipe", "r-"+k, "--kind", k); err != nil {
				t.Fatalf("new-recipe --kind %s should be accepted: %v", k, err)
			}
		})
	}
}

// starterRecipe substitutes name + kind into the template.
func TestStarterRecipe(t *testing.T) {
	s := starterRecipe("demo", "addon")
	mustContain(t, s, "id: demo", "kind: addon")
}

// chooseEnv prefers the requested env, else the default, else the first.
func TestChooseEnv(t *testing.T) {
	isolate(t)
	reg, err := catalog.Registry()
	if err != nil {
		t.Fatal(err)
	}
	// Laravel has a ddev default env.
	if got := chooseEnv(reg, "laravel", "ddev"); got != "ddev" {
		t.Errorf("chooseEnv preferred = %q, want ddev", got)
	}
	if got := chooseEnv(reg, "laravel", ""); got == "" {
		t.Error("chooseEnv should pick a default env for laravel")
	}
	if got := chooseEnv(reg, "no-such-fw", ""); got != "" {
		t.Errorf("chooseEnv for unknown fw = %q, want empty", got)
	}
}
