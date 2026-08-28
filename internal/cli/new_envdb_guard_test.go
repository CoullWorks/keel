package cli

import (
	"strings"
	"testing"
)

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
