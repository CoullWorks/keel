package resolver

import (
	"testing"

	"github.com/coullworks/keel/internal/recipe"
)

// webFixture adds the pieces the seeding rules are about: two web servers that
// refuse each other, and one that only knows how to provision for compose.
func webFixture() *recipe.Registry {
	reg := fixture()
	_ = reg.Add(recipe.Recipe{
		ID: "nginx", Kind: recipe.Service, AppliesTo: []string{"*"},
		Provides: []string{"webserver", "nginx"}, Conflicts: []string{"apache"}, Default: true,
		Provision: map[string]recipe.Fragment{"compose": {}},
	})
	_ = reg.Add(recipe.Recipe{
		ID: "apache", Kind: recipe.Service, AppliesTo: []string{"*"},
		Provides: []string{"webserver", "apache"}, Conflicts: []string{"nginx"},
		Provision: map[string]recipe.Fragment{"compose": {}},
	})
	_ = reg.Add(recipe.Recipe{
		ID: "compose", Kind: recipe.Env, AppliesTo: []string{"laravel"},
		Provides: []string{"env"}, EnvFamily: "compose",
	})
	_ = reg.Add(recipe.Recipe{
		ID: "venv", Kind: recipe.Env, AppliesTo: []string{"laravel"},
		Provides: []string{"env"}, EnvFamily: "venv", // a family nginx has never heard of
	})
	return reg
}

// A conflict in either direction counts: whichever of the pair declares it.
func TestConflictsWithAnyIsSymmetric(t *testing.T) {
	reg := webFixture()
	nginx, _ := reg.Get("nginx")
	apache, _ := reg.Get("apache")

	if !ConflictsWithAny(reg, []string{"apache"}, nginx) {
		t.Error("nginx should refuse to join a plan that already has apache")
	}
	if !ConflictsWithAny(reg, []string{"nginx"}, apache) {
		t.Error("apache should refuse to join a plan that already has nginx")
	}
	if ConflictsWithAny(reg, []string{"redis"}, nginx) {
		t.Error("nginx and redis do not conflict")
	}
	// An id the registry does not know is ignored rather than fatal: a manifest
	// written by an older keel may name a recipe that has since been renamed.
	if ConflictsWithAny(reg, []string{"who-is-this"}, nginx) {
		t.Error("an unknown id should not be treated as a conflict")
	}
}

// The rule a default lives by: yield rather than break the build.
func TestSeedableWithYieldsRatherThanBreakingTheBuild(t *testing.T) {
	reg := webFixture()
	nginx, _ := reg.Get("nginx")
	redis, _ := reg.Get("redis")
	filament, _ := reg.Get("filament")

	if !SeedableWith(reg, []string{"laravel", "compose"}, nginx) {
		t.Error("nginx provisions for compose, so it is seedable there")
	}
	// Yields to an explicit choice it cannot sit beside. Without this,
	// `--with apache` failed on a conflict the user never created.
	if SeedableWith(reg, []string{"laravel", "compose", "apache"}, nginx) {
		t.Error("nginx must yield to an explicitly chosen apache")
	}
	// Yields to an environment it has no provisioning for, rather than making
	// the whole plan fail. A pack can define an env family keel has never seen.
	if SeedableWith(reg, []string{"laravel", "venv"}, nginx) {
		t.Error("nginx must yield to an environment family it cannot provision for")
	}
	// With no environment at all there is no family to key on, so a recipe that
	// provisions per family has nothing it could contribute.
	if SeedableWith(reg, []string{"laravel"}, nginx) {
		t.Error("a per-family recipe is not seedable when no environment is selected")
	}
	// No provision map at all: applies the same everywhere.
	if !SeedableWith(reg, []string{"laravel"}, redis) {
		t.Error("a recipe with no provision map provisions the same way everywhere")
	}
	// Unmet requirements disqualify a default too: Telescope was briefly
	// declared as requiring a database, and on a native environment that
	// refused to build over an add-on nobody asked for.
	if SeedableWith(reg, []string{"laravel"}, filament) {
		t.Error("a default requiring db must yield when no database is selected")
	}
	if !SeedableWith(reg, []string{"laravel", "mysql"}, filament) {
		t.Error("with a database selected the requirement is met")
	}
}
