package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/gen"
)

// TestMakeAddListGenerate walks the object-first loop end to end: stage a module,
// add a model with fields and a block, list the plan, then generate the whole
// thing to disk — the mage2gen interaction the fitness review asks for.
func TestMakeAddListGenerate(t *testing.T) {
	wd := genProject(t, "magento", "docker")

	if out, err := runRoot(t, "make", "add", "module", "Acme/Blog"); err != nil {
		t.Fatalf("make add module: %v\n%s", err, out)
	}
	if out, err := runRoot(t, "make", "add", "model", "Post", "-m", "Acme/Blog",
		"--field", "title:string,len=120", "--field", "body:text,nullable"); err != nil {
		t.Fatalf("make add model: %v\n%s", err, out)
	}
	if out, err := runRoot(t, "make", "add", "block", "Sidebar", "-m", "Acme/Blog"); err != nil {
		t.Fatalf("make add block: %v\n%s", err, out)
	}

	// The plan is staged on disk, not generated yet.
	planPath := filepath.Join(wd, ".keel", "modules", "Blog.yaml")
	if _, err := os.Stat(planPath); err != nil {
		t.Fatalf("plan not persisted at %s: %v", planPath, err)
	}
	if _, err := os.Stat(filepath.Join(wd, "app", "code", "Acme", "Blog", "registration.php")); err == nil {
		t.Fatal("add should stage only; nothing should be generated before `generate`")
	}

	list, err := runRoot(t, "make", "list", "-m", "Acme/Blog")
	if err != nil {
		t.Fatalf("make list: %v", err)
	}
	mustContain(t, list, "module", "model Post", "block Sidebar", "title string")

	gout, err := runRoot(t, "make", "generate", "-m", "Acme/Blog")
	if err != nil {
		t.Fatalf("make generate: %v\n%s", err, gout)
	}
	mustContain(t, gout, "files written")

	// The whole plan rendered: module + model + block files all under app/code.
	schema, err := os.ReadFile(filepath.Join(wd, "app", "code", "Acme", "Blog", "etc", "db_schema.xml"))
	if err != nil {
		t.Fatalf("db_schema not generated: %v", err)
	}
	for _, w := range []string{`name="title"`, `length="120"`, `name="body"`} {
		if !strings.Contains(string(schema), w) {
			t.Errorf("db_schema missing %q:\n%s", w, schema)
		}
	}
	if _, err := os.Stat(filepath.Join(wd, "app", "code", "Acme", "Blog", "Block", "Sidebar.php")); err != nil {
		t.Errorf("block not generated: %v", err)
	}
}

// TestMakeRemove drops a staged component by index.
func TestMakeRemove(t *testing.T) {
	genProject(t, "magento", "docker")
	_, _ = runRoot(t, "make", "add", "module", "Acme/Blog")
	_, _ = runRoot(t, "make", "add", "model", "Post", "-m", "Acme/Blog")

	out, err := runRoot(t, "make", "remove", "1", "-m", "Acme/Blog")
	if err != nil {
		t.Fatalf("make remove: %v\n%s", err, out)
	}
	mustContain(t, out, "removed component 1")

	list, _ := runRoot(t, "make", "list", "-m", "Acme/Blog")
	mustNotContain(t, list, "model Post")
}

// TestMakeInfoShowsForm proves `make info` surfaces a component's typed inputs —
// the same GenInput form the studio renders — so the CLI exposes the uniform form.
func TestMakeInfoShowsForm(t *testing.T) {
	genProject(t, "magento", "docker")
	out, err := runRoot(t, "make", "info", "model")
	if err != nil {
		t.Fatalf("make info model: %v", err)
	}
	mustContain(t, out, "name:", "fields:")

	out2, err := runRoot(t, "make", "info", "module")
	if err != nil {
		t.Fatalf("make info module: %v", err)
	}
	mustContain(t, out2, "target:", "app-code")
}

// TestMakePackageTarget routes files into a Composer package tree.
func TestMakePackageTarget(t *testing.T) {
	wd := genProject(t, "magento", "docker")
	if _, err := runRoot(t, "make", "add", "module", "Acme/Blog", "--target", "package"); err != nil {
		t.Fatalf("make add module (package): %v", err)
	}
	if _, err := runRoot(t, "make", "generate", "-m", "Acme/Blog"); err != nil {
		t.Fatalf("make generate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wd, "vendor", "acme", "module-blog", "registration.php")); err != nil {
		t.Errorf("expected files under the package base vendor/acme/module-blog: %v", err)
	}
}

// TestMakeAddRejectsUnknownComponent catches a typo at `add`, not at `generate`.
func TestMakeAddRejectsUnknownComponent(t *testing.T) {
	genProject(t, "magento", "docker")
	_, err := runRoot(t, "make", "add", "wormhole", "-m", "Acme/Blog")
	if err == nil {
		t.Fatal("expected an unknown component to be rejected at add")
	}
	mustContain(t, err.Error(), "unknown component")
}

// TestMakeFieldOnlyOnModel rejects --field on a non-model at add time.
func TestMakeFieldOnlyOnModel(t *testing.T) {
	genProject(t, "magento", "docker")
	_, err := runRoot(t, "make", "add", "block", "Sidebar", "-m", "Acme/Blog", "--field", "x:string")
	if err == nil {
		t.Fatal("expected --field to be rejected on a block")
	}
	mustContain(t, err.Error(), "--field only applies to a model")
}

// TestMakeGenerateNoPlan errors clearly when nothing was staged.
func TestMakeGenerateNoPlan(t *testing.T) {
	genProject(t, "magento", "docker")
	_, err := runRoot(t, "make", "generate", "-m", "Acme/Nope")
	if err == nil {
		t.Fatal("expected an error generating with no staged plan")
	}
}

// TestResolveVendorModule covers the module-reference parsing directly.
func TestResolveVendorModule(t *testing.T) {
	tests := []struct {
		name         string
		module       string
		vendor       string
		wantV, wantM string
		wantErr      bool
	}{
		{"vendor/module", "Acme/Blog", "", "Acme", "Blog", false},
		{"module only", "Blog", "", "", "Blog", false},
		{"module + separate vendor", "Blog", "Acme", "Acme", "Blog", false},
		{"empty", "", "", "", "", true},
		{"bad module", "../evil", "", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, m, err := resolveVendorModule(tc.module, tc.vendor)
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if err == nil && (v != tc.wantV || m != tc.wantM) {
				t.Fatalf("got (%q,%q), want (%q,%q)", v, m, tc.wantV, tc.wantM)
			}
		})
	}
}

// TestModulePlanPathShape pins the on-disk location the studio/CLI share.
func TestModulePlanPathShape(t *testing.T) {
	got := modulePlanPath("/proj", "Blog")
	want := filepath.Join("/proj", ".keel", "modules", "Blog.yaml")
	if got != want {
		t.Fatalf("modulePlanPath = %q, want %q", got, want)
	}
	// Sanity: a fresh plan for a magento project validates and renders.
	p := gen.ModulePlan{Vendor: "Acme", Module: "Blog", Framework: "magento",
		Components: []gen.PlanComponent{{Type: "module"}}}
	if _, err := gen.RenderPlan(p); err != nil {
		t.Fatalf("RenderPlan on a minimal plan: %v", err)
	}
}
