package gen

import (
	"strings"
	"testing"
)

func TestMagentoByKeyMissing(t *testing.T) {
	if _, ok := MagentoByKey("does-not-exist"); ok {
		t.Fatal("expected MagentoByKey to report a missing component")
	}
}

func TestRenderParseError(t *testing.T) {
	// An unclosed action is a template parse error.
	if _, err := render("{{.Name", MagentoVars{Name: "X"}); err == nil {
		t.Fatal("expected a parse error for a malformed template")
	}
}

func TestRenderExecuteError(t *testing.T) {
	// Calling a missing method on the field triggers an execution error (parse is
	// fine, Execute fails).
	if _, err := render("{{.Name.Missing}}", MagentoVars{Name: "X"}); err == nil {
		t.Fatal("expected an execution error calling a missing field")
	}
}

func TestRenderMagentoPathError(t *testing.T) {
	// A bad path template surfaces as an error from RenderMagento (path rendered
	// before content).
	comp := MagentoComponent{Key: "x", files: []mFile{{path: "{{.Name", content: "ok"}}}
	if _, err := RenderMagento(comp, MagentoVars{Name: "N"}); err == nil {
		t.Fatal("expected RenderMagento to fail on a bad path template")
	}
}

func TestRenderMagentoContentError(t *testing.T) {
	// A good path but a bad content template fails after the path renders.
	comp := MagentoComponent{Key: "x", files: []mFile{{path: "ok/{{.Name}}.php", content: "{{.Name.Nope}}"}}}
	if _, err := RenderMagento(comp, MagentoVars{Name: "N"}); err == nil {
		t.Fatal("expected RenderMagento to fail on a bad content template")
	}
}

// TestRenderMagentoLowerFunc proves the {{lower}} template func is wired into the
// render pipeline for every component that uses it (console command DI naming).
func TestRenderMagentoConsole(t *testing.T) {
	comp, ok := MagentoByKey("console")
	if !ok {
		t.Fatal("console component missing")
	}
	files, err := RenderMagento(comp, MagentoVars{Vendor: "Acme", Module: "Blog", Name: "Sync"})
	if err != nil {
		t.Fatal(err)
	}
	var cmd, di string
	for _, f := range files {
		if strings.HasSuffix(f.Path, "Command/Sync.php") {
			cmd = f.Content
		}
		if strings.HasSuffix(f.Path, "etc/di.xml") {
			di = f.Content
		}
	}
	if !strings.Contains(cmd, "$this->setName('acme:sync')") {
		t.Fatalf("console setName not lowered:\n%s", cmd)
	}
	if !strings.Contains(di, `name="acme_sync"`) {
		t.Fatalf("console di item name not lowered:\n%s", di)
	}
}

// TestRenderMagentoObserverAndBlockAndHelper renders the remaining catalogue
// entries so their templates are exercised end-to-end.
func TestRenderMagentoObserverBlockHelper(t *testing.T) {
	for _, key := range []string{"observer", "block", "helper"} {
		comp, ok := MagentoByKey(key)
		if !ok {
			t.Fatalf("%s component missing", key)
		}
		files, err := RenderMagento(comp, MagentoVars{Vendor: "Acme", Module: "Blog", Name: "Thing"})
		if err != nil {
			t.Fatalf("render %s: %v", key, err)
		}
		if len(files) == 0 {
			t.Fatalf("%s produced no files", key)
		}
		sawPHP := false
		for _, f := range files {
			if !strings.HasPrefix(f.Path, "app/code/Acme/Blog/") {
				t.Fatalf("%s file has wrong base path: %s", key, f.Path)
			}
			if strings.HasSuffix(f.Path, ".php") {
				sawPHP = true
				if !strings.Contains(f.Content, `namespace Acme\Blog\`) {
					t.Fatalf("%s php content missing namespace:\n%s", key, f.Content)
				}
			}
		}
		if !sawPHP {
			t.Fatalf("%s produced no php file", key)
		}
	}
}
