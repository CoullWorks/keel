package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/gen"
)

// TestGenLaravelModelWithFields: a Laravel model with --field is written as files
// (model + migration) carrying real columns and $fillable, not the empty artisan
// migration.
func TestGenLaravelModelWithFields(t *testing.T) {
	wd := genProject(t, "laravel", "ddev")
	out, err := runRoot(t, "gen", "model", "Order",
		"--field", "title:string", "--field", "total:decimal", "--field", "note:text:nullable")
	if err != nil {
		t.Fatalf("gen model with fields: %v", err)
	}
	mustContain(t, out, "app/Models/Order.php", "files written")
	// The migration filename is timestamped, so glob for it.
	migs, _ := filepath.Glob(filepath.Join(wd, "database", "migrations", "*_create_orders_table.php"))
	if len(migs) != 1 {
		t.Fatalf("expected one orders migration, got %v", migs)
	}
	mig, _ := os.ReadFile(migs[0])
	for _, w := range []string{"$table->string('title')", "$table->decimal('total', 12, 4)", "$table->text('note')->nullable()"} {
		if !strings.Contains(string(mig), w) {
			t.Errorf("migration missing %q:\n%s", w, mig)
		}
	}
	model, _ := os.ReadFile(filepath.Join(wd, "app", "Models", "Order.php"))
	if !strings.Contains(string(model), "protected $fillable = ['title', 'total', 'note'];") {
		t.Errorf("model $fillable wrong:\n%s", model)
	}
}

// TestGenLaravelModelNoFieldsKeepsArtisan: without --field the Laravel model keeps
// the artisan make path, so nothing regresses.
func TestGenLaravelModelNoFieldsKeepsArtisan(t *testing.T) {
	genProject(t, "laravel", "ddev")
	out, err := runRoot(t, "gen", "model", "Order", "--dry-run")
	if err != nil {
		t.Fatalf("gen model no fields: %v", err)
	}
	mustContain(t, out, "make:model", "Order")
}

// TestGenFieldOnlyOnModel: --field is rejected on a non-model component.
func TestGenFieldOnlyOnModel(t *testing.T) {
	genProject(t, "laravel", "ddev")
	_, err := runRoot(t, "gen", "controller", "OrderController", "--field", "x:string")
	if err == nil {
		t.Fatal("expected --field to be rejected on a controller")
	}
	mustContain(t, err.Error(), "--field only applies to a model")
}

// TestGenBadFieldRejected: an unknown field type is refused before any write.
func TestGenBadFieldRejected(t *testing.T) {
	wd := genProject(t, "laravel", "ddev")
	_, err := runRoot(t, "gen", "model", "Order", "--field", "title:blob")
	if err == nil {
		t.Fatal("expected an unknown field type to be refused")
	}
	if _, statErr := os.Stat(filepath.Join(wd, "app", "Models", "Order.php")); statErr == nil {
		t.Fatal("a bad field should not have written any file")
	}
}

// TestGenMagentoModelWithFields: a Magento model with --field renders db_schema.xml
// columns beyond the hardcoded entity_id.
func TestGenMagentoModelWithFields(t *testing.T) {
	wd := genProject(t, "magento", "docker")
	out, err := runRoot(t, "gen", "model", "Post", "--module", "Acme/Blog",
		"--field", "title:string", "--field", "author_id:foreignId")
	if err != nil {
		t.Fatalf("gen magento model with fields: %v", err)
	}
	mustContain(t, out, "files written")
	schema, err := os.ReadFile(filepath.Join(wd, "app", "code", "Acme", "Blog", "etc", "db_schema.xml"))
	if err != nil {
		t.Fatalf("db_schema not written: %v", err)
	}
	for _, w := range []string{`name="entity_id"`, `name="title"`, `name="author_id"`} {
		if !strings.Contains(string(schema), w) {
			t.Errorf("db_schema missing %q:\n%s", w, schema)
		}
	}
}

