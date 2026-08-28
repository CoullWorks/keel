package catalog

import (
	"testing"

	"github.com/coullworks/keel/internal/recipe"
)

// TestEnvsAreFrameworkScoped guards the bug where `keel init` showed Django's
// uv-local env under Laravel: env recipes must be scoped to their framework.
func TestEnvsAreFrameworkScoped(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	laravel := map[string]bool{}
	for _, r := range reg.ForFramework("laravel", recipe.Env) {
		laravel[r.ID] = true
	}
	for _, want := range []string{"ddev", "laravel-docker", "laravel-local", "sail"} {
		if !laravel[want] {
			t.Errorf("laravel envs missing %q (got %v)", want, laravel)
		}
	}
	for _, leak := range []string{"django-local", "django-ddev", "django-docker", "magento-ddev"} {
		if laravel[leak] {
			t.Errorf("%q leaked into laravel envs: %v", leak, laravel)
		}
	}
}
