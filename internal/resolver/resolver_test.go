package resolver

import (
	"testing"

	"github.com/coullworks/keel/internal/recipe"
)

func fixture() *recipe.Registry {
	reg := recipe.NewRegistry()
	_ = reg.Add(recipe.Recipe{ID: "laravel", Kind: recipe.Framework, Provides: []string{"php", "laravel"}})
	_ = reg.Add(recipe.Recipe{ID: "magento", Kind: recipe.Framework})
	_ = reg.Add(recipe.Recipe{ID: "filament", Kind: recipe.Addon, AppliesTo: []string{"laravel"}, Requires: []string{"db"}, Provides: []string{"admin-panel"}})
	_ = reg.Add(recipe.Recipe{ID: "ddev", Kind: recipe.Env, AppliesTo: []string{"laravel", "magento"}, Provides: []string{"env"}})
	_ = reg.Add(recipe.Recipe{ID: "sail", Kind: recipe.Env, AppliesTo: []string{"laravel"}, Provides: []string{"env"}})
	_ = reg.Add(recipe.Recipe{ID: "mysql", Kind: recipe.DB, AppliesTo: []string{"laravel", "magento"}, Provides: []string{"db"}})
	_ = reg.Add(recipe.Recipe{ID: "redis", Kind: recipe.Service, AppliesTo: []string{"laravel", "magento"}, Provides: []string{"redis"}})
	_ = reg.Add(recipe.Recipe{ID: "pest", Kind: recipe.Extra, AppliesTo: []string{"laravel"}, Provides: []string{"tests"}})
	return reg
}

func TestResolveValidPlanOrders(t *testing.T) {
	reg := fixture()
	// deliberately out of order; resolver must sort by kind
	plan, err := Resolve(reg, []string{"pest", "mysql", "filament", "ddev", "laravel", "redis"})
	if err != nil {
		t.Fatalf("expected valid plan, got %v", err)
	}
	if plan.Framework != "laravel" {
		t.Fatalf("framework = %s", plan.Framework)
	}
	want := []string{"laravel", "filament", "ddev", "mysql", "redis", "pest"}
	got := plan.IDs()
	if len(got) != len(want) {
		t.Fatalf("len want %d got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order[%d] want %s got %s (full %v)", i, want[i], got[i], got)
		}
	}
}

func TestResolveRequiresUnsatisfied(t *testing.T) {
	reg := fixture()
	// filament requires db, but no db selected
	if _, err := Resolve(reg, []string{"laravel", "filament", "ddev"}); err == nil {
		t.Fatal("expected error: filament requires db")
	}
}

func TestResolveFrameworkMismatch(t *testing.T) {
	reg := fixture()
	// sail does not apply to magento
	if _, err := Resolve(reg, []string{"magento", "sail"}); err == nil {
		t.Fatal("expected error: sail does not apply to magento")
	}
}

func TestResolveNoFramework(t *testing.T) {
	reg := fixture()
	if _, err := Resolve(reg, []string{"mysql", "redis"}); err == nil {
		t.Fatal("expected error: no framework selected")
	}
}

func TestResolveTwoFrameworks(t *testing.T) {
	reg := fixture()
	if _, err := Resolve(reg, []string{"laravel", "magento"}); err == nil {
		t.Fatal("expected error: two frameworks")
	}
}
