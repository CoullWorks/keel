// Package recipe defines the composable unit Keel builds projects from.
//
// A recipe is a small, declarative node in the decision tree (a framework,
// add-on, local-dev env, database, service, extra, or generator). A user's
// project is a *path* of recipes that the resolver validates and orders and the
// engine executes. Recipes are data, not code, which is what makes Keel
// enhanceable by others: drop a YAML file in and a new stack exists. See
// docs/ROUTES.md for the full catalogue and docs/PLAN.md for the rationale.
package recipe

import (
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Kind is the category of a recipe. Kinds resolve and execute in Order.
type Kind string

const (
	Framework Kind = "framework"
	Starter   Kind = "starter"
	Addon     Kind = "addon"
	Env       Kind = "env"
	DB        Kind = "db"
	Config    Kind = "config"
	Service   Kind = "service"
	Frontend  Kind = "frontend"
	Extra     Kind = "extra"
	Generator Kind = "generator"
)

// Order is the sequence kinds are resolved and executed in. Frontend runs after
// the backend + env + services so it can wire against a live API. Config runs
// straight after the database it wires up.
var Order = []Kind{Framework, Starter, Addon, Env, DB, Config, Service, Frontend, Extra, Generator}

// rankOf indexes Order for O(1) Rank lookups, built once in init rather than
// linear-scanned per sort comparator (mirrors langOrder). Kept in sync with
// Order by construction.
var rankOf = map[Kind]int{}

func init() {
	for i, k := range Order {
		rankOf[k] = i
	}
}

// GenLevel is the granularity a generator produces at. It is the modular ladder
// from the fitness review: a code-block (a class, a route) sits inside a module
// (a Magento module, a Django app), a module inside a package (a Composer
// skeleton, an npm package), and a package inside a stack (a whole capability
// like authentication or a test-suite). The studio groups the generate menu by
// this so "add auth" and "add a model" sit at the right rung.
type GenLevel string

const (
	LevelCodeBlock GenLevel = "code-block"
	LevelModule    GenLevel = "module"
	LevelPackage   GenLevel = "package"
	LevelStack     GenLevel = "stack"
)

// GenLevels is the set of valid generator levels, for validation and help.
var GenLevels = map[GenLevel]bool{
	LevelCodeBlock: true, LevelModule: true, LevelPackage: true, LevelStack: true,
}

// Input types a GenInput may declare. These are the framework-neutral widget
// vocabulary the studio's per-component form and the CLI's prompts are built
// from, so a component describes its own form as data rather than each surface
// hardcoding one. "fields" is the mage2gen primitive (a typed column table);
// the rest are the scalar/relationship widgets a component form needs.
const (
	InText    = "text"    // free text (was "string"; both accepted)
	InInt     = "int"     // integer
	InBool    = "bool"    // checkbox
	InClass   = "class"   // a class/identifier name (e.g. a block or observer name)
	InPath    = "path"    // a project-relative path
	InRef     = "ref"     // a reference to another named thing (e.g. an event id)
	InOptions = "options" // a closed choice from Choices (was "choice"; both accepted)
	InFields  = "fields"  // the typed field table
	InGroup   = "group"   // a labelled grouping of child inputs (Children)
)

// InputTypes is the set of valid GenInput types, for validation and help. The two
// legacy spellings ("string", "choice") stay accepted so recipes and studio DTOs
// written against the first cut keep working.
var InputTypes = map[string]bool{
	InText: true, "string": true, InInt: true, InBool: true, InClass: true,
	InPath: true, InRef: true, InOptions: true, "choice": true, InFields: true, InGroup: true,
}

// GenInput is one typed value a generator collects before it runs — the recipe's
// way of describing the studio's per-component form and the CLI's prompts
// declaratively, so ONE generation shell serves every framework and only the
// catalogue (each component's Inputs) differs. An input of Type "fields" is the
// mage2gen primitive: the list of typed columns a model generator turns into
// real DDL.
type GenInput struct {
	Name     string `yaml:"name"`
	Type     string `yaml:"type"`     // one of InputTypes
	Label    string `yaml:"label"`    // studio/CLI prompt
	Required bool   `yaml:"required"` // block generation if unset
	// Choices lists the allowed values for an options input (e.g. Breeze's blade |
	// react | vue stack). Empty for the other types.
	Choices []string `yaml:"choices"`
	// Default is the value used when the input is left unset, rendered literally.
	Default string `yaml:"default"`
	// Help is a one-line hint shown under the prompt in the studio.
	Help string `yaml:"help"`
	// DependsOn names another input in the same form; this input is only collected
	// when that one is set (a truthy scalar / a non-empty selection). It is how a
	// component form shows a field conditionally (e.g. a "grid" toggle revealing
	// grid columns) without a per-framework special case.
	DependsOn string `yaml:"dependsOn"`
	// Repeatable marks an input the form may collect many times (e.g. several route
	// paths), surfaced as an add-row in the studio and a repeatable flag on the CLI.
	Repeatable bool `yaml:"repeatable"`
	// Children are the nested inputs of a Type "group" input, letting a component
	// declare a sub-form as data.
	Children []GenInput `yaml:"children"`
}

// Validate checks a GenInput is well-formed: a name, a known type, and — for an
// options input — a non-empty Choices list. It is called from Recipe.Validate so
// a malformed recipe form is rejected at load, not at render.
func (i GenInput) Validate() error {
	if strings.TrimSpace(i.Name) == "" {
		return fmt.Errorf("input has no name")
	}
	if i.Type == "" {
		return fmt.Errorf("input %s has no type", i.Name)
	}
	if !InputTypes[i.Type] {
		return fmt.Errorf("input %s has unknown type %q", i.Name, i.Type)
	}
	if (i.Type == InOptions || i.Type == "choice") && len(i.Choices) == 0 {
		return fmt.Errorf("input %s is an options input but lists no choices", i.Name)
	}
	for _, c := range i.Children {
		if err := c.Validate(); err != nil {
			return fmt.Errorf("input %s: %w", i.Name, err)
		}
	}
	return nil
}

// Rank returns the position of a kind in Order (used for stable ordering).
func (k Kind) Rank() int {
	if i, ok := rankOf[k]; ok {
		return i
	}
	return len(Order)
}

// Patch is an edit Keel applies to a generated file (the "join" it owns).
type Patch struct {
	File string            `yaml:"file"`
	Set  map[string]string `yaml:"set"`
}

// File is a file a recipe drops into the project (e.g. AGENTS.md, a Cursor rule,
// a tuned php.ini). Path and Content are templated ({{env}}, {{project}}) at build time.
//
// Verbatim turns the content pass off. Vendored library content is not a
// template, and a file that legitimately contains {{env}}, say a rule that
// documents Nuxt interpolation or a GitHub Actions expression, would otherwise
// have that token silently replaced or emptied.
type File struct {
	Path     string `yaml:"path"`
	Content  string `yaml:"content"`
	Verbatim bool   `yaml:"verbatim"`
}

// Render returns the content ready to write, honouring Verbatim.
func (f File) Render(apply func(string) string) string {
	if f.Verbatim {
		return f.Content
	}
	return apply(f.Content)
}

// Hook is one action fired at a lifecycle stage. Exactly one of Message/Run/
// Script is set. Run/Script/When/WorkingDir are templated (the same {{tokens}} as
// Install) at fire time.
type Hook struct {
	Message    string `yaml:"message"`           // print a line
	Run        string `yaml:"run"`               // host shell command (via sh -c, like Install)
	Script     string `yaml:"script"`            // path (relative to the recipe) to a script file
	When       string `yaml:"when"`              // optional guard; skip if it renders empty/false/0
	WorkingDir string `yaml:"working_directory"` // optional; relative to the project dir
}

// Hooks maps a lifecycle stage name to its ordered actions.
type Hooks map[string][]Hook

// Stages is the set of valid hook stage keys (see docs/EXTENDING.md).
var Stages = map[string]bool{
	"pre_build":   true, // once, before the recipe loop
	"post_recipe": true, // after each recipe's files+install
	"post_create": true, // after the framework recipe only
	"post_build":  true, // once, after the recipe loop
	"post_open":   true, // in the CLI, after a successful build
}

// EnvFamily groups env recipes that provision the same way, so a database or
// service can describe how it comes up once per family instead of once per
// framework. Env recipes declare it; everything else keys off it.
const (
	FamilyDDEV    = "ddev"    // ddev config / ddev add-on
	FamilyCompose = "compose" // docker compose
	FamilySail    = "sail"    // Laravel Sail (compose, but its own service names)
	FamilyLocal   = "local"   // native, no containers
)

// EnvFamilies is the set of env families a recipe may key provisioning on.
// (Distinct from Families below, which groups framework variants for the UI.)
var EnvFamilies = map[string]bool{
	FamilyDDEV: true, FamilyCompose: true, FamilySail: true, FamilyLocal: true,
}

// Fragment is the part of a recipe that only applies under one env family: how
// this database or service actually comes up there.
//
// Provisioning a database ("start a Postgres") depends on the environment, while
// wiring an app to it ("set DB_CONNECTION=pgsql") depends on the framework.
// Splitting those apart is what lets one database recipe serve every framework
// instead of one copy per pair.
type Fragment struct {
	Install []string `yaml:"install"`
	Files   []File   `yaml:"files"`
	Patch   []Patch  `yaml:"patch"`
}

// Match is the condition a config recipe attaches itself on. A config recipe is
// never chosen by the user: the resolver injects it when the resolved plan
// matches, which is how a framework contributes its own wiring for a shared
// database without either side knowing about the other.
type Match struct {
	Framework string `yaml:"framework"` // framework id, or "" for any
	// Frameworks is the list form, for wiring that several frameworks share.
	// Eight Node frameworks read the same DATABASE_URL, and one overlay naming
	// them beats eight identical files or a match so broad it also catches
	// Laravel.
	Frameworks []string `yaml:"frameworks"`
	Uses       string   `yaml:"uses"` // a capability the plan provides, e.g. "postgres"
	Env        string   `yaml:"env"`  // env id or family, or "" for any
}

// MatchesFramework reports whether this condition covers framework fw.
func (m Match) MatchesFramework(fw string) bool {
	if m.Framework == "" && len(m.Frameworks) == 0 {
		return true
	}
	if m.Framework == fw {
		return true
	}
	for _, f := range m.Frameworks {
		if f == fw {
			return true
		}
	}
	return false
}

// Credential is something a recipe cannot install without: a private Composer
// repository's keys, or an API key the app needs at runtime.
//
// The recipe declares WHAT it needs. keel asks for it once, and the environment
// decides WHERE it is written (a DDEV project reads Composer auth from a
// different place than a compose one). Neither side knows about the other, which
// is what lets a third-party pack require its own vendor's keys without a change
// to keel.
type Credential struct {
	// ID is the Composer repository host (repo.magento.com) for kind composer,
	// or the variable name (OPENAI_API_KEY) for kind env.
	ID    string `yaml:"id"`
	Kind  string `yaml:"kind"`  // composer | env
	Label string `yaml:"label"` // what to call it when asking
	Help  string `yaml:"help"`  // where to get it: a URL, or a sentence
	// Required blocks the build when missing. Optional credentials are offered
	// and skipped without complaint, because most projects need none of them.
	Required bool `yaml:"required"`
	// Auth is the Composer authentication type: http-basic (default) or bearer.
	Auth string `yaml:"auth"`
}

// Credential kinds and Composer auth types.
const (
	CredComposer  = "composer"
	CredEnv       = "env"
	AuthHTTPBasic = "http-basic"
	AuthBearer    = "bearer"
)

// SupportedSchema is the recipe schema version this keel understands.
//
// v2 added: aliases, vars, provision + env_family, the config kind, and
// credentials. A recipe declaring a higher version is refused with an upgrade
// message rather than half-understood.
const SupportedSchema = 2

// Recipe is one node in the tree. Keel orchestrates official installers via
// Install and owns only the join (env + service wiring) via Patch.
type Recipe struct {
	ID          string   `yaml:"id"`
	Kind        Kind     `yaml:"kind"`
	Label       string   `yaml:"label"`
	Lang        string   `yaml:"lang"`         // for framework recipes: the language group (php, python…)
	NodeVersion string   `yaml:"node_version"` // framework recipes: required Node line (e.g. ">=24"); keel switches via nvm. `keel doctor --fix` sets nvm up.
	Bin         string   `yaml:"bin"`          // for env recipes: the CLI binary {{env}} resolves to (ddev, sail); defaults to ID
	AppliesTo   []string `yaml:"appliesTo"`    // framework ids this is valid under; empty or "*" = any
	Requires    []string `yaml:"requires"`     // capabilities that must be present
	Conflicts   []string `yaml:"conflicts"`    // capabilities that must be absent
	Provides    []string `yaml:"provides"`     // capabilities this contributes
	Default     bool     `yaml:"default"`      // profile-seeded default for its kind
	Priority    int      `yaml:"priority"`     // execution order override (lower runs first; 0 = by kind). DDEV env = negative so it provisions before the app is created.
	Install     []string `yaml:"install"`      // ordered shell steps (shell out to official installers)
	Patch       []Patch  `yaml:"patch"`        // edits Keel owns (.env, config)
	Files       []File   `yaml:"files"`        // files the recipe drops in (AGENTS.md, Cursor rules, tuned config)
	Smoke       []string `yaml:"smoke"`        // CI proof-of-boot steps

	// Framework recipes only: the canonical cross-stack task vocabulary
	// (dev/test/lint/typecheck/build/…) mapped to the real command, templated
	// against the env vocab. Exposes one surface — `keel run test` — regardless
	// of stack. Agents and humans both self-correct against these.
	Tasks map[string]string `yaml:"tasks"`

	// Env recipes only: the command vocabulary other recipes template against, so
	// a framework's steps work under any env. Keys like start/restart/exec/composer/
	// artisan/magento/manage map to how this env runs that command (empty = no-op,
	// e.g. `local` has no start). Exposed as {{start}}, {{composer}}, {{exec}}…
	Commands map[string]string `yaml:"commands"`

	// Framework recipes only: how the app is created under each env (keyed by env
	// id). App creation genuinely differs per env (ddev composer create vs a host
	// composer create-project vs a docker one-shot), so it can't be one token.
	// Exposed to the framework's own steps as {{create}}.
	Create map[string]string `yaml:"create"`

	// Variant grouping (framework recipes only): recipes sharing a Family are one
	// chooser entry with a "type" sub-choice (e.g. WooCommerce → Classic | Bedrock).
	// The recipe whose ID == Family is the family's primary/default; others carry a
	// short Variant label. Studio-display only — the CLI resolves each ID directly.
	Family  string `yaml:"family"`
	Variant string `yaml:"variant"`

	// Category groups recipes within a step in the studio (e.g. services:
	// Search / Cache / Queue / Storage / Database). Empty = ungrouped.
	Category string `yaml:"category"`

	// Freshness & reproducibility (see docs/EXTENDING.md).
	Updated string            `yaml:"updated"` // ISO date (YYYY-MM-DD) this recipe was last reviewed; drives `keel recipes freshness`
	Pins    map[string]string `yaml:"pins"`    // named version pins (name -> version) exposed as {{pin.<name>}} in commands, for reproducible installs

	// Versions are the pins the user may choose, rather than the ones this recipe
	// fixes. A recommendation is not a constraint: Laravel runs on PHP 8.3, 8.4
	// and 8.5, and picking the one keel suggests should not be the only way to
	// get a working project. Each entry becomes a wizard step, and the answer
	// overrides Pins for that name, so recipes keep reading {{pin.<name>}} and
	// nothing downstream has to know a choice was involved.
	Versions map[string]VersionChoice `yaml:"versions,omitempty"`

	// Aliases are ids this recipe also answers to. Renaming a recipe (e.g. the
	// per-framework django-postgres becoming the shared postgres) keeps existing
	// project manifests, saved profiles and scripted `keel new` invocations
	// working instead of breaking on an id that no longer exists.
	Aliases []string `yaml:"aliases"`

	// DefaultFor names the frameworks this recipe is the default choice for,
	// overriding Default. A shared recipe serves several frameworks that do not
	// agree on a default: MariaDB is the right default for Magento and
	// WooCommerce, PostgreSQL for Django and Laravel, and one boolean cannot say
	// both.
	DefaultFor []string `yaml:"defaultFor"`

	// Vars are template values this recipe contributes, referenced as {{name}} by
	// any recipe in the plan. Databases publish a `db.*` contract (db.host,
	// db.port, db.name, db.user, db.password, db.type) and frameworks read it, so
	// a framework works with any database that fills the contract and a new
	// database works with every framework that reads it.
	Vars map[string]string `yaml:"vars"`

	// Provision is how this recipe comes up, keyed by env family (ddev, compose,
	// sail, local). The resolver merges the entry for the plan's env into the
	// recipe's own Install/Files/Patch. A recipe with a Provision map and no
	// entry for the chosen env fails to resolve, which is what turns "the
	// database choice was quietly ignored" into an error you can read.
	Provision map[string]Fragment `yaml:"provision"`

	// EnvFamily is which family an env recipe belongs to (env recipes only).
	// Empty falls back to the recipe id, so a v1 pack's env keeps working.
	EnvFamily string `yaml:"env_family"`

	// When is the condition a config recipe is injected on (config recipes only).
	When *Match `yaml:"when"`

	// Generator recipes only (kind: generator): the level of the thing generated
	// (code-block / module / package / stack), the typed inputs the studio collects
	// before running it, and — for a stack generator — the addon recipe(s) it
	// applies. A stack generator like "auth" is a thin, framework-scoped pointer at
	// an existing addon: it lets `Generatables(fw)` offer "add auth" without the
	// generator having to re-describe how Breeze or Auth.js install.
	Level   GenLevel   `yaml:"level"`  // code-block | module | package | stack
	Inputs  []GenInput `yaml:"inputs"` // typed inputs the studio collects
	Applies []string   `yaml:"apply"`  // recipe ids a stack generator resolves to (in order)

	// Credentials this recipe cannot install without (private Composer repos,
	// API keys). keel collects them before the build and writes them where the
	// chosen environment reads them.
	Credentials []Credential `yaml:"credentials"`

	// EnvSuggestions are variable names this recipe's stack commonly needs,
	// offered as completions when someone adds their own. Suggestions only: a
	// name that is not listed is still perfectly valid.
	EnvSuggestions []string `yaml:"env_suggestions"`

	// Extensibility (see docs/EXTENDING.md).
	SchemaVersion int    `yaml:"schema_version"`          // absent = 1; higher than SupportedSchema is refused
	KeelVersion   string `yaml:"keel_version_constraint"` // reserved for the pack version gate
	Hooks         Hooks  `yaml:"hooks"`                   // stage -> ordered actions

	// Provenance, stamped by the loader (never authored) for the trust model.
	Pack   string `yaml:"-"` // owning pack name ("" = built-in / loose user recipe)
	Source string `yaml:"-"` // "builtin" | "user" | "pack:<name>" | "url:<u>"
}

// Validate checks a recipe is well-formed.
func (r Recipe) Validate() error {
	if strings.TrimSpace(r.ID) == "" {
		return fmt.Errorf("recipe: empty id")
	}
	if r.Kind.Rank() == len(Order) {
		return fmt.Errorf("recipe %s: unknown kind %q", r.ID, r.Kind)
	}
	if r.SchemaVersion > SupportedSchema {
		return fmt.Errorf("recipe %s: schema_version %d newer than this keel (%d) - upgrade keel", r.ID, r.SchemaVersion, SupportedSchema)
	}
	for fam := range r.Provision {
		if !EnvFamilies[fam] {
			return fmt.Errorf("recipe %s: unknown env family %q in provision (want ddev, compose, sail or local)", r.ID, fam)
		}
	}
	if r.EnvFamily != "" {
		if r.Kind != Env {
			return fmt.Errorf("recipe %s: env_family is only for env recipes", r.ID)
		}
		if !EnvFamilies[r.EnvFamily] {
			return fmt.Errorf("recipe %s: unknown env_family %q (want ddev, compose, sail or local)", r.ID, r.EnvFamily)
		}
	}
	if r.Kind == Config {
		if r.When == nil {
			return fmt.Errorf("recipe %s: a config recipe needs a when: block saying when to apply it", r.ID)
		}
		if r.When.Framework == "" && len(r.When.Frameworks) == 0 && r.When.Uses == "" && r.When.Env == "" {
			return fmt.Errorf("recipe %s: when: must constrain at least one of framework, uses or env", r.ID)
		}
	} else if r.When != nil {
		return fmt.Errorf("recipe %s: when: is only for config recipes", r.ID)
	}
	if r.Kind == Generator {
		if r.Level == "" {
			return fmt.Errorf("recipe %s: a generator needs a level: (code-block, module, package or stack)", r.ID)
		}
		if !GenLevels[r.Level] {
			return fmt.Errorf("recipe %s: unknown generator level %q (want code-block, module, package or stack)", r.ID, r.Level)
		}
		if r.Level == LevelStack && len(r.Applies) == 0 {
			return fmt.Errorf("recipe %s: a stack generator must apply at least one recipe (apply:)", r.ID)
		}
		for _, in := range r.Inputs {
			if err := in.Validate(); err != nil {
				return fmt.Errorf("recipe %s: %w", r.ID, err)
			}
		}
	} else {
		if r.Level != "" {
			return fmt.Errorf("recipe %s: level: is only for generator recipes", r.ID)
		}
		if len(r.Applies) > 0 {
			return fmt.Errorf("recipe %s: apply: is only for generator recipes", r.ID)
		}
	}
	for i, c := range r.Credentials {
		if strings.TrimSpace(c.ID) == "" {
			return fmt.Errorf("recipe %s: credentials[%d] has no id", r.ID, i)
		}
		switch c.Kind {
		case CredComposer:
			if c.Auth != "" && c.Auth != AuthHTTPBasic && c.Auth != AuthBearer {
				return fmt.Errorf("recipe %s: credential %s has unknown auth %q (want http-basic or bearer)", r.ID, c.ID, c.Auth)
			}
		case CredEnv:
			if c.Auth != "" {
				return fmt.Errorf("recipe %s: credential %s is an env var, so auth does not apply", r.ID, c.ID)
			}
		default:
			return fmt.Errorf("recipe %s: credential %s has unknown kind %q (want composer or env)", r.ID, c.ID, c.Kind)
		}
	}
	for stage, hooks := range r.Hooks {
		if !Stages[stage] {
			return fmt.Errorf("recipe %s: unknown hook stage %q", r.ID, stage)
		}
		for i, h := range hooks {
			n := 0
			for _, s := range []string{h.Message, h.Run, h.Script} {
				if strings.TrimSpace(s) != "" {
					n++
				}
			}
			if n != 1 {
				return fmt.Errorf("recipe %s: hook %s[%d] must set exactly one of message/run/script", r.ID, stage, i)
			}
		}
	}
	return nil
}

// AppliesToFramework reports whether this recipe is valid under framework fw.
func (r Recipe) AppliesToFramework(fw string) bool {
	if len(r.AppliesTo) == 0 {
		return true
	}
	for _, a := range r.AppliesTo {
		if a == "*" || a == fw {
			return true
		}
	}
	return false
}

// IsDefaultFor reports whether this recipe is the default choice of its kind
// under framework fw. An explicit DefaultFor wins; otherwise the plain Default
// flag applies wherever the recipe applies.
func (r Recipe) IsDefaultFor(fw string) bool {
	if len(r.DefaultFor) > 0 {
		for _, f := range r.DefaultFor {
			if f == "*" || f == fw {
				return true
			}
		}
		return false
	}
	return r.Default
}

// WebServer is the capability that marks a service as an alternative ingress
// rather than an addition: NGINX and Apache both claim it, and a plan may hold
// at most one. Keyed on what a recipe provides, not on its id, so a pack that
// ships its own web server is treated as one without keel knowing its name.
const WebServer = "webserver"

// Ingress capabilities that split the "Web server" question by what the app
// actually needs. A PHP or Python app has no listener of its own, so a web
// server is a front controller that mounts the language handler (PHP-FPM,
// WSGI). A Node app is its own HTTP server, so its ingress is a process manager
// that keeps that server alive and, optionally, a reverse proxy in front of it.
// Offering a PHP front controller to a Node app is the bug this split fixes:
// nginx/apache-as-front-controller is a concept a self-serving framework has no
// use for.
const (
	// FrontController marks a web server that terminates and serves the app by
	// mounting its language handler (Apache/NGINX + PHP-FPM or WSGI). Only apps
	// that do not serve their own traffic need one.
	FrontController = "front-controller"
	// ReverseProxy marks a web server that proxies to an app already listening on
	// its own port — the right shape for a self-serving (Node) framework.
	ReverseProxy = "reverse-proxy"
	// ProcessManager marks an ingress that keeps a self-serving app's own HTTP
	// process alive (PM2), rather than fronting it. It is a web server for the
	// wizard's purposes — the single "how is this served" answer — without being
	// a proxy at all.
	ProcessManager = "process-manager"
	// SelfServing is the capability a framework declares when it runs its own
	// HTTP listener (every Node framework: `node`/`next start`). The webserver
	// step reads it to offer process-manager/reverse-proxy ingress instead of a
	// PHP-style front controller. Kept as a capability, not a hardcoded language
	// check, so a pack that ships a self-serving framework in any language lands
	// in the right question without keel knowing its name.
	SelfServing = "self-serving"
)

// IsWebServer reports whether this recipe fronts the application.
//
// One definition, because three places need the same answer and disagreeing
// would be silent: the wizards ask for a web server separately from the other
// services, and `keel new` seeds one when your profile has not chosen. A
// process manager (PM2) counts too: it is not a proxy, but it is the ingress
// answer for a self-serving app, so it belongs in the same single-select step.
func (r Recipe) IsWebServer() bool {
	for _, p := range r.Provides {
		if p == WebServer || p == ProcessManager {
			return true
		}
	}
	return false
}

// IsFrontController reports whether this web server serves the app by mounting
// its language handler (PHP-FPM/WSGI) rather than proxying to an app that
// listens for itself. A recipe that provides webserver but names neither
// front-controller nor reverse-proxy is treated as a front controller, because
// that is what the shared apache/nginx recipes were before this split and the
// default must not change under them.
func (r Recipe) IsFrontController() bool {
	front, reverse := false, false
	for _, p := range r.Provides {
		switch p {
		case FrontController:
			front = true
		case ReverseProxy, ProcessManager:
			reverse = true
		}
	}
	if front {
		return true
	}
	// A plain webserver with no ingress-shape capability is the pre-split
	// apache/nginx: a front controller by definition.
	return r.IsWebServer() && !reverse
}

// SelfServes reports whether this framework runs its own HTTP listener, so its
// ingress is a process manager and/or reverse proxy rather than a front
// controller. Read off the SelfServing capability it provides.
func (r Recipe) SelfServes() bool {
	for _, p := range r.Provides {
		if p == SelfServing {
			return true
		}
	}
	return false
}

// FrameworkSelfServes reports whether framework fw runs its own HTTP listener.
// The webserver step uses it to decide which ingress recipes to offer. "" (no
// framework chosen yet) is false, so nothing is pruned before the answer.
func (reg *Registry) FrameworkSelfServes(fw string) bool {
	if fw == "" {
		return false
	}
	r, ok := reg.Get(fw)
	return ok && r.SelfServes()
}

// Registry is the loaded set of recipes, indexed by id. byAlias maps every
// declared alias to the id it resolves to, so Get's alias fallback is O(1)
// instead of scanning every recipe on each miss — the resolver's hot loop calls
// Get thousands of times. The index is maintained in Add; a later recipe wins an
// alias the same way it wins an id, so a user recipe overriding a built-in also
// takes over its aliases.
//
// A Registry is immutable once loading finishes (no Add after the first read),
// so the ordered views the resolver reads repeatedly — the full sorted list, the
// per-kind slices, and the vocabulary keys — are computed once, lazily, under
// cacheOnce and served from there instead of being re-allocated and re-sorted on
// every call. This turns CompatibleDefault/SeedDefaults from re-sorting the whole
// registry per candidate into map lookups.
type Registry struct {
	byID    map[string]Recipe
	byAlias map[string]string

	cacheOnce sync.Once
	allSorted []Recipe          // every recipe, ordered by kind then id
	byKind    map[Kind][]Recipe // per-kind, each already ordered
	vocab     []string          // sorted command-vocabulary tokens
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byID: map[string]Recipe{}, byAlias: map[string]string{}}
}

