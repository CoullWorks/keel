package catalog

import (
	"reflect"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/recipe"
)

// TestBothMagentoDistributionsInstallIdentically.
//
// Adobe's Magento Open Source and Mage-OS are the same codebase from different
// Composer repositories, so everything after `create` - the setup:install flags,
// the module state, the indexer mode, the cache flush - must stay identical.
//
// Those twenty lines live in both recipes rather than in a shared one, and that
// is a deliberate trade with this test as the other half of it. The shared
// version was tried: a `kind: config` recipe applies cleanly, but config sorts
// after addon, so `magefan-blog`'s setup:upgrade would have run before
// setup:install had ever created the database. Duplication that cannot drift
// beats an ordering that is wrong.
//
// It matters more than usual here because only one of the two can be built
// without an Adobe account. If they drift, the one nobody can test is the one
// that breaks.
func TestBothMagentoDistributionsInstallIdentically(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	adobe, ok := reg.Get("magento")
	if !ok {
		t.Fatal("no magento recipe")
	}
	mageos, ok := reg.Get("magento-mageos")
	if !ok {
		t.Fatal("no magento-mageos recipe")
	}
	if !reflect.DeepEqual(adobe.Install, mageos.Install) {
		t.Errorf("the two distributions no longer install the same way.\nadobe:  %q\nmageos: %q",
			adobe.Install, mageos.Install)
	}
	if !reflect.DeepEqual(adobe.Smoke, mageos.Smoke) {
		t.Errorf("the two distributions no longer smoke-test the same way:\n%q\n%q", adobe.Smoke, mageos.Smoke)
	}
	// The repository is the whole difference, so it had better differ.
	if adobe.Vars["magento.repo"] == mageos.Vars["magento.repo"] {
		t.Error("both distributions point at the same Composer repository")
	}
	if !strings.Contains(mageos.Vars["magento.repo"], "mage-os") {
		t.Errorf("the Mage-OS variant does not use the Mage-OS repository: %q", mageos.Vars["magento.repo"])
	}
	// And the point of the variant: no account needed.
	if len(mageos.Credentials) != 0 {
		t.Errorf("the Mage-OS variant asks for credentials, which is the one thing it exists to avoid: %+v",
			mageos.Credentials)
	}
	if len(adobe.Credentials) == 0 {
		t.Error("the Adobe variant no longer asks for Marketplace keys, which its repository requires")
	}
}

// TestAnInstallCanReachItsContainers: every step after create is an exec into
// the stack, so something has to start it. Magento had no {{start}} at all: its
// first command failed with `service "php" is not running`, on both
// distributions, which is why it had never been built end to end.
func TestAnInstallCanReachItsContainers(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, fw := range reg.OfKind(recipe.Framework) {
		var started bool
		for _, step := range fw.Install {
			if strings.Contains(step, "{{start}}") || strings.Contains(step, "{{restart}}") {
				started = true
			}
			if !started && strings.Contains(step, "{{exec}}") && !strings.Contains(step, "{{create}}") {
				t.Errorf("%s: %q execs into the stack before anything starts it",
					fw.ID, strings.TrimSpace(step)[:min(70, len(strings.TrimSpace(step)))])
				break
			}
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestOpenSearchIsStartableAtAll.
//
// Since OpenSearch 2.12 the image's entrypoint installs a security demo
// configuration before it reads any node setting, and that step exits 1 unless
// OPENSEARCH_INITIAL_ADMIN_PASSWORD is set. Turning security off via the
// opensearch.yml setting - plugins.security.disabled - does not skip it,
// because the setting is read later than the check.
//
// DISABLE_SECURITY_PLUGIN is the variable the entrypoint itself tests, so it is
// the one that has to be present. Magento's stack set only the setting, so
// OpenSearch never started; `docker compose ps` just did not list it, and the
// real symptom arrived two minutes later, 672 modules into setup:install, as
// "Could not validate a connection to the OpenSearch. No alive nodes found in
// your cluster".
// Parses the compose document rather than grepping it. The first version of
// this test searched the raw text, which matched the comment explaining the
// variable and passed even with the variable deleted.
func TestOpenSearchIsStartableAtAll(t *testing.T) {
	eachComposeDoc(t, func(where string, doc composeDoc, _ map[string]string) {
		{
			for name, svc := range doc.Services {
				if !strings.Contains(svc.Image, "opensearchproject/opensearch") {
					continue
				}
				// The other half of the same trap: DISABLE_SECURITY_PLUGIN makes
				// the entrypoint pass -Eplugins.security.disabled=true itself, so
				// setting that as a second environment variable hands OpenSearch
				// the same setting twice and it refuses to start with "setting
				// [plugins.security.disabled] already set, saw [true] and
				// [true]". Putting it in a mounted opensearch.yml is fine; a
				// duplicate environment variable is not.
				if hasEnv(svc.Environment, "DISABLE_SECURITY_PLUGIN") &&
					hasEnv(svc.Environment, "plugins.security.disabled") {
					t.Errorf("%s: service %q sets both DISABLE_SECURITY_PLUGIN and "+
						"plugins.security.disabled in its environment, which gives OpenSearch the "+
						"setting twice and stops it starting", where, name)
				}
				if !hasEnv(svc.Environment, "DISABLE_SECURITY_PLUGIN") {
					t.Errorf("%s: service %q runs OpenSearch without DISABLE_SECURITY_PLUGIN, so its "+
						"entrypoint demands OPENSEARCH_INITIAL_ADMIN_PASSWORD and the container exits 1 "+
						"before it ever starts. plugins.security.disabled does not substitute: it is a "+
						"node setting, read after the check that fails.", where, name)
				}
			}
		}
	})
}

// hasEnv reports whether a compose `environment` block sets key, in either of
// the two shapes compose accepts (a mapping, or a list of KEY=value strings).
func hasEnv(env any, key string) bool {
	switch v := env.(type) {
	case map[string]any:
		_, ok := v[key]
		return ok
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && strings.HasPrefix(s, key+"=") {
				return true
			}
		}
	}
	return false
}

// TestAStorefrontThatDeliversNothingSaysSo.
//
// The Alokai recipe deliberately scaffolds nothing: its CLI is interactive and
// asks for Magento API credentials keel cannot invent, so it writes
// instructions instead. That is the right call - a recipe that guessed at
// credentials would produce a storefront that builds and cannot talk to
// anything. What is not right is a label promising a storefront and delivering
// a markdown file, because the label is the whole basis on which it is picked.
//
// "Delivers nothing" means no install steps AND no files except documentation.
// It is deliberately not "no install steps": django-htmx has none, because HTMX
// and Alpine load from a CDN, and it still writes a 115-line working page. An
// earlier version of this test flagged it, and would have had me "fix" a recipe
// that was doing its job.
func TestAStorefrontThatDeliversNothingSaysSo(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reg.OfKind(recipe.Frontend) {
		delivers := false
		for _, step := range r.Install {
			if s := strings.TrimSpace(step); s != "" && s != "true" {
				delivers = true
			}
		}
		for _, f := range r.Files {
			if !strings.HasSuffix(strings.ToLower(f.Path), ".md") {
				delivers = true
			}
		}
		if delivers {
			continue
		}
		lower := strings.ToLower(r.Label)
		admits := strings.Contains(lower, "not scaffolded") ||
			strings.Contains(lower, "guided") ||
			strings.Contains(lower, "instructions") ||
			strings.Contains(lower, "manual")
		if !admits {
			t.Errorf("%s writes only documentation but its label does not say so: %q", r.ID, r.Label)
		}
	}
}
