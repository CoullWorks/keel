package gen

import (
	"slices"
	"testing"
)

// TestRegisterPanicsOnNoFamily proves Register rejects a FrameworkGen with no
// family — a programming error surfaced at startup, not a silent bad entry.
func TestRegisterPanicsOnNoFamily(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Register should panic when Family is empty")
		}
	}()
	Register(&FrameworkGen{})
}

// TestRegisterPanicsOnDuplicate proves a second registration for an existing
// family panics rather than last-wins clobbering the first.
func TestRegisterPanicsOnDuplicate(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Register should panic on a duplicate family")
		}
	}()
	// "laravel" is already registered in laravel.go's init.
	Register(&FrameworkGen{Family: "laravel"})
}

// TestFrameworkRenderUnknownFamily reports ok=false for a family with no
// registered stance and for a CLI-driven family with no Render.
func TestFrameworkRenderUnknownFamily(t *testing.T) {
	if _, ok, _ := FrameworkRender("no-such-framework", "x", "Y", nil, nil); ok {
		t.Fatal("an unregistered family must not render")
	}
	// symfony is CLI-driven (no Render func).
	if _, ok, _ := FrameworkRender("symfony", "controller", "Y", nil, nil); ok {
		t.Fatal("a CLI-driven family must report ok=false for FrameworkRender")
	}
}

// TestFrameworkCommandUnknownFamily reports ok=false for a family with no
// registered stance and for a template-only family with no Command.
func TestFrameworkCommandUnknownFamily(t *testing.T) {
	if _, ok := FrameworkCommand("no-such-framework", "ddev", "x", "Y"); ok {
		t.Fatal("an unregistered family must not build a command")
	}
	if _, ok := FrameworkCommand("astro", "ddev", "component", "Y"); ok {
		t.Fatal("a template-only family must report ok=false for FrameworkCommand")
	}
}

// TestCLIDriven distinguishes the two generation stances.
func TestCLIDriven(t *testing.T) {
	for _, f := range []string{"laravel", "symfony", "nestjs", "adonisjs", "django"} {
		if !CLIDriven(f) {
			t.Errorf("%s should be CLI-driven", f)
		}
	}
	for _, f := range []string{"magento", "nextjs", "nuxt", "astro", "sveltekit", "fastapi", "flask", "unknown"} {
		if CLIDriven(f) {
			t.Errorf("%s should not be CLI-driven", f)
		}
	}
}

// TestDjangoKeys lists every Django key (CLI + template) so help/completion has a
// single source.
func TestDjangoKeys(t *testing.T) {
	keys := DjangoKeys()
	for _, want := range []string{"startapp", "makemigrations", "model", "serializer", "viewset", "management-command"} {
		if !slices.Contains(keys, want) {
			t.Errorf("DjangoKeys missing %q, got %v", want, keys)
		}
	}
}

// TestDjangoRenderUnknownKey reports ok=false for a key Django does not template
// (its CLI keys are handled by djangoCommand, an unknown key by neither).
func TestDjangoRenderUnknownKey(t *testing.T) {
	if _, ok, _ := FrameworkRender("django", "nope", "X", nil, nil); ok {
		t.Fatal("django must not render an unknown key")
	}
}

// TestRenderLaravelPackageDefaultVendor proves a package with no vendor still
// produces a valid PSR-4 namespace (defaults to "Vendor") rather than an empty
// one.
func TestRenderLaravelPackageDefaultVendor(t *testing.T) {
	files, err := RenderLaravelPackage(LaravelPackageVars{Name: "Blog", Target: TargetPackage})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no files")
	}
	// packages/vendor/blog/... with a Vendor\Blog namespace.
	found := false
	for _, f := range files {
		if f.Path == "packages/vendor/blog/composer.json" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected packages/vendor/blog/composer.json, got %+v", files)
	}
}

// TestRenderLaravelPackageBadVendor refuses an unsafe vendor before rendering.
func TestRenderLaravelPackageBadVendor(t *testing.T) {
	if _, err := RenderLaravelPackage(LaravelPackageVars{Name: "Blog", Vendor: "../x"}); err == nil {
		t.Fatal("an unsafe vendor should be refused")
	}
}