// buildCache computes the ordered views once. Safe because the registry is
// immutable after load: the first read freezes these and every later read reuses
// them.
func (reg *Registry) buildCache() {
	reg.cacheOnce.Do(func() {
		all := make([]Recipe, 0, len(reg.byID))
		for _, r := range reg.byID {
			all = append(all, r)
		}
		sort.Slice(all, func(i, j int) bool {
			if a, b := all[i].Kind.Rank(), all[j].Kind.Rank(); a != b {
				return a < b
			}
			return all[i].ID < all[j].ID
		})
		reg.allSorted = all

		byKind := map[Kind][]Recipe{}
		for _, r := range all { // all is sorted, so each per-kind slice inherits the order
			byKind[r.Kind] = append(byKind[r.Kind], r)
		}
		reg.byKind = byKind

		seen := map[string]bool{}
		var vocab []string
		for _, r := range all {
			for k := range r.Commands {
				if !seen[k] {
					seen[k] = true
					vocab = append(vocab, k)
				}
			}
		}
		sort.Strings(vocab)
		reg.vocab = vocab
	})
}

// Add inserts a recipe. A later Add with the same id overrides the earlier one,
// so a user recipe can override a built-in. Its aliases are indexed here so the
// alias fallback in Get is a single map lookup.
func (reg *Registry) Add(r Recipe) error {
	if err := r.Validate(); err != nil {
		return err
	}
	reg.byID[r.ID] = r
	for _, a := range r.Aliases {
		reg.byAlias[a] = r.ID
	}
	return nil
}

