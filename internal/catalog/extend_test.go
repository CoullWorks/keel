package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/resolver"
)

// dropRecipe writes a loose user recipe into an isolated config dir so a test
// can extend the catalogue exactly the way a third party would.
func dropRecipe(t *testing.T, name, body string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("KEEL_CONFIG_DIR"), "recipes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestThirdPartyCanAddADatabase is the extensibility claim, tested: a new
// database becomes available to a framework by dropping in one file, with no
// edit to any built-in recipe. Before the shared-recipe split this needed a
// copy of the database per framework.
func TestThirdPartyCanAddADatabase(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	dropRecipe(t, "cockroach.yaml", `
id: cockroach
schema_version: 2
kind: db
label: CockroachDB
appliesTo: [django]
provides: [db, cockroach, db-cockroach]
conflicts: [db-postgres, db-mysql, db-mariadb, db-mongo]
priority: -5
vars:
  db.host: db
  db.port: "26257"
  db.name: app
  db.user: root
  db.password: ""
provision:
  ddev:
    files:
      - path: .ddev/docker-compose.cockroach.yaml
        content: |
          services:
            db:
              image: cockroachdb/cockroach:latest
              command: start-single-node --insecure
`)
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := resolver.Resolve(reg, []string{"django", "django-ddev", "cockroach"})
	if err != nil {
		t.Fatalf("a dropped-in database should just work: %v", err)
	}
	if !hasRecipe(plan, "cockroach") {
		t.Fatalf("plan should include the new database: %v", plan.IDs())
	}
	// It reaches only the framework it declared, not every framework.
	if _, err := resolver.Resolve(reg, []string{"laravel", "ddev", "cockroach"}); err == nil {
		t.Fatal("a database should not apply to a framework it does not name")
	}
	// And it is refused, with a reason, under an environment it never described.
	if _, err := resolver.Resolve(reg, []string{"django", "django-local", "cockroach"}); err == nil {
		t.Fatal("an unsupported environment should be refused, not silently ignored")
	}
}

// TestThirdPartyCanWireAnExistingDatabaseToAFramework is the other half: a
// framework gains a database that already exists by contributing only its own
// wiring, with no change to the database recipe's provisioning. Symfony ships
// with no database at all, so this is a genuine addition.
func TestThirdPartyCanWireAnExistingDatabaseToAFramework(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	// Widening which frameworks a shared database serves is a one-line override
	// of its appliesTo; how it comes up is untouched.
	dropRecipe(t, "postgres.yaml", `
id: postgres
schema_version: 2
kind: db
label: PostgreSQL 16
appliesTo: [django, fastapi, laravel, nextjs, symfony]
provides: [db, postgres, db-postgres]
conflicts: [db-mysql, db-mariadb, db-mongo]
priority: -5
vars:
  db.host: db
  db.port: "5432"
  db.name: db
  db.user: db
  db.password: db
provision:
  ddev:
    files:
      - path: .ddev/config.db.yaml
        content: |
          database:
            type: postgres
            version: "16"
`)
	// The framework-specific half: how Symfony is told about it.
	dropRecipe(t, "postgres-symfony.yaml", `
id: postgres-symfony
schema_version: 2
kind: config
label: Symfony wiring for PostgreSQL
when:
  framework: symfony
  uses: postgres
provision:
  ddev:
    patch:
      - file: .env
        set:
          DATABASE_URL: "postgresql://{{db.user}}:{{db.password}}@{{db.host}}:{{db.port}}/{{db.name}}"
`)
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := resolver.Resolve(reg, []string{"symfony", "symfony-ddev", "postgres"})
	if err != nil {
		t.Fatalf("symfony should now offer postgres: %v", err)
	}
	// The overlay is injected by the resolver, never named by the caller.
	if !hasRecipe(plan, "postgres-symfony") {
		t.Fatalf("the matching overlay should be injected: %v", plan.IDs())
	}
	// Naming a config recipe explicitly is ignored rather than rejected: it is
	// injected from the rest of the plan anyway, and an older manifest may still
	// list one. The plan comes out the same either way.
	named, err := resolver.Resolve(reg, []string{"symfony", "symfony-ddev", "postgres", "postgres-symfony"})
	if err != nil {
		t.Fatalf("naming a config recipe should be harmless: %v", err)
	}
	if len(named.IDs()) != len(plan.IDs()) {
		t.Fatalf("naming a config recipe changed the plan: %v vs %v", named.IDs(), plan.IDs())
	}
	// And its wiring reaches the build, reading the database's published vars.
	eff := engine.Effective(plan, "app")
	if !strings.Contains(eff, "DATABASE_URL=postgresql://db:db@db:5432/db") {
		t.Fatalf("the overlay should patch the connection using the db.* contract:\n%s", eff)
	}
	// The overlay does not leak to a framework it does not name.
	other, err := resolver.Resolve(reg, []string{"django", "django-ddev", "postgres"})
	if err != nil {
		t.Fatal(err)
	}
	if hasRecipe(other, "postgres-symfony") {
		t.Fatal("a symfony overlay must not attach to django")
	}
}

// TestOldDatabaseIdsStillResolve pins the promise made to existing projects: the
// per-framework ids written into manifests and profiles keep working after the
// databases were merged into shared recipes.
func TestOldDatabaseIdsStillResolve(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for old, want := range map[string]string{
		"django-postgres":  "postgres",
		"django-mysql":     "mysql",
		"django-mariadb":   "mariadb",
		"django-mongo":     "mongo",
		"django-supabase":  "supabase",
		"fastapi-postgres": "postgres",
		"nextjs-postgres":  "postgres",
	} {
		got, ok := reg.Canonical(old)
		if !ok {
			t.Errorf("old id %q no longer resolves", old)
			continue
		}
		if got != want {
			t.Errorf("old id %q resolves to %q, want %q", old, got, want)
		}
	}
	// A manifest holding an old id still produces the same plan as the new one.
	oldPlan, err := resolver.Resolve(reg, []string{"django", "django-ddev", "django-postgres"})
	if err != nil {
		t.Fatal(err)
	}
	newPlan, err := resolver.Resolve(reg, []string{"django", "django-ddev", "postgres"})
	if err != nil {
		t.Fatal(err)
	}
	if a, b := oldPlan.IDs(), newPlan.IDs(); len(a) != len(b) {
		t.Fatalf("old and new ids should build the same plan: %v vs %v", a, b)
	}
}

func hasRecipe(p *resolver.Plan, id string) bool {
	for _, r := range p.Recipes {
		if r.ID == id {
			return true
		}
	}
	return false
}
