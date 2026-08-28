// Guards for the contract a build has to keep, all of them written after a real
// build broke it: the address keel prints is the one the app answers on, an
// environment is told what it needs before it starts, a create step can run
// against the directory keel has already written to, and anything started can
// be stopped again.
//
// None of these are checkable by reading a recipe. Each one passed review, and
// each was found by running the thing.
package catalog

import (
	"regexp"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

// TestTheBaseURLFollowsThePublishedPort.
//
// Every compose environment publishes ${HTTP_PORT:-8080}, so HTTP_PORT decides
// which port the stack can be reached on. The base URL written into the project
// has to name the same port, and for a long time it did not: site_url was the
// literal http://localhost:8080 in four environments.
//
// With HTTP_PORT unset the two agreed by coincidence and nothing showed. Set it,
// and Magento's setup:install saved a base URL pointing at 8080 while nginx
// listened somewhere else - and Magento redirects every request to its base URL,
// so the port that worked handed the browser one that refused the connection.
// WordPress does the same through WP_HOME. The e2e suite found this the moment
// it started choosing a free port instead of assuming 8080.
//
// Rendering twice is what makes this a real check rather than a spelling test: a
// literal port survives both renders, a followed one does not.
func TestTheBaseURLFollowsThePublishedPort(t *testing.T) {
	const port = "39123" // nothing in the catalogue mentions this
	t.Setenv("HTTP_PORT", port)

	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	// Both spellings of loopback, because a recipe is free to write either and
	// only one of them was ever in the catalogue.
	stale := []string{"localhost:8080", "127.0.0.1:8080"}
	firstStale := func(s string) (string, int) {
		for _, want := range stale {
			if i := strings.Index(s, want); i >= 0 {
				return want, i
			}
		}
		return "", -1
	}

	checked := 0
	for _, fw := range reg.OfKind(recipe.Framework) {
		for _, env := range reg.ForFramework(fw.ID, recipe.Env) {
			if env.EnvFamily != "compose" {
				continue
			}
			ids := []string{fw.ID, env.ID}
			if db := defaultDB(reg, fw.ID, env.ID); db != "" {
				ids = append(ids, db)
			}
			plan, err := resolver.Resolve(reg, ids)
			if err != nil {
				continue
			}
			checked++
			for path, body := range engine.RenderedFiles(plan, "proj") {
				if found, i := firstStale(body); i >= 0 {
					t.Errorf("%s/%s: %s names %s while the stack publishes %s, so the app is "+
						"installed pointing at a port nothing listens on:\n  %s",
						fw.ID, env.ID, path, found, port, lineAround(body, i))
				}
			}
			// And the steps, which is where Magento's --base-url and WordPress's
			// --url are: those are saved into the database at install time,
			// where a wrong value outlives the build.
			for _, step := range engine.Steps(plan) {
				if found, i := firstStale(step); i >= 0 {
					t.Errorf("%s/%s: an install step names %s while the stack publishes %s, "+
						"and this one is written to the database:\n  %s",
						fw.ID, env.ID, found, port, step)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no compose stack resolved, so this checked nothing")
	}
}

// TestEveryContainerisedStackKnowsWhereItIs.
//
// A build ends by telling you where to open the project, and it can only do that
// if the environment says. Every compose and DDEV environment publishes
// something, so every one of them must define site_url - it was defined in four
// PHP environments only, because only Magento and WooCommerce consumed it, so
// every other containerised build finished without an address.
//
// Native environments are excluded on purpose: they start nothing, and the port
// belongs to whatever `keel run dev` launches.
func TestEveryContainerisedStackKnowsWhereItIs(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, fw := range reg.OfKind(recipe.Framework) {
		for _, env := range reg.ForFramework(fw.ID, recipe.Env) {
			if env.EnvFamily != "compose" && env.EnvFamily != "ddev" {
				continue
			}
			ids := []string{fw.ID, env.ID}
			if db := defaultDB(reg, fw.ID, env.ID); db != "" {
				ids = append(ids, db)
			}
			plan, err := resolver.Resolve(reg, ids)
			if err != nil {
				continue
			}
			checked++
			url := engine.SiteURL(plan, "proj")
			if url == "" {
				t.Errorf("%s/%s publishes a port but defines no site_url, so the build "+
					"finishes without saying where the project is", fw.ID, env.ID)
				continue
			}
			if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
				t.Errorf("%s/%s: site_url %q is not a URL anyone can open", fw.ID, env.ID, url)
			}
			if strings.Contains(url, "{{") {
				t.Errorf("%s/%s: site_url %q still holds an unrendered token", fw.ID, env.ID, url)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no containerised stack resolved, so this checked nothing")
	}
}

// TestDDEVIsToldItsDatabaseBeforeItStarts.
//
// DDEV creates the database volume the first time a project starts, and it
// refuses to start again against a volume of a different type:
//
//	unable to start project because the configured database type does not
//	match the current actual database. Please change your database type back
//	to mariadb:10.6 ... export, delete, and then change configuration
//
// keel used to declare the type in .ddev/config.db.yaml, written by the database
// recipe - which runs AFTER the environment, because the environment is
// priority -10 and the database -5. So `ddev start` had already created the
// volume with DDEV's own default by the time keel said which database it wanted,
// and the next `ddev restart` refused. Every DDEV build whose database differed
// from DDEV's default died there.
//
// The fix is to say it at `ddev config` time, before anything starts. This
// asserts the two halves that make that work: the flag reaches the config step,
// and it is never left as an unrendered token.
func TestDDEVIsToldItsDatabaseBeforeItStarts(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, fw := range reg.OfKind(recipe.Framework) {
		for _, env := range reg.ForFramework(fw.ID, recipe.Env) {
			if env.EnvFamily != "ddev" {
				continue
			}
			// Every database, not just the default. The flag differs per
			// database and one of them has none at all - Mongo runs as a DDEV
			// add-on rather than as its database - so checking only the default
			// would leave the case most likely to be wrong unchecked.
			for _, db := range reg.ForFramework(fw.ID, recipe.DB) {
				checkDDEVOrder(t, reg, fw.ID, env.ID, db.ID, &checked)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no DDEV stack resolved, so this checked nothing")
	}
}

// checkDDEVOrder asserts one framework/env/database combination configures DDEV
// before it starts it, and that nothing reaches the shell as a literal token.
func checkDDEVOrder(t *testing.T, reg *recipe.Registry, fw, env, db string, checked *int) {
	t.Helper()
	plan, err := resolver.Resolve(reg, []string{fw, env, db})
	if err != nil {
		return // not a combination keel offers
	}
	*checked++
	where := fw + "/" + env + " + " + db

	configAt, startAt := -1, -1
	for i, step := range engine.Steps(plan) {
		if strings.Contains(step, "{{") {
			t.Errorf("%s: step %d still holds an unrendered token, which reaches the "+
				"shell literally:\n  %s", where, i, step)
		}
		if strings.Contains(step, "ddev config ") && configAt < 0 {
			configAt = i
		}
		if strings.HasPrefix(strings.TrimSpace(step), "ddev start") && startAt < 0 {
			startAt = i
		}
	}
	if configAt < 0 || startAt < 0 {
		t.Errorf("%s: a DDEV environment with no config step or no start step", where)
		return
	}
	if configAt > startAt {
		t.Errorf("%s: `ddev config` runs after `ddev start`, so the project is created "+
			"before it is told what database it wants", where)
	}
}

// TestCreateStepsDoNotBuildInPlace.
//
// `composer create-project <pkg> .` refuses a target that is not empty, and by
// the time a framework's create step runs the project directory is not empty:
// keel writes the environment's and the database's files first, because those
// recipes have a lower priority. The database recipe alone drops
// .keel/<engine>/<engine>.conf in there.
//
// So Laravel on Sail died with "Project directory ... is not empty", which names
// composer's rule rather than the file that tripped it, and the same trap was
// waiting in the two native creates beside it. Every other create in the
// catalogue already builds in a temp directory and moves the result in; this
// keeps it that way.
//
// DDEV is exempt: `ddev composer create-project` runs inside the web container
// against a directory DDEV manages, and handles the existing .ddev/ itself.
func TestCreateStepsDoNotBuildInPlace(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	// `create-project <package> .` or `create-project . ` - the target being the
	// project root itself.
	inPlace := regexp.MustCompile(`create-project\s+(?:--\S+\s+)*\S+\s+\.(\s|$)`)

	checked := 0
	for _, r := range reg.All() {
		for env, cmd := range r.Create {
			if strings.Contains(cmd, "ddev composer create-project") {
				continue
			}
			checked++
			if inPlace.MatchString(cmd) {
				t.Errorf("%s: the %q create step builds into the project root, which keel has "+
					"already written to, so composer refuses it as non-empty. Build in a temp "+
					"directory and move the result in:\n  %s", r.ID, env, cmd)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no create step was read, so this checked nothing")
	}
}

// TestEveryEnvironmentCanStopWhatItStarts.
//
// keel removes a project directory on rollback and on `keel delete`, so anything
// still running is then bind-mounted to a path that does not exist. It also
// holds ports: the native database publishes a fixed 127.0.0.1:55432, so one
// container left behind stops the next project from starting at all.
//
// Every environment used to declare a `down`, except the native ones, which
// declared `down: ""` on the reasoning that they start nothing. That was wrong
// by one container: the database recipe's native provision runs
// `docker compose -f docker-compose.db.yml up -d`, and nothing ever stopped it.
// `keel recipes verify` leaked one per framework until this was found.
//
// So: if a plan starts anything, it must say how to stop it.
func TestEveryEnvironmentCanStopWhatItStarts(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, fw := range reg.OfKind(recipe.Framework) {
		for _, env := range reg.ForFramework(fw.ID, recipe.Env) {
			// Seeded the way `keel recipes verify` seeds - every default that
			// this stack can take - because that is the path that leaked. A
			// native environment takes a database this way even though the
			// wizard would not offer one, and that database is a container.
			ids := []string{fw.ID, env.ID}
			for _, kind := range []recipe.Kind{recipe.DB, recipe.Service} {
				for _, d := range reg.ForFramework(fw.ID, kind) {
					if d.IsDefaultFor(fw.ID) && resolver.SeedableWith(reg, ids, d) {
						ids = append(ids, d.ID)
					}
				}
			}
			plan, err := resolver.Resolve(reg, ids)
			if err != nil {
				continue
			}
			// Does anything in this plan start a container?
			starts := false
			for _, step := range engine.Steps(plan) {
				if strings.Contains(step, "docker compose") && strings.Contains(step, " up") ||
					strings.Contains(step, "ddev start") || strings.Contains(step, "sail up") {
					starts = true
				}
			}
			if !starts {
				continue
			}
			checked++
			if engine.DownCommand(plan, "proj") == "" {
				t.Errorf("%s/%s starts containers but its environment declares no `down`, so "+
					"keel deletes the project directory and leaves them running against it",
					fw.ID, env.ID)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no plan that starts anything was found, so this checked nothing")
	}
}

// TestComposeMountsPointAtSomethingKeelWrites.
//
// Docker creates a missing bind-mount source, and it creates it as root. So a
// compose file that mounts ./.keel/mysql/my.cnf when nothing in the plan writes
// that file does not fail: it silently produces a root-owned DIRECTORY of that
// name inside the user's project. The next thing keel writes under .keel/ then
// dies with "permission denied", naming a path that has nothing to do with the
// mount that caused it.
//
// That is exactly how Laravel's and Magento's compose stacks broke. Both
// mounted a database tuning file "the db recipe writes" - but both environments
// declare `provides: db-<engine>`, so the database recipe is never in the plan
// and no one writes it. Symfony and WooCommerce mount the same kind of file and
// are fine, because in their plans that recipe really is present. The difference
// is invisible in the recipe and obvious here.
func TestComposeMountsPointAtSomethingKeelWrites(t *testing.T) {
	eachComposeDoc(t, func(where string, doc composeDoc, files map[string]string) {
		for svc, s := range doc.Services {
			for _, v := range s.Volumes {
				src, _, ok := strings.Cut(v, ":")
				// Relative paths only: a named volume has no slash, and an
				// absolute path is not keel's to write.
				if !ok || !strings.HasPrefix(src, "./") {
					continue
				}
				rel := strings.TrimPrefix(src, "./")
				written := false
				for p := range files {
					// The file itself, or a directory keel populates.
					if p == rel || strings.HasPrefix(p, rel+"/") {
						written = true
						break
					}
				}
				if !written {
					t.Errorf("%s: service %q mounts %s, which no recipe in this plan writes. "+
						"Docker will create it as a root-owned directory inside the project, and "+
						"the next write under that path fails with permission denied.",
						where, svc, src)
				}
			}
		}
	})
}
