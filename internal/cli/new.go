package cli

import (
	"context"
	"fmt"
	"github.com/coullworks/keel/plugin"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/creds"
	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/profile"
	"github.com/coullworks/keel/internal/project"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
	"github.com/coullworks/keel/internal/scaffold"
	"github.com/coullworks/keel/internal/tui"
	"github.com/spf13/cobra"
)

func newCmd() *cobra.Command {
	var env, db, dir string
	var with []string
	var dryRun, yes, trust, jsFlag, tsFlag bool

	c := &cobra.Command{
		Use: "new [framework] [dir]",
		Example: "  keel new                                        # pick everything interactively\n" +
			"  keel new laravel myshop --with filament,redis\n" +
			"  keel new laravel myshop --env sail --db postgres --yes\n" +
			"  keel new django --dry-run                       # show the steps, run nothing\n",
		Args:  cobra.MaximumNArgs(2),
		Short: "Scaffold a new project - interactive, or flag-driven",
		Long: "Composes a framework with a local dev environment, a database, services\n" +
			"and a UI kit, then runs the real installers. With no arguments it asks;\n" +
			"with flags it builds straight away.\n\n" +
			"An explicit --env or --db is honoured or refused, never quietly swapped\n" +
			"for something else. Use --dry-run to see the exact steps first.",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if len(args) == 0 {
				return runNewInteractive(cmd.Context(), out, dryRun, yes, trust)
			}
			reg, err := catalog.Registry()
			if err != nil {
				return err
			}
			prof, err := profile.Load()
			if err != nil {
				return err
			}
			fw := args[0]
			if _, ok := reg.Get(fw); !ok {
				return fmt.Errorf("unknown framework %q (see docs/ROUTES.md)", fw)
			}
			// --js / --ts pick the JavaScript or TypeScript variant of a Node
			// framework that ships both (nextjs <-> nextjs-js). TypeScript is the
			// base recipe (the default); the -js id is the JavaScript variant.
			fw, err = langVariant(reg, fw, jsFlag, tsFlag)
			if err != nil {
				return err
			}
			if len(args) > 1 && dir == "" {
				dir = args[1]
			}
			// An explicit choice is honoured or refused, never quietly swapped.
			// Asking for --db mysql and getting PostgreSQL (because the id
			// belonged to another framework and keel fell back to the default)
			// is the kind of thing you only discover after the build.
			requested := func(kind recipe.Kind, flag, prefer string) (string, error) {
				if prefer == "" {
					return "", nil
				}
				// An exact id (or alias) that applies to this framework always wins,
				// so `--env django-docker`, `--db postgres` and Laravel's bare-id
				// `ddev`/`sail` keep resolving exactly as before.
				if r, ok := reg.Get(prefer); ok && r.Kind == kind && r.AppliesToFramework(fw) {
					return r.ID, nil
				}
				// `--env ddev` names a family, not one framework's recipe. Every
				// framework namespaces its env (fastapi-ddev, nextjs-docker) except
				// Laravel's legacy bare `ddev`/`sail`, so resolving by id alone made
				// `--env ddev` resolve to Laravel's recipe and get refused everywhere
				// else - even though fastapi-ddev and django-ddev exist. Resolve the
				// family (docker is the everyday name for the compose family) to this
				// framework's own variant.
				if kind == recipe.Env {
					if id, ok := envByFamily(reg, fw, prefer); ok {
						return id, nil
					}
				}
				// Tell "known recipe, wrong framework" apart from "no such thing".
				if r, ok := reg.Get(prefer); ok {
					if r.Kind != kind {
						return "", fmt.Errorf("--%s %s is a %s recipe, not a %s", flag, prefer, r.Kind, kind)
					}
					return "", fmt.Errorf("--%s %s does not apply to %s", flag, prefer, fw)
				}
				if kind == recipe.Env && isEnvFamilyName(prefer) {
					return "", fmt.Errorf("--%s %s does not apply to %s", flag, prefer, fw)
				}
				return "", fmt.Errorf("unknown %s %q for --%s", kind, prefer, flag)
			}
			env, err = requested(recipe.Env, "env", env)
			if err != nil {
				return err
			}
			db, err = requested(recipe.DB, "db", db)
			if err != nil {
				return err
			}
			ids := []string{fw}
			if env == "" {
				env = resolver.CompatibleDefault(reg, ids, recipe.Env, fw, prof.Get(fw, "env"))
			}
			if env != "" {
				ids = append(ids, env)
			}
			// The database depends on the environment: Laravel prefers PostgreSQL
			// but its Docker stack brings up MySQL, and a native environment runs
			// no database at all unless you ask for one by name.
			if db == "" {
				db = resolver.CompatibleDefault(reg, ids, recipe.DB, fw, prof.Get(fw, "database"))
			}
			if db != "" {
				ids = append(ids, db)
			}
			// Fail early on an env/db combo that cannot be built, with the flag to
			// fix it, rather than letting it die deep in resolver.Resolve at build
			// time. Supabase provisions only its local stack, so `keel new django
			// --env django-docker --db supabase` is doomed - name the env(s) that
			// would work. Only the explicit-flag path reaches here; the wizard prunes
			// the same combos out of the Database step (tui.BuildSteps).
			if env != "" && db != "" {
				if dbr, ok := reg.Get(db); ok && !resolver.SeedableWith(reg, []string{fw, env}, dbr) {
					envs := resolver.CompatibleEnvs(reg, fw, dbr)
					hint := "a compatible environment"
					if len(envs) > 0 {
						hint = "--env " + strings.Join(envs, " or --env ")
					}
					return fmt.Errorf("%s cannot be used with the %s environment; try %s", db, env, hint)
				}
			}
			ids = append(ids, with...)
			// Your saved default stack (set in the studio / `keel init`) drives the
			// frontend + services + add-ons + extras when building its framework.
			// Each layer: use the profile's list if set, else the recipe defaults.
			useProfile := prof.Get("", "framework") == fw
			selfServes := reg.FrameworkSelfServes(fw)
			addID := func(id string, kind recipe.Kind) {
				r, ok := reg.Get(id)
				if !ok || r.Kind != kind || !r.AppliesToFramework(fw) || contains(ids, id) {
					return
				}
				// A PHP-style front controller cannot front a self-serving app:
				// it would mount a language handler the Node process does not
				// expose. apache/nginx declare appliesTo:* (any app can be
				// fronted, the config is what differs), so AppliesToFramework
				// alone lets one through here from a stale profile or a --with.
				// Drop it the way the DB prune drops an env-incompatible database,
				// and let the reachability fallback seed PM2 instead of building a
				// stack that fronts nothing.
				if selfServes && r.IsFrontController() {
					return
				}
				// A default yields rather than breaking the build: to an
				// explicit choice it conflicts with, and to an environment it
				// has no provision for. Without the first, `--with apache`
				// would seed NGINX alongside it and fail on a conflict the user
				// never created; without the second, a pack environment keel
				// has never heard of would refuse the default web server.
				if !resolver.SeedableWith(reg, ids, r) {
					return
				}
				ids = append(ids, id)
			}
			seedKind := func(key string, kind recipe.Kind) {
				if list := strings.TrimSpace(prof.Get("", key)); useProfile && list != "" {
					for _, id := range strings.Split(list, ",") {
						addID(strings.TrimSpace(id), kind)
					}
					return
				}
				for _, r := range reg.ForFramework(fw, kind) { // batteries-included fallback
					if r.IsDefaultFor(fw) {
						addID(r.ID, kind)
					}
				}
			}
			// The web server is single-select and carries its own profile key, so
			// it is seeded on its own rather than through the services list.
			//
			// An explicit answer is authoritative and goes in first, before a
			// stale services list can claim the slot with a different one.
			ws := ""
			if useProfile {
				ws = strings.TrimSpace(prof.Get("", "webserver"))
			}
			if ws != "" && ws != profile.NoWebServer {
				addID(ws, recipe.Service)
			}
			seedKind("services", recipe.Service)
			seedKind("addons", recipe.Addon)
			seedKind("extras", recipe.Extra)
			// Unanswered is not the same as declined. A profile written before
			// this question existed has no answer to it, and reading that as "no
			// web server wanted" built a stack with nothing listening on it -
			// every container up, curl refused. Fall back to the framework's
			// default so the build is reachable; profile.NoWebServer is the only
			// way to opt out.
			//
			// The condition is what ended up in the plan, not what the profile
			// said, because those differ: a saved web server that this framework
			// has no recipe for is dropped by addID above, and treating "the key
			// was set" as "a web server was seeded" would leave the same stack
			// with no way in that this fallback exists to prevent.
			//
			// Last, so a profile that still carries its web server in the services
			// list keeps the one it chose.
			if ws != profile.NoWebServer && !hasWebServer(reg, ids) {
				for _, r := range defaultWebServersFor(reg, fw) {
					addID(r.ID, recipe.Service)
				}
			}
			// Frontend (single-select): your saved default wins; on an ad-hoc build
			// fall back to the framework's default:true front end (e.g. Hyvä for
			// Magento). A profile framework with an empty frontend = you opted out.
			if fe := strings.TrimSpace(prof.Get("", "frontend")); useProfile && fe != "" {
				addID(fe, recipe.Frontend)
			} else if !useProfile {
				for _, r := range reg.ForFramework(fw, recipe.Frontend) {
					if r.IsDefaultFor(fw) {
						addID(r.ID, recipe.Frontend)
					}
				}
			}
			// The flag-driven path has no wizard, so no brand result: nil means
			// ApplyBrand is a no-op (brand colours are a wizard-only choice).
			return build(cmd.Context(), out, reg, ids, nil, dir, dryRun, yes, trust)
		},
	}
	c.Flags().StringVar(&env, "env", "", "local dev env recipe (e.g. ddev, sail)")
	c.Flags().StringVar(&db, "db", "", "database recipe (e.g. mysql, postgres)")
	c.Flags().StringSliceVar(&with, "with", nil, "add recipes (e.g. --with filament,redis,pest)")
	c.Flags().BoolVar(&jsFlag, "js", false, "JavaScript variant (Node frameworks; default is TypeScript)")
	c.Flags().BoolVar(&tsFlag, "ts", false, "TypeScript variant (Node frameworks; the default)")
	c.Flags().StringVarP(&dir, "dir", "o", "", "target directory (default: <framework>-app)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "show the steps without running them")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	c.Flags().BoolVar(&trust, "trust", false, "run untrusted pack recipes without prompting")
	return c
}

