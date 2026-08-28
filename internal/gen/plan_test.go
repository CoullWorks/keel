package gen

import (
	"strings"
	"testing"
)

func TestTargetBasePath(t *testing.T) {
	tests := []struct {
		name           string
		target         Target
		vendor, module string
		want           string
	}{
		{"app-code default", TargetAppCode, "Acme", "Blog", "app/code/Acme/Blog"},
		{"zero value is app-code", "", "Acme", "Blog", "app/code/Acme/Blog"},
		{"package under vendor", TargetPackage, "Acme", "Blog", "vendor/acme/module-blog"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.target.BasePath(tc.vendor, tc.module); got != tc.want {
				t.Fatalf("BasePath = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestModulePlanValidate(t *testing.T) {
	tests := []struct {
		name    string
		plan    ModulePlan
		wantErr bool
	}{
		{"ok", ModulePlan{Vendor: "Acme", Module: "Blog", Target: TargetAppCode,
			Components: []PlanComponent{{Type: "model", Params: map[string]any{"name": "Post"}}}}, false},
		{"no module", ModulePlan{Vendor: "Acme"}, true},
		{"bad module name", ModulePlan{Module: "../evil"}, true},
		{"bad vendor name", ModulePlan{Vendor: "../x", Module: "Blog"}, true},
		{"unknown target", ModulePlan{Module: "Blog", Target: "cloud"}, true},
		{"bad component name", ModulePlan{Module: "Blog",
			Components: []PlanComponent{{Type: "model", Params: map[string]any{"name": "bad;name"}}}}, true},
		{"component no type", ModulePlan{Module: "Blog", Components: []PlanComponent{{}}}, true},
		{"bad field", ModulePlan{Module: "Blog",
			Components: []PlanComponent{{Type: "model", Fields: []Field{{Name: "x", Type: "widget"}}}}}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.plan.Validate()
			if tc.wantErr != (err != nil) {
				t.Fatalf("Validate() err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestModulePlanAddRemove(t *testing.T) {
	p := ModulePlan{Module: "Blog"}
	p.AddComponent(PlanComponent{Type: "model", Params: map[string]any{"name": "Post"}})
	p.AddComponent(PlanComponent{Type: "block", Params: map[string]any{"name": "Sidebar"}})
	if len(p.Components) != 2 {
		t.Fatalf("want 2 components, got %d", len(p.Components))
	}
	if !p.RemoveComponent(0) {
		t.Fatal("RemoveComponent(0) = false")
	}
	if len(p.Components) != 1 || p.Components[0].Type != "block" {
		t.Fatalf("after remove, want [block], got %+v", p.Components)
	}
	if p.RemoveComponent(5) {
		t.Fatal("RemoveComponent(5) should be false (out of range)")
	}
}

// TestRenderPlanWholeModule proves a whole plan renders every component into the
// target base path, so the plan-as-a-whole path (the XML-merge precondition)
// produces the module + model files under vendor/ for a package target.
func TestRenderPlanWholeModule(t *testing.T) {
	p := ModulePlan{
		Vendor: "Acme", Module: "Blog", Framework: "magento", Target: TargetPackage,
		Components: []PlanComponent{
			{Type: "module"},
			{Type: "model", Params: map[string]any{"name": "Post"}, Fields: []Field{{Name: "title", Type: TypeString}}},
		},
	}
	files, err := RenderPlan(p)
	if err != nil {
		t.Fatalf("RenderPlan: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("RenderPlan produced no files")
	}
	var sawReg, sawSchema bool
	for _, f := range files {
		if !strings.HasPrefix(f.Path, "vendor/acme/module-blog/") {
			t.Errorf("file %q not under the package base", f.Path)
		}
		if strings.HasSuffix(f.Path, "registration.php") {
			sawReg = true
		}
		if strings.HasSuffix(f.Path, "db_schema.xml") {
			sawSchema = true
			if !strings.Contains(f.Content, `name="title"`) {
				t.Errorf("db_schema should carry the title column: %q", f.Content)
			}
		}
	}
	if !sawReg {
		t.Error("plan should have rendered the module registration")
	}
	if !sawSchema {
		t.Error("plan should have rendered the model db_schema")
	}
}

func TestRenderPlanRejectsUnwiredFramework(t *testing.T) {
	p := ModulePlan{Module: "Blog", Framework: "laravel",
		Components: []PlanComponent{{Type: "model", Params: map[string]any{"name": "Post"}}}}
	if _, err := RenderPlan(p); err == nil {
		t.Fatal("expected RenderPlan to refuse a laravel plan (not wired yet)")
	}
}

func TestRenderPlanUnknownComponent(t *testing.T) {
	p := ModulePlan{Vendor: "Acme", Module: "Blog", Framework: "magento",
		Components: []PlanComponent{{Type: "wormhole"}}}
	if _, err := RenderPlan(p); err == nil {
		t.Fatal("expected RenderPlan to reject an unknown component type")
	}
}

func TestMarshalUnmarshalPlanRoundTrip(t *testing.T) {
	p := ModulePlan{
		Vendor: "Acme", Module: "Blog", Framework: "magento", Target: TargetAppCode,
		Components: []PlanComponent{
			{Type: "model", Params: map[string]any{"name": "Post"}, Fields: []Field{{Name: "title", Type: TypeString, Length: 120}}},
		},
	}
	b, err := MarshalPlan(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalPlan(b)
	if err != nil {
		t.Fatalf("UnmarshalPlan: %v", err)
	}
	if got.Module != "Blog" || got.Vendor != "Acme" || len(got.Components) != 1 {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if got.Components[0].Fields[0].Length != 120 {
		t.Fatalf("round trip lost the field length: %+v", got.Components[0].Fields)
	}
}

func TestUnmarshalPlanRejectsInvalid(t *testing.T) {
	if _, err := UnmarshalPlan([]byte("module: \"\"\ncomponents: []\n")); err == nil {
		t.Fatal("expected an empty-module plan to be rejected")
	}
	if _, err := UnmarshalPlan([]byte(":\n  not yaml")); err == nil {
		t.Fatal("expected malformed YAML to be rejected")
	}
}
