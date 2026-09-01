// Package engine executes a resolved plan: for each recipe (in execution order)
// it drops the recipe's files then runs its install steps, and writes a manifest
// recording what was built. Docker-dependent steps are gated on the daemon being
// up. The Runner seam lets tests record commands instead of running them.
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/coullworks/keel/internal/atomicfile"
	"github.com/coullworks/keel/internal/envfile"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
	"gopkg.in/yaml.v3"
)

// Runner executes one shell command in a directory.
type Runner interface {
	Run(ctx context.Context, dir, command string) error
}

// ExecRunner runs a rendered command for real via `sh -c`, streaming output.
// Env adds to the process environment rather than replacing it, which is how
// `keel run dev` hands the dev server the port it allocated.
type ExecRunner struct {
	Out io.Writer
	Env []string
}

// Run executes command in dir, streaming stdout/stderr to Out (or os.Stdout).
// It goes through a shell, so the command string must never be assembled from
// user or agent input — use RunArgv for that.
func (r ExecRunner) Run(ctx context.Context, dir, command string) error {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	out := r.Out
	if out == nil {
		out = os.Stdout
	}
	cmd.Stdout, cmd.Stderr = out, out
	if len(r.Env) > 0 {
		cmd.Env = append(os.Environ(), r.Env...)
	}
	return cmd.Run()
}

// RunArgv executes argv directly in dir, with no shell in between, streaming
// stdout/stderr to Out (or os.Stdout). Use this whenever any element of the
// command comes from outside keel: a name in argv is one argument no matter what
// characters it contains.
func (r ExecRunner) RunArgv(ctx context.Context, dir string, argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("no command to run")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	out := r.Out
	if out == nil {
		out = os.Stdout
	}
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}

// Options control a build.
type Options struct {
	Dir      string      // target project directory
	DryRun   bool        // print what would happen, change nothing
	Runner   Runner      // nil -> ExecRunner
	Out      io.Writer   // nil -> os.Stdout
	DockerUp func() bool // gate for docker/ddev steps; nil -> skip the gate
	Trusted  bool        // untrusted plans (pack/url recipes) refuse to run unless set (see docs/EXTENDING.md)
}

// Manifest is the state keel writes into a built project so it can later manage
// it (config edits, generators, the dashboard). See docs/VISION.md.
//
// Two shapes share this struct. A normal (single-app) manifest leaves Kind empty
// and carries Framework/Env/Recipes. A monorepo-root manifest sets Kind to
// KindMonorepo, lists its Members, and carries a Services block describing the
// one backend all members share — the members inherit that DB/env instead of
// each re-deriving one. Keeping both in one struct keeps ReadManifest a single
// front door; the Kind field is the discriminator.
type Manifest struct {
	// Kind distinguishes a monorepo root ("monorepo") from a single app (""),
	// so a reader can branch without guessing from the presence of other fields.
	Kind      string   `yaml:"kind,omitempty"`
	Framework string   `yaml:"framework"`
	Env       string   `yaml:"env"`
	Recipes   []string `yaml:"recipes"`
	// Members are the monorepo root's workspace packages, as paths relative to
	// the root. Only set when Kind == KindMonorepo. Each is a keel-known member
	// (an app or a lib) that inherits this root's shared services.
	Members []Member `yaml:"members,omitempty"`
	// Services is the backend the members share — the DB/env one hosted stack
	// provides for the whole repo (the MyFamilyInfo case: one Supabase project
	// for seven apps). Only meaningful on a monorepo root. Members read their
	// effective DB/env from here rather than each deriving a local one.
	Services *Services `yaml:"services,omitempty"`
	// Files records the sha256 of each keel-owned file at build time, so
	// `keel update` can tell an untouched file (safe to refresh) from one the
	// user edited (a conflict to preserve). Copier/Cookiecutter-style provenance.
	Files map[string]string `yaml:"files,omitempty"`
	// Brand is the project's brand override: the seed hex(es) this project's
	// theme is generated from. When set it wins over the global default
	// (~/.config/keel/brand.yaml) in the resolution order project → global →
	// kit's own colours. Nil means "no override" — inherit the global default.
	// It is a seed, not the full token set, so the manifest stays small and a
	// re-render always picks up the current generation logic; the brand package
	// owns turning the seed into tokens.
	Brand *BrandRef `yaml:"brand,omitempty"`
}

// BrandRef is the per-project brand override stored in the manifest: the seed
// colour(s) the project's theme is derived from. It deliberately mirrors only
// the seed (not the whole scale), so the engine stays free of the brand
// package's token model and the manifest records intent, not generated output.
type BrandRef struct {
	Primary string `yaml:"primary"`
	Accent  string `yaml:"accent,omitempty"`
}

// LaunchCommandHint renders the whole-workspace root launch command for display
// (the summary `keel adopt` prints), e.g. "pnpm dev" / "npm run dev" / "turbo
// dev". It is the member-agnostic form — the run resolver
// (project.RootRunCommand) scopes it to a member when one has its own script.
func LaunchCommandHint(l *Launch) string {
	if l == nil || l.Manager == "" {
		return ""
	}
	script := l.DevScript
	if script == "" {
		script = "dev"
	}
	switch l.Manager {
	case "pnpm", "yarn", "turbo":
		return l.Manager + " " + script
	case "npm":
		if script == "start" {
			return "npm start"
		}
		return "npm run " + script
	}
	return l.Manager + " " + script
}

