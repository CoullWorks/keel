package gen

import (
	"testing"

	"github.com/coullworks/keel/internal/recipe"
)

// testRegistry builds a minimal registry with a framework and two generator
// recipes, so the catalogue tests do not depend on the embedded catalogue.
func testRegistry(t *testing.T) *recipe.Registry {
	t.Helper()
	reg := recipe.NewRegistry()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(reg.Add(recipe.Recipe{ID: "laravel", Kind: recipe.Framework, Lang: "php"}))
	must(reg.Add(recipe.Recipe{ID: "django", Kind: recipe.Framework, Lang: "python"}))
	must(reg.Add(recipe.Recipe{ID: "laravel-breeze", Kind: recipe.Addon, AppliesTo: []string{"laravel"}, Provides: []string{"auth"}}))
	must(reg.Add(recipe.Recipe{ID: "django-allauth", Kind: recipe.Addon, AppliesTo: []string{"django"}, Provides: []string{"auth"}}))
	must(reg.Add(recipe.Recipe{
		ID: "gen-auth-laravel", Kind: recipe.Generator, Level: recipe.LevelStack,
		Label: "Auth (Breeze)", AppliesTo: []string{"laravel"}, Provides: []string{"auth"}, Applies: []string{"laravel-breeze"},
	}))
	must(reg.Add(recipe.Recipe{
		ID: "gen-auth-django", Kind: recipe.Generator, Level: recipe.LevelStack,
		Label: "Auth (allauth)", AppliesTo: []string{"django"}, Provides: []string{"auth"}, Applies: []string{"django-allauth"},
	}))
	return reg
}

func keys(gs []Generatable) map[string]Generatable {
	m := make(map[string]Generatable, len(gs))
	for _, g := range gs {
		m[g.Key] = g
	}
	return m
}

// TestGeneratablesFrameworkScoped proves the accessor is framework-aware: Laravel
// gets its code-blocks + its auth stack and NOT Django's, and vice versa.
func TestGeneratablesFrameworkScoped(t *testing.T) {
	reg := testRegistry(t)

	lar := keys(Generatables(reg, "laravel"))
	if _, ok := lar["model"]; !ok {
		t.Error("laravel should offer the model code-block")
	}
	if _, ok := lar["gen-auth-laravel"]; !ok {
		t.Error("laravel should offer its auth stack")
	}
	if _, ok := lar["gen-auth-django"]; ok {
		t.Error("laravel must not offer Django's auth stack")
	}

	dj := keys(Generatables(reg, "django"))
	if _, ok := dj["gen-auth-django"]; !ok {
		t.Error("django should offer its auth stack")
	}
	// Django now has its own built-in generators (manage.py startapp + templates),
	// so it offers a model — but its own, template-driven one, and it must not pick
	// up Laravel-only nouns (filament-resource) or Laravel's auth stack.
	if _, ok := dj["startapp"]; !ok {
		t.Error("django should offer its manage.py startapp generator")
	}
	if _, ok := dj["serializer"]; !ok {
		t.Error("django should offer its DRF serializer generator")
	}
	if _, ok := dj["filament-resource"]; ok {
		t.Error("django must not offer Laravel's filament-resource")
	}
	if _, ok := dj["gen-auth-laravel"]; ok {
		t.Error("django must not offer Laravel's auth stack")
	}
}

// TestGeneratablesModelCarriesFieldsInput proves the model code-block advertises
// a typed form: a name input and the typed field-list input the studio renders a
// fields table from.
func TestGeneratablesModelCarriesFieldsInput(t *testing.T) {
	g := keys(Generatables(testRegistry(t), "laravel"))["model"]
	if !hasInput(g.Inputs, "fields", recipe.InFields) {
		t.Fatalf("model should carry a fields input, got %+v", g.Inputs)
	}
	if !hasInput(g.Inputs, "name", recipe.InClass) {
		t.Fatalf("model should carry a name input, got %+v", g.Inputs)
	}
	if g.Level != recipe.LevelCodeBlock {
		t.Fatalf("model level = %q, want code-block", g.Level)
	}
}

