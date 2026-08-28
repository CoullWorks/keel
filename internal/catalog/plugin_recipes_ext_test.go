package catalog_test

// These are the end-to-end plugin-recipe tests. They import the plugin registry
// (whose init registers the real fold into the catalog), so they live in the
// external test package: importing internal/plugins from `package catalog` would
// be an import cycle, since plugins imports catalog.

import (
	"testing"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/plugins" // its init registers the plugin-recipe fold
	"github.com/coullworks/keel/internal/plugintest"
)

// keep the plugins import referenced even though it is used only for its init, so
// goimports does not drop it.
var _ = plugins.SupportedSchema

// End-to-end: a discovered, enabled plugin's shipped recipe reaches the real
// catalog through the live plugin registry, not just the seam. keel ships zero
// built-ins, so the test discovers a real on-disk plugin that ships a recipe in
// its recipes/ directory; enabling it (the default) makes that recipe appear in
// catalog.Registry(), stamped with its plugin provenance — the "a pack is a plugin
// that only ships recipes" mechanism, proven end to end.
func TestEnabledPluginRecipeReachesTheCatalog(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	plugintest.Install(t, "demo") // discovered and enabled by default; ships recipes/demo-stack.yaml

	reg, err := catalog.Registry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	r, ok := reg.Get("demo-stack")
	if !ok {
		t.Fatal("an enabled plugin's shipped recipe did not reach the catalog")
	}
	if r.Source != "plugin:demo" {
		t.Errorf("source = %q, want plugin:demo", r.Source)
	}
}

// A disabled plugin contributes no recipes: the fold honours the on/off switch.
func TestDisabledPluginContributesNoRecipes(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	f := plugintest.Install(t, "demo")
	f.Enable(false) // switched off, so it must contribute nothing

	reg, err := catalog.Registry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	if _, ok := reg.Get("demo-stack"); ok {
		t.Error("a disabled plugin's recipe reached the catalog anyway")
	}
}