// KindMonorepo is the Manifest.Kind value for a monorepo-root manifest.
const KindMonorepo = "monorepo"

// Member is one workspace of a monorepo root, recorded so keel can list and
// scope its packages without re-globbing the workspace on every read.
type Member struct {
	// Path is the member's location relative to the monorepo root (e.g.
	// "apps/web"), so a moved repo keeps working.
	Path string `yaml:"path"`
	// Framework is the member's detected stack, or "lib" for a shared,
	// non-runnable package (see project.FrameworkLib) that has no framework of
	// its own but is still a real member.
	Framework string `yaml:"framework"`
}

// Services describes the shared backend a monorepo's members inherit. Today
// that is a single database; the struct is a block (not a flat DB field) so a
// shared cache/queue/storage service can join it later without a schema break.
type Services struct {
	// DB is the one database all members talk to. Nil when the root has no
	// detectable shared database.
	DB *DBService `yaml:"db,omitempty"`
	// EnvFile is the env file (relative to the root) the shared credentials
	// live in — ".env.local" for a Node/Next monorepo, ".env" otherwise — so a
	// member with no env of its own knows which root file to read.
	EnvFile string `yaml:"env_file,omitempty"`
	// Launch records that this workspace launches from the ROOT: a pnpm/yarn/
	// npm-workspace or Turborepo whose root package.json has a dev/start script
	// runs all its members from one command (`pnpm dev` / `turbo dev`), so the
	// members must NOT each get their own Docker/env/webserver — they run
	// together via the root. Nil when the root has no launch script (a
	// non-runnable workspace, e.g. a pure library monorepo), so an ordinary
	// monorepo is unaffected.
	Launch *Launch `yaml:"launch,omitempty"`
}

// Launch records a workspace's root launch: the package manager and the root
// script that brings every member up at once. It exists because a pnpm/turbo
// workspace does not give each app its own runtime — one root command
// (`pnpm dev`) starts them together — so a member's Run action resolves to that
// command instead of a per-member env/Docker spin-up.
type Launch struct {
	// Manager is the package manager that owns the root command ("pnpm" |
	// "yarn" | "npm" | "turbo").
	Manager string `yaml:"manager"`
	// DevScript is the root package.json script the launch runs ("dev" | "start"),
	// so `keel run dev` maps to `<manager> run <devScript>` (or `<manager>
	// <devScript>` for the manager's built-in verb).
	DevScript string `yaml:"dev_script,omitempty"`
}

// DBService is the shared database's shape: enough for a consumer (the studio's
// DB panel, `keel db`) to know how to reach it without re-deriving from scratch.
type DBService struct {
	// Engine is the database family ("postgres" | "mysql").
	Engine string `yaml:"engine"`
	// Provider names the hosting, when known ("supabase", "planetscale", ...),
	// empty for a self-hosted/local database.
	Provider string `yaml:"provider,omitempty"`
	// Source is where the connection is read from: an env reference of the form
	// "<envFile>:<KEY>" (e.g. ".env.local:SUPABASE_DB_URL") that a member
	// resolves against the shared env file, so no secret is copied into the
	// manifest.
	Source string `yaml:"source,omitempty"`
	// Schema is the Postgres schema the apps share when they namespace their
	// tables under one (the MyFamilyInfo "myfamilyinfo" schema). Empty for the
	// default schema.
	Schema string `yaml:"schema,omitempty"`
}

// ReadManifest loads a project's .keel/manifest.yaml (used by `keel gen` and the
// future dashboard to know a project's framework + env).
func ReadManifest(dir string) (*Manifest, error) {
	path := filepath.Join(dir, ".keel", "manifest.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		// A missing manifest ("not a keel project") and an unreadable one
		// (permissions) are different problems; both propagate the os error so a
		// caller can tell them apart with os.IsNotExist. See ManifestIsMalformed
		// for the corrupt-but-present case handled below.
		return nil, err
	}
	var m Manifest
	if err := yaml.Unmarshal(b, &m); err != nil {
		// The file exists but is not valid YAML — usually a hand edit. Say so, and
		// where, instead of leaving the caller to report it as "not a keel project".
		return nil, fmt.Errorf("%w: %s is not valid YAML: %w", ErrManifestMalformed, path, err)
	}
	return &m, nil
}

// ErrManifestMalformed marks a manifest that is present but not valid YAML, so a
// CLI caller can distinguish "no project here" (os.IsNotExist) from "this project's
// manifest is corrupt" and give the user the actionable message for each.
var ErrManifestMalformed = errors.New("manifest is malformed")

