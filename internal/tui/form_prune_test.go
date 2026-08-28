package tui

import (
	"testing"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/profile"
)

// dbStep returns the Database step's options for a prior selection that has the
// framework at index 2 and the env at index 3 (envStep) — the shape the wizard
// hands each step's Dynamic. It fails the test if there is no Database step.
func dbStep(t *testing.T, framework, env string) []Choice {
	t.Helper()
	reg, err := catalog.Registry()
	if err != nil {
		t.Skip("no catalogue")
	}
	steps := BuildSteps(reg, &profile.Profile{Defaults: map[string]string{}})
	prior := [][]string{{"python"}, {framework}, {framework}, {env}}
	for i := range steps {
		if steps[i].Title == "Database" {
			if steps[i].Dynamic == nil {
				t.Fatal("Database step has no Dynamic")
			}
			return steps[i].Dynamic(prior)
		}
	}
	t.Fatal("no Database step")
	return nil
}

func hasKey(opts []Choice, key string) bool {
	for _, o := range opts {
		if o.Key == key {
			return true
		}
	}
	return false
}

// TestDatabaseStepPrunesIncompatibleDB is the wizard half of the Supabase +
// compose bug. Supabase provisions only its local stack, so under a compose env
// it must be hidden from the Database step rather than offered and left to die
// in resolver.Resolve at build time (docs/ROUTES.md §4: never build an invalid
// combo). This one filter in tui.BuildSteps is what fixes the CLI wizard and the
// console at once, because both drive the same steps.
func TestDatabaseStepPrunesIncompatibleDB(t *testing.T) {
	// Under a compose env, Supabase (local-only) is gone but Postgres — which
	// provisions for compose — remains.
	compose := dbStep(t, "django", "django-docker")
	if hasKey(compose, "supabase") {
		t.Error("Supabase offered under the compose env, but it only provisions its local stack — the wizard would build a combo resolver.Resolve rejects")
	}
	if !hasKey(compose, "postgres") {
		t.Error("Postgres pruned under the compose env, but it provisions for compose — a valid combo must stay offered")
	}
}

// TestDatabaseStepKeepsValidComboUnderLocal: the same env-only-local database is
// offered under the env it does support, so pruning removes the invalid combo
// without hiding the valid one.
func TestDatabaseStepKeepsValidComboUnderLocal(t *testing.T) {
	local := dbStep(t, "django", "django-local")
	if !hasKey(local, "supabase") {
		t.Error("Supabase pruned under the local env, but that is exactly the env it provisions for")
	}
	if !hasKey(local, "postgres") {
		t.Error("Postgres pruned under the local env — it provisions for local")
	}
}

// TestDatabaseStepUnprunedWithoutEnv: with no env answered yet there is nothing
// to prune against (SeedableWith needs the env in the base to judge a db's
// provision), so every database for the framework is offered. This is also the
// shape older tests use when they call a later step's Dynamic with a short prior.
func TestDatabaseStepUnprunedWithoutEnv(t *testing.T) {
	none := dbStep(t, "django", "")
	if !hasKey(none, "supabase") {
		t.Error("Supabase hidden with no env chosen yet; pruning must wait until there is an env to prune against")
	}
}

// webServerStepOpts returns the Web server step's options for a framework, the
// same shape the wizard hands each step's Dynamic (language, family, type, env).
func webServerStepOpts(t *testing.T, lang, framework string) []Choice {
	t.Helper()
	reg, err := catalog.Registry()
	if err != nil {
		t.Skip("no catalogue")
	}
	steps := BuildSteps(reg, &profile.Profile{Defaults: map[string]string{}})
	prior := [][]string{{lang}, {framework}, {framework}, {framework + "-docker"}}
	for i := range steps {
		if steps[i].Title == "Web server" {
			if steps[i].Dynamic == nil {
				t.Fatal("Web server step has no Dynamic")
			}
			return steps[i].Dynamic(prior)
		}
	}
	t.Fatal("no Web server step")
	return nil
}

// TestWebServerStepPrunesFrontControllersForSelfServing is the webserver twin of
// the DB env-prune. A Node framework serves its own traffic, so the Web server
// step must offer its process manager (PM2) and never a PHP front controller
// (Apache/NGINX + a language handler): fronting a self-serving app with one
// builds a stack the front controller has nothing to mount. Both wizards drive
// this one filter, so the CLI and studio agree.
func TestWebServerStepPrunesFrontControllersForSelfServing(t *testing.T) {
	node := webServerStepOpts(t, "node", "express")
	if hasKey(node, "apache") {
		t.Error("Apache (a PHP front controller) offered to a self-serving Node framework")
	}
	if hasKey(node, "nginx") {
		t.Error("shared NGINX (a PHP front controller) offered to a self-serving Node framework")
	}
	if !hasKey(node, "pm2") {
		t.Error("PM2 (the process-manager ingress) not offered to a self-serving Node framework")
	}
}

// TestWebServerStepKeepsFrontControllersForNonSelfServing: a PHP framework has no
// listener of its own, so the front controllers stay and the Node process
// manager is not offered — the split does not touch the frameworks that need a
// front controller.
func TestWebServerStepKeepsFrontControllersForNonSelfServing(t *testing.T) {
	php := webServerStepOpts(t, "php", "laravel")
	if !hasKey(php, "nginx") || !hasKey(php, "apache") {
		t.Error("a front controller was pruned for Laravel, which needs one")
	}
	if hasKey(php, "pm2") {
		t.Error("PM2 (a Node process manager) offered to a PHP framework that has no self-serving listener")
	}
}
