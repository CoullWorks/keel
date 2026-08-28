package gen

import (
	"testing"

	"github.com/coullworks/keel/internal/recipe"
)

// TestKeysAndSupports covers the small catalogue accessors the CLI relies on.
func TestKeysAndSupports(t *testing.T) {
	// laravel, magento and now django (its manage.py + template generators) are
	// supported; a framework with no registered stance (streamlit) is not.
	if !Supports("laravel") || !Supports("magento") || !Supports("django") || Supports("streamlit") {
		t.Fatal("Supports wrong for the framework families")
	}
	if len(Keys()) != len(LaravelComponents) {
		t.Fatalf("Keys length = %d, want %d", len(Keys()), len(LaravelComponents))
	}
	if len(MagentoKeys()) != len(MagentoComponents) {
		t.Fatalf("MagentoKeys length = %d, want %d", len(MagentoKeys()), len(MagentoComponents))
	}
}

// TestParseFieldsErrorPropagates covers the ParseFields error path (a bad entry
// aborts the whole parse).
func TestParseFieldsErrorPropagates(t *testing.T) {
	if _, err := ParseFields([]string{"good:string", "bad-entry"}); err == nil {
		t.Fatal("expected ParseFields to fail on a malformed entry")
	}
}

// TestRecipeGeneratableFallsBackToID covers labelOr: a generator recipe with no
// label surfaces under its id.
func TestRecipeGeneratableFallsBackToID(t *testing.T) {
	reg := recipe.NewRegistry()
	_ = reg.Add(recipe.Recipe{ID: "nextjs", Kind: recipe.Framework, Lang: "node"})
	_ = reg.Add(recipe.Recipe{ID: "gen-x", Kind: recipe.Generator, Level: recipe.LevelStack, AppliesTo: []string{"nextjs"}, Applies: []string{"y"}})
	for _, g := range Generatables(reg, "nextjs") {
		if g.Key == "gen-x" {
			if g.Label != "gen-x" {
				t.Fatalf("label = %q, want the id as fallback", g.Label)
			}
			return
		}
	}
	t.Fatal("gen-x generatable not found")
}