// hasInput reports whether inputs contains one with the given name and type.
func hasInput(inputs []recipe.GenInput, name, typ string) bool {
	for _, in := range inputs {
		if in.Name == name && in.Type == typ {
			return true
		}
	}
	return false
}

func TestGeneratablesMagentoLevels(t *testing.T) {
	reg := recipe.NewRegistry()
	_ = reg.Add(recipe.Recipe{ID: "magento", Kind: recipe.Framework, Lang: "php", Family: "magento"})
	gs := keys(Generatables(reg, "magento"))
	if gs["module"].Level != recipe.LevelModule {
		t.Errorf("magento module should be level module, got %q", gs["module"].Level)
	}
	if gs["block"].Level != recipe.LevelCodeBlock {
		t.Errorf("magento block should be level code-block, got %q", gs["block"].Level)
	}
}

// TestGeneratablesFamilyResolution proves a variant framework id (magento-mageos)
// resolves to its family's code-block generators.
func TestGeneratablesFamilyResolution(t *testing.T) {
	reg := recipe.NewRegistry()
	_ = reg.Add(recipe.Recipe{ID: "magento", Kind: recipe.Framework, Lang: "php", Family: "magento"})
	_ = reg.Add(recipe.Recipe{ID: "magento-mageos", Kind: recipe.Framework, Lang: "php", Family: "magento"})
	gs := keys(Generatables(reg, "magento-mageos"))
	if _, ok := gs["module"]; !ok {
		t.Fatal("a magento variant should still offer magento code-block generators")
	}
}

func TestGeneratablesNilRegistry(t *testing.T) {
	// With no registry, the built-in code-blocks still work (old behaviour).
	gs := keys(Generatables(nil, "laravel"))
	if _, ok := gs["model"]; !ok {
		t.Fatal("laravel code-blocks must survive a nil registry")
	}
}

func TestResolveStack(t *testing.T) {
	reg := testRegistry(t)
	ids, ok := ResolveStack(reg, "gen-auth-laravel")
	if !ok {
		t.Fatal("gen-auth-laravel should resolve as a stack")
	}
	if len(ids) != 1 || ids[0] != "laravel-breeze" {
		t.Fatalf("gen-auth-laravel applies = %v, want [laravel-breeze]", ids)
	}
	// A non-stack id does not resolve as a stack.
	if _, ok := ResolveStack(reg, "laravel-breeze"); ok {
		t.Fatal("an addon must not resolve as a stack")
	}
	if _, ok := ResolveStack(reg, "nope"); ok {
		t.Fatal("an unknown id must not resolve")
	}
	if _, ok := ResolveStack(nil, "gen-auth-laravel"); ok {
		t.Fatal("a nil registry must not resolve")
	}
}

func TestAuthStackFor(t *testing.T) {
	reg := testRegistry(t)
	g, ok := AuthStackFor(reg, "laravel")
	if !ok {
		t.Fatal("laravel should have an auth stack")
	}
	if g.Recipe != "gen-auth-laravel" {
		t.Fatalf("laravel auth stack = %q, want gen-auth-laravel", g.Recipe)
	}
	ids, _ := ResolveStack(reg, g.Recipe)
	if len(ids) != 1 || ids[0] != "laravel-breeze" {
		t.Fatalf("resolved auth stack = %v", ids)
	}
	// A framework with no auth generator returns false rather than a wrong one.
	reg2 := recipe.NewRegistry()
	_ = reg2.Add(recipe.Recipe{ID: "hono", Kind: recipe.Framework, Lang: "node"})
	if _, ok := AuthStackFor(reg2, "hono"); ok {
		t.Fatal("hono has no auth generator; AuthStackFor should report false")
	}
}
