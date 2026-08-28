package gen_test

import (
	"testing"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/gen"
	"github.com/coullworks/keel/internal/recipe"
)

// TestTestSuiteStackShippedPerFramework proves the shipped catalogue offers a
// test-suite stack generator for each framework that has one, exactly as it
// offers the auth stack. It runs against the real embedded recipes (not a
// synthetic registry), so a generator that stops resolving — or an apply: target
// renamed out from under it — fails here rather than silently vanishing from the
// studio's generate menu.
//
// The mapping mirrors the task: Laravel → Pest, Django → pytest, FastAPI →
// pytest, Next.js → Vitest + Playwright. Each generator must be level: stack,
// provide `tests`, and resolve (ResolveStack) to at least one recipe that
// genuinely provides the `tests` capability.
func TestTestSuiteStackShippedPerFramework(t *testing.T) {
	reg, err := catalog.Registry()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		framework string
		generator string   // the stack generator id
		applies   []string // the recipe ids it must apply, in order
	}{
		{"laravel", "gen-tests-laravel", []string{"pest"}},
		{"django", "gen-tests-django", []string{"django-pytest"}},
		{"fastapi", "gen-tests-fastapi", []string{"fastapi-pytest"}},
		{"nextjs", "gen-tests-nextjs", []string{"nextjs-vitest", "nextjs-playwright"}},
	}

	for _, tc := range cases {
		t.Run(tc.framework, func(t *testing.T) {
			// Generatables must surface the test-suite stack for this framework and
			// for no other (framework-scoped, like the auth stack).
			var found *gen.Generatable
			for _, g := range gen.Generatables(reg, tc.framework) {
				if g.Key == tc.generator {
					g := g
					found = &g
				}
			}
			if found == nil {
				t.Fatalf("Generatables(%q) does not include the test-suite stack %q",
					tc.framework, tc.generator)
			}
			if found.Level != recipe.LevelStack {
				t.Errorf("%s level = %q, want stack", tc.generator, found.Level)
			}
			if !containsCap(found.Provides, "tests") {
				t.Errorf("%s must provide the `tests` capability, got %v", tc.generator, found.Provides)
			}

			// ResolveStack must turn it into the real testing recipe(s), in order.
			ids, ok := gen.ResolveStack(reg, tc.generator)
			if !ok {
				t.Fatalf("%s should resolve as a stack", tc.generator)
			}
			if len(ids) != len(tc.applies) {
				t.Fatalf("%s applies = %v, want %v", tc.generator, ids, tc.applies)
			}
			for i, want := range tc.applies {
				if ids[i] != want {
					t.Errorf("%s applies[%d] = %q, want %q", tc.generator, i, ids[i], want)
				}
			}

			// At least one applied recipe must genuinely provide `tests`, or the
			// stack is a pointer at nothing that tests anything.
			var providesTests bool
			for _, id := range ids {
				r, ok := reg.Get(id)
				if !ok {
					t.Errorf("%s applies %q, which is not in the catalogue", tc.generator, id)
					continue
				}
				if containsCap(r.Provides, "tests") {
					providesTests = true
				}
			}
			if !providesTests {
				t.Errorf("%s applies %v, none of which provides the `tests` capability", tc.generator, ids)
			}
		})
	}

	// The mirror of the auth guard: a framework with no test-suite generator must
	// not be handed another framework's. Laravel must not see Django's.
	for _, g := range gen.Generatables(reg, "laravel") {
		if g.Key == "gen-tests-django" || g.Key == "gen-tests-fastapi" || g.Key == "gen-tests-nextjs" {
			t.Errorf("laravel must not offer another framework's test-suite stack: %q", g.Key)
		}
	}
}

func containsCap(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