// Get returns a recipe by id, falling back to an alias. Aliases let a recipe be
// renamed or merged without invalidating the ids already written into project
// manifests, saved profiles and people's shell history.
func (reg *Registry) Get(id string) (Recipe, bool) {
	if r, ok := reg.byID[id]; ok {
		return r, true
	}
	// The alias index resolves a rename in one lookup. A real id always wins
	// (checked first), and a real recipe still owning the aliased id keeps the
	// resolution correct even if the two disagree.
	if canonical, ok := reg.byAlias[id]; ok {
		if r, ok := reg.byID[canonical]; ok {
			return r, true
		}
	}
	return Recipe{}, false
}

// Canonical maps an id or alias to the recipe id it resolves to, and reports
// whether anything answers to it.
func (reg *Registry) Canonical(id string) (string, bool) {
	r, ok := reg.Get(id)
	return r.ID, ok
}

// All returns every recipe, ordered by kind then id. The ordering is computed
// once (the registry is immutable after load); callers must not mutate the
// returned slice.
func (reg *Registry) All() []Recipe {
	reg.buildCache()
	return reg.allSorted
}

// OfKind returns all recipes of a kind, ordered. Served from the precomputed
// per-kind index rather than filtering the whole sorted list on each call.
func (reg *Registry) OfKind(k Kind) []Recipe {
	reg.buildCache()
	return reg.byKind[k]
}

