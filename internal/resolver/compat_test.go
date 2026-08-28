package resolver

import (
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/catalog"
)

// TestCompatibleEnvsNamesLocalOnlyEnv: Supabase provisions only its local stack,
// so the compatible-env list for Django names django-local (and only it). The
// resolve-time error and the early flag guard both use this to turn an abstract
// "provisions for: local" into a "try --env django-local" the user can act on.
func TestCompatibleEnvsNamesLocalOnlyEnv(t *testing.T) {
	reg, err := catalog.Registry()
	if err != nil {
		t.Skip("no catalogue")
	}
	sup, ok := reg.Get("supabase")
	if !ok {
		t.Skip("no supabase recipe")
	}
	envs := CompatibleEnvs(reg, "django", sup)
	if len(envs) != 1 || envs[0] != "django-local" {
		t.Fatalf("CompatibleEnvs(supabase) = %v, want [django-local] (its only provisioning env)", envs)
	}
}

// TestResolveNamesCompatibleEnvOnBadCombo: building Supabase under a compose env
// must fail, and the error must name the env that would work — not die with a
// bare capability clash. This is the resolver-error half of the fix.
func TestResolveNamesCompatibleEnvOnBadCombo(t *testing.T) {
	reg, err := catalog.Registry()
	if err != nil {
		t.Skip("no catalogue")
	}
	if _, ok := reg.Get("supabase"); !ok {
		t.Skip("no supabase recipe")
	}
	_, err = Resolve(reg, []string{"django", "django-docker", "supabase"})
	if err == nil {
		t.Fatal("resolved Supabase under a compose env; that combo cannot be built")
	}
	if !strings.Contains(err.Error(), "django-local") {
		t.Errorf("error %q does not name the compatible env (django-local) — the user is told what fails but not how to fix it", err)
	}
}

// TestResolveAcceptsValidCombo: the valid twin still passes. Supabase under its
// local env, and Postgres under compose, both resolve — pruning/guarding the bad
// combo must not block the good ones.
func TestResolveAcceptsValidCombo(t *testing.T) {
	reg, err := catalog.Registry()
	if err != nil {
		t.Skip("no catalogue")
	}
	if _, ok := reg.Get("supabase"); ok {
		if _, err := Resolve(reg, []string{"django", "django-local", "supabase"}); err != nil {
			t.Errorf("Supabase under its local env should resolve, got %v", err)
		}
	}
	if _, err := Resolve(reg, []string{"django", "django-docker", "postgres"}); err != nil {
		t.Errorf("Postgres under a compose env should resolve, got %v", err)
	}
}

// TestSeedableWithGuardsEnvDBCombo is the predicate both front doors share: the
// wizard prunes the Database step with it, and `keel new` guards the flag path
// with it. Assert it agrees with Resolve so the two never drift.
func TestSeedableWithGuardsEnvDBCombo(t *testing.T) {
	reg, err := catalog.Registry()
	if err != nil {
		t.Skip("no catalogue")
	}
	sup, ok := reg.Get("supabase")
	if !ok {
		t.Skip("no supabase recipe")
	}
	if SeedableWith(reg, []string{"django", "django-docker"}, sup) {
		t.Error("SeedableWith says Supabase fits a compose env, but resolver.Resolve rejects it — the wizard prune and the flag guard would offer a combo that cannot build")
	}
	if !SeedableWith(reg, []string{"django", "django-local"}, sup) {
		t.Error("SeedableWith rejects Supabase under its own local env — pruning would hide a valid combo")
	}

	pg, ok := reg.Get("postgres")
	if !ok {
		return
	}
	if !SeedableWith(reg, []string{"django", "django-docker"}, pg) {
		t.Error("SeedableWith rejects Postgres under compose, which it provisions for")
	}
}
