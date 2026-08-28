package catalog

import (
	"testing"

	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

// TestMagentoStorefrontsAreMutuallyExclusive: Hyvä, PWA Studio, ScandiPWA,
// Alokai and a bespoke Next.js front end are five answers to the same question.
// Two of them in one plan is not a richer store, it is two storefronts fighting
// over the same routes, so the resolver has to refuse it rather than build it.
func TestMagentoStorefrontsAreMutuallyExclusive(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	fronts := reg.ForFramework("magento", recipe.Frontend)
	if len(fronts) < 2 {
		t.Skip("fewer than two storefronts to compare")
	}
	for i := range fronts {
		for j := range fronts {
			if i >= j {
				continue
			}
			ids := []string{"magento", "magento-docker", "mariadb", fronts[i].ID, fronts[j].ID}
			if _, err := resolver.Resolve(reg, ids); err == nil {
				t.Errorf("%s and %s resolved together; they are alternative storefronts and must conflict",
					fronts[i].ID, fronts[j].ID)
			}
		}
	}
}

// TestEachMagentoStorefrontResolvesAlone: the flip side. Every storefront must
// still build on its own, or the conflicts above are just a way to make all of
// them unusable.
func TestEachMagentoStorefrontResolvesAlone(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range reg.ForFramework("magento", recipe.Frontend) {
		if _, err := resolver.Resolve(reg, []string{"magento", "magento-docker", "mariadb", f.ID}); err != nil {
			t.Errorf("%s does not resolve on its own: %v", f.ID, err)
		}
	}
}

// TestEveryFrameworkGetsGit: a scaffolded project should be a git repository,
// whatever it is built with.
//
// The git recipe used to live under laravel/extras and name three PHP
// frameworks, so a Symfony, Python or Node project came out of keel with no
// repository and no first commit - the one thing every project wants and the
// easiest to not notice is missing.
func TestEveryFrameworkGetsGit(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, fw := range reg.OfKind(recipe.Framework) {
		found := false
		for _, e := range reg.ForFramework(fw.ID, recipe.Extra) {
			if e.ID == "git" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is not offered the git extra", fw.ID)
		}
	}
}

// TestEveryFrameworkGetsCI: CI shipped for laravel, django and fastapi only, so
// seventeen of the twenty frameworks scaffolded with no pipeline.
//
// There is one CI extra per language, because the toolchain setup step is the
// part that cannot be shared. This fails when a framework is added to the
// catalogue without being added to one of them.
func TestEveryFrameworkGetsCI(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, fw := range reg.OfKind(recipe.Framework) {
		var offered []string
		for _, e := range reg.ForFramework(fw.ID, recipe.Extra) {
			for _, p := range e.Provides {
				if p == "ci" {
					offered = append(offered, e.ID)
				}
			}
		}
		switch len(offered) {
		case 0:
			t.Errorf("%s is offered no CI workflow", fw.ID)
		case 1: // as intended
		default:
			// Two would both write .github/workflows/ci.yml, and the second
			// would silently overwrite the first.
			t.Errorf("%s is offered %d CI workflows (%v); they write the same file", fw.ID, len(offered), offered)
		}
	}
}

// TestEveryFrameworkHasTestAndLintTasks: `keel run test` and `keel run lint`
// are advertised as the way to do those things whatever the stack. Only 8 of 20
// frameworks declared a test task and 4 declared lint, so the verb worked on
// some projects and not others with nothing to explain the difference.
//
// A framework genuinely without a runner should say so in a comment and be
// listed here as a known exception, rather than be silently missing.
func TestEveryFrameworkHasTestAndLintTasks(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	// WordPress ships no test runner and no linter of its own, and keel does not
	// install one: declaring the tasks would mean `keel run test` failing on a
	// freshly scaffolded store.
	knownWithout := map[string][]string{
		"woocommerce":         {"test", "lint"},
		"woocommerce-bedrock": {"test", "lint"},
		"magento":             {}, // has both
	}
	excused := func(fw, task string) bool {
		for _, x := range knownWithout[fw] {
			if x == task {
				return true
			}
		}
		return false
	}
	for _, fw := range reg.OfKind(recipe.Framework) {
		for _, task := range []string{"test", "lint"} {
			if _, ok := fw.Tasks[task]; !ok && !excused(fw.ID, task) {
				t.Errorf("%s declares no %q task, so `keel run %s` does not work there", fw.ID, task, task)
			}
		}
	}
}
