package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scaffoldManifest writes a .keel manifest so engine.ReadManifest reports the
// given stack.
func scaffoldManifest(t *testing.T, dir, fw, env string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".keel"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := "framework: " + fw + "\nenv: " + env + "\nrecipes: [" + fw + ", " + env + "]\n"
	if err := os.WriteFile(filepath.Join(dir, ".keel", "manifest.yaml"), []byte(m), 0o644); err != nil {
		t.Fatal(err)
	}
}

// scaffoldLaravelManifest is the Laravel-on-DDEV case, used where the point of
// the test is that gen reads the framework from the manifest.
func scaffoldLaravelManifest(t *testing.T, dir string) {
	t.Helper()
	scaffoldManifest(t, dir, "laravel", "ddev")
}

// genProject isolates a working directory and makes it a keel project, because
// `keel gen` operates on one the way `keel db` and `keel run` do. It used to
// invent laravel-on-ddev wherever it was run, so every one of these tests passed
// in a directory with no project in it at all.
func genProject(t *testing.T, fw, env string) string {
	t.Helper()
	wd := isolate(t)
	scaffoldManifest(t, wd, fw, env)
	return wd
}

// gen (Laravel) dry-run renders the artisan command and writes nothing.
func TestGenLaravelDryRun(t *testing.T) {
	genProject(t, "laravel", "ddev")
	out, err := runRoot(t, "gen", "model", "Order", "--dry-run", "-f", "laravel")
	if err != nil {
		t.Fatalf("gen model dry-run: %v", err)
	}
	mustContain(t, out, "make:model", "Order")
}

// gen reads the framework from the project manifest when -f is absent.
func TestGenReadsFrameworkFromManifest(t *testing.T) {
	wd := isolate(t)
	scaffoldLaravelManifest(t, wd)
	out, err := runRoot(t, "gen", "model", "Widget", "--dry-run")
	if err != nil {
		t.Fatalf("gen (manifest fw): %v", err)
	}
	mustContain(t, out, "make:model", "Widget")
}

// gen with an unknown component errors before running anything.
func TestGenLaravelUnknownComponent(t *testing.T) {
	genProject(t, "laravel", "ddev")
	_, err := runRoot(t, "gen", "not-a-component", "X", "--dry-run", "-f", "laravel")
	if err == nil {
		t.Fatal("expected error for unknown component")
	}
	mustContain(t, err.Error(), "unknown laravel component")
}

// gen with a known component but no name asks for one.
func TestGenLaravelNoName(t *testing.T) {
	genProject(t, "laravel", "ddev")
	_, err := runRoot(t, "gen", "model", "--dry-run", "-f", "laravel")
	if err == nil {
		t.Fatal("expected error for missing name")
	}
	mustContain(t, err.Error(), "give at least one name")
}

// TestGenRefusesInjectingNames is the CLI-level regression test for command
// injection through `keel gen`: names used to be interpolated into a string run
// by `sh -c`, and the path is reachable from the MCP write tools where an agent
// supplies the argument. Every one of these must be refused, not defused.
func TestGenRefusesInjectingNames(t *testing.T) {
	// A leading dash never reaches this check: cobra rejects it as an unknown
	// flag first. Everything that does reach the generator is checked here.
	for _, name := range []string{
		"X; touch pwned", "X && touch pwned", "X | sh", "$(touch pwned)", "`touch pwned`",
		"../../escape", "X\ntouch pwned",
	} {
		t.Run(name, func(t *testing.T) {
			wd := genProject(t, "laravel", "ddev")
			// Not a dry run: the command would really execute if it were built.
			_, err := runRoot(t, "gen", "model", name, "-f", "laravel")
			if err == nil {
				t.Fatalf("gen should refuse the name %q", name)
			}
			mustContain(t, err.Error(), "invalid name")
			if _, statErr := os.Stat(filepath.Join(wd, "pwned")); statErr == nil {
				t.Fatalf("name %q executed a command", name)
			}
		})
	}
}

// The same names are refused on the Magento path, where they would otherwise
// become file paths under the project.
func TestGenMagentoRefusesTraversingNames(t *testing.T) {
	genProject(t, "magento", "docker")
	if _, err := runRoot(t, "gen", "model", "../../escape", "--module", "Acme/Blog", "--dry-run", "-f", "magento"); err == nil {
		t.Fatal("a traversing component name should be refused")
	}
	if _, err := runRoot(t, "gen", "module", "../../etc/passwd", "--dry-run", "-f", "magento"); err == nil {
		t.Fatal("a traversing vendor/module should be refused")
	}
}