// ForFramework returns recipes of a kind valid under framework fw. This is what
// the TUI uses to grey out invalid options (no "Sail + Magento").
func (reg *Registry) ForFramework(fw string, k Kind) []Recipe {
	kind := reg.OfKind(k)
	var out []Recipe
	for _, r := range kind {
		if r.AppliesToFramework(fw) {
			out = append(out, r)
		}
	}
	return out
}

// PropagateFamilyApplicability makes framework variants inherit their family's
// shared recipes. A variant (nextjs-js, family nextjs) is the same product
// configured differently, so every db, service, add-on, env, frontend and config
// recipe that names the family primary should apply to the variant too. Rather
// than list the variant id in every shared recipe by hand - the fragility
// TestVariantsOfferWhatTheirFamilyOffers exists to catch - keel folds the variant
// ids into those recipes' appliesTo (and defaultFor) once, at load time. The
// expansion is one-way: a recipe naming the primary gains the variants; a recipe
// naming only a specific variant is left alone.
func (reg *Registry) PropagateFamilyApplicability() {
	// family primary id -> its variant ids (the convention is that the primary's
	// id equals the family name; siblings share the family and differ in id).
	// Read byID directly, not OfKind: OfKind memoises the per-kind cache on first
	// call, and doing that here would freeze it before the mutations below and
	// leave every later reader seeing the un-propagated recipes.
	variantsOf := map[string][]string{}
	for _, r := range reg.byID {
		if r.Kind == Framework && r.Family != "" && r.ID != r.Family {
			variantsOf[r.Family] = append(variantsOf[r.Family], r.ID)
		}
	}
	if len(variantsOf) == 0 {
		return
	}
	expand := func(list []string) []string {
		seen := map[string]bool{}
		for _, x := range list {
			seen[x] = true
		}
		for _, x := range list {
			for _, v := range variantsOf[x] {
				if !seen[v] {
					list = append(list, v)
					seen[v] = true
				}
			}
		}
		return list
	}
	for id, r := range reg.byID {
		if r.Kind == Framework {
			continue
		}
		changed := false
		if len(r.AppliesTo) > 0 {
			if n := expand(r.AppliesTo); len(n) != len(r.AppliesTo) {
				r.AppliesTo = n
				changed = true
			}
		}
		if len(r.DefaultFor) > 0 {
			if n := expand(r.DefaultFor); len(n) != len(r.DefaultFor) {
				r.DefaultFor = n
				changed = true
			}
		}
		if changed {
			reg.byID[id] = r
		}
	}
	// Drop any cache built before this ran so the ordered/by-kind views recompute
	// from the propagated recipes on next read.
	reg.cacheOnce = sync.Once{}
	reg.allSorted = nil
	reg.byKind = nil
}

