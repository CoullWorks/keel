package catalog

import (
	"testing"

	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

// TestAWebServerResolvesForEveryFrameworkAndEnv.
//
// No env recipe publishes a host port. That is deliberate: the web server is
// the single ingress, so there is no second, shorter path into the app than
// the one the proxy config describes. The consequence is that picking a web
// server is not a nicety, it is the difference between a stack you can open in
// a browser and five healthy containers you cannot reach.
//
// So every framework must be able to take either web server under every
// environment it offers. A missing provision family here is a hard resolve
// error at build time.
func TestAWebServerResolvesForEveryFrameworkAndEnv(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, fw := range reg.OfKind(recipe.Framework) {
		for _, env := range reg.ForFramework(fw.ID, recipe.Env) {
			for _, web := range []string{"nginx", "apache"} {
				t.Run(fw.ID+"/"+env.ID+"/"+web, func(t *testing.T) {
					if _, err := resolver.Resolve(reg, []string{fw.ID, env.ID, web}); err != nil {
						t.Errorf("cannot front %s on %s with %s: %v", fw.ID, env.ID, web, err)
					}
				})
			}
		}
	}
}

// TestTheDefaultStackIsReachable is the regression guard for a stack you cannot
// open.
//
// `keel new laravel` used to build five healthy containers, a database that
// answered queries, and a printed site_url of http://localhost:8080 that
// refused the connection: no environment recipe publishes a host port, and
// neither web server was selected by default, so nothing was listening. Every
// check keel ran passed.
//
// A default build must therefore publish a port. This asserts the property that
// makes it true - some selected recipe provides "webserver" - rather than
// asserting NGINX by name, so choosing a different default stays legal.
func TestTheDefaultStackIsReachable(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, fw := range reg.OfKind(recipe.Framework) {
		for _, env := range reg.ForFramework(fw.ID, recipe.Env) {
			// Only containerised environments: a native environment runs the
			// framework's own dev server on the host, where there is no port to
			// publish and no proxy to publish it.
			if env.EnvFamily == "local" || env.EnvFamily == "" {
				continue
			}
			t.Run(fw.ID+"/"+env.ID, func(t *testing.T) {
				ids := []string{fw.ID, env.ID}
				for _, s := range reg.ForFramework(fw.ID, recipe.Service) {
					if s.IsDefaultFor(fw.ID) && resolver.SeedableWith(reg, ids, s) {
						ids = append(ids, s.ID)
					}
				}
				plan, err := resolver.Resolve(reg, ids)
				if err != nil {
					t.Fatalf("default stack does not resolve: %v", err)
				}
				for _, r := range plan.Recipes {
					for _, p := range r.Provides {
						if p == "webserver" {
							return
						}
					}
				}
				t.Errorf("a default %s build on %s has no web server, so nothing "+
					"publishes a port and the site_url it prints cannot be opened",
					fw.ID, env.ID)
			})
		}
	}
}
