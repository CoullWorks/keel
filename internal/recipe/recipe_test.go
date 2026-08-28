package recipe

import (
	"testing"
	"testing/fstest"
)

func TestRegistryAddGet(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Add(Recipe{ID: "laravel", Kind: Framework, Label: "Laravel"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, ok := reg.Get("laravel"); !ok {
		t.Fatal("expected laravel present")
	}
	if err := reg.Add(Recipe{ID: "", Kind: Framework}); err == nil {
		t.Fatal("expected error adding empty id")
	}
	if err := reg.Add(Recipe{ID: "x", Kind: "bogus"}); err == nil {
		t.Fatal("expected error adding unknown kind")
	}
}

func TestForFrameworkFilters(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Add(Recipe{ID: "laravel", Kind: Framework})
	_ = reg.Add(Recipe{ID: "magento", Kind: Framework})
	_ = reg.Add(Recipe{ID: "filament", Kind: Addon, AppliesTo: []string{"laravel"}})
	_ = reg.Add(Recipe{ID: "ddev", Kind: Env, AppliesTo: []string{"laravel", "magento"}})

	if got := reg.ForFramework("laravel", Addon); len(got) != 1 || got[0].ID != "filament" {
		t.Fatalf("laravel addons: want [filament], got %+v", got)
	}
	if got := reg.ForFramework("magento", Addon); len(got) != 0 {
		t.Fatalf("magento addons: want none, got %+v", got)
	}
	if got := reg.ForFramework("magento", Env); len(got) != 1 {
		t.Fatalf("magento env: want [ddev], got %+v", got)
	}
}

func TestLoadFromFS(t *testing.T) {
	fsys := fstest.MapFS{
		"laravel/framework.yaml": {Data: []byte("id: laravel\nkind: framework\nlabel: Laravel\nprovides: [php, laravel]\n")},
		"laravel/env.yaml":       {Data: []byte("id: ddev\nkind: env\nlabel: DDEV\nappliesTo: [laravel]\n")},
		"README.md":              {Data: []byte("not a recipe")},
	}
	reg, err := Load(fsys)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if reg.Len() != 2 {
		t.Fatalf("want 2 recipes loaded, got %d", reg.Len())
	}
	if _, ok := reg.Get("ddev"); !ok {
		t.Fatal("expected ddev loaded from fs")
	}
}

func TestLaterSourceOverrides(t *testing.T) {
	a := fstest.MapFS{"x.yaml": {Data: []byte("id: ddev\nkind: env\nlabel: builtin\n")}}
	b := fstest.MapFS{"x.yaml": {Data: []byte("id: ddev\nkind: env\nlabel: user-override\n")}}
	reg, err := Load(a, b)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := reg.Get("ddev")
	if r.Label != "user-override" {
		t.Fatalf("want user override to win, got %q", r.Label)
	}
}
