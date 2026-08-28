package cli

import (
	"testing"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/recipe"
)

func TestRenderTask(t *testing.T) {
	ddev := map[string]string{"exec": "ddev exec", "artisan": "ddev exec php artisan"}
	local := map[string]string{"exec": ""} // Local env: no exec prefix
	cases := []struct {
		cmd, want string
		env       map[string]string
	}{
		{"{{exec}} php artisan test", "ddev exec php artisan test", ddev},
		{"{{artisan}} migrate", "ddev exec php artisan migrate", ddev},
		{"{{exec}} npm run dev", "npm run dev", local},
		{"", "", ddev},
	}
	for _, c := range cases {
		if got := renderTask(c.cmd, c.env, "app"); got != c.want {
			t.Errorf("renderTask(%q) = %q, want %q", c.cmd, got, c.want)
		}
	}
}

// Every framework must define at least the core tasks so `keel run` is uniform.
func TestFrameworksDefineCoreTasks(t *testing.T) {
	reg, err := catalog.Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, fw := range reg.OfKind(recipe.Framework) {
		if len(fw.Tasks) == 0 {
			t.Errorf("framework %s defines no tasks", fw.ID)
			continue
		}
		if _, ok := fw.Tasks["dev"]; !ok {
			t.Errorf("framework %s missing a 'dev' task", fw.ID)
		}
	}
}

// The e2e harness, a compose file and a developer with a reason all set PORT
// and expect the server to be there. Allocating one anyway put Next.js on a
// different port from the one the caller was polling, and every boot scenario
// failed.
func TestPortFromEnv(t *testing.T) {
	t.Setenv("PORT", "")
	if p, err := portFromEnv(); p != 0 || err != nil {
		t.Errorf("unset PORT should mean allocate, got (%d, %v)", p, err)
	}
	t.Setenv("PORT", "4321")
	if p, err := portFromEnv(); p != 4321 || err != nil {
		t.Errorf("want 4321, got (%d, %v)", p, err)
	}
	t.Setenv("PORT", " 8080 ")
	if p, err := portFromEnv(); p != 8080 || err != nil {
		t.Errorf("surrounding space should be tolerated, got (%d, %v)", p, err)
	}
	for _, bad := range []string{"nonsense", "0", "-1", "70000"} {
		t.Setenv("PORT", bad)
		if _, err := portFromEnv(); err == nil {
			t.Errorf("PORT=%q should be an error, not silently ignored", bad)
		}
	}
}

func TestContainerEnvsRouteForThemselves(t *testing.T) {
	for _, env := range []string{"ddev", "django-ddev", "sail", "laravel-docker", "nextjs-docker"} {
		if !containerEnv(env) {
			t.Errorf("%s brings its own routing and should not get a host port", env)
		}
	}
	for _, env := range []string{"express-local", "nextjs-local", "fastapi-local", "flask-local"} {
		if containerEnv(env) {
			t.Errorf("%s has no router, so it needs a host port", env)
		}
	}
}