// selectRecipes is the interactive recipe picker behind a package var: a test
// seam so runNewInteractive's build path can be covered with canned ids (and a
// canned raw result carrying the brand choice) instead of a real terminal.
var selectRecipes = tui.SelectRecipes

// runNewInteractive is the picker path, shared by `keel new` (no args) and the
// home menu.
func runNewInteractive(ctx context.Context, out io.Writer, dryRun, yes, trust bool) error {
	reg, err := catalog.Registry()
	if err != nil {
		return err
	}
	prof, err := profile.Load()
	if err != nil {
		return err
	}
	ids, res, err := selectRecipes(reg, prof)
	if err != nil {
		return err
	}
	// Same safety net the flag path has (new.go, the hasWebServer block): a stack
	// with no ingress has every container up and nothing listening. The wizard's
	// "Web server" step always offers a "None" option, so unlike the flag path
	// there is no ambiguous "never asked" state — only an explicit decline. Unless
	// the user chose None, re-add the framework's default web server when the
	// picked recipes front nothing.
	if !tui.DeclinedWebServer(res) && !hasWebServer(reg, ids) {
		if fw := frameworkFromIDs(reg, ids); fw != "" {
			for _, r := range defaultWebServersFor(reg, fw) {
				if resolver.SeedableWith(reg, ids, r) && !contains(ids, r.ID) {
					ids = append(ids, r.ID)
				}
			}
		}
	}
	dir := ""
	if !yes {
		if err := huh.NewInput().Title("Project directory").Placeholder("myshop").Value(&dir).Run(); err != nil {
			return err
		}
	}
	return build(ctx, out, reg, ids, res, dir, dryRun, yes, trust)
}