// ordered returns the plan's recipes in execution order (Priority then kind).
func ordered(plan *resolver.Plan) []recipe.Recipe {
	rs := make([]recipe.Recipe, len(plan.Recipes))
	copy(rs, plan.Recipes)
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].Priority != rs[j].Priority {
			return rs[i].Priority < rs[j].Priority
		}
		return rs[i].Kind.Rank() < rs[j].Kind.Rank()
	})
	return rs
}

func envID(plan *resolver.Plan) string {
	for _, r := range plan.Recipes {
		if r.Kind == recipe.Env {
			return r.ID
		}
	}
	return ""
}

// envBin is what {{env}} resolves to — the env recipe's CLI binary (ddev, sail).
// It defaults to the recipe ID, so laravel's `ddev` env keeps working unchanged;
// django's `django-ddev` env sets bin: ddev.
func envBin(plan *resolver.Plan) string {
	for _, r := range plan.Recipes {
		if r.Kind == recipe.Env {
			if r.Bin != "" {
				return r.Bin
			}
			return r.ID
		}
	}
	return ""
}

// projectName returns a DDEV-safe project name from a target directory (lower,
// non-alphanumerics collapsed to '-'). Used for {{project}} (e.g. the DDEV
// subdomain a frontend service is exposed on).
func projectName(dir string) string {
	base := strings.ToLower(filepath.Base(dir))
	var b strings.Builder
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "app"
	}
	return name
}

// render substitutes the recipe template vars in s ({{env}}, {{project}}, and the
// env's command vocabulary {{start}}/{{exec}}/{{composer}}/… plus {{create}}).
func render(s string, vars map[string]string) string {
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{{"+k+"}}", v)
	}
	return s
}

func recipeOfKind(plan *resolver.Plan, k recipe.Kind) *recipe.Recipe {
	for i := range plan.Recipes {
		if plan.Recipes[i].Kind == k {
			return &plan.Recipes[i]
		}
	}
	return nil
}

// baseVocabKeys are the command-vocabulary tokens keel itself defines. Every
// other token is discovered from the loaded catalogue and carried on the plan
// (resolver.Plan.Vocab), so a pack that adds an env command makes that token
// available to its recipes without an edit here. The built-in list used to be
// the whole vocabulary, which meant a pack's token stayed literal in the command.
//
// A token any env defines defaults to empty under an env that doesn't define it,
// so the step renders away and is skipped. A token nobody defines stays literal,
// which surfaces a typo instead of silently dropping part of a command.
var baseVocabKeys = []string{"start", "restart", "exec", "create", "down"}

