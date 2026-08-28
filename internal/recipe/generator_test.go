package recipe

import "testing"

func TestGeneratorValidate(t *testing.T) {
	tests := []struct {
		name    string
		r       Recipe
		wantErr bool
	}{
		{
			name:    "valid stack generator",
			r:       Recipe{ID: "gen-auth", Kind: Generator, Level: LevelStack, Applies: []string{"laravel-breeze"}},
			wantErr: false,
		},
		{
			name:    "valid code-block generator",
			r:       Recipe{ID: "gen-model", Kind: Generator, Level: LevelCodeBlock},
			wantErr: false,
		},
		{
			name:    "generator without level",
			r:       Recipe{ID: "gen-x", Kind: Generator},
			wantErr: true,
		},
		{
			name:    "generator with unknown level",
			r:       Recipe{ID: "gen-x", Kind: Generator, Level: "galaxy"},
			wantErr: true,
		},
		{
			name:    "stack generator with no apply",
			r:       Recipe{ID: "gen-x", Kind: Generator, Level: LevelStack},
			wantErr: true,
		},
		{
			name:    "level on a non-generator",
			r:       Recipe{ID: "addon-x", Kind: Addon, Level: LevelStack},
			wantErr: true,
		},
		{
			name:    "apply on a non-generator",
			r:       Recipe{ID: "addon-x", Kind: Addon, Applies: []string{"y"}},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.r.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("Validate() = nil, want error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

// TestForFrameworkGenerators proves the existing ForFramework accessor scopes
// generator recipes per framework — the accessor gen.Generatables reuses.
func TestForFrameworkGenerators(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Add(Recipe{ID: "laravel", Kind: Framework, Lang: "php"})
	_ = reg.Add(Recipe{ID: "django", Kind: Framework, Lang: "python"})
	_ = reg.Add(Recipe{ID: "gen-auth-laravel", Kind: Generator, Level: LevelStack, AppliesTo: []string{"laravel"}, Applies: []string{"x"}})
	_ = reg.Add(Recipe{ID: "gen-auth-django", Kind: Generator, Level: LevelStack, AppliesTo: []string{"django"}, Applies: []string{"y"}})

	lar := reg.ForFramework("laravel", Generator)
	if len(lar) != 1 || lar[0].ID != "gen-auth-laravel" {
		t.Fatalf("laravel generators = %v, want [gen-auth-laravel]", ids(lar))
	}
	dj := reg.ForFramework("django", Generator)
	if len(dj) != 1 || dj[0].ID != "gen-auth-django" {
		t.Fatalf("django generators = %v, want [gen-auth-django]", ids(dj))
	}
}

func ids(rs []Recipe) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}
