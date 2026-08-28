package cli

import (
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/recipe"
)

// TestNewEnvFlagResolvesByFamily is the fix for the bug where `--env ddev` only
// ever meant Laravel's bare-id `ddev` recipe, so `keel new fastapi --env ddev`
// was refused even though fastapi-ddev exists. `--env <family>` must resolve to
// the env recipe of that family that applies to the named framework.
func TestNewEnvFlagResolvesByFamily(t *testing.T) {
	reg, err := loadCatalog(t)
	if err != nil {
		t.Skip("no catalogue")
	}
	// (framework, family) pairs that must resolve, given a matching recipe exists.
	cases := []struct{ fw, fam string }{
		{"fastapi", "ddev"},  // Python has a ddev recipe; the flag must reach it
		{"django", "ddev"},   // ditto
		{"fastapi", "local"}, // families other than ddev resolve the same way
		{"laravel", "ddev"},  // Laravel's bare-id recipe still resolves
		{"laravel", "sail"},  // and its sail recipe
		{"django", "docker"}, // "docker" is the everyday name for the compose family
	}
	for _, c := range cases {
		if len(reg.ForFramework(c.fw, recipe.Env)) == 0 {
			continue
		}
		isolate(t)
		if _, err := runRoot(t, "new", c.fw, "--env", c.fam, "--dry-run"); err != nil {
			t.Errorf("keel new %s --env %s should resolve by family, got: %v", c.fw, c.fam, err)
		}
	}
	// A token that is neither an id nor a family is still a clear "unknown".
	isolate(t)
	if _, err := runRoot(t, "new", "fastapi", "--env", "banana", "--dry-run"); err == nil ||
		!strings.Contains(err.Error(), "unknown env") {
		t.Errorf("--env banana should be an unknown-env error, got: %v", err)
	}
}

// TestNewGuardsIncompatibleEnvDB is the flag-path half of the Supabase + compose
// bug. `keel new django --env django-docker --db supabase` cannot be built —
// Supabase provisions only its local stack — so the command must fail early with
// a message that names the env to switch to, rather than dying deep in
// resolver.Resolve at build time. --dry-run proves it stops before any build.
func TestNewGuardsIncompatibleEnvDB(t *testing.T) {
	isolate(t)
	reg, err := loadCatalog(t)
	if err != nil {
		t.Skip("no catalogue")
	}
	if _, ok := reg.Get("supabase"); !ok {
		t.Skip("no supabase recipe")
	}
	out, err := runRoot(t, "new", "django", "--env", "django-docker", "--db", "supabase", "--dry-run")
	if err == nil {
		t.Fatalf("expected an early error for Supabase + a compose env, got none\n%s", out)
	}
	if !strings.Contains(err.Error(), "django-local") {
		t.Errorf("error %q must name the env that works (django-local) so the user can fix the flag", err)
	}
}

// TestNewAllowsValidEnvDB: the valid twin still builds (dry-run). Guarding the
// bad combo must not block Supabase under its own local env, nor Postgres under
// a compose env.
func TestNewAllowsValidEnvDB(t *testing.T) {
	isolate(t)
	reg, err := loadCatalog(t)
	if err != nil {
		t.Skip("no catalogue")
	}
	if _, ok := reg.Get("supabase"); ok {
		if _, err := runRoot(t, "new", "django", "--env", "django-local", "--db", "supabase", "--dry-run"); err != nil {
			t.Errorf("Supabase under its local env should pass the guard, got %v", err)
		}
	}
	if _, err := runRoot(t, "new", "django", "--env", "django-docker", "--db", "postgres", "--dry-run"); err != nil {
		t.Errorf("Postgres under a compose env should pass the guard, got %v", err)
	}
}
