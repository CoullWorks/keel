package catalog

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/pack"
	"github.com/coullworks/keel/internal/recipe"
)

// writeFile is a helper that writes content to a path under dir, creating parent
// directories as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRegistryLoadsBuiltins proves the embedded catalogue loads with no user
// config dir present (KEEL_CONFIG_DIR points at an empty temp dir, so os.ReadDir
// of <dir>/recipes fails and Registry returns just the built-ins).
func TestRegistryLoadsBuiltins(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir()) // exists but has no recipes/ subdir
	reg, err := Registry()
	if err != nil {
		t.Fatalf("Registry: %v", err)
	}
	if _, ok := reg.Get("laravel"); !ok {
		t.Fatal("expected built-in laravel recipe")
	}
	if reg.Len() == 0 {
		t.Fatal("expected built-in recipes to load")
	}
}

// TestRegistryLooseUserRecipe: a loose YAML in <config>/recipes is loaded as a
// "user" recipe and, when it reuses a built-in id, overrides it.
func TestRegistryLooseUserRecipe(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("KEEL_CONFIG_DIR", cfg)
	recipesDir := filepath.Join(cfg, "recipes")

	// A brand-new user recipe (.yaml).
	writeFile(t, filepath.Join(recipesDir, "my-fw.yaml"),
		"id: my-fw\nkind: framework\nlabel: My Framework\nlang: go\n")
	// A .yml that overrides the built-in laravel label — proves override-by-id
	// and that both .yaml and .yml extensions are picked up.
	writeFile(t, filepath.Join(recipesDir, "override.yml"),
		"id: laravel\nkind: framework\nlabel: Overridden Laravel\n")

	reg, err := Registry()
	if err != nil {
		t.Fatalf("Registry: %v", err)
	}

	got, ok := reg.Get("my-fw")
	if !ok {
		t.Fatal("expected loose user recipe my-fw to load")
	}
	if got.Source != "user" {
		t.Errorf("loose user recipe Source = %q, want %q", got.Source, "user")
	}

	lar, ok := reg.Get("laravel")
	if !ok {
		t.Fatal("laravel should still exist")
	}
	if lar.Label != "Overridden Laravel" {
		t.Errorf("user recipe should override built-in label, got %q", lar.Label)
	}
	if lar.Source != "user" {
		t.Errorf("overridden laravel Source = %q, want %q", lar.Source, "user")
	}
}

// TestRegistryUserSubdirNoManifest: a subdir without keel.pack.yaml is loaded as
// a plain "user" source directory.
func TestRegistryUserSubdirNoManifest(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("KEEL_CONFIG_DIR", cfg)
	writeFile(t, filepath.Join(cfg, "recipes", "mystack", "thing.yaml"),
		"id: user-subdir-fw\nkind: framework\nlabel: Sub\n")

	reg, err := Registry()
	if err != nil {
		t.Fatalf("Registry: %v", err)
	}
	got, ok := reg.Get("user-subdir-fw")
	if !ok {
		t.Fatal("expected recipe from user subdir to load")
	}
	if got.Source != "user" {
		t.Errorf("subdir recipe Source = %q, want %q", got.Source, "user")
	}
	if got.Pack != "" {
		t.Errorf("non-pack subdir recipe Pack = %q, want empty", got.Pack)
	}
}

// TestRegistryPackSubdir: a subdir containing keel.pack.yaml is loaded as a pack,
// stamped Source="pack:<name>" and Pack=<name>, and its manifest is skipped (not
// parsed as a recipe).
func TestRegistryPackSubdir(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("KEEL_CONFIG_DIR", cfg)
	packDir := filepath.Join(cfg, "recipes", "acme")
	writeFile(t, filepath.Join(packDir, "keel.pack.yaml"),
		"schema_version: 1\nname: acme\nversion: 1.0.0\n")
	writeFile(t, filepath.Join(packDir, "cool.yaml"),
		"id: acme-cool\nkind: addon\nlabel: Cool\n")

	reg, err := Registry()
	if err != nil {
		t.Fatalf("Registry: %v", err)
	}
	got, ok := reg.Get("acme-cool")
	if !ok {
		t.Fatal("expected pack recipe acme-cool to load")
	}
	if got.Source != "pack:acme" {
		t.Errorf("pack recipe Source = %q, want %q", got.Source, "pack:acme")
	}
	if got.Pack != "acme" {
		t.Errorf("pack recipe Pack = %q, want %q", got.Pack, "acme")
	}
	// The manifest must not have been ingested as a recipe.
	if _, ok := reg.Get("acme"); ok {
		t.Error("keel.pack.yaml manifest should not load as a recipe")
	}
}

