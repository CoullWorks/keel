package gen

import (
	"slices"
	"strings"
	"testing"
)

// TestRegisteredFrameworks proves every framework this pass wired is registered
// and served by the registry, so the extensibility refactor actually replaced the
// hardcoded laravel+magento switch.
func TestRegisteredFrameworks(t *testing.T) {
	want := []string{
		"adonisjs", "astro", "django", "fastapi", "flask", "laravel",
		"magento", "nestjs", "nextjs", "nuxt", "sveltekit", "symfony",
	}
	got := RegisteredFrameworks()
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("framework %q should be registered, got %v", w, got)
		}
		if !Supports(w) {
			t.Errorf("Supports(%q) should be true", w)
		}
	}
	// Frameworks (the CLI-facing slice) must match the registry and be sorted.
	if !slices.Equal(Frameworks, got) {
		t.Errorf("Frameworks %v != RegisteredFrameworks %v", Frameworks, got)
	}
	if !slices.IsSorted(Frameworks) {
		t.Errorf("Frameworks not sorted: %v", Frameworks)
	}
}

// TestHasModuleConcept is the module-concept signal the studio render fix reads:
// TRUE only for magento (and its variants) and nestjs, FALSE for everyone else.
func TestHasModuleConcept(t *testing.T) {
	forTrue := []string{"magento", "nestjs"}
	forFalse := []string{
		"laravel", "symfony", "adonisjs", "django", "nextjs", "nuxt",
		"astro", "sveltekit", "fastapi", "flask", "unknown-framework",
	}
	for _, f := range forTrue {
		if !HasModuleConcept(f) {
			t.Errorf("HasModuleConcept(%q) should be true", f)
		}
	}
	for _, f := range forFalse {
		if HasModuleConcept(f) {
			t.Errorf("HasModuleConcept(%q) should be false", f)
		}
	}
}

