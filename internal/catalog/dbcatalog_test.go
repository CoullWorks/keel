package catalog

import (
	"testing"

	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

// nodeFrameworks is the set of Node framework ids whose database catalogue this
// file guards. They share one client layer that reads DATABASE_URL / KEEL_DB, so
// the databases keel offers them should reflect what that layer can actually
// talk to — not the accident of which recipe happened to name them.
var nodeFrameworks = []string{
	"nextjs", "express", "fastify", "hono", "nestjs",
	"adonisjs", "nuxt", "astro", "sveltekit",
}

// offersDB reports whether framework fw is offered the database recipe id — it
// must be a db-kind recipe that applies to the framework and resolves into a
// valid plan alongside the framework's default (containerised) env, so "offered"
// means "actually buildable", not merely "listed in appliesTo".
func offersDB(t *testing.T, reg *recipe.Registry, fw, dbID string) bool {
	t.Helper()
	r, ok := reg.Get(dbID)
	if !ok || r.Kind != recipe.DB || !r.AppliesToFramework(fw) {
		return false
	}
	// Resolve against a containerised env, where a server database can actually
	// come up (the native env deliberately declines to auto-start one).
	env := ""
	for _, e := range reg.ForFramework(fw, recipe.Env) {
		if e.EnvFamily == recipe.FamilyCompose {
			env = e.ID
			break
		}
	}
	if env == "" {
		return false
	}
	_, err := resolver.Resolve(reg, []string{fw, env, dbID})
	return err == nil
}

// TestNodeFrameworksOfferMongoAndSQLite locks in the fix: every Node framework
// must be able to build on MongoDB and on SQLite, and must keep PostgreSQL. Mongo
// was Django-only and SQLite did not exist, so a Node project could pick neither,
// even though Prisma speaks both and keel's own Express scaffold defaults to
// SQLite.
func TestNodeFrameworksOfferMongoAndSQLite(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, fw := range nodeFrameworks {
		if _, ok := reg.Get(fw); !ok {
			t.Fatalf("node framework %q is not in the catalogue", fw)
		}
		for _, dbID := range []string{"postgres", "mongo", "sqlite"} {
			if !offersDB(t, reg, fw, dbID) {
				t.Errorf("%s should offer %s as a primary database, but does not", fw, dbID)
			}
		}
	}
}

// TestSQLiteIsTheNativeNodeDefault: under the native (local) env, a Node
// framework has no Docker, so the sensible default is the server-less database.
// Before SQLite existed, the resolver either offered a container-starting
// database behind the back of someone who chose a native env, or none at all.
func TestSQLiteIsTheNativeNodeDefault(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, fw := range nodeFrameworks {
		var localEnv string
		for _, e := range reg.ForFramework(fw, recipe.Env) {
			if e.EnvFamily == recipe.FamilyLocal {
				localEnv = e.ID
				break
			}
		}
		if localEnv == "" {
			continue // a framework with no native env has nothing to default here
		}
		if got := defaultDB(reg, fw, localEnv); got != "sqlite" {
			t.Errorf("%s / %s should default to sqlite (server-less), got %q", fw, localEnv, got)
		}
	}
}

// TestPgvectorReachableForNode: pgvector must be a real, resolvable choice for a
// Node framework on Postgres — modelled as a Postgres addon (a service that
// requires the postgres capability and adds the `vector` extension), not a
// separate primary database. This proves it composes into a valid plan.
func TestPgvectorReachableForNode(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	pv, ok := reg.Get("pgvector")
	if !ok {
		t.Fatal("pgvector recipe is missing")
	}
	// It is a service that layers onto Postgres, not a primary database.
	if pv.Kind != recipe.Service {
		t.Errorf("pgvector should be kind service (layers onto Postgres), got %q", pv.Kind)
	}
	requiresPostgres := false
	for _, r := range pv.Requires {
		if r == "postgres" {
			requiresPostgres = true
		}
	}
	if !requiresPostgres {
		t.Error("pgvector should require the postgres capability")
	}
	// Reachable: express + its compose env + Postgres + pgvector resolves.
	if _, err := resolver.Resolve(reg, []string{"express", "express-docker", "postgres", "pgvector"}); err != nil {
		t.Errorf("express + postgres + pgvector should resolve, got: %v", err)
	}
	// And it must refuse a non-Postgres primary rather than silently doing nothing.
	if _, err := resolver.Resolve(reg, []string{"express", "express-docker", "mysql", "pgvector"}); err == nil {
		t.Error("pgvector should refuse a MySQL plan (the vector extension is PostgreSQL-only)")
	}
}

// TestStandaloneVectorStoresSitBesideThePrimary: Qdrant and Chroma are vector
// databases that run ALONGSIDE the relational primary, so they must NOT conflict
// with a primary database — only with each other and with pgvector (one vector
// backend per project).
func TestStandaloneVectorStoresSitBesideThePrimary(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"qdrant", "chroma"} {
		r, ok := reg.Get(id)
		if !ok {
			t.Fatalf("%s recipe is missing", id)
		}
		if r.Kind != recipe.Service {
			t.Errorf("%s should be kind service (sits beside the primary), got %q", id, r.Kind)
		}
		provides := false
		for _, p := range r.Provides {
			if p == "vectordb" {
				provides = true
			}
		}
		if !provides {
			t.Errorf("%s should provide the vectordb capability", id)
		}
		// Must resolve beside a relational primary: express + Postgres + the store.
		if _, err := resolver.Resolve(reg, []string{"express", "express-docker", "postgres", id}); err != nil {
			t.Errorf("express + postgres + %s should resolve (it sits beside the primary), got: %v", id, err)
		}
	}
	// The two standalone stores and pgvector are mutually exclusive: one backend.
	if _, err := resolver.Resolve(reg, []string{"express", "express-docker", "postgres", "qdrant", "chroma"}); err == nil {
		t.Error("qdrant + chroma in one plan should be refused (one vector backend per project)")
	}
}
