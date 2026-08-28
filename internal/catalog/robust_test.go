package catalog

import (
	"bytes"
	"strings"
	"testing"
)

// TestRegistryDegradesOnBadUserRecipe is the robustness claim: one hand-edited or
// otherwise broken user recipe must not lock a user out of the whole catalog.
// Registry skips it (with a warning) and still returns the built-ins; only
// RegistryStrict (which `keel recipes validate` uses) treats it as an error.
func TestRegistryDegradesOnBadUserRecipe(t *testing.T) {
	tests := []struct {
		name string
		file string
		body string
	}{
		{
			name: "malformed yaml",
			file: "broken.yaml",
			body: "id: oops\n  this: : is not: valid yaml\n\tmixed tabs\n",
		},
		{
			name: "valid yaml but invalid recipe",
			file: "invalid.yaml",
			// Parses as YAML but fails recipe.Validate (no kind, no framework).
			body: "label: nonsense\nrandom: field\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
			dropRecipe(t, tc.file, tc.body)

			// Capture the warning instead of spamming the test's stderr.
			var buf bytes.Buffer
			old := warnW
			warnW = &buf
			t.Cleanup(func() { warnW = old })

			reg, err := Registry()
			if err != nil {
				t.Fatalf("Registry must not fail over one bad user recipe: %v", err)
			}
			// The built-ins are still there — a bad user file did not brick the catalog.
			if _, ok := reg.Get("laravel"); !ok {
				t.Fatal("built-in recipes should still load after skipping a bad user recipe")
			}
			if got := buf.String(); !strings.Contains(got, "skipping recipe") {
				t.Errorf("expected a skip warning, got %q", got)
			}

			// RegistryStrict, by contrast, surfaces the problem for `recipes validate`.
			if _, err := RegistryStrict(); err == nil {
				t.Error("RegistryStrict should report a broken user recipe, not skip it")
			}
		})
	}
}

// TestRegistryCleanWhenNoBadRecipes guards against a false positive: with only
// valid user recipes present, neither loader warns nor errors.
func TestRegistryCleanWhenNoBadRecipes(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	dropRecipe(t, "ok.yaml", `
id: robusttest
schema_version: 2
kind: addon
label: Robust Test Addon
appliesTo: [laravel]
`)
	var buf bytes.Buffer
	old := warnW
	warnW = &buf
	t.Cleanup(func() { warnW = old })

	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("robusttest"); !ok {
		t.Fatal("a valid user recipe should load")
	}
	if buf.Len() != 0 {
		t.Errorf("no warning expected for valid recipes, got %q", buf.String())
	}
	if _, err := RegistryStrict(); err != nil {
		t.Errorf("RegistryStrict should also succeed with only valid recipes: %v", err)
	}
}