// gen -f magento: `module Vendor/Module` renders the module skeleton (dry-run).
func TestGenMagentoModuleDryRun(t *testing.T) {
	genProject(t, "magento", "docker")
	out, err := runRoot(t, "gen", "module", "Acme/Blog", "--dry-run", "-f", "magento")
	if err != nil {
		t.Fatalf("gen magento module: %v", err)
	}
	mustContain(t, out, "app/code/Acme/Blog", "registration.php")
}

// gen -f magento: a non-module component needs --module and renders under it.
func TestGenMagentoModelDryRun(t *testing.T) {
	genProject(t, "magento", "docker")
	out, err := runRoot(t, "gen", "model", "Post", "--module", "Acme/Blog", "--dry-run", "-f", "magento")
	if err != nil {
		t.Fatalf("gen magento model: %v", err)
	}
	mustContain(t, out, "app/code/Acme/Blog/Model/Post.php")
}

// gen -f magento: an unknown magento component errors.
func TestGenMagentoUnknownComponent(t *testing.T) {
	genProject(t, "magento", "docker")
	_, err := runRoot(t, "gen", "nope", "X", "--dry-run", "-f", "magento")
	if err == nil {
		t.Fatal("expected error for unknown magento component")
	}
	mustContain(t, err.Error(), "unknown magento component")
}

// gen -f magento: a non-module component without --module errors.
func TestGenMagentoNoModule(t *testing.T) {
	genProject(t, "magento", "docker")
	_, err := runRoot(t, "gen", "model", "Post", "--dry-run", "-f", "magento")
	if err == nil {
		t.Fatal("expected error for missing --module")
	}
	mustContain(t, err.Error(), "need --module")
}

// gen -f magento: `module` with no Vendor/Module arg errors.
func TestGenMagentoModuleNoArg(t *testing.T) {
	genProject(t, "magento", "docker")
	_, err := runRoot(t, "gen", "module", "--dry-run", "-f", "magento")
	if err == nil {
		t.Fatal("expected usage error for module with no arg")
	}
	mustContain(t, err.Error(), "usage: keel gen module")
}

// gen -f magento: a malformed Vendor/Module string errors.
func TestGenMagentoBadVendorModule(t *testing.T) {
	genProject(t, "magento", "docker")
	_, err := runRoot(t, "gen", "module", "NoSlashHere", "--dry-run", "-f", "magento")
	if err == nil {
		t.Fatal("expected error for malformed Vendor/Module")
	}
	mustContain(t, err.Error(), "expected Vendor/Module")
}

// gen -f magento without --dry-run writes the module files to disk (no Docker,
// no network — pure template rendering).
func TestGenMagentoWritesFiles(t *testing.T) {
	wd := genProject(t, "magento", "docker")
	out, err := runRoot(t, "gen", "module", "Acme/Blog", "-f", "magento")
	if err != nil {
		t.Fatalf("gen magento module (write): %v", err)
	}
	mustContain(t, out, "files written")
	if _, err := os.Stat(filepath.Join(wd, "app", "code", "Acme", "Blog", "registration.php")); err != nil {
		t.Errorf("module file not written: %v", err)
	}
}

// gen --help documents both stacks.
func TestGenHelp(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "gen", "--help")
	if err != nil {
		t.Fatalf("gen --help: %v", err)
	}
	mustContain(t, out, "Laravel", "Magento")
}

// splitVendorModule + splitCSV unit coverage.
func TestSplitHelpers(t *testing.T) {
	v, m, err := splitVendorModule("Acme/Blog")
	if err != nil || v != "Acme" || m != "Blog" {
		t.Errorf("splitVendorModule = (%q,%q,%v)", v, m, err)
	}
	if _, _, err := splitVendorModule("noslash"); err == nil {
		t.Error("expected error for no slash")
	}
	if _, _, err := splitVendorModule("/Blog"); err == nil {
		t.Error("expected error for empty vendor")
	}
	got := splitCSV(" a, b ,,c ")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("splitCSV = %v", got)
	}
	if len(splitCSV("   ")) != 0 {
		t.Error("splitCSV of blanks should be empty")
	}
}

