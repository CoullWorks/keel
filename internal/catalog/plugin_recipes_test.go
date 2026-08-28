package catalog

import (
	"testing"

	"github.com/coullworks/keel/internal/recipe"
)

// A plugin's recipes are folded into the catalog through the pluginRecipes seam,
// stamped Source="plugin:<name>" so the engine trust-gates them exactly like a
// pack. This proves the bridge admits a valid plugin recipe and preserves its
// provenance without any plugin on disk. It drives the seam directly, so it stays
// in the internal test package where the seam var is reachable.
func TestPluginRecipesAreFoldedIn(t *testing.T) {
	swapPluginRecipes(t, []recipe.Recipe{{
		ID:     "widget",
		Kind:   recipe.Generator,
		Label:  "A plugin-provided generator",
		Level:  recipe.LevelCodeBlock,
		Source: "plugin:demo",
		Pack:   "demo",
	}})

	reg, err := Registry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	got, ok := reg.Get("widget")
	if !ok {
		t.Fatal("a plugin's recipe was not folded into the catalog")
	}
	if got.Source != "plugin:demo" {
		t.Errorf("source = %q, want plugin:demo so the engine trust-gates it", got.Source)
	}
	// A built-in is still present: the fold adds, it does not replace.
	if _, ok := reg.Get("laravel"); !ok {
		t.Error("folding plugin recipes dropped the built-in catalogue")
	}
}

// A plugin recipe that does not validate is dropped, not fatal: one broken plugin
// recipe must never lock a user out of the whole catalogue.
func TestBrokenPluginRecipeIsDroppedNotFatal(t *testing.T) {
	swapPluginRecipes(t, []recipe.Recipe{
		{ID: "", Kind: recipe.Addon, Source: "plugin:demo"},                    // no id: invalid
		{ID: "good", Kind: recipe.Addon, Label: "fine", Source: "plugin:demo"}, // valid
	})

	reg, err := Registry()
	if err != nil {
		t.Fatalf("a broken plugin recipe should not fail the catalog, got: %v", err)
	}
	if _, ok := reg.Get("good"); !ok {
		t.Error("the valid plugin recipe should still be admitted alongside the broken one")
	}
}

// swapPluginRecipes replaces the plugin-recipe seam with a fixed set for the test
// and restores it afterwards, so the bridge is exercised without a plugin on disk.
func swapPluginRecipes(t *testing.T, rs []recipe.Recipe) {
	t.Helper()
	prev := pluginRecipes
	pluginRecipes = func() []recipe.Recipe { return rs }
	t.Cleanup(func() { pluginRecipes = prev })
}