// vocabKeysFor is every token recipes in this plan may reference.
func vocabKeysFor(plan *resolver.Plan) []string {
	keys := append([]string{}, baseVocabKeys...)
	seen := map[string]bool{}
	for _, k := range keys {
		seen[k] = true
	}
	add := func(k string) {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for _, k := range plan.Vocab {
		add(k)
	}
	// A plan built by hand (tests, or a caller that skipped Resolve) has no
	// Vocab, so fall back to what its own recipes define.
	for _, r := range plan.Recipes {
		for k := range r.Commands {
			add(k)
		}
	}
	sort.Strings(keys)
	return keys
}

// httpPort is the port a compose stack will publish, read from the same
// variable the compose files read.
//
// 8080 is the default on both sides, so an unset HTTP_PORT changes nothing.
// A value that is not a plain port number is ignored rather than rendered into
// a URL: this ends up in a base URL saved to a database, and half a hostname
// there is worse than the default.
func httpPort() string {
	const fallback = "8080"
	p := strings.TrimSpace(os.Getenv("HTTP_PORT"))
	if p == "" {
		return fallback
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > 65535 {
		return fallback
	}
	return p
}

// posixID renders an os.Getuid()/os.Getgid() result for the {{uid}}/{{gid}} build
// args. These are a POSIX concept: on Windows both syscalls return -1, which
// would produce `--build-arg UID=-1` and either break the image build or create a
// nonsense user. There the tree-ownership problem these solve does not arise
// (bind mounts are not uid-owned the same way), so a -1 is clamped to the
// conventional first-user id 1000, matching Sail's WWWUSER default.
func posixID(id int) string {
	if id < 0 {
		return "1000"
	}
	return strconv.Itoa(id)
}

// idFromEnvOr returns env var name when it is set, otherwise the POSIX id. It lets
// a reproducible or CI build pin {{uid}}/{{gid}} deterministically (KEEL_UID /
// KEEL_GID) instead of always taking the invoking user's id, which varies per
// machine — the difference that made a build's effective-plan snapshot depend on
// who ran it. The default (env unset) is unchanged: the invoking user's id.
func idFromEnvOr(name string, id int) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return posixID(id)
}

// planVars builds the template vars for a plan: {{env}}/{{project}}, the chosen
// env's command vocabulary ({{start}}, {{exec}}, {{composer}}, {{artisan}}…), and
// {{create}} = the framework's create command for that env.
func planVars(plan *resolver.Plan, project string) map[string]string {
	// base vars ({{project}}, {{env}}) are substituted into the env's command
	// vocabulary first, so a value like site_url = "https://{{project}}.ddev.site"
	// resolves before other recipes reference {{site_url}}.
	// {{uid}} / {{gid}} are the invoking user's, and they exist because a
	// container that bind-mounts your working tree has to agree with you about
	// who owns it. The PHP images run FPM as www-data (uid 33) while the tree on
	// the host is yours (uid 1000), so the two take turns creating files the
	// other cannot write: the app fails to write its own log, or composer fails
	// to write vendor/. Passing these as build args lets the image adopt your
	// uid for its application user, which is what Sail's WWWUSER does.
	// {{http_port}} is the port the stack will actually be published on, and it
	// exists because the answer has to be the same in two places that were free
	// to disagree. Every compose environment publishes ${HTTP_PORT:-8080}, while
	// site_url was written as a literal http://localhost:8080 - so building with
	// HTTP_PORT set installed a store whose base URL named a port nothing was
	// listening on. Magento and WordPress both redirect to their base URL, so
	// the first request left the port that worked for one that refused the
	// connection, and nothing in the output said why.
	base := map[string]string{
		"env":       envBin(plan),
		"project":   project,
		"uid":       idFromEnvOr("KEEL_UID", os.Getuid()),
		"gid":       idFromEnvOr("KEEL_GID", os.Getgid()),
		"http_port": httpPort(),
	}
	v := map[string]string{
		"env":       base["env"],
		"project":   project,
		"uid":       base["uid"],
		"gid":       base["gid"],
		"http_port": base["http_port"],
	}
	for _, k := range vocabKeysFor(plan) {
		v[k] = "" // default; overridden below by the chosen env / framework
	}
	env := recipeOfKind(plan, recipe.Env)
	if env != nil {
		for k, cmd := range env.Commands {
			v[k] = render(cmd, base)
		}
	}
	// Version pins: any recipe's pins become {{pin.<name>}} for reproducible
	// installs (later recipes win a name clash). Referenced but undefined pins
	// stay literal, which surfaces a typo rather than silently dropping a version.
	for i := range plan.Recipes {
		for name, ver := range plan.Recipes[i].Pins {
			v["pin."+name] = ver
		}
	}
	// A version choice contributes its recommendation as the default pin, so a
	// non-interactive build (--yes, a manifest rebuild, the anti-rot tests) still
	// renders a concrete version rather than an unsubstituted token.
	for i := range plan.Recipes {
		for name, vc := range plan.Recipes[i].Versions {
			if vc.Recommended != "" {
				v["pin."+name] = vc.Recommended
			}
		}
	}
	// A version the user chose overrides the recipe's own pin. Applied after the
	// pins above so the choice wins, and through the same {{pin.<name>}} token so
	// no recipe has to know whether a version was fixed or picked.
	for name, ver := range plan.Versions {
		if ver != "" {
			v["pin."+name] = ver
		}
	}
	// Recipe vars: the contract shared recipes publish and frameworks read (a
	// database's db.host/db.port/db.name…). Rendered against the vocab so a var
	// may reference {{project}} or an env token. Recipes are visited in execution
	// order, and each recipe's own vars in sorted key order, so a var that
	// references another resolves the same way on every run. A later recipe (a
	// config overlay) can refine an earlier value.
	for _, r := range ordered(plan) {
		for _, name := range sortedKeys(r.Vars) {
			v[name] = render(r.Vars[name], v)
		}
	}
	// {{create}} is rendered last, against everything above: creating the app can
	// depend on the database that was chosen (Sail installs the service for it),
	// so the vars have to exist before this line runs.
	if fw := recipeOfKind(plan, recipe.Framework); fw != nil && env != nil {
		v["create"] = render(fw.Create[env.ID], v) // "" if this framework/env has no create
	}
	return v
}

// Steps returns the plan's install commands in execution order, templated, with
// empty (no-op) steps dropped. The preview uses this, so it matches what runs;
// {{project}} shows as a placeholder since the target dir isn't known until Build.
// DownCommand is how this plan's environment tears itself down, or "" if it
// starts nothing.
//
// The environment's own `down`, not a guess from the directory's contents.
// Sniffing gets it wrong: the native environments run their database from
// docker-compose.db.yml, which looks like no compose file any detector was
// taught to recognise, so a project that plainly had a database running was
// read as having nothing to stop.
func DownCommand(plan *resolver.Plan, project string) string {
	return planVars(plan, project)["down"]
}

// SiteURL is where a built project can be opened, or "" if its environment does
// not define one.
//
// It is the same site_url the install steps use, so what keel tells you to open
// is what it installed the application to answer on - including the port from
// HTTP_PORT rather than an assumed 8080.
func SiteURL(plan *resolver.Plan, project string) string {
	return planVars(plan, project)["site_url"]
}

func Steps(plan *resolver.Plan) []string {
	vars := planVars(plan, "<project>")
	var out []string
	for _, r := range ordered(plan) {
		for _, s := range r.Install {
			if c := strings.TrimSpace(render(s, vars)); c != "" {
				out = append(out, c)
			}
		}
	}
	return out
}

// SmokeSteps returns a plan's rendered proof-of-boot commands (recipe `smoke:`),
// used by `keel recipes verify` to prove a stack actually runs after a build.
func SmokeSteps(plan *resolver.Plan, project string) []string {
	vars := planVars(plan, project)
	var out []string
	for _, r := range ordered(plan) {
		for _, s := range r.Smoke {
			if c := strings.TrimSpace(render(s, vars)); c != "" {
				out = append(out, c)
			}
		}
	}
	return out
}

func skipHook(h recipe.Hook, vars map[string]string) bool {
	if h.When == "" {
		return false
	}
	w := strings.TrimSpace(render(h.When, vars))
	return w == "" || w == "false" || w == "0"
}

// hookCmd returns the rendered shell command for a run/script hook and its
// working dir (empty command for a message hook).
func hookCmd(h recipe.Hook, vars map[string]string, dir string) (cmd, wdir string) {
	wdir = dir
	if h.WorkingDir != "" {
		wdir = filepath.Join(dir, render(h.WorkingDir, vars))
	}
	switch {
	case strings.TrimSpace(h.Run) != "":
		return strings.TrimSpace(render(h.Run, vars)), wdir
	case strings.TrimSpace(h.Script) != "":
		return "sh " + strings.TrimSpace(render(h.Script, vars)), wdir
	}
	return "", wdir
}

// runHooks fires the hooks for a stage. For recipe-scope stages (post_recipe,
// post_create) pass rec; for project-scope stages (pre_build, post_build) pass
// nil and it collects every recipe's block in execution order. A non-zero exit
// aborts the build (Cookiecutter semantics).
func runHooks(ctx context.Context, stage string, rec *recipe.Recipe, recs []recipe.Recipe, vars map[string]string, dir string, runner Runner, out io.Writer) error {
	var hooks []recipe.Hook
	if rec != nil {
		hooks = rec.Hooks[stage]
	} else {
		for i := range recs {
			hooks = append(hooks, recs[i].Hooks[stage]...)
		}
	}
	for _, h := range hooks {
		if skipHook(h, vars) {
			continue
		}
		if strings.TrimSpace(h.Message) != "" {
			fmt.Fprintf(out, "• %s\n", render(h.Message, vars))
			continue
		}
		c, wdir := hookCmd(h, vars, dir)
		if c == "" {
			continue
		}
		fmt.Fprintf(out, "→ %s\n", c)
		if err := runner.Run(ctx, wdir, c); err != nil {
			return fmt.Errorf("hook %s failed (%s): %w", stage, c, err)
		}
	}
	return nil
}

// HookSteps returns every hook's rendered shell command in execution order (for
// the untrusted-plan consent preview). Message-only hooks are omitted.
func HookSteps(plan *resolver.Plan) []string {
	vars := planVars(plan, "<project>")
	var out []string
	add := func(hooks []recipe.Hook) {
		for _, h := range hooks {
			if skipHook(h, vars) {
				continue
			}
			if c, _ := hookCmd(h, vars, "."); c != "" {
				out = append(out, c)
			}
		}
	}
	recs := ordered(plan)
	for i := range recs {
		add(recs[i].Hooks["pre_build"])
	}
	for i := range recs {
		add(recs[i].Hooks["post_recipe"])
		if recs[i].Kind == recipe.Framework {
			add(recs[i].Hooks["post_create"])
		}
	}
	for i := range recs {
		add(recs[i].Hooks["post_build"])
	}
	return out
}

// PlanTrusted reports whether every recipe in the plan is trusted (built-in or a
// loose user recipe). A pack/url recipe makes the whole plan untrusted.
func PlanTrusted(plan *resolver.Plan) bool {
	return RecipesTrusted(plan.Recipes)
}

// RecipesTrusted reports whether every recipe in the slice is trusted (built-in
// or a loose user recipe). Used by `keel add` to gate a delta on consent the same
// way PlanTrusted gates a whole build: a pack/url recipe in the set makes it
// untrusted, so installing it into an existing project needs an explicit yes.
func RecipesTrusted(rs []recipe.Recipe) bool {
	for _, r := range rs {
		switch r.Source {
		case "", "builtin", "user":
		default:
			return false
		}
	}
	return true
}

// ApplySteps returns the rendered install commands for a subset of a plan's
// recipes (the delta an add installs), in execution order, empty steps dropped.
// Vars come from the full plan so a step referencing a shared var renders the
// same as it would in a full build. This is the preview `keel add` prints.
func ApplySteps(plan *resolver.Plan, add []recipe.Recipe) []string {
	vars := planVars(plan, "<project>")
	var out []string
	for _, r := range ordered(&resolver.Plan{Recipes: add}) {
		for _, s := range r.Install {
			if c := strings.TrimSpace(render(s, vars)); c != "" {
				out = append(out, c)
			}
		}
	}
	return out
}

// Build runs a plan into opts.Dir and writes .keel/manifest.yaml on success.
// Hooks fire at the lifecycle stages (see docs/EXTENDING.md); a fresh target dir
// is rolled back if the build fails partway.
func Build(ctx context.Context, plan *resolver.Plan, opts Options) (err error) {
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	recs := ordered(plan)
	vars := planVars(plan, projectName(opts.Dir))

	// The chosen env declares whether it needs Docker (ddev/sail/docker provide it;
	// local does not), so we only gate on the daemon when it's actually required.
	needsDocker := false
	if env := recipeOfKind(plan, recipe.Env); env != nil {
		for _, p := range env.Provides {
			if p == "docker" {
				needsDocker = true
			}
		}
	}

	if opts.DryRun {
		dumpHooks := func(hooks []recipe.Hook) {
			for _, h := range hooks {
				if c, _ := hookCmd(h, vars, "."); c != "" && !skipHook(h, vars) {
					fmt.Fprintf(out, "$ %s\n", c)
				}
			}
		}
		for i := range recs {
			dumpHooks(recs[i].Hooks["pre_build"])
		}
		for _, r := range recs {
			for _, f := range r.Files {
				fmt.Fprintf(out, "✎ write %s\n", render(f.Path, vars))
			}
			for _, s := range r.Install {
				if c := strings.TrimSpace(render(s, vars)); c != "" {
					fmt.Fprintf(out, "$ %s\n", c)
				}
			}
			for _, p := range r.Patch {
				for k := range p.Set {
					fmt.Fprintf(out, "✎ patch %s: set %s\n", render(p.File, vars), k)
				}
			}
			dumpHooks(r.Hooks["post_recipe"])
			if r.Kind == recipe.Framework {
				dumpHooks(r.Hooks["post_create"])
			}
		}
		for i := range recs {
			dumpHooks(recs[i].Hooks["post_build"])
		}
		return nil
	}

	// Safe-by-default: an untrusted plan (pack/url recipe) refuses to run shell
	// unless the caller has obtained consent (Options.Trusted).
	if !PlanTrusted(plan) && !opts.Trusted {
		return fmt.Errorf("this build includes recipes from an untrusted source - re-run with consent (--trust)")
	}
	if needsDocker && opts.DockerUp != nil && !opts.DockerUp() {
		return fmt.Errorf("docker doesn't appear to be running - start it and try again (keel doctor)")
	}
	freshDir := false
	if _, statErr := os.Stat(opts.Dir); os.IsNotExist(statErr) {
		freshDir = true
	}
	if err = os.MkdirAll(opts.Dir, 0o755); err != nil {
		return err
	}
	runner := opts.Runner
	if runner == nil {
		runner = ExecRunner{Out: out}
	}
	// Roll back a dir we created if the build fails partway (Cookiecutter).
	//
	// Tearing the environment down comes FIRST, and it is not optional. A failed
	// build has usually already started containers: they hold a bind mount to
	// this directory, and a named volume and a network besides. Removing the
	// directory without stopping them leaves all three orphaned, and the next
	// attempt at the same path reuses the stale container and dies with
	// "current working directory is outside of container mount namespace root" -
	// an error that says nothing about the actual cause.
	//
	// The teardown is the env recipe's own `down` command, so DDEV deletes a
	// project, Sail and compose take their volumes with them, and a native env
	// renders empty and does nothing.
	defer func() {
		if err == nil || !freshDir {
			return
		}
		if down := vars["down"]; strings.TrimSpace(down) != "" {
			fmt.Fprintf(out, "cleaning up the environment this build started\n")
			// Best effort: the build has already failed, and a teardown error
			// must not replace the error that actually explains why.
			_ = runner.Run(ctx, opts.Dir, down)
		}
		_ = os.RemoveAll(opts.Dir)
	}()

	if err = runHooks(ctx, "pre_build", nil, recs, vars, opts.Dir, runner, out); err != nil {
		return err
	}
	for i := range recs {
		// recs is the whole plan, so a post_recipe hook that spans recipes still
		// sees them all; the recipe being applied is recs[i].
		if err = applyRecipe(ctx, recs[i], recs, vars, opts.Dir, runner, out); err != nil {
			return err
		}
	}
	if err = runHooks(ctx, "post_build", nil, recs, vars, opts.Dir, runner, out); err != nil {
		return err
	}
	return writeManifest(opts.Dir, plan)
}

// applyRecipe writes one recipe's files, runs its install steps, applies its
// dotenv patches, and fires its recipe-scope hooks, against dir with the plan's
// rendered vars. recs is the full recipe set (so a hook that collects across
// recipes still sees them all). Shared by Build (whole plan) and Apply (a delta
// added to a built project) so the two cannot drift in how a recipe is installed.
func applyRecipe(ctx context.Context, r recipe.Recipe, recs []recipe.Recipe, vars map[string]string, dir string, runner Runner, out io.Writer) error {
	for _, f := range r.Files {
		content := f.Render(func(s string) string { return render(s, vars) })
		if err := WriteFile(dir, render(f.Path, vars), content); err != nil {
			return err
		}
		fmt.Fprintf(out, "✎ %s\n", render(f.Path, vars))
	}
	for _, s := range r.Install {
		c := strings.TrimSpace(render(s, vars))
		if c == "" { // e.g. {{start}} under the Local env
			continue
		}
		fmt.Fprintf(out, "→ %s\n", c)
		if err := runner.Run(ctx, dir, c); err != nil {
			return fmt.Errorf("step failed (%s): %w", c, err)
		}
	}
	// Apply the recipe's dotenv joins (e.g. db recipe setting DB_CONNECTION),
	// now that earlier recipes' installers have created the target file.
	if err := applyPatches(dir, r.Patch, vars, out); err != nil {
		return err
	}
	if err := runHooks(ctx, "post_recipe", &r, recs, vars, dir, runner, out); err != nil {
		return err
	}
	if r.Kind == recipe.Framework {
		if err := runHooks(ctx, "post_create", &r, recs, vars, dir, runner, out); err != nil {
			return err
		}
	}
	return nil
}

// Apply installs a subset of a plan's recipes into an already-built project,
// without re-running the whole build. It is the engine half of `keel add`: the
// full plan (existing recipes + the new one, resolved together so requires and
// conflicts are honoured) is passed as plan so vars like {{db.host}} resolve the
// same as at build time, but only the recipes in add are actually installed.
//
// Vars come from the full plan for correctness, ordering follows execution order
// so a config overlay lands after the service it refines, and the manifest is the
// caller's to update — Apply changes files, not the record of what was chosen.
// There is no roll-back of a pre-existing project: unlike a fresh build, the
// directory was not created here, so a partial add leaves the project as it was
// plus whatever the failing step managed, and the error says which step.
func Apply(ctx context.Context, plan *resolver.Plan, add []recipe.Recipe, opts Options) error {
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	if !PlanTrusted(plan) && !opts.Trusted {
		return fmt.Errorf("this includes recipes from an untrusted source - re-run with consent (--trust)")
	}
	recs := ordered(plan)
	vars := planVars(plan, projectName(opts.Dir))

	if opts.DryRun {
		for _, r := range ordered(&resolver.Plan{Recipes: add}) {
			for _, f := range r.Files {
				fmt.Fprintf(out, "✎ write %s\n", render(f.Path, vars))
			}
			for _, s := range r.Install {
				if c := strings.TrimSpace(render(s, vars)); c != "" {
					fmt.Fprintf(out, "$ %s\n", c)
				}
			}
			for _, p := range r.Patch {
				for k := range p.Set {
					fmt.Fprintf(out, "✎ patch %s: set %s\n", render(p.File, vars), k)
				}
			}
		}
		return nil
	}

	runner := opts.Runner
	if runner == nil {
		runner = ExecRunner{Out: out}
	}
	// Install the delta in execution order, so a config overlay for the new
	// recipe runs after the recipe it configures.
	for _, r := range ordered(&resolver.Plan{Recipes: add}) {
		if err := applyRecipe(ctx, r, recs, vars, opts.Dir, runner, out); err != nil {
			return err
		}
	}
	return nil
}

// FireOpenHooks runs the plan's post_open hooks. The CLI calls this after a
// successful build (the engine has no concept of "open the project"). The plan's
// trust must already have been consented to before reaching here.
func FireOpenHooks(ctx context.Context, plan *resolver.Plan, dir string, out io.Writer) error {
	if out == nil {
		out = os.Stdout
	}
	vars := planVars(plan, projectName(dir))
	return runHooks(ctx, "post_open", nil, ordered(plan), vars, dir, ExecRunner{Out: out}, out)
}

// PatchFile upserts key=value pairs into a dotenv-style file (the "join" a recipe
// owns, e.g. a db recipe setting DB_CONNECTION in .env). No-op if the file
// doesn't exist yet — the installer that creates it (composer/artisan) runs
// first; the patch only edits what's there. Returns whether it patched.
func PatchFile(path string, set map[string]string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		return false, nil // not there yet — nothing to patch
	}
	f, err := envfile.Load(path)
	if err != nil {
		return false, err
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic order
	for _, k := range keys {
		f.Set(k, set[k])
	}
	return true, os.WriteFile(path, []byte(f.Render()), 0o644)
}

// applyPatches runs a recipe's dotenv patches (templated) against dir.
func applyPatches(dir string, patches []recipe.Patch, vars map[string]string, out io.Writer) error {
	for _, p := range patches {
		rendered := map[string]string{}
		for k, v := range p.Set {
			rendered[k] = render(v, vars)
		}
		rel := render(p.File, vars)
		// Confine the patch target to the project: a pack must not patch a file
		// outside the tree it is building. filepath.IsLocal rejects "..", absolute
		// and rooted paths, and is the barrier the path-traversal analysis reads.
		if !filepath.IsLocal(rel) {
			return fmt.Errorf("refusing patch path %q: it escapes the project directory", rel)
		}
		ok, err := PatchFile(filepath.Join(dir, rel), rendered)
		if err != nil {
			return err
		}
		if ok {
			fmt.Fprintf(out, "✎ patched %s\n", render(p.File, vars))
		}
	}
	return nil
}

// WriteFile writes rel (relative to dir) with content, creating parent dirs.
// Exported so `keel gen` can emit generated component files.
func WriteFile(dir, rel, content string) error {
	// Confine rel to dir: recipe/pack data names it, so "..", an absolute path or
	// a rooted name must never let a write land outside the project. filepath.IsLocal
	// is exactly that check and the barrier the path-traversal analysis recognizes.
	if !filepath.IsLocal(rel) {
		return fmt.Errorf("refusing path %q: it escapes the project directory", rel)
	}
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

// RecordAdd updates a project's manifest after Apply has installed a delta:
// newIDs (the recipe ids the user added) are appended to the recorded recipes,
// and every keel-owned file in the now-full plan is re-hashed and re-snapshotted
// so `keel update`'s 3-way merge has a correct base for the files the add wrote.
//
// The manifest's recipe list is what the user chose, so config recipes the
// resolver injects are not appended (chosenIDs drops them in a fresh build too);
// only newIDs the caller passes are added, and only if not already present, so a
// repeated add is a no-op on the record as well as on disk.
func RecordAdd(dir string, plan *resolver.Plan, m *Manifest, newIDs []string) error {
	if m.Files == nil {
		m.Files = map[string]string{}
	}
	for path, content := range RenderedFiles(plan, dir) {
		m.Files[path] = Sha(content)
		warnIfBaseUnwritten(dir, path, content)
	}
	for _, id := range newIDs {
		if !slices.Contains(m.Recipes, id) {
			m.Recipes = append(m.Recipes, id)
		}
	}
	return WriteManifestFile(dir, m)
}

func writeManifest(dir string, plan *resolver.Plan) error {
	files := map[string]string{}
	for path, content := range RenderedFiles(plan, dir) {
		files[path] = Sha(content)
		warnIfBaseUnwritten(dir, path, content) // snapshot for the 3-way `keel update` merge
	}
	b, err := yaml.Marshal(Manifest{
		Framework: plan.Framework, Env: envID(plan), Recipes: chosenIDs(plan), Files: files,
	})
	if err != nil {
		return err
	}
	kd := filepath.Join(dir, ".keel")
	if err := os.MkdirAll(kd, 0o755); err != nil {
		return err
	}
	return atomicfile.WriteFile(filepath.Join(kd, "manifest.yaml"), b, 0o644)
}

// basePath is where a keel-owned file's last-generated content is snapshotted,
// used as the merge base by `keel update`.
func basePath(dir, rel string) string { return filepath.Join(dir, ".keel", "base", rel) }

// warnIfBaseUnwritten snapshots a generated file's merge base and, if that write
// fails, warns instead of silently swallowing it. The base is what `keel update`
// 3-way-merges against; a manifest that records a file as keel-owned but has no
// base for it makes a later update mis-merge or clobber the user's edits — a
// silent, much-later data loss. The build is not failed (a missing base degrades
// update, it does not corrupt this build), but the operator is told.
func warnIfBaseUnwritten(dir, rel, content string) {
	if err := WriteBase(dir, rel, content); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not snapshot the merge base for %s: %v\n"+
			"  keel update may not cleanly merge later edits to this file.\n", rel, err)
	}
}

