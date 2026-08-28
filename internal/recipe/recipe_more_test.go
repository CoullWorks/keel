package recipe

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestKindRank(t *testing.T) {
	if Framework.Rank() != 0 {
		t.Fatalf("Framework rank = %d, want 0", Framework.Rank())
	}
	if Generator.Rank() != len(Order)-1 {
		t.Fatalf("Generator rank = %d, want %d", Generator.Rank(), len(Order)-1)
	}
	if Kind("bogus").Rank() != len(Order) {
		t.Fatalf("unknown kind rank = %d, want %d", Kind("bogus").Rank(), len(Order))
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		r       Recipe
		wantSub string // "" = expect no error
	}{
		{"ok minimal", Recipe{ID: "x", Kind: Framework}, ""},
		{"empty id", Recipe{ID: "  ", Kind: Framework}, "empty id"},
		{"unknown kind", Recipe{ID: "x", Kind: "nope"}, "unknown kind"},
		{
			name:    "schema too new",
			r:       Recipe{ID: "x", Kind: Framework, SchemaVersion: SupportedSchema + 1},
			wantSub: "newer than this keel",
		},
		{
			name:    "unknown hook stage",
			r:       Recipe{ID: "x", Kind: Framework, Hooks: Hooks{"bogus_stage": {{Message: "hi"}}}},
			wantSub: "unknown hook stage",
		},
		{
			name:    "hook with no action",
			r:       Recipe{ID: "x", Kind: Framework, Hooks: Hooks{"pre_build": {{}}}},
			wantSub: "exactly one of message/run/script",
		},
		{
			name: "hook with two actions",
			r: Recipe{ID: "x", Kind: Framework, Hooks: Hooks{
				"pre_build": {{Message: "hi", Run: "echo hi"}},
			}},
			wantSub: "exactly one of message/run/script",
		},
		{
			name: "hook with exactly one action ok",
			r: Recipe{ID: "x", Kind: Framework, Hooks: Hooks{
				"post_build": {{Run: "echo done"}},
			}},
			wantSub: "",
		},
		{
			name:    "schema equal to supported is ok",
			r:       Recipe{ID: "x", Kind: Framework, SchemaVersion: SupportedSchema},
			wantSub: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.r.Validate()
			if tt.wantSub == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tt.wantSub)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantSub)
			}
		})
	}
}

func TestAppliesToFramework(t *testing.T) {
	tests := []struct {
		name      string
		appliesTo []string
		fw        string
		want      bool
	}{
		{"empty applies to any", nil, "laravel", true},
		{"explicit match", []string{"laravel", "magento"}, "magento", true},
		{"no match", []string{"laravel"}, "magento", false},
		{"wildcard matches any", []string{"*"}, "anything", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Recipe{AppliesTo: tt.appliesTo}
			if got := r.AppliesToFramework(tt.fw); got != tt.want {
				t.Fatalf("AppliesToFramework(%q) = %v, want %v", tt.fw, got, tt.want)
			}
		})
	}
}

func TestAllAndOfKindOrdering(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Add(Recipe{ID: "redis", Kind: Service})
	_ = reg.Add(Recipe{ID: "laravel", Kind: Framework})
	_ = reg.Add(Recipe{ID: "magento", Kind: Framework})
	_ = reg.Add(Recipe{ID: "ddev", Kind: Env})

	all := reg.All()
	// Kind rank first (Framework < Env < Service), then id.
	wantIDs := []string{"laravel", "magento", "ddev", "redis"}
	if len(all) != len(wantIDs) {
		t.Fatalf("All len = %d, want %d", len(all), len(wantIDs))
	}
	for i, id := range wantIDs {
		if all[i].ID != id {
			t.Fatalf("All[%d] = %q, want %q", i, all[i].ID, id)
		}
	}
	if got := reg.OfKind(Framework); len(got) != 2 || got[0].ID != "laravel" || got[1].ID != "magento" {
		t.Fatalf("OfKind(Framework) wrong: %+v", got)
	}
	if reg.Len() != 4 {
		t.Fatalf("Len = %d, want 4", reg.Len())
	}
}