// Len reports how many recipes are loaded.
func (reg *Registry) Len() int { return len(reg.byID) }

// langOrder ranks languages for a stable, PHP-first display order.
var langOrder = map[string]int{"php": 0, "python": 1, "node": 2, "other": 9}

// Languages returns the distinct languages of framework recipes, PHP-first.
func (reg *Registry) Languages() []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range reg.OfKind(Framework) {
		l := r.Lang
		if l == "" {
			l = "other"
		}
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		ri, oki := langOrder[out[i]]
		rj, okj := langOrder[out[j]]
		if !oki {
			ri = 8
		}
		if !okj {
			rj = 8
		}
		if ri != rj {
			return ri < rj
		}
		return out[i] < out[j]
	})
	return out
}

// FrameworksForLang returns framework recipes for a given language.
func (reg *Registry) FrameworksForLang(lang string) []Recipe {
	var out []Recipe
	for _, r := range reg.OfKind(Framework) {
		l := r.Lang
		if l == "" {
			l = "other"
		}
		if l == lang {
			out = append(out, r)
		}
	}
	return out
}

// AddYAML parses one recipe from YAML and adds it, stamped with provenance.
func AddYAML(reg *Registry, data []byte, source, pack string) error {
	var r Recipe
	if err := yaml.Unmarshal(data, &r); err != nil {
		return err
	}
	r.Source, r.Pack = source, pack
	return reg.Add(r)
}

