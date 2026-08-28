package catalog

import (
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

// TestOnlyClassicWordPressEditsWpConfig.
//
// `wp config set` edits wp-config.php, and Bedrock has no wp-config.php: its
// configuration is .env plus config/application.php, which is the point of the
// layout. Running those hooks against a Bedrock project fails with "Could not
// process the 'wp-config.php' transformation. Reason: Unable to locate
// placement anchor." - and it fails in post_build, so the store is installed,
// the plugins are activated and the very last step kills the build.
//
// The two layouts share the whole web and PHP-FPM tier, which is what makes
// this easy to get wrong: it is one file away from applying to both.
func TestOnlyClassicWordPressEditsWpConfig(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	editsWpConfig := func(fw string) bool {
		plan, err := resolver.Resolve(reg, []string{fw, "woocommerce-docker", "nginx"})
		if err != nil {
			t.Fatalf("%s: %v", fw, err)
		}
		for _, r := range plan.Recipes {
			for _, h := range r.Hooks["post_build"] {
				if strings.Contains(h.Run, "config set") {
					return true
				}
			}
		}
		return false
	}
	if !editsWpConfig("woocommerce") {
		t.Error("classic WordPress no longer sets its production wp-config.php constants " +
			"(WP_DEBUG, DISALLOW_FILE_EDIT, DISABLE_WP_CRON): the store would run with " +
			"WP-Cron on and the file editor enabled")
	}
	if editsWpConfig("woocommerce-bedrock") {
		t.Error("Bedrock is being handed `wp config set`, which needs a wp-config.php it " +
			"does not have; the build fails after the store is already installed")
	}
}

// TestBothWordPressLayoutsOfferTheSameEnvironments: Bedrock had DDEV and nothing
// else, so choosing it in the wizard silently removed the Docker option that the
// classic layout offers.
func TestBothWordPressLayoutsOfferTheSameEnvironments(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	families := func(fw string) map[string]bool {
		out := map[string]bool{}
		for _, e := range reg.ForFramework(fw, recipe.Env) {
			out[e.EnvFamily] = true
		}
		return out
	}
	classic, bedrock := families("woocommerce"), families("woocommerce-bedrock")
	for fam := range classic {
		if !bedrock[fam] {
			t.Errorf("classic WordPress offers a %q environment and Bedrock does not, so "+
				"picking Bedrock takes an option away", fam)
		}
	}
}

// TestVariantsOfferWhatTheirFamilyOffers.
//
// A framework variant is the same product configured differently, so it should
// be offered the same databases, services and add-ons as the rest of its
// family. Nothing enforces that: applicability is a list of framework ids, so
// adding a variant means editing every shared recipe that names the original,
// and missing one removes a choice silently.
//
// magento-mageos was added and recipes/infra/db/mariadb.yaml still listed only
// magento. The database recipe therefore never applied, so the my.cnf its
// compose file mounts was never written - and Docker, asked to bind-mount a
// path that does not exist, created it as a root-owned directory inside the
// user's project. The build then failed several minutes later with
// "mkdir .keel/nginx: permission denied", which names neither the database nor
// the missing file.
func TestVariantsOfferWhatTheirFamilyOffers(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	offered := func(fw string, k recipe.Kind) map[string]bool {
		out := map[string]bool{}
		for _, r := range reg.ForFramework(fw, k) {
			out[r.ID] = true
		}
		return out
	}
	for _, g := range recipe.Families(reg.OfKind(recipe.Framework)) {
		if len(g.Variants) < 2 {
			continue
		}
		for _, kind := range []recipe.Kind{recipe.DB, recipe.Service} {
			base := offered(g.Primary.ID, kind)
			for _, v := range g.Variants {
				if v.ID == g.Primary.ID {
					continue
				}
				got := offered(v.ID, kind)
				for id := range base {
					if !got[id] {
						t.Errorf("%s offers %s %q and its variant %s does not: the variant was added "+
							"without extending that recipe's appliesTo",
							g.Primary.ID, kind, id, v.ID)
					}
				}
			}
		}
	}
}
