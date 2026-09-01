package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

// TestDryMatrixCoversEveryFrameworkAndEnv drives the real binary once for every
// framework and environment the catalogue offers.
//
// The table is derived from the catalogue rather than written by hand, so a new
// framework, environment or database is covered the day it lands. That is the
// property that keeps recipes honest as upstream tooling drifts: a hand-written
// list only ever tests what someone remembered to add.
//
// It also asserts the *resolved* environment, not just that the command
// succeeded. keel used to fall back silently when an env did not apply, so a row
// could pass while building something else entirely.
func TestDryMatrixCoversEveryFrameworkAndEnv(t *testing.T) {
	bin, err := buildKeel()
	if err != nil {
		t.Fatal(err)
	}
	reg, err := catalog.RegistryBuiltin()
	if err != nil {
		t.Fatal(err)
	}

	rows := dryRows(reg)
	if len(rows) == 0 {
		t.Fatal("no framework/env combinations found: the catalogue failed to load")
	}
	// Every framework must appear, so a framework with no usable environment is
	// a failure rather than a silently empty section of the matrix.
	covered := map[string]bool{}
	for _, r := range rows {
		covered[r.framework] = true
	}
	for _, fw := range reg.OfKind(recipe.Framework) {
		if !covered[fw.ID] {
			t.Errorf("framework %s has no environment it can build in", fw.ID)
		}
	}
	t.Logf("dry matrix: %d framework/env combinations across %d frameworks", len(rows), len(covered))

	work := t.TempDir()
	for _, r := range rows {
		t.Run(r.framework+"/"+r.env, func(t *testing.T) {
			t.Parallel()
			w := &world{bin: bin, work: work}
			target := filepath.Join(work, r.framework+"-"+r.env)
			args := []string{"new", r.framework, "-o", target, "--env", r.env, "--yes", "--dry-run"}
			if r.db != "" {
				args = append(args, "--db", r.db)
			}
			out, err := w.run(90*time.Second, bin, args...)
			if err != nil {
				t.Fatalf("keel new %s/%s: %v\n%s", r.framework, r.env, err, out)
			}
			// The env the plan actually resolved to, not the one we asked for.
			if !strings.Contains(out, r.envLabel) {
				t.Errorf("plan does not show the %s environment (%q):\n%s", r.env, r.envLabel, out)
			}
			if strings.Contains(out, "{{") {
				t.Errorf("plan leaks an unsubstituted token:\n%s", out)
			}
			if _, statErr := os.Stat(target); statErr == nil {
				t.Errorf("a dry run must not create %s", target)
			}
		})
	}
}

type dryRow struct {
	framework, env, envLabel, db string
}

// dryRows is every framework x environment the catalogue can build, each with a
// database that works there (or none, when the environment provides one).
func dryRows(reg *recipe.Registry) []dryRow {
	var out []dryRow
	for _, fw := range reg.OfKind(recipe.Framework) {
		for _, env := range reg.ForFramework(fw.ID, recipe.Env) {
			base := []string{fw.ID, env.ID}
			if _, err := resolver.Resolve(reg, base); err != nil {
				continue // a combination the resolver refuses is covered by the unit tests
			}
			out = append(out, dryRow{
				framework: fw.ID,
				env:       env.ID,
				envLabel:  env.Label,
				db:        resolver.CompatibleDefault(reg, base, recipe.DB, fw.ID, ""),
			})
		}
	}
	return out
}

// TestDryMatrixCountIsAsserted fails when the matrix shrinks unexpectedly. A
// coverage sweep that quietly drops rows still reports success, which is the
// failure mode this guards: the number is a floor, so adding recipes is fine and
// losing them is not.
func TestDryMatrixCountIsAsserted(t *testing.T) {
	reg, err := catalog.RegistryBuiltin()
	if err != nil {
		t.Fatal(err)
	}
	// Raise this when the catalogue genuinely grows.
	const floor = 30
	if n := len(dryRows(reg)); n < floor {
		t.Errorf("dry matrix covers only %d framework/env combinations, expected at least %d: %s",
			n, floor, "did a recipe stop applying, or stop resolving?")
	}
}

// TestEveryDatabaseIsBuildableSomewhere makes sure a database offered in the UI
// can actually be built. A database that applies to a framework but works in no
// environment is a dead choice, which is exactly what the per-framework recipes
// used to be.
func TestEveryDatabaseIsBuildableSomewhere(t *testing.T) {
	reg, err := catalog.RegistryBuiltin()
	if err != nil {
		t.Fatal(err)
	}
	for _, db := range reg.OfKind(recipe.DB) {
		if reachable(reg, db) {
			continue
		}
		t.Errorf("database %s applies to %v but resolves in no environment", db.ID, db.AppliesTo)
	}
}

func reachable(reg *recipe.Registry, db recipe.Recipe) bool {
	for _, fw := range reg.OfKind(recipe.Framework) {
		if !db.AppliesToFramework(fw.ID) {
			continue
		}
		for _, env := range reg.ForFramework(fw.ID, recipe.Env) {
			if _, err := resolver.Resolve(reg, []string{fw.ID, env.ID, db.ID}); err == nil {
				return true
			}
		}
	}
	return false
}