// TestFrameworkCommandCLIDriven checks each CLI-driven framework builds the
// correct argv for a representative component — the exact-analogue-of-artisan
// contract for Symfony/NestJS/Adonis/Django.
func TestFrameworkCommandCLIDriven(t *testing.T) {
	tests := []struct {
		name     string
		family   string
		key      string
		compName string
		want     []string
	}{
		{"laravel model", "laravel", "model", "Order",
			[]string{"ddev", "exec", "php", "artisan", "make:model", "Order", "-mfs"}},
		{"symfony controller", "symfony", "controller", "BlogController",
			[]string{"ddev", "exec", "php", "bin/console", "make:controller", "BlogController"}},
		{"symfony entity", "symfony", "entity", "Post",
			[]string{"ddev", "exec", "php", "bin/console", "make:entity", "Post"}},
		{"symfony migration (no name)", "symfony", "migration", "Ignored",
			[]string{"ddev", "exec", "php", "bin/console", "make:migration"}},
		{"nestjs module", "nestjs", "module", "cats",
			[]string{"ddev", "exec", "npx", "nest", "generate", "module", "cats"}},
		{"nestjs service", "nestjs", "service", "cats",
			[]string{"ddev", "exec", "npx", "nest", "generate", "service", "cats"}},
		{"adonis controller", "adonisjs", "controller", "PostsController",
			[]string{"ddev", "exec", "node", "ace", "make:controller", "PostsController"}},
		{"adonis model", "adonisjs", "model", "Post",
			[]string{"ddev", "exec", "node", "ace", "make:model", "Post"}},
		{"django startapp", "django", "startapp", "blog",
			[]string{"ddev", "exec", "python", "manage.py", "startapp", "blog"}},
		{"django makemigrations (no name)", "django", "makemigrations", "Ignored",
			[]string{"ddev", "exec", "python", "manage.py", "makemigrations"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := FrameworkCommand(tc.family, "ddev", tc.key, tc.compName)
			if !ok {
				t.Fatalf("FrameworkCommand(%q, %q) not ok", tc.family, tc.key)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("argv = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFrameworkCommandMultiWordEnv proves a multi-word env prefix stays split
// into separate argv elements for the new CLIs, exactly like Laravel's.
func TestFrameworkCommandMultiWordEnv(t *testing.T) {
	got, ok := FrameworkCommand("nestjs", "docker compose exec app", "controller", "Cats")
	if !ok {
		t.Fatal("expected ok")
	}
	want := []string{"docker", "compose", "exec", "app", "exec", "npx", "nest", "generate", "controller", "Cats"}
	if !slices.Equal(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
}

// TestFrameworkCommandUnknownKey rejects a key a CLI framework does not define,
// so an unknown component never produces a half-formed argv.
func TestFrameworkCommandUnknownKey(t *testing.T) {
	if _, ok := FrameworkCommand("symfony", "ddev", "nope", "X"); ok {
		t.Fatal("symfony must not build argv for an unknown key")
	}
	// A template-driven framework has no Command at all.
	if _, ok := FrameworkCommand("nextjs", "ddev", "component", "X"); ok {
		t.Fatal("nextjs is template-driven; FrameworkCommand must report false")
	}
}

// TestFrameworkRenderTemplateDriven checks a representative render for each
// template-driven framework: the right path and idiomatic content.
func TestFrameworkRenderTemplateDriven(t *testing.T) {
	tests := []struct {
		name       string
		family     string
		key        string
		compName   string
		wantPath   string
		wantInBody string
	}{
		{"next component", "nextjs", "component", "Hero", "src/components/Hero.tsx", "export function Hero()"},
		{"next api-route", "nextjs", "api-route", "Health", "src/app/api/health/route.ts", "NextResponse.json"},
		{"next hook", "nextjs", "hook", "Cart", "src/hooks/useCart.ts", "export function useCart()"},
		{"nuxt composable", "nuxt", "composable", "Auth", "composables/useAuth.ts", "export function useAuth()"},
		{"nuxt server-route", "nuxt", "server-route", "Ping", "server/api/ping.ts", "defineEventHandler"},
		{"astro page", "astro", "page", "About", "src/pages/about.astro", "About page"},
		{"astro endpoint", "astro", "endpoint", "Api", "src/pages/api.ts", "APIRoute"},
		{"svelte +page", "sveltekit", "page", "Dash", "src/routes/dash/+page.svelte", "Dash"},
		{"svelte +server", "sveltekit", "server", "Data", "src/routes/data/+server.ts", "RequestHandler"},
		{"svelte store", "sveltekit", "store", "User", "src/lib/stores/user.ts", "writable"},
		{"fastapi router", "fastapi", "router", "Items", "app/routers/items.py", "APIRouter"},
		{"fastapi schema", "fastapi", "schema", "Item", "app/schemas/item.py", "class ItemRead"},
		{"flask blueprint", "flask", "blueprint", "Auth", "app/auth/__init__.py", "Blueprint"},
		{"django model", "django", "model", "Post", "models.py", "class Post(models.Model)"},
		{"django serializer", "django", "serializer", "Post", "serializers.py", "PostSerializer"},
		{"django mgmt command", "django", "management-command", "Sync", "management/commands/sync.py", "BaseCommand"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files, ok, err := FrameworkRender(tc.family, tc.key, tc.compName, nil, nil)
			if err != nil {
				t.Fatalf("render error: %v", err)
			}
			if !ok {
				t.Fatalf("FrameworkRender(%q, %q) not ok", tc.family, tc.key)
			}
			if len(files) == 0 {
				t.Fatal("no files rendered")
			}
			if files[0].Path != tc.wantPath {
				t.Fatalf("path = %q, want %q", files[0].Path, tc.wantPath)
			}
			if !strings.Contains(files[0].Content, tc.wantInBody) {
				t.Fatalf("body missing %q:\n%s", tc.wantInBody, files[0].Content)
			}
		})
	}
}

// TestDjangoHybrid proves Django is both CLI-driven (startapp/makemigrations) and
// template-driven (model/serializer/…): FrameworkCommand serves the manage.py
// keys and FrameworkRender serves the template keys, each reporting false for the
// other's keys.
func TestDjangoHybrid(t *testing.T) {
	if _, ok := FrameworkCommand("django", "ddev", "model", "Post"); ok {
		t.Error("django model is a template, not a manage.py command")
	}
	if _, ok, _ := FrameworkRender("django", "startapp", "blog", nil, nil); ok {
		t.Error("django startapp is a manage.py command, not a template")
	}
	if !CLIDriven("django") {
		t.Error("django is CLI-driven (it has manage.py commands)")
	}
}

// TestLaravelPackageTarget covers the app-vs-package Target the fitness review
// wants: a package target scaffolds a distributable Composer package under
// packages/, an app-code target drops it in the app tree.
func TestLaravelPackageTarget(t *testing.T) {
	// Package target (default).
	files, ok, err := FrameworkRender("laravel", "package", "Blog", nil, map[string]any{"vendor": "Acme"})
	if err != nil || !ok {
		t.Fatalf("render package: ok=%v err=%v", ok, err)
	}
	var composer, provider bool
	for _, f := range files {
		if f.Path == "packages/acme/blog/composer.json" {
			composer = true
			if !strings.Contains(f.Content, `"Acme\\Blog\\": "src/"`) {
				t.Errorf("composer.json missing PSR-4 autoload:\n%s", f.Content)
			}
		}
		if f.Path == "packages/acme/blog/src/BlogServiceProvider.php" {
			provider = true
			if !strings.Contains(f.Content, "class BlogServiceProvider extends ServiceProvider") {
				t.Errorf("provider wrong:\n%s", f.Content)
			}
		}
	}
	if !composer || !provider {
		t.Fatalf("package target should emit composer.json + provider, got %d files", len(files))
	}
	// App-code target lands in the app tree, not packages/.
	files, _, err = FrameworkRender("laravel", "package", "Blog", nil, map[string]any{"vendor": "Acme", "target": string(TargetAppCode)})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasPrefix(f.Path, "packages/") {
			t.Errorf("app-code target must not write under packages/: %s", f.Path)
		}
	}
}

// TestLaravelPackageValidatesName refuses an unsafe package name before rendering
// any file, since the name lands in file paths and a PHP namespace.
func TestLaravelPackageValidatesName(t *testing.T) {
	if _, err := RenderLaravelPackage(LaravelPackageVars{Name: "../evil"}); err == nil {
		t.Fatal("an unsafe package name should be refused")
	}
}

// TestNextHasNoModule guards the specific bug: Next.js must not expose a "module"
// generatable, because its render fix hides the Module header only where
// HasModuleConcept is false, and Next has no module concept.
func TestNextHasNoModule(t *testing.T) {
	gs := keys(Generatables(nil, "nextjs"))
	if _, ok := gs["module"]; ok {
		t.Fatal("nextjs must not offer a module generatable")
	}
	if HasModuleConcept("nextjs") {
		t.Fatal("nextjs has no module concept")
	}
	// It must still offer the App-Router essentials.
	for _, k := range []string{"component", "page", "route", "api-route", "hook", "layout"} {
		if _, ok := gs[k]; !ok {
			t.Errorf("nextjs should offer %q", k)
		}
	}
}

// TestNestModuleIsModuleLevel proves the one Node framework with a real module
// concept marks its module generatable at level:module, so a UI can render it as
// a genuine grouping (unlike the spurious Magento-shaped header on stack-only
// frameworks).
func TestNestModuleIsModuleLevel(t *testing.T) {
	gs := keys(Generatables(nil, "nestjs"))
	m, ok := gs["module"]
	if !ok {
		t.Fatal("nestjs should offer a module generatable")
	}
	if string(m.Level) != "module" {
		t.Fatalf("nestjs module level = %q, want module", m.Level)
	}
}