// projectDirElement is the allowed shape of one path segment in a project
// directory name: letters, digits, dot, dash and underscore. It excludes
// whitespace and every shell metacharacter, so a segment that passes cannot
// smuggle an injection or a space into the path/working-directory the build uses.
var projectDirElement = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// validateProjectDir refuses a directory name that would be unsafe as a path or a
// working directory: traversal ("..", or a ".." element), whitespace, and shell
// metacharacters. A leading ~ or / (home/absolute) is allowed, and normal path
// separators between valid segments are allowed, so `--dir ~/code/myshop` works
// while `../escape`, `a;b` and `my shop` are refused. An empty name is allowed:
// it becomes "<framework>-app".
func validateProjectDir(dir string) error {
	if dir == "" {
		return nil
	}
	// Strip a leading ~ (home) or / (absolute); the remaining path is checked
	// element by element. filepath.Clean would collapse a ".." we want to catch,
	// so split the raw string on the separator instead.
	rest := strings.TrimPrefix(dir, "~")
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		return fmt.Errorf("invalid project directory %q", dir)
	}
	for _, elem := range strings.Split(rest, "/") {
		if elem == "" {
			continue // a doubled or trailing separator is harmless
		}
		if elem == ".." {
			return fmt.Errorf("invalid project directory %q: must not contain \"..\" (path traversal)", dir)
		}
		if !projectDirElement.MatchString(elem) {
			return fmt.Errorf("invalid project directory %q: use letters, digits, dot, dash and underscore (no spaces or shell characters)", dir)
		}
	}
	return nil
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// langVariant applies the --js/--ts flags. A Node framework that offers both
// languages is one family with two recipe ids: the default (whatever language it
// ships as) plus a sibling suffixed "-js" or "-ts" for the other. keel infers the
// pair from whichever sibling exists, so the flags work whether the default is
// TypeScript (nextjs + nextjs-js) or JavaScript (express + express-ts). Passing a
// flag to a framework with no such choice is an error, so the user is told plainly
// rather than getting the default silently.
func langVariant(reg *recipe.Registry, fw string, js, ts bool) (string, error) {
	if !js && !ts {
		return fw, nil
	}
	if js && ts {
		return "", fmt.Errorf("choose --js or --ts, not both")
	}
	base := strings.TrimSuffix(strings.TrimSuffix(fw, "-js"), "-ts")
	var jsID, tsID string
	if _, ok := reg.Get(base + "-js"); ok { // default (base) is TypeScript
		jsID, tsID = base+"-js", base
	} else if _, ok := reg.Get(base + "-ts"); ok { // default (base) is JavaScript
		jsID, tsID = base, base+"-ts"
	} else {
		return "", fmt.Errorf("%s has no JavaScript/TypeScript choice - only Node frameworks do", base)
	}
	if js {
		return jsID, nil
	}
	return tsID, nil
}