// genTargetFor reads the stack from the manifest.
func TestGenTargetFromManifest(t *testing.T) {
	wd := genProject(t, "magento", "local")
	tgt, err := genTargetFor("")
	if err != nil {
		t.Fatalf("genTargetFor: %v", err)
	}
	if tgt.Framework != "magento" || tgt.Env != "local" || tgt.Dir != wd {
		t.Errorf("genTargetFor = %+v, want magento/local in %s", tgt, wd)
	}
}

// In a directory that is not a keel project, gen refuses instead of generating.
//
// It used to fall back to laravel-on-ddev, so `keel gen model Order` in an empty
// directory printed a plan to run `ddev exec php artisan make:model Order` there
// — a command for a project that does not exist, aimed at whatever happened to
// be in the working directory.
func TestGenRefusesOutsideAProject(t *testing.T) {
	isolate(t)
	_, err := runRoot(t, "gen", "model", "Order", "--dry-run")
	if err == nil {
		t.Fatal("gen outside a keel project should refuse, not assume laravel")
	}
	mustContain(t, err.Error(), "not a keel project")
	// The forced-framework flag is an override, not a way past the check: the
	// project is still what says where the files go.
	if _, err := runRoot(t, "gen", "model", "Order", "--dry-run", "-f", "laravel"); err == nil {
		t.Fatal("-f should not waive the project requirement")
	}
}

// A framework that ships its own generator CLI drives it, rather than being
// handed Laravel's artisan. Every non-Magento stack used to fall through to
// artisan, so a Django project asking for a model was given `php artisan
// make:model`; now each of these drives the framework's own generator. Each is a
// dry run, so it asserts the command plan without executing anything.
func TestGenDrivesEachFrameworksOwnGenerator(t *testing.T) {
	cases := []struct{ fw, comp, name, want string }{
		{"symfony", "entity", "Product", "bin/console make:entity"},
		{"nestjs", "service", "Orders", "nest generate service"},
		{"adonisjs", "model", "Invoice", "ace make:model"},
		{"django", "startapp", "billing", "manage.py startapp"},
	}
	for _, c := range cases {
		t.Run(c.fw, func(t *testing.T) {
			genProject(t, c.fw, "docker")
			out, err := runRoot(t, "gen", c.comp, c.name, "--dry-run")
			if err != nil {
				t.Fatalf("gen %s on %s: %v", c.comp, c.fw, err)
			}
			mustContain(t, out, c.want, c.name)
			if strings.Contains(out, "artisan") {
				t.Errorf("gen on %s produced a Laravel command:\n%s", c.fw, out)
			}
		})
	}
}

// A framework with no generator CLI (the Node meta-frameworks, the Python
// micro-frameworks, Django's templated half) renders idiomatic files into its
// conventional paths — never a Laravel command. The dry run shows the file plan.
func TestGenRendersTemplatesForTemplateFrameworks(t *testing.T) {
	cases := []struct{ fw, comp, name, wantPath string }{
		{"nextjs", "component", "Button", "src/components/Button.tsx"},
		{"fastapi", "router", "Orders", "app/routers/orders.py"},
		{"django", "model", "Product", "models.py"},
		{"flask", "blueprint", "Shop", "app/shop/__init__.py"},
	}
	for _, c := range cases {
		t.Run(c.fw, func(t *testing.T) {
			genProject(t, c.fw, "docker")
			out, err := runRoot(t, "gen", c.comp, c.name, "--dry-run")
			if err != nil {
				t.Fatalf("gen %s on %s: %v", c.comp, c.fw, err)
			}
			mustContain(t, out, c.wantPath)
			if strings.Contains(out, "artisan") {
				t.Errorf("gen on %s produced a Laravel command:\n%s", c.fw, out)
			}
		})
	}
}

