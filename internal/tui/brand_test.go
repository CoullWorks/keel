package tui

import (
	"fmt"
	"testing"

	"github.com/coullworks/keel/internal/recipe"
)

// brandTestRegistry builds a tiny registry: a framework, a UI-kit add-on, and a
// plain add-on. Enough for chosenRecipes/hasUIKit to resolve ids to categories.
func brandTestRegistry(t *testing.T) *recipe.Registry {
	t.Helper()
	reg := recipe.NewRegistry()
	add := func(r recipe.Recipe) {
		if err := reg.Add(r); err != nil {
			t.Fatalf("add %s: %v", r.ID, err)
		}
	}
	add(recipe.Recipe{ID: "nextjs", Kind: recipe.Framework, Label: "Next.js"})
	add(recipe.Recipe{ID: "nextjs-tailwind", Kind: recipe.Addon, Label: "Tailwind", Category: uiKitCategory})
	add(recipe.Recipe{ID: "redis", Kind: recipe.Service, Label: "Redis", Category: "Cache"})
	return reg
}

// resultWith frames a recipe id at a recipe position (index >= 2), since
// chosenRecipes treats indices 0/1 as groupings, not recipes.
func resultWith(ids ...string) [][]string {
	res := [][]string{{"node"}, {"nextjs"}} // language, framework family
	for _, id := range ids {
		res = append(res, []string{id})
	}
	return res
}

func TestBrandChoicesSkipsWithoutUIKit(t *testing.T) {
	reg := brandTestRegistry(t)
	opts := brandChoices(reg, resultWith("nextjs", "redis"))
	if len(opts) != 1 || opts[0].Key != brandNone {
		t.Fatalf("no UI kit should collapse to a single skip option, got %+v", opts)
	}
}

func TestBrandChoicesOffersPalettesWithUIKit(t *testing.T) {
	reg := brandTestRegistry(t)
	opts := brandChoices(reg, resultWith("nextjs", "nextjs-tailwind"))
	if len(opts) != len(brandPresets)+1 {
		t.Fatalf("a UI kit should offer the palette + keep-defaults, got %d options", len(opts))
	}
	if opts[0].Key != brandNone || !opts[0].Selected {
		t.Fatalf("keep-defaults must be first and pre-selected: %+v", opts[0])
	}
	// Every preset key must decode to its hexes.
	for _, o := range opts[1:] {
		p, a := BrandFromResult([][]string{{o.Key}})
		if p == "" || a == "" {
			t.Fatalf("preset %q did not encode both colours: primary=%q accent=%q", o.Label, p, a)
		}
	}
}

func TestBrandFromResult(t *testing.T) {
	cases := []struct {
		name              string
		res               [][]string
		wantPrim, wantAcc string
	}{
		{"none is empty", [][]string{{brandNone}}, "", ""},
		{"no brand token", [][]string{{"nextjs"}, {"redis"}}, "", ""},
		{"primary + accent", [][]string{{brandToken("#4f46e5", "#06b6d4")}}, "#4f46e5", "#06b6d4"},
		{"primary only", [][]string{{brandToken("#4f46e5", "")}}, "#4f46e5", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, a := BrandFromResult(c.res)
			if p != c.wantPrim || a != c.wantAcc {
				t.Fatalf("BrandFromResult = %q/%q, want %q/%q", p, a, c.wantPrim, c.wantAcc)
			}
		})
	}
}

func TestBrandTokenDropsFromRecipeIDs(t *testing.T) {
	res := [][]string{{"node"}, {"nextjs"}, {"nextjs"}, {"nextjs-tailwind"}, {brandToken("#4f46e5", "#06b6d4")}}
	for _, id := range IDsFromResult(res) {
		if id == brandToken("#4f46e5", "#06b6d4") {
			t.Fatalf("a brand token must not appear in the recipe ids: %v", IDsFromResult(res))
		}
	}
}

func TestApplyBrandRunsPostBuild(t *testing.T) {
	restore := brandApply
	defer func() { brandApply = restore }()

	t.Run("applies the chosen colours", func(t *testing.T) {
		var gotDir, gotPrim, gotAcc string
		called := false
		brandApply = func(dir, primary, accent string) (brandResult, error) {
			called, gotDir, gotPrim, gotAcc = true, dir, primary, accent
			return brandResult{}, nil
		}
		res := [][]string{{brandToken("#4f46e5", "#06b6d4")}}
		if err := ApplyBrand("/tmp/app", res); err != nil {
			t.Fatalf("ApplyBrand: %v", err)
		}
		if !called || gotDir != "/tmp/app" || gotPrim != "#4f46e5" || gotAcc != "#06b6d4" {
			t.Fatalf("brand.Apply called wrong: called=%v dir=%q p=%q a=%q", called, gotDir, gotPrim, gotAcc)
		}
	})

	t.Run("no-op when defaults kept", func(t *testing.T) {
		called := false
		brandApply = func(dir, primary, accent string) (brandResult, error) {
			called = true
			return brandResult{}, nil
		}
		if err := ApplyBrand("/tmp/app", [][]string{{brandNone}}); err != nil {
			t.Fatalf("ApplyBrand: %v", err)
		}
		if called {
			t.Fatalf("keeping defaults must not call brand.Apply")
		}
	})

	t.Run("surfaces an apply error", func(t *testing.T) {
		brandApply = func(dir, primary, accent string) (brandResult, error) {
			return brandResult{}, fmt.Errorf("no CSS framework found")
		}
		if err := ApplyBrand("/tmp/app", [][]string{{brandToken("#4f46e5", "")}}); err == nil {
			t.Fatalf("an apply failure must surface")
		}
	})
}
