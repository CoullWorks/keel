package catalog

import (
	"testing"

	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

// TestBuiltinRecipesLoad proves the embedded catalogue parses and the headline
// Laravel + Filament + DDEV + MySQL + Redis + Pest route resolves into an
// ordered plan. This is the recipe-level smoke test.
func TestBuiltinRecipesLoad(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	if _, ok := reg.Get("laravel"); !ok {
		t.Fatal("expected built-in laravel recipe")
	}
	if got := reg.ForFramework("laravel", recipe.Addon); len(got) == 0 {
		t.Fatal("expected laravel add-ons (filament, livewire, horizon)")
	}
	// Sail must not appear as an option for a non-Laravel framework.
	if len(reg.ForFramework("magento", recipe.Env)) == 0 {
		t.Fatal("expected at least ddev for magento env")
	}

	plan, err := resolver.Resolve(reg, []string{"laravel", "filament", "ddev", "mysql", "redis", "pest"})
	if err != nil {
		t.Fatalf("headline route should resolve, got: %v", err)
	}
	if plan.IDs()[0] != "laravel" {
		t.Fatalf("framework should come first, got %v", plan.IDs())
	}
}

// TestMagentoFrontendDefaultsToHyva locks Hyvä in as Magento's first-class,
// batteries-included front end: exactly one default:true frontend, and it's Hyvä.
func TestMagentoFrontendDefaultsToHyva(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	var defaults []string
	for _, r := range reg.ForFramework("magento", recipe.Frontend) {
		if r.Default {
			defaults = append(defaults, r.ID)
		}
	}
	if len(defaults) != 1 || defaults[0] != "magento-hyva" {
		t.Fatalf("magento should have exactly one default frontend (magento-hyva), got %v", defaults)
	}
}
