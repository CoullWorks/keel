package recipe

import "testing"

// TestGenInputValidate covers the extended input vocabulary added in the P0
// foundation: the new types are accepted, options needs choices, unknown types
// are refused, and group children are validated recursively.
func TestGenInputValidate(t *testing.T) {
	tests := []struct {
		name    string
		in      GenInput
		wantErr bool
	}{
		{"text", GenInput{Name: "title", Type: InText}, false},
		{"legacy string type", GenInput{Name: "title", Type: "string"}, false},
		{"int", GenInput{Name: "qty", Type: InInt}, false},
		{"class", GenInput{Name: "model", Type: InClass}, false},
		{"path", GenInput{Name: "dir", Type: InPath}, false},
		{"ref", GenInput{Name: "event", Type: InRef}, false},
		{"fields", GenInput{Name: "fields", Type: InFields}, false},
		{"options with choices", GenInput{Name: "stack", Type: InOptions, Choices: []string{"a", "b"}}, false},
		{"legacy choice with choices", GenInput{Name: "stack", Type: "choice", Choices: []string{"a"}}, false},
		{"options without choices", GenInput{Name: "stack", Type: InOptions}, true},
		{"group with valid child", GenInput{Name: "g", Type: InGroup, Children: []GenInput{{Name: "c", Type: InText}}}, false},
		{"group with bad child", GenInput{Name: "g", Type: InGroup, Children: []GenInput{{Name: "c", Type: "widget"}}}, true},
		{"no name", GenInput{Type: InText}, true},
		{"no type", GenInput{Name: "x"}, true},
		{"unknown type", GenInput{Name: "x", Type: "widget"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.in.Validate()
			if tc.wantErr != (err != nil) {
				t.Fatalf("Validate() err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

// TestGeneratorRecipeValidatesInputs proves a generator recipe with a malformed
// input is rejected at recipe validation, so a bad form fails at load not render.
func TestGeneratorRecipeValidatesInputs(t *testing.T) {
	good := Recipe{ID: "gen-x", Kind: Generator, Level: LevelCodeBlock,
		Inputs: []GenInput{{Name: "name", Type: InClass, Required: true}}}
	if err := good.Validate(); err != nil {
		t.Fatalf("good generator rejected: %v", err)
	}
	bad := Recipe{ID: "gen-y", Kind: Generator, Level: LevelCodeBlock,
		Inputs: []GenInput{{Name: "stack", Type: InOptions}}} // options, no choices
	if err := bad.Validate(); err == nil {
		t.Fatal("expected a generator with a malformed input to be rejected")
	}
}

// TestGenInputExtendedFieldsCarried proves the new declarative attributes survive
// as data (they are what the studio/MCP read to build a form).
func TestGenInputExtendedFieldsCarried(t *testing.T) {
	in := GenInput{
		Name: "grid", Type: InBool, Default: "false", Help: "show in the admin grid",
		DependsOn: "hasGrid", Repeatable: true,
	}
	if err := in.Validate(); err != nil {
		t.Fatalf("valid extended input rejected: %v", err)
	}
	if in.Default != "false" || in.Help == "" || in.DependsOn != "hasGrid" || !in.Repeatable {
		t.Fatalf("extended attributes not carried: %+v", in)
	}
}