// A framework keel has no generator for at all — no code-block generator and no
// auth/tests stack either (the data-app frameworks: dash, streamlit) — is told so
// by name, rather than being handed Laravel's artisan. This is the honest refusal
// for a stack keel does not yet generate. (express is not used here anymore: it
// now carries a tests stack via node-vitest, so it hits the "no code-block, but
// `keel gen tests`" branch instead.)
func TestGenRefusesUnsupportedFramework(t *testing.T) {
	for _, fw := range []string{"dash", "streamlit"} {
		t.Run(fw, func(t *testing.T) {
			genProject(t, fw, "docker")
			out, err := runRoot(t, "gen", "model", "Order", "--dry-run")
			if err == nil {
				t.Fatalf("gen model on %s should refuse, got:\n%s", fw, out)
			}
			mustContain(t, err.Error(), "no generators for "+fw)
			if strings.Contains(out, "artisan") {
				t.Errorf("gen on %s produced a Laravel command:\n%s", fw, out)
			}
		})
	}
}

// The Laravel package key renders a distributable Composer package skeleton (not
// artisan), honouring the app-vs-package Target: the CLI half of the choice the
// fitness review asks for. With --target app-code the same files land in the app
// tree instead of packages/.
func TestGenLaravelPackage(t *testing.T) {
	genProject(t, "laravel", "ddev")
	out, err := runRoot(t, "gen", "package", "Blog", "--vendor", "Acme", "--dry-run")
	if err != nil {
		t.Fatalf("gen package: %v", err)
	}
	mustContain(t, out, "packages/acme/blog", "composer.json", "BlogServiceProvider.php")
	if strings.Contains(out, "artisan") {
		t.Errorf("package generation should render files, not run artisan:\n%s", out)
	}

	out, err = runRoot(t, "gen", "package", "Blog", "--target", "app-code", "--dry-run")
	if err != nil {
		t.Fatalf("gen package app-code: %v", err)
	}
	mustContain(t, out, filepath.Join("app", "Packages", "Blog"))
}

// A framework variant generates as its family: a Mage-OS project is a Magento
// project, and used to fall through to Laravel's artisan because the check was
// an equality test against "magento".
func TestGenResolvesFrameworkVariantToItsFamily(t *testing.T) {
	genProject(t, "magento-mageos", "docker")
	out, err := runRoot(t, "gen", "module", "Acme/Blog", "--dry-run")
	if err != nil {
		t.Fatalf("gen on magento-mageos: %v", err)
	}
	mustContain(t, out, "app/code/Acme/Blog", "registration.php")
}

// Every generated plan names the project, the framework and (where commands run
// through it) the environment. "Which project did that just write into" should
// not be a question answered by inspecting the filesystem afterwards.
func TestGenNamesItsTarget(t *testing.T) {
	wd := genProject(t, "laravel", "sail")
	out, err := runRoot(t, "gen", "model", "Order", "--dry-run")
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	mustContain(t, out, "laravel", "sail", wd)

	wd = genProject(t, "magento", "ddev")
	out, err = runRoot(t, "gen", "module", "Acme/Blog", "--dry-run")
	if err != nil {
		t.Fatalf("gen magento: %v", err)
	}
	mustContain(t, out, "magento", wd)
	// Magento's files are written straight to disk, so naming the env here would
	// imply a container round-trip that does not happen.
	if strings.Contains(out, "on ddev") {
		t.Errorf("magento file generation should not claim to run through the env:\n%s", out)
	}
}

// An unknown component lists the ones that exist, per framework. The catalogue
// used to be discoverable only by opening the picker or reading the source.
func TestGenUnknownComponentListsTheRealOnes(t *testing.T) {
	genProject(t, "laravel", "ddev")
	_, err := runRoot(t, "gen", "not-a-component", "X", "--dry-run")
	if err == nil {
		t.Fatal("expected an error for an unknown component")
	}
	mustContain(t, err.Error(), "model", "controller", "migration")

	genProject(t, "magento", "ddev")
	_, err = runRoot(t, "gen", "not-a-component", "X", "--dry-run")
	if err == nil {
		t.Fatal("expected an error for an unknown magento component")
	}
	mustContain(t, err.Error(), "module", "observer")
}

// A manifest with no env cannot say how to reach artisan, so gen says that
// rather than building an argv with an empty command in front of it.
func TestGenRefusesLaravelWithNoEnv(t *testing.T) {
	genProject(t, "laravel", "")
	_, err := runRoot(t, "gen", "model", "Order", "--dry-run")
	if err == nil {
		t.Fatal("expected an error for a manifest with no env")
	}
	mustContain(t, err.Error(), "no env")
}