// envByFamily resolves an env-family name to the env recipe of that family that
// applies to fw. It lets `--env ddev` mean "this framework's ddev env" instead
// of one hard-coded recipe id: fastapi-ddev for FastAPI, nextjs-ddev for Next.js,
// the bare `ddev` for Laravel. `docker` is accepted as the everyday name for the
// compose family, whose recipes are all named `<framework>-docker`.
func envByFamily(reg *recipe.Registry, fw, name string) (string, bool) {
	fam := name
	if name == "docker" {
		fam = recipe.FamilyCompose
	}
	if !recipe.EnvFamilies[fam] {
		return "", false
	}
	for _, r := range reg.ForFramework(fw, recipe.Env) {
		if r.EnvFamily == fam {
			return r.ID, true
		}
	}
	return "", false
}

// isEnvFamilyName reports whether name is an env-family name a user might pass to
// --env (including the `docker` synonym for compose). Used only to word the error
// when the family exists but not for this framework.
func isEnvFamilyName(name string) bool {
	return name == "docker" || recipe.EnvFamilies[name]
}

// frameworkFromIDs returns the framework recipe id among ids, or "" if none is
// present. The interactive path, unlike the flag path, learns its framework from
// the picker's answers rather than from a positional arg.
func frameworkFromIDs(reg *recipe.Registry, ids []string) string {
	for _, id := range ids {
		if r, ok := reg.Get(id); ok && r.Kind == recipe.Framework {
			return r.ID
		}
	}
	return ""
}

