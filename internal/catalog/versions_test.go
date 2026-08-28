package catalog

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
	"gopkg.in/yaml.v3"
)

// patchValue finds what a plan would write for one key of one file.
func patchValue(t *testing.T, plan *resolver.Plan, file, key string) string {
	t.Helper()
	vars := engine.PlanVarsForTest(plan, "proj")
	for _, r := range plan.Recipes {
		for _, p := range r.Patch {
			if p.File != file {
				continue
			}
			if v, ok := p.Set[key]; ok {
				return engine.RenderForTest(v, vars)
			}
		}
	}
	return ""
}

// TestVersionChoiceDefaultsToRecommended: a build where nobody answered the
// version question must still produce a concrete version, not an unsubstituted
// token or an empty value. This is the path every --yes build and every manifest
// rebuild takes.
func TestVersionChoiceDefaultsToRecommended(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := resolver.Resolve(reg, []string{"laravel", "laravel-docker", "mysql"})
	if err != nil {
		t.Fatal(err)
	}
	got := patchValue(t, plan, ".env", "PHP_VERSION")
	if got != "8.4" {
		t.Fatalf("with no choice made, PHP_VERSION should be the recommendation 8.4, got %q", got)
	}
}

// TestVersionChoiceOverridesRecommendation: picking a supported version that is
// not the recommended one has to actually take effect. A recommendation the user
// cannot override is just a hardcoded value with extra steps.
func TestVersionChoiceOverridesRecommendation(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := resolver.Resolve(reg, []string{
		"laravel", "laravel-docker", "mysql", recipe.VersionToken("php_version", "8.5"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := patchValue(t, plan, ".env", "PHP_VERSION"); got != "8.5" {
		t.Fatalf("choosing 8.5 did not take effect, got %q", got)
	}
}

// TestVersionTokenIsNotMistakenForARecipe: the answers travel with the recipe
// ids, so the resolver must strip them. If it did not, the build would fail with
// "unknown recipe".
func TestVersionTokenIsNotMistakenForARecipe(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := resolver.Resolve(reg, []string{
		"laravel", "laravel-docker", "mysql", recipe.VersionToken("php_version", "8.3"),
	})
	if err != nil {
		t.Fatalf("a version answer among the ids broke resolution: %v", err)
	}
	for _, id := range plan.IDs() {
		if strings.HasPrefix(id, recipe.VersionTokenPrefix) {
			t.Errorf("a version answer survived into the recipe list: %q", id)
		}
	}
	if plan.Versions["php_version"] != "8.3" {
		t.Errorf("the plan did not carry the chosen version: %v", plan.Versions)
	}
}

// TestEveryVersionChoiceRecommendsAnOfferedValue: a recommendation that is not
// in the option list would pre-select nothing, so the wizard would open on
// whatever happened to be first.
func TestEveryVersionChoiceRecommendsAnOfferedValue(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reg.All() {
		for name, vc := range r.Versions {
			if vc.Recommended == "" {
				t.Errorf("%s: version %q has no recommendation", r.ID, name)
				continue
			}
			found := false
			for _, o := range vc.Options {
				if o.Value == vc.Recommended {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: version %q recommends %q, which is not one of its options", r.ID, name, vc.Recommended)
			}
		}
	}
}

// TestEveryPHPFrameworkOffersAVersion: the four PHP frameworks all build a
// PHP-FPM image, and all four support more than one PHP line. A framework that
// silently pins one is the thing this feature exists to remove, so a new PHP
// framework added without a choice fails here.
func TestEveryPHPFrameworkOffersAVersion(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, fw := range reg.OfKind(recipe.Framework) {
		if fw.Lang != "php" {
			continue
		}
		if _, ok := fw.Versions["php_version"]; !ok {
			t.Errorf("%s offers no PHP version choice", fw.ID)
		}
	}
}

// TestPHPVersionChoiceReachesCompose: offering the question is not the same as
// honouring the answer. Each PHP framework must write the chosen version where
// compose reads it, or the wizard step is decoration.
func TestPHPVersionChoiceReachesCompose(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ fw, env, db string }{
		{"laravel", "laravel-docker", "mysql"},
		{"symfony", "symfony-docker", "postgres"},
		{"magento", "magento-docker", "mariadb"},
		{"woocommerce", "woocommerce-docker", "mariadb"},
	} {
		plan, err := resolver.Resolve(reg, []string{tc.fw, tc.env, tc.db, recipe.VersionToken("php_version", "8.4")})
		if err != nil {
			t.Errorf("%s: resolve: %v", tc.fw, err)
			continue
		}
		if got := patchValue(t, plan, ".env", "PHP_VERSION"); got != "8.4" {
			t.Errorf("%s: chosen PHP version did not reach .env, got %q", tc.fw, got)
		}
	}
}

// TestChosenDBVersionFlowsIntoTheDBVar: every database recipe renders its image
// tag, its DDEV database type and its tuning file from one var, db.version. That
// var must follow the user's choice.
//
// This asserts the var, not a rendered image tag, because which file carries the
// database service is currently inconsistent - see the note on
// TestNoFrameworkPinsAnOlderDBThanTheDBRecipe.
func TestChosenDBVersionFlowsIntoTheDBVar(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ db, pin, pick string }{
		{"postgres", "postgres_version", "17"},
		{"mysql", "mysql_version", "9.4"},
		{"mariadb", "mariadb_version", "12.3"},
	} {
		plan := planWithDB(t, reg, tc.db, recipe.VersionToken(tc.pin, tc.pick))
		if plan == nil {
			t.Errorf("%s: no framework/env combination accepts it", tc.db)
			continue
		}
		vars := engine.PlanVarsForTest(plan, "proj")
		if vars["db.version"] != tc.pick {
			t.Errorf("%s: choosing %s left db.version = %q", tc.db, tc.pick, vars["db.version"])
		}
		if vars["pin."+tc.pin] != tc.pick {
			t.Errorf("%s: pin.%s = %q, expected %q", tc.db, tc.pin, vars["pin."+tc.pin], tc.pick)
		}
	}
}

// planWithDB finds any framework and compose env this database applies to, so
// the test does not hardcode a pairing that the catalogue is free to change.
func planWithDB(t *testing.T, reg *recipe.Registry, db string, extra ...string) *resolver.Plan {
	t.Helper()
	for _, fw := range reg.OfKind(recipe.Framework) {
		for _, env := range reg.ForFramework(fw.ID, recipe.Env) {
			if env.EnvFamily != recipe.FamilyCompose {
				continue
			}
			ids := append([]string{fw.ID, env.ID, db}, extra...)
			if plan, err := resolver.Resolve(reg, ids); err == nil {
				return plan
			}
		}
	}
	return nil
}

// TestEveryFrameworkOffersARuntimeVersion: every framework builds an image on
// some language runtime, and every one of those runtimes has more than one
// supported line. A framework that offers no choice has quietly decided for the
// user, which is the thing this feature removes.
func TestEveryFrameworkOffersARuntimeVersion(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"php": "php_version", "python": "python_version", "node": "node_version"}
	for _, fw := range reg.OfKind(recipe.Framework) {
		pin, ok := want[fw.Lang]
		if !ok {
			continue
		}
		if _, has := fw.Versions[pin]; !has {
			t.Errorf("%s (%s) offers no %s choice", fw.ID, fw.Lang, pin)
		}
	}
}

// dbImageRe finds a hardcoded primary-database image tag.
var dbImageRe = regexp.MustCompile(`image: (postgres|mysql|mariadb):([0-9][0-9.]*)`)

// TestNoFrameworkPinsAnOlderDBThanTheDBRecipe: four PHP compose files are
// verbatim (they are full of ${...} that must reach Docker untouched), so their
// database image tag is a literal rather than {{db.version}}.
//
// A literal drifts. Django shipped postgres:16 while the postgres recipe had
// moved to 18, and Magento shipped mariadb:11.4 against a recipe recommending
// 11.8 - so the tuning file and the server it tuned were different versions.
// This fails when they diverge again.
func TestNoFrameworkPinsAnOlderDBThanTheDBRecipe(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	recommended := map[string]string{}
	for _, r := range reg.OfKind(recipe.DB) {
		for name, vc := range r.Versions {
			engineName := strings.TrimSuffix(name, "_version")
			recommended[engineName] = vc.Recommended
		}
	}
	if len(recommended) == 0 {
		t.Skip("no database offers a version choice")
	}

	for _, fw := range reg.OfKind(recipe.Framework) {
		for _, env := range reg.ForFramework(fw.ID, recipe.Env) {
			plan, err := resolver.Resolve(reg, []string{fw.ID, env.ID})
			if err != nil {
				continue
			}
			for path, content := range engine.RenderedFiles(plan, "proj") {
				for _, m := range dbImageRe.FindAllStringSubmatch(content, -1) {
					engineName, tag := m[1], m[2]
					want, ok := recommended[engineName]
					if !ok {
						continue
					}
					if tag != want {
						t.Errorf("%s / %s: %s pins %s:%s but the %s recipe recommends %s",
							fw.ID, env.ID, path, engineName, tag, engineName, want)
					}
				}
			}
		}
	}
}

// TestSelectingADatabaseProducesADatabase is the guard for the worst defect
// found in this catalogue so far.
//
// All nine Node frameworks accepted a database, resolved cleanly, validated,
// linted, passed every test and booted - with no database container anywhere.
// compose.yaml held only `app`; the db recipe's docker-compose.<engine>.yml
// defined a service with no image, and compose only auto-merges
// compose.override.yaml, so that file was never read. Nothing failed. You just
// did not get a database.
//
// Health is not the assertion. A stack whose app and web server are healthy can
// still be missing the database entirely, which is exactly how it went unnoticed.
func TestSelectingADatabaseProducesADatabase(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, fw := range reg.OfKind(recipe.Framework) {
		for _, env := range reg.ForFramework(fw.ID, recipe.Env) {
			if env.EnvFamily != recipe.FamilyCompose {
				continue
			}
			db := defaultDB(reg, fw.ID, env.ID)
			if db == "" {
				continue // nothing selected, nothing to promise
			}
			plan, err := resolver.Resolve(reg, []string{fw.ID, env.ID, db})
			if err != nil {
				continue
			}
			files := engine.RenderedFiles(plan, "proj")
			compose, ok := files["compose.yaml"]
			if !ok {
				continue
			}
			var doc struct {
				Services map[string]struct {
					Image string `yaml:"image"`
				} `yaml:"services"`
			}
			if err := yaml.Unmarshal([]byte(compose), &doc); err != nil {
				t.Errorf("%s / %s: compose.yaml does not parse: %v", fw.ID, env.ID, err)
				continue
			}
			svc, has := doc.Services["db"]
			if !has {
				if _, alt := doc.Services["database"]; alt {
					continue
				}
				t.Errorf("%s / %s selects %s but compose.yaml declares no database service", fw.ID, env.ID, db)
				continue
			}
			// A service with no image is not a database either: that is precisely
			// what the unmerged overlay contained.
			if svc.Image == "" {
				t.Errorf("%s / %s: the db service has no image", fw.ID, env.ID)
			}
		}
	}
}

// TestDBTuningIsActuallyMounted: the database recipes each generate a tuned
// configuration file under .keel/, with sourced numbers behind every value.
//
// For a long time nothing read them. The mount lived in a separate
// docker-compose.<engine>.yml, and compose only auto-merges compose.override.yaml,
// so every stack shipped a carefully tuned config and ran on image defaults. The
// file existing is not the same as the server reading it.
func TestDBTuningIsActuallyMounted(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, fw := range reg.OfKind(recipe.Framework) {
		for _, env := range reg.ForFramework(fw.ID, recipe.Env) {
			if env.EnvFamily != recipe.FamilyCompose {
				continue
			}
			db := defaultDB(reg, fw.ID, env.ID)
			if db == "" {
				continue
			}
			plan, err := resolver.Resolve(reg, []string{fw.ID, env.ID, db})
			if err != nil {
				continue
			}
			files := engine.RenderedFiles(plan, "proj")
			compose, ok := files["compose.yaml"]
			if !ok {
				continue
			}
			// Find the tuning file this plan wrote, then require compose to mount it.
			var conf string
			for path := range files {
				if strings.HasPrefix(path, ".keel/") && (strings.HasSuffix(path, ".conf") || strings.HasSuffix(path, ".cnf")) {
					conf = path
				}
			}
			if conf == "" {
				continue // this database ships no tuning file
			}
			if !strings.Contains(compose, conf) {
				t.Errorf("%s / %s: %s is generated but compose.yaml never mounts it, so the server runs on image defaults",
					fw.ID, env.ID, conf)
			}
		}
	}
}

// TestNonFPMServicesDoNotInheritTheFPMProbe: the PHP image carries a HEALTHCHECK
// that speaks FastCGI to port 9000. A container built from that image but
// running something else - a queue worker, cron - inherits it and can never pass.
//
// The symptom is a container that works perfectly and reports "unhealthy"
// forever, and any service waiting on `condition: service_healthy` for it hangs.
// Laravel's queue and scheduler both did exactly this.
func TestNonFPMServicesDoNotInheritTheFPMProbe(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, fw := range reg.OfKind(recipe.Framework) {
		if fw.Lang != "php" {
			continue
		}
		for _, env := range reg.ForFramework(fw.ID, recipe.Env) {
			if env.EnvFamily != recipe.FamilyCompose {
				continue
			}
			plan, err := resolver.Resolve(reg, []string{fw.ID, env.ID})
			if err != nil {
				continue
			}
			compose, ok := engine.RenderedFiles(plan, "proj")["compose.yaml"]
			if !ok {
				continue
			}
			var doc struct {
				Services map[string]struct {
					Build       any `yaml:"build"`
					Command     any `yaml:"command"`
					Healthcheck *struct {
						Disable bool `yaml:"disable"`
						Test    any  `yaml:"test"`
					} `yaml:"healthcheck"`
				} `yaml:"services"`
			}
			if err := yaml.Unmarshal([]byte(compose), &doc); err != nil {
				continue
			}
			for name, svc := range doc.Services {
				// Only services built from the PHP image that override its command.
				if svc.Build == nil || svc.Command == nil {
					continue
				}
				cmd := fmt.Sprint(svc.Command)
				if !strings.Contains(cmd, "cron") && !strings.Contains(cmd, "queue:work") && !strings.Contains(cmd, "messenger:consume") {
					continue
				}
				if svc.Healthcheck == nil {
					t.Errorf("%s / %s: service %q runs %s but inherits the image's PHP-FPM healthcheck, so it can never be healthy",
						fw.ID, env.ID, name, cmd)
					continue
				}
				if !svc.Healthcheck.Disable && svc.Healthcheck.Test == nil {
					t.Errorf("%s / %s: service %q has an empty healthcheck override", fw.ID, env.ID, name)
				}
			}
		}
	}
}

// tokenRe finds an unsubstituted {{token}}.
var tokenRe = regexp.MustCompile(`\{\{[a-zA-Z0-9_.]+\}\}`)

// TestNoRenderedFileLeaksAToken: TestNoTokenLeaks covers the install STEPS. The
// files a recipe writes were never checked, and a token that survives into a
// file is not a syntax error anyone notices - `image: postgres:{{db.version}}`
// is valid YAML that Docker rejects at run time with "invalid reference format".
//
// That is exactly how it shipped: a framework with no database recipe
// referenced a var only a database recipe defines.
//
// Note for whoever trips this next: a COMMENT that names a token in braces
// looks identical to a leak from here, because the comment is inside the file
// being rendered. Describe the var in words instead of quoting it.
func TestNoRenderedFileLeaksAToken(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, fw := range reg.OfKind(recipe.Framework) {
		for _, env := range reg.ForFramework(fw.ID, recipe.Env) {
			ids := []string{fw.ID, env.ID}
			if db := defaultDB(reg, fw.ID, env.ID); db != "" {
				ids = append(ids, db)
			}
			// The default services too, which is how a real build composes and
			// therefore which files really get written. Without them the web
			// tier was never in any plan here, so an unsubstituted token in a
			// server block or VirtualHost was invisible to this test - and one
			// was: {{wp.docroot}} sat unrendered in WordPress's nginx and Apache
			// configs, which would have produced a server block with a literal
			// brace in its root directive.
			for _, s := range reg.ForFramework(fw.ID, recipe.Service) {
				if s.IsDefaultFor(fw.ID) && resolver.SeedableWith(reg, ids, s) {
					ids = append(ids, s.ID)
				}
			}
			plan, err := resolver.Resolve(reg, ids)
			if err != nil {
				continue
			}
			for path, content := range engine.RenderedFiles(plan, "proj") {
				for _, tok := range tokenRe.FindAllString(content, -1) {
					t.Errorf("%s / %s: %s still contains %s", fw.ID, env.ID, path, tok)
				}
			}
		}
	}
}