// LoadInto walks src for *.yaml / *.yml recipes (skipping the pack manifest) and
// adds them to reg, stamping each with the given source/pack provenance.
//
// Hidden directories are skipped whole: a pack distributed as a git repository
// carries .git/ and .github/ (a CI workflow is itself YAML), and treating that
// tooling as recipes would fail every real pack. Recipes live in the pack's own
// files, never in its VCS or CI metadata, so skipping dot-directories is safe.
func LoadInto(reg *Registry, src fs.FS, source, pack string) error {
	if src == nil {
		return nil
	}
	return fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != "." && strings.HasPrefix(path.Base(p), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !(strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml")) {
			return nil
		}
		if path.Base(p) == "keel.pack.yaml" { // manifest, not a recipe
			return nil
		}
		b, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		if err := AddYAML(reg, b, source, pack); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		return nil
	})
}

// Load reads every *.yaml / *.yml under each source into one Registry. Later
// sources override earlier ones by id (built-ins first, then user recipes).
func Load(sources ...fs.FS) (*Registry, error) {
	reg := NewRegistry()
	for _, src := range sources {
		if err := LoadInto(reg, src, "", ""); err != nil {
			return nil, err
		}
	}
	return reg, nil
}

// FamilyGroup collapses framework variants that share a Family (e.g. WooCommerce
// Classic + Bedrock) into one chooser entry with a variant sub-choice. Primary is
// the recipe whose ID == the family key (falls back to the first).
type FamilyGroup struct {
	Key      string
	Variants []Recipe
	Primary  Recipe
}