// WriteBase snapshots a generated file's content (the merge base for updates).
func WriteBase(dir, rel, content string) error {
	if !filepath.IsLocal(rel) {
		return fmt.Errorf("refusing path %q: it escapes the project directory", rel)
	}
	p := basePath(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

// ReadBase returns the snapshotted base content for a file (false if none — e.g.
// a project built before snapshots existed).
func ReadBase(dir, rel string) (string, bool) {
	if !filepath.IsLocal(rel) {
		return "", false
	}
	b, err := os.ReadFile(basePath(dir, rel))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// chosenIDs is the recipe ids the user actually selected, which is what belongs
// in the manifest. Config recipes are injected by the resolver from the rest of
// the plan, so recording them would make `keel update` re-resolve a list it then
// refuses, and would freeze today's overlays into a project that should pick up
// tomorrow's.
func chosenIDs(plan *resolver.Plan) []string {
	out := make([]string, 0, len(plan.Recipes))
	for _, r := range plan.Recipes {
		if r.Kind == recipe.Config {
			continue
		}
		out = append(out, r.ID)
	}
	return out
}

// WriteManifestFile persists a manifest (used by `keel update` to refresh the
// recorded file hashes after a non-destructive update).
func WriteManifestFile(dir string, m *Manifest) error {
	b, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	kd := filepath.Join(dir, ".keel")
	if err := os.MkdirAll(kd, 0o755); err != nil {
		return err
	}
	return atomicfile.WriteFile(filepath.Join(kd, "manifest.yaml"), b, 0o644)
}

// RenderedFiles returns every keel-owned file a plan writes, keyed by relative
// path, with tokens substituted (last writer wins, matching Build). External
// installers' output (composer/npm) is not included — keel only owns these joins.
func RenderedFiles(plan *resolver.Plan, dir string) map[string]string {
	vars := planVars(plan, projectName(dir))
	out := map[string]string{}
	for _, r := range ordered(plan) {
		for _, f := range r.Files {
			out[render(f.Path, vars)] = f.Render(func(s string) string { return render(s, vars) })
		}
	}
	return out
}

// Sha returns the hex sha256 of s (file provenance for `keel update`).
func Sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