func TestLanguages(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Add(Recipe{ID: "django", Kind: Framework, Lang: "python"})
	_ = reg.Add(Recipe{ID: "nextjs", Kind: Framework, Lang: "node"})
	_ = reg.Add(Recipe{ID: "laravel", Kind: Framework, Lang: "php"})
	_ = reg.Add(Recipe{ID: "gleam", Kind: Framework, Lang: "gleam"})  // unknown lang -> rank 8
	_ = reg.Add(Recipe{ID: "bespoke", Kind: Framework})               // no lang -> "other"
	_ = reg.Add(Recipe{ID: "laravel2", Kind: Framework, Lang: "php"}) // dup lang, should collapse

	got := reg.Languages()
	// php(0) < python(1) < node(2) < gleam(8, unknown) < other(9)
	want := []string{"php", "python", "node", "gleam", "other"}
	if len(got) != len(want) {
		t.Fatalf("Languages = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Languages[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestFrameworksForLang(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Add(Recipe{ID: "laravel", Kind: Framework, Lang: "php"})
	_ = reg.Add(Recipe{ID: "symfony", Kind: Framework, Lang: "php"})
	_ = reg.Add(Recipe{ID: "django", Kind: Framework, Lang: "python"})
	_ = reg.Add(Recipe{ID: "bespoke", Kind: Framework})          // no lang -> "other"
	_ = reg.Add(Recipe{ID: "redis", Kind: Service, Lang: "php"}) // not a framework -> ignored

	php := reg.FrameworksForLang("php")
	if len(php) != 2 {
		t.Fatalf("php frameworks = %d, want 2: %+v", len(php), php)
	}
	other := reg.FrameworksForLang("other")
	if len(other) != 1 || other[0].ID != "bespoke" {
		t.Fatalf("other frameworks wrong: %+v", other)
	}
	if got := reg.FrameworksForLang("rust"); len(got) != 0 {
		t.Fatalf("rust frameworks = %d, want 0", len(got))
	}
}

func TestAddYAMLBadYAML(t *testing.T) {
	reg := NewRegistry()
	err := AddYAML(reg, []byte("id: x\nkind: [not-a-scalar\n"), "user", "mypack")
	if err == nil {
		t.Fatal("want error for malformed YAML")
	}
}

func TestAddYAMLStampsProvenance(t *testing.T) {
	reg := NewRegistry()
	if err := AddYAML(reg, []byte("id: laravel\nkind: framework\n"), "pack:acme", "acme"); err != nil {
		t.Fatalf("AddYAML: %v", err)
	}
	r, ok := reg.Get("laravel")
	if !ok {
		t.Fatal("laravel not added")
	}
	if r.Source != "pack:acme" || r.Pack != "acme" {
		t.Fatalf("provenance = %q/%q, want pack:acme/acme", r.Source, r.Pack)
	}
}

func TestAddYAMLInvalidRecipeRejected(t *testing.T) {
	reg := NewRegistry()
	// Parses fine but fails Validate (unknown kind) -> Add returns an error.
	err := AddYAML(reg, []byte("id: x\nkind: bogus\n"), "", "")
	if err == nil {
		t.Fatal("want error for a recipe that fails validation")
	}
}

func TestLoadIntoNilFS(t *testing.T) {
	reg := NewRegistry()
	if err := LoadInto(reg, nil, "", ""); err != nil {
		t.Fatalf("LoadInto(nil) = %v, want nil", err)
	}
	if reg.Len() != 0 {
		t.Fatalf("nil fs should add nothing, got %d", reg.Len())
	}
}

func TestLoadIntoSkipsManifestAndNonRecipes(t *testing.T) {
	fsys := fstest.MapFS{
		"keel.pack.yaml":        {Data: []byte("name: acme\n")},   // manifest, skipped
		"nested/keel.pack.yaml": {Data: []byte("name: nested\n")}, // manifest by base name, skipped
		"README.md":             {Data: []byte("docs")},           // not yaml
		"a/laravel.yaml":        {Data: []byte("id: laravel\nkind: framework\n")},
		"b/ddev.yml":            {Data: []byte("id: ddev\nkind: env\n")},
	}
	reg := NewRegistry()
	if err := LoadInto(reg, fsys, "builtin", ""); err != nil {
		t.Fatalf("LoadInto: %v", err)
	}
	if reg.Len() != 2 {
		t.Fatalf("want 2 recipes, got %d", reg.Len())
	}
}

func TestLoadIntoBadRecipeWrapsPath(t *testing.T) {
	fsys := fstest.MapFS{
		"bad/broken.yaml": {Data: []byte("id: x\nkind: bogus\n")}, // fails Validate
	}
	reg := NewRegistry()
	err := LoadInto(reg, fsys, "", "")
	if err == nil {
		t.Fatal("want error from a bad recipe")
	}
	if !strings.Contains(err.Error(), "bad/broken.yaml") {
		t.Fatalf("error should include the file path, got: %v", err)
	}
}

// errFS returns a walk error to exercise LoadInto's err != nil branch.
type errFS struct{}

func (errFS) Open(name string) (fs.File, error) { return nil, errors.New("boom") }

func TestLoadIntoWalkError(t *testing.T) {
	reg := NewRegistry()
	err := LoadInto(reg, errFS{}, "", "")
	if err == nil {
		t.Fatal("want error when the filesystem walk fails")
	}
}

func TestLoadPropagatesError(t *testing.T) {
	good := fstest.MapFS{"laravel.yaml": {Data: []byte("id: laravel\nkind: framework\n")}}
	bad := fstest.MapFS{"broken.yaml": {Data: []byte("id: x\nkind: bogus\n")}}
	if _, err := Load(good, bad); err == nil {
		t.Fatal("want Load to propagate the error from a bad source")
	}
}