// Families groups recipes by Family (empty Family = its own single-variant group),
// preserving first-seen order.
func Families(rs []Recipe) []FamilyGroup {
	idx := map[string]int{}
	var out []FamilyGroup
	for _, r := range rs {
		k := r.Family
		if k == "" {
			k = r.ID
		}
		if i, ok := idx[k]; ok {
			out[i].Variants = append(out[i].Variants, r)
		} else {
			idx[k] = len(out)
			out = append(out, FamilyGroup{Key: k, Variants: []Recipe{r}})
		}
	}
	for i := range out {
		out[i].Primary = out[i].Variants[0]
		for _, v := range out[i].Variants {
			if v.ID == out[i].Key {
				out[i].Primary = v
			}
		}
	}
	return out
}

// VocabKeys is every command-vocabulary token defined by any recipe in the
// registry. The engine uses it to tell a token an env simply does not provide
// (render it away, skip the step) from one nobody defines (leave it literal, so
// a typo is visible). Built from the loaded recipes so a pack's own tokens count.
func (reg *Registry) VocabKeys() []string {
	reg.buildCache()
	return reg.vocab
}

// VersionChoice is a version the user picks rather than one keel fixes.
//
// The distinction matters: a pin is what a recipe needs, a choice is what a
// recipe supports. Keeping them apart means keel can recommend one without
// pretending the others are unsupported.
type VersionChoice struct {
	// Label is the wizard question, e.g. "PHP version".
	Label string `yaml:"label"`
	// Recommended is the value pre-selected. It must appear in Options.
	Recommended string          `yaml:"recommended"`
	Options     []VersionOption `yaml:"options"`
	// AppliesTo narrows the question to particular frameworks, the same way a
	// recipe does. Empty means it is asked whenever this recipe is chosen.
	AppliesTo []string `yaml:"appliesTo,omitempty"`
}