// hasWebServer reports whether anything already in the plan fronts the app.
// Unknown ids are ignored rather than assumed: an id that resolves to nothing
// serves no traffic.
func hasWebServer(reg *recipe.Registry, ids []string) bool {
	for _, id := range ids {
		if r, ok := reg.Get(id); ok && r.IsWebServer() {
			return true
		}
	}
	return false
}

// defaultWebServersFor returns the framework's default ingress recipes to seed
// when the plan fronts nothing, pruned by whether the framework serves its own
// traffic — the same split as the wizard's Web server step (tui.WebServersFor).
//
// Without the prune a self-serving Node framework would seed BOTH its process
// manager (defaultFor node) AND the shared front-controller nginx (plain
// default:true, appliesTo *), fronting an app the front controller has nothing
// to mount. A front controller is only a default here for a framework that
// actually needs one.
func defaultWebServersFor(reg *recipe.Registry, fw string) []recipe.Recipe {
	selfServes := reg.FrameworkSelfServes(fw)
	var out []recipe.Recipe
	for _, r := range reg.ForFramework(fw, recipe.Service) {
		if !r.IsWebServer() || !r.IsDefaultFor(fw) {
			continue
		}
		if selfServes && r.IsFrontController() {
			continue // a self-serving app does not want a PHP front controller
		}
		out = append(out, r)
	}
	return out
}

// build resolves, previews, confirms, and executes.
// planEnv is the env recipe's id, for describing the project to a plugin.
func planEnv(plan *resolver.Plan) string {
	for _, r := range plan.Recipes {
		if r.Kind == recipe.Env {
			return r.ID
		}
	}
	return ""
}