// TestDisabledPackContributesNoRecipes: a pack switched off keeps its files but
// its recipes leave the catalog, and rejoin when it is turned back on — the pack
// twin of a disabled plugin.
func TestDisabledPackContributesNoRecipes(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("KEEL_CONFIG_DIR", cfg)
	packDir := filepath.Join(cfg, "recipes", "acme")
	writeFile(t, filepath.Join(packDir, "keel.pack.yaml"), "schema_version: 1\nname: acme\nversion: 1.0.0\n")
	writeFile(t, filepath.Join(packDir, "cool.yaml"), "id: acme-cool\nkind: addon\nlabel: Cool\n")

	// Register it in packs.yaml so it can be toggled — a disable targets an
	// installed pack, exactly as the CLI/studio does after `keel recipes add`.
	reg, err := pack.Load()
	if err != nil {
		t.Fatal(err)
	}
	reg.Upsert(pack.Installed{Name: "acme", Version: "1.0.0"})
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}

	// Enabled by default: the recipe is in the catalog.
	cat, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cat.Get("acme-cool"); !ok {
		t.Fatal("an enabled pack's recipe should reach the catalog")
	}

	// Disabled: the recipe leaves.
	if err := pack.SetEnabled("acme", false); err != nil {
		t.Fatal(err)
	}
	cat, err = Registry()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cat.Get("acme-cool"); ok {
		t.Error("a disabled pack's recipe reached the catalog anyway")
	}

	// Re-enabled: the recipe returns, proving the toggle is not one-way.
	if err := pack.SetEnabled("acme", true); err != nil {
		t.Fatal(err)
	}
	cat, _ = Registry()
	if _, ok := cat.Get("acme-cool"); !ok {
		t.Error("a re-enabled pack's recipe did not return to the catalog")
	}
}

// TestRegistryErrors exercises the strict-load error branches: a malformed loose
// recipe, and a malformed recipe inside a subdir/pack. RegistryStrict (used by
// `keel recipes validate`) surfaces these; plain Registry skips them with a
// warning (covered by TestRegistryDegradesOnBadUserRecipe) so one bad user file
// cannot brick the catalog.
func TestRegistryErrors(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string // path (relative to <config>/recipes) -> content
	}{
		{
			// Invalid YAML in a loose file -> AddYAML unmarshal error.
			name:  "loose invalid yaml",
			files: map[string]string{"bad.yaml": "id: [unterminated\n"},
		},
		{
			// Well-formed YAML but an invalid recipe (unknown kind) -> Validate error.
			name:  "loose invalid recipe",
			files: map[string]string{"bad.yaml": "id: x\nkind: nonsense\n"},
		},
		{
			// Invalid recipe inside a plain subdir -> LoadInto error path.
			name:  "subdir invalid recipe",
			files: map[string]string{"sub/bad.yaml": "id: x\nkind: nonsense\n"},
		},
		{
			// Invalid recipe inside a pack subdir -> LoadInto error path (pack source).
			name: "pack invalid recipe",
			files: map[string]string{
				"pk/keel.pack.yaml": "name: pk\nversion: 1.0.0\n",
				"pk/bad.yaml":       "id: x\nkind: nonsense\n",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := t.TempDir()
			t.Setenv("KEEL_CONFIG_DIR", cfg)
			for rel, content := range tc.files {
				writeFile(t, filepath.Join(cfg, "recipes", rel), content)
			}
			// Strict mode reports the bad recipe...
			if _, err := RegistryStrict(); err == nil {
				t.Fatal("expected RegistryStrict to return an error")
			}
			// ...while plain Registry degrades: it skips the bad file (with a
			// warning to warnW) and still succeeds, so the user keeps the built-ins.
			var buf bytes.Buffer
			old := warnW
			warnW = &buf
			t.Cleanup(func() { warnW = old })
			if _, err := Registry(); err != nil {
				t.Fatalf("Registry should skip a bad recipe, not fail: %v", err)
			}
			if !strings.Contains(buf.String(), "skipping recipe") {
				t.Errorf("expected a skip warning, got %q", buf.String())
			}
		})
	}
}

// TestRegistryLooseReadError covers the branch where a loose *.yaml entry exists
// but can't be read: a dangling symlink named *.yaml is not a dir, matches the
// suffix, and fails os.ReadFile.
func TestRegistryLooseReadError(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("KEEL_CONFIG_DIR", cfg)
	recipesDir := filepath.Join(cfg, "recipes")
	if err := os.MkdirAll(recipesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(recipesDir, "dangling.yaml")
	if err := os.Symlink(filepath.Join(cfg, "does-not-exist"), link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	// Strict mode errors on an unreadable loose recipe...
	if _, err := RegistryStrict(); err == nil {
		t.Fatal("expected RegistryStrict to error on an unreadable loose recipe")
	}
	// ...but plain Registry skips it with a warning and still loads the rest.
	var buf bytes.Buffer
	old := warnW
	warnW = &buf
	t.Cleanup(func() { warnW = old })
	if _, err := Registry(); err != nil {
		t.Fatalf("Registry should skip an unreadable loose recipe, not fail: %v", err)
	}
}

// TestRegistryFamiliesAndKinds sanity-checks the registry query surface the CLI
// relies on against the loaded built-ins: framework families collapse variants,
// and OfKind is non-empty for the core kinds.
func TestRegistryFamiliesAndKinds(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	reg, err := Registry()
	if err != nil {
		t.Fatalf("Registry: %v", err)
	}

	frameworks := reg.OfKind(recipe.Framework)
	if len(frameworks) == 0 {
		t.Fatal("expected framework recipes")
	}
	// Families must cover every framework recipe exactly once.
	fams := recipe.Families(frameworks)
	total := 0
	for _, f := range fams {
		if len(f.Variants) == 0 {
			t.Errorf("family %q has no variants", f.Key)
		}
		total += len(f.Variants)
	}
	if total != len(frameworks) {
		t.Errorf("Families dropped/duplicated recipes: %d variants for %d frameworks", total, len(frameworks))
	}

	for _, k := range []recipe.Kind{recipe.Framework, recipe.Env, recipe.DB, recipe.Addon} {
		if len(reg.OfKind(k)) == 0 {
			t.Errorf("expected at least one recipe of kind %q", k)
		}
	}
}