// VersionOption is one selectable version.
type VersionOption struct {
	Value string `yaml:"value"`
	Label string `yaml:"label,omitempty"`
	// Note carries the reason to pick or avoid it: a support window, a known
	// incompatibility, "latest, not yet supported by every extension".
	Note string `yaml:"note,omitempty"`
}

// VersionOptions returns the choices a plan offers, keyed by pin name, in a
// stable order. A name offered by more than one chosen recipe is asked once.
func VersionOptions(rs []Recipe, framework string) map[string]VersionChoice {
	out := map[string]VersionChoice{}
	for _, r := range rs {
		for name, vc := range r.Versions {
			if len(vc.AppliesTo) > 0 && framework != "" && !slices.Contains(vc.AppliesTo, framework) {
				continue
			}
			if _, seen := out[name]; !seen {
				out[name] = vc
			}
		}
	}
	return out
}

// VersionTokenPrefix marks a selection entry as a version answer rather than a
// recipe id.
//
// Version answers travel with the recipe ids instead of through a separate
// argument, which keeps every signature between the wizard and the engine
// unchanged, and means a project manifest records the version it was built with:
// rebuilding reproduces the same PHP, not merely the same recipes.
const VersionTokenPrefix = "version:"

// VersionToken encodes a chosen version.
func VersionToken(name, value string) string { return VersionTokenPrefix + name + "=" + value }

// ParseVersionToken decodes one, reporting whether the id was a version answer.
func ParseVersionToken(id string) (name, value string, ok bool) {
	if !strings.HasPrefix(id, VersionTokenPrefix) {
		return "", "", false
	}
	name, value, ok = strings.Cut(strings.TrimPrefix(id, VersionTokenPrefix), "=")
	return name, value, ok
}