// TestGenAuthResolvesStack: `keel gen auth` names the framework's auth recipe(s)
// and points at `keel add`, replacing the broken `gen auth` verb.
func TestGenAuthResolvesStack(t *testing.T) {
	cases := map[string]string{
		"laravel":   "laravel-breeze",
		"django":    "django-allauth",
		"nextjs":    "nextjs-authjs",
		"fastapi":   "fastapi-jwt",
		"symfony":   "symfony-security",
		"flask":     "flask-login",
		"nestjs":    "nestjs-jwt",
		"nuxt":      "nuxt-auth-utils",
		"sveltekit": "sveltekit-better-auth",
		"astro":     "astro-better-auth",
	}
	for fw, recipeID := range cases {
		t.Run(fw, func(t *testing.T) {
			genProject(t, fw, "docker")
			out, err := runRoot(t, "gen", "auth")
			if err != nil {
				t.Fatalf("gen auth on %s: %v", fw, err)
			}
			mustContain(t, out, "keel add", recipeID)
		})
	}
}

// TestGenTestsResolvesStack: `keel gen tests` names the framework's test-suite
// recipe(s) and points at `keel add`, the tests twin of `keel gen auth`. It closes
// the Wave-3 gap where the Node meta-frameworks, Flask and Symfony had a proven
// test addon (node-vitest / flask-pytest / symfony-phpunit) but no stack verb to
// reach it.
func TestGenTestsResolvesStack(t *testing.T) {
	cases := map[string]string{
		"nuxt":      "node-vitest",
		"sveltekit": "node-vitest",
		"astro":     "node-vitest",
		"nestjs":    "node-vitest",
		"express":   "node-vitest",
		"symfony":   "symfony-phpunit",
		"flask":     "flask-pytest",
	}
	for fw, recipeID := range cases {
		t.Run(fw, func(t *testing.T) {
			genProject(t, fw, "docker")
			out, err := runRoot(t, "gen", "tests")
			if err != nil {
				t.Fatalf("gen tests on %s: %v", fw, err)
			}
			mustContain(t, out, "keel add", recipeID)
		})
	}
}

// TestGenAuthMagento: Magento has no auth stack in the catalogue, so gen auth says
// so rather than pretending.
func TestGenAuthMagento(t *testing.T) {
	genProject(t, "magento", "docker")
	_, err := runRoot(t, "gen", "auth")
	if err == nil {
		t.Fatal("expected magento to have no auth stack")
	}
	mustContain(t, err.Error(), "no auth stack")
}

// TestGenGeneratablesAccessor: the studio-facing accessor returns the right
// generatables per framework — the signature the studio round calls.
func TestGenGeneratablesAccessor(t *testing.T) {
	reg, err := loadCatalog(t)
	if err != nil {
		t.Fatal(err)
	}
	// Laravel: code-blocks + auth stack.
	lar := gen.Generatables(reg, "laravel")
	if !hasKey(lar, "model") {
		t.Error("laravel generatables should include model")
	}
	if !hasKey(lar, "gen-auth-laravel") {
		t.Error("laravel generatables should include its auth stack")
	}
	// Next.js: no code-blocks, but its auth stack.
	next := gen.Generatables(reg, "nextjs")
	if hasKey(next, "model") {
		t.Error("nextjs has no built-in model code-block")
	}
	if !hasKey(next, "gen-auth-nextjs") {
		t.Error("nextjs generatables should include its auth stack")
	}
}

// TestMakeAliasHelp: `keel make` resolves as an alias of gen (defined in gen.go),
// keeping keel gen back-compat while adding the object-first entrypoint.
func TestMakeAliasHelp(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "make", "--help")
	if err != nil {
		t.Fatalf("make --help: %v", err)
	}
	mustContain(t, out, "guided", "Magento")
}

func hasKey(gs []gen.Generatable, key string) bool {
	for _, g := range gs {
		if g.Key == key {
			return true
		}
	}
	return false
}