// res is the raw wizard result (nil on the flag-driven path). It carries the
// brand-colour choice, applied to the built project after a successful build.
func build(ctx context.Context, out io.Writer, reg *recipe.Registry, ids []string, res [][]string, dir string, dryRun, yes, trust bool) error {
	plan, err := resolver.Resolve(reg, ids)
	if err != nil {
		return err
	}
	// The directory name becomes a real path and a working directory, so it is
	// validated before anything is planned: a name with ".." traversal, shell
	// metacharacters, or whitespace is refused rather than turned into a path.
	// A default (empty) name is fine — it becomes "<framework>-app" below.
	if err := validateProjectDir(dir); err != nil {
		return err
	}
	fmt.Fprint(out, tui.Splash())
	fmt.Fprint(out, tui.RenderPlan(plan, engine.Steps(plan)))
	if dir == "" {
		dir = plan.Framework + "-app"
	}
	// A bare project name is created under the profile's projects folder (if set).
	if !filepath.IsAbs(dir) && !strings.ContainsRune(dir, filepath.Separator) {
		if p, err := profile.Load(); err == nil {
			if base := project.Expand(p.Get("", "projects_dir")); base != "" {
				dir = filepath.Join(base, dir)
			}
		}
	}
	// Consent gate: an untrusted plan (recipes from a pack/url) must show its
	// commands and get explicit yes before running any shell. --trust opts in.
	consented := trust
	if !dryRun && !engine.PlanTrusted(plan) && !trust {
		fmt.Fprintln(out, "\n⚠ This build includes recipes from an untrusted pack. It will run the commands")
		fmt.Fprintln(out, "  above, plus these hooks:")
		for _, s := range engine.HookSteps(plan) {
			fmt.Fprintln(out, "    → "+s)
		}
		ok := false
		if err := huh.NewConfirm().Title("Run these untrusted recipes?").Value(&ok).Run(); err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(out, "cancelled")
			return nil
		}
		consented = true
	}
	if !dryRun && !yes {
		ok := false
		if err := huh.NewConfirm().Title(fmt.Sprintf("Build this in ./%s?", dir)).Value(&ok).Run(); err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(out, "cancelled")
			return nil
		}
	}
	var credentials []creds.Value
	if !dryRun {
		// Ask for credentials first. A build that stops here has changed nothing;
		// one that stops inside composer has a half-installed project and an
		// error message from someone else's tool.
		var cerr error
		credentials, cerr = collectCredentials(out, plan, dir, yes)
		if cerr != nil {
			return cerr
		}
		// Pre-flight: block early on missing host tools we can't auto-install,
		// so the real installers never hit a cryptic failure.
		if err := preflight(plan, out); err != nil {
			return err
		}
		// Auto-install the env's tools (docker/ddev) per platform, then run the
		// Magento guided-auth flow (opens the Adobe keys page, writes auth.json).
		if err := ensureTools(context.Background(), out, plan); err != nil {
			return err
		}
		// Credentials the plan declared, collected once and written where this
		// environment reads them. Any Composer-based stack gets this, not just
		// Magento, and the path follows the env rather than always being DDEV's.
		if err := applyCredentials(out, plan, dir, credentials); err != nil {
			return err
		}
	}
	if err := engine.Build(ctx, plan, engine.Options{
		Dir:      dir,
		DryRun:   dryRun,
		DockerUp: DockerRunning,
		Out:      out,
		Trusted:  consented,
	}); err != nil {
		return err
	}
	if !dryRun {
		// The build succeeded, so the project's CSS framework exists on disk:
		// paint the wizard's chosen brand colours into it now. A no-op unless a
		// palette was actually picked (defaults kept / no UI kit / flag path).
		if primary, _ := tui.BrandFromResult(res); primary != "" {
			if err := tui.ApplyBrand(dir, res); err != nil {
				return err
			}
			fmt.Fprintf(out, "Applied brand colour %s\n", primary)
		}
		proj := plugin.Project{
			Dir:       dir,
			Name:      filepath.Base(dir),
			Framework: plan.Framework,
			Env:       planEnv(plan),
		}
		// Plugin steps run after the project exists, so a step can write into
		// it, and before the summary, so what they did is part of the build
		// rather than an afterthought. This is the interactive half that stays
		// CLI-side; the shared tail (events, open hooks, tracking) is scaffold.Finish.
		if err := runPluginSteps(ctx, out, proj, yes); err != nil {
			return err
		}
		// The shared post-build sequence — one definition for `keel new` and the
		// studio: project.created + project.built, the plan's open hooks, and
		// tracking the project so every surface can find it.
		url := scaffold.Finish(ctx, scaffold.Options{
			Plan: plan, Dir: dir, Proj: proj,
			Emitter: registry(), PluginIO: newIO(out, out), Out: out, Track: true,
		})
		fmt.Fprint(out, tui.RenderDone(dir, url))
	}
	return nil
}
