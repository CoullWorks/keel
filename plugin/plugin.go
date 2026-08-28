// Package plugin is the contract a keel plugin implements.
//
// A plugin is a Go package under plugins/ with a fixed layout. It declares
// itself in config/register.yaml and implements the pieces it needs:
//
//	plugins/<name>/
//	  config/register.yaml   REQUIRED  identity and what this plugin provides
//	  config/event.yaml      event subscriptions
//	  plugin.go              REQUIRED  the entry point: New() returns the plugin
//	  cli/commands.go        commands, added as `keel <name>`
//	  studio/screen.go       screens, rendered as an area of the studio
//	  wizard/step_1.go       steps, shown while building a project
//	  resources/             assets the plugin ships
//
// Plugins are Go, compiled into keel. Go has no practical dynamic loading: a
// shared object has to be built with the identical toolchain and flags as the
// binary loading it, which would mean a plugin only worked against one exact
// build of keel. Compiling them in means a plugin is type-checked against this
// contract and cannot fail at the moment a user reaches for it.
package plugin

import (
	"context"

	"github.com/coullworks/keel/internal/recipe"
)

// Plugin is what a plugin package returns from New(). Everything else is
// optional: a plugin that only adds a command implements only Commands.
type Plugin interface {
	// Meta is the plugin's identity, read from config/register.yaml.
	Meta() Meta
}

// Meta is a plugin's identity. It is loaded from config/register.yaml rather
// than written in Go so that `keel plugins` can describe a plugin without
// running any of its code.
type Meta struct {
	Name        string   `yaml:"name"`
	Version     string   `yaml:"version"`
	Description string   `yaml:"description"`
	Author      string   `yaml:"author"`
	License     string   `yaml:"license"`
	Homepage    string   `yaml:"homepage,omitempty"`
	Schema      int      `yaml:"schema"`
	AppliesTo   []string `yaml:"appliesTo,omitempty"` // framework ids; empty means any
	// Capabilities are the powers this plugin needs beyond reading and writing
	// project files: reaching the network, using stored secrets, or executing
	// commands. keel is offline-by-design, so a plugin that wants any of these
	// declares it here and the user consents once, at trust time, rather than a
	// plugin quietly acquiring them. An action that needs a capability the plugin
	// did not declare (or the user did not grant) is refused. See Capability.
	Capabilities []Capability `yaml:"capabilities,omitempty"`
}

// Capability is a power a plugin may need beyond reading and writing project
// files. The set is closed so trust is a finite, checkable list rather than an
// open-ended "trust me": a plugin cannot invent a new capability keel has never
// heard of and slip it past the consent screen.
type Capability string

const (
	// CapNet lets a plugin reach the network (a deploy integration calling a
	// hosting API, a fetch of a remote manifest).
	CapNet Capability = "net"
	// CapSecrets lets a plugin read the project's stored secrets (an API token a
	// deploy needs). Without it a plugin sees only what a project file holds.
	CapSecrets Capability = "secrets"
	// CapExec lets a plugin run commands on the host (git push, a vendor CLI).
	CapExec Capability = "exec"
)

// Capabilities is every capability keel recognises, for validation and for the
// trust screen to enumerate.
var Capabilities = []Capability{CapNet, CapSecrets, CapExec}

// KnownCapability reports whether c is one keel understands. A register file
// naming an unknown capability is refused rather than silently ignored: a typo
// in a security-relevant declaration must fail loudly.
func KnownCapability(c Capability) bool {
	for _, k := range Capabilities {
		if k == c {
			return true
		}
	}
	return false
}

// HasCapability reports whether this plugin declared capability c. keel checks
// it before running an action that requires c, so an action can rely on a power
// only when the plugin asked for it and the user granted it.
func (m Meta) HasCapability(c Capability) bool {
	for _, x := range m.Capabilities {
		if x == c {
			return true
		}
	}
	return false
}

// AppliesToFramework reports whether this plugin is relevant to a framework.
func (m Meta) AppliesToFramework(fw string) bool {
	if len(m.AppliesTo) == 0 {
		return true
	}
	for _, a := range m.AppliesTo {
		if a == "*" || a == fw {
			return true
		}
	}
	return false
}

// Project is the project a plugin is acting on. During `keel new` the directory
// does not exist yet and Dir is where it is about to be created.
type Project struct {
	Dir       string
	Name      string
	Framework string
	Env       string
}

// --- commands ---------------------------------------------------------------

// Commander adds CLI commands, each reachable as `keel <name>`.
type Commander interface {
	Commands() []Command
}

// Command is one CLI command.
//
// A plugin parses its own arguments (keel passes them raw), so it is also the
// authority on its own help. Summary is the one-line description; the optional
// Long and Example are the full help text and usage examples keel renders for
// `keel <name> --help`, so a plugin's flags and usage are documented rather than
// left to the bare summary. Flags is the optional list of flags to show in the
// help's Flags section — the plugin still parses them itself; this is purely
// documentation so `--help` lists them.
type Command struct {
	Name    string
	Summary string
	Long    string
	Example string
	Flags   []CommandFlag
	// NeedsProject makes keel refuse the command outside a keel project, so the
	// command does not have to check.
	NeedsProject bool
	Run          func(ctx context.Context, io IO, p Project, args []string) error
}

// CommandFlag documents one flag a plugin command accepts, so keel can list it
// under Flags in `keel <name> --help`. The plugin parses the flag itself; this
// carries only the name and description for the help renderer.
type CommandFlag struct {
	Name  string // e.g. "--crawl"
	Usage string // one-line description
}

// IO is how a plugin writes. It is deliberately not an io.Writer.
//
// keel renders every screen through one theme, and a plugin reaching for
// fmt.Fprintf would print unstyled text into the middle of it. Giving a plugin
// these instead means its output carries the same colours and alignment as
// keel's own, it cannot break a screen, and the studio and the tests can
// capture the same calls without a terminal.
type IO interface {
	// Title is a heading inside the plugin's output.
	Title(text string)
	// Detail is a label and a value, aligned with the rest.
	Detail(label, value string)
	// Note is an ordinary line.
	Note(text string)
	// List renders a titled list, for files written or findings.
	List(title string, items []string)
	// OK, Warn and Bad carry keel's own status colours.
	OK(text string)
	Warn(text string)
	Bad(text string)
}

// --- studio -----------------------------------------------------------------

// Screener adds areas to the studio.
type Screener interface {
	Screens() []Screen
}

// Screen is one area of the studio.
//
// Render returns data, not markup. The studio draws it, so a plugin cannot
// break the studio's layout or script into the page, and the same screen works
// if the studio is ever rendered anywhere else.
type Screen struct {
	ID    string
	Title string
	Icon  string
	// Component names a built ES module (a path inside the plugin dir, served at
	// /plugin-assets/<name>/<Component>) that the React studio dynamic-imports and
	// MOUNTS — the component-based own-UI tier that replaces a Render'd View, the
	// per-project mirror of Page.Component. The module exports mount(el, keel) and
	// optionally unmount(el); the plugin owns the whole surface and reaches its own
	// actions through the keel.call bridge (its Caller), threading the project dir so
	// the action runs against the screen's project. A component screen only loads
	// once the plugin is trusted.
	Component string
	Render    func(ctx context.Context, p Project) (View, error)
}

// View is what a screen returns.
type View struct {
	Sections []Section `json:"sections"`
}

// Section is one block of a screen. Kind is "stat", "list" or "text".
type Section struct {
	Title string `json:"title"`
	Kind  string `json:"kind"`
	Items []Item `json:"items"`
}

// Item is one row. Href makes it a link.
type Item struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Href  string `json:"href,omitempty"`
}

// --- studio: overview, actions and pages -----------------------------------
//
// These three hooks are additive companions to Screener. Screener draws a
// per-project tab; the studio wanted three more shapes without a plugin having
// to hand-roll each, so they reuse the same View/Section/Item DTOs a Screener
// already returns and stay opt-in: a plugin implements only the ones it needs
// and every existing plugin keeps compiling.

// Overviewer contributes sections to the studio's project overview: the summary
// a person sees on opening a project, before drilling into any one tab. A plugin
// implements it to surface a headline fact (sonar's score, ai-core's coverage)
// where it is seen without a click.
type Overviewer interface {
	// OverviewSections returns the sections this plugin adds to the overview for a
	// project. Same DTO as a Screen's View.Sections, so the studio draws them the
	// same way and a plugin cannot break the layout.
	OverviewSections(ctx context.Context, p Project) ([]Section, error)
}

// Actioner contributes actions a person can run against a project from the
// studio: an audit, a deploy, a re-sync. Unlike a Screener (which only reads and
// returns data) an action does something, so it is trust-gated — an action that
// declares a Capability runs only when the plugin holds it and the user has
// consented. It also underpins deploy integrations, which is why the capability
// model exists.
type Actioner interface {
	// Actions lists what this plugin can do to a project, as data the studio draws
	// as buttons. Computed for the project so an action can be offered only when it
	// is relevant (a deploy only once a target is configured).
	Actions(ctx context.Context, p Project) ([]Action, error)
	// RunAction runs one action by id, with any arguments the studio collected from
	// the action's Inputs. It writes progress through io, so the same call streams
	// to the studio and captures in a test. args maps an input Name to the value
	// the user gave.
	RunAction(ctx context.Context, io IO, p Project, id string, args map[string]string) error
}

// Action is one thing a plugin can do to a project. It is data, not a handler:
// the studio draws the button and collects the inputs, then calls RunAction by
// id, so a plugin never draws UI and the same declaration works over HTTP.
type Action struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Help is a one-line description shown under the button.
	Help string `json:"help,omitempty"`
	// Group buckets the action in the studio's Develop / Configure / Ship / Plugins
	// grouping. Empty groups it under the plugin's own name.
	Group string `json:"group,omitempty"`
	// Needs is the capability this action requires. The studio disables the button,
	// and RunAction is refused, unless the plugin declared it and the user granted
	// it. Empty means the action needs nothing beyond project files.
	Needs Capability `json:"needs,omitempty"`
	// Inputs the studio collects before running the action, described declaratively
	// the same way a wizard step's options are.
	Inputs []OptionSchema `json:"inputs,omitempty"`
}

// Pager contributes whole pages to the studio, outside any one project: a
// dashboard, a settings panel, a marketplace. Where a Screener's screen is a tab
// on the current project, a Page stands on its own.
type Pager interface {
	// Pages returns the global pages this plugin adds.
	Pages() []Page
}

// Page is one global studio page. It takes no project because a page is not
// scoped to one. A page comes in two tiers, the plugin's choice:
//
//   - Data page: Render returns a View and keel draws it — good for read-only
//     or status surfaces.
//   - Own-UI page ("webview"): HTML is true and RenderHTML returns the plugin's
//     own HTML/CSS/JS, which keel renders in a sandboxed iframe. The plugin owns
//     the whole surface — any layout, any interactivity — and drives its own
//     actions back through keel's bridge (keel.call in the frame → the plugin's
//     Caller). This is how a plugin ships a real front-end (sonar's scanner) that
//     keel merely hosts.
type Page struct {
	ID    string
	Title string
	Icon  string
	// HTML marks an own-UI page: keel renders RenderHTML in a sandboxed iframe
	// rather than drawing Render's View.
	HTML       bool
	Render     func(ctx context.Context) (View, error)
	RenderHTML func(ctx context.Context) (string, error)
	// Component names a built ES module (a path inside the plugin dir, served at
	// /plugin-assets/<name>/<Component>) that the React studio dynamic-imports and
	// MOUNTS — the component-based own-UI tier that replaces the iframe. The module
	// exports mount(el, keel) and optionally unmount(el); the plugin owns the whole
	// surface and reaches its own actions through the keel.call bridge (its Caller).
	// A component page only loads once the plugin is trusted.
	Component string
}

// WebUI is a plugin surface that renders its own HTML — a "webview" — instead of
// returning a View for keel to draw. keel hosts the HTML in a sandboxed iframe;
// the plugin owns everything inside it. A plugin implements this to serve a page,
// screen or hook fragment as its own front-end.
type WebUI interface {
	// UI returns the HTML for one surface: surface is "page" | "screen" | "hook",
	// id is that surface's id. A page has no project; a screen/hook receives the
	// project through the bridge (keel.project()).
	UI(ctx context.Context, surface, id string) (string, error)
}

// Caller handles a call a plugin's own UI makes back through keel's bridge
// (keel.call(action, args) in the frame). It runs in the plugin's process, so the
// scan, the fetch, the work all happen in the plugin — keel only proxies the call,
// gated by trust and the action's capability. The result is returned to the frame
// as JSON.
type Caller interface {
	Call(ctx context.Context, action string, args map[string]string) (any, error)
}

// --- recipes ----------------------------------------------------------------

// Reciper is the bridge that lets a plugin contribute recipes to keel's catalog.
// It is the single hook that makes a plugin able to add new generation types,
// default env and secrets, and services-under-a-recipe, because all three are
// already recipe-driven: a plugin ships the same declarative Recipe data a YAML
// pack would, and keel folds an enabled plugin's recipes into the catalog stamped
// Source="plugin:<name>" and trust-gated like any other non-builtin recipe.
//
// It returns concrete recipe.Recipe values rather than a filesystem so a plugin
// can build them in Go (from its own embedded content) without inventing a YAML
// layout; a plugin that prefers YAML can unmarshal its own files and return the
// result. keel validates every returned recipe before it enters the catalog, so
// a malformed one is dropped with its reason rather than corrupting the registry.
type Reciper interface {
	Recipes() []recipe.Recipe
}

// --- wizard -----------------------------------------------------------------

// Stepper adds steps to the wizard, shown in `keel new` and in the studio
// builder. keel does the prompting, so the same declaration is a terminal
// prompt in one and a form control in the other, and the plugin never draws UI.
type Stepper interface {
	Steps() []Step
}

// Step is one question asked while building a project.
type Step struct {
	ID    string
	Title string
	Help  string
	Multi bool // several options may be chosen
	Order int  // lower is asked earlier; keel's own steps occupy 0 to 50

	// Options returns the choices. It takes the project so a step can offer
	// only what is relevant: ai-core lists the skills for the chosen framework
	// and nothing else.
	Options func(ctx context.Context, p Project) ([]Option, error)

	// Apply runs once, with the values the user chose.
	Apply func(ctx context.Context, io IO, p Project, chosen []string) error
}

// Option is one choice in a step.
type Option struct {
	Value       string
	Label       string
	Description string
	Default     bool
}

// OptionStepper is an additive companion to Stepper: it exposes the same wizard
// steps as a declarative schema a headless client (the studio) can render and
// apply without keel's interactive prompts.
//
// Stepper's Options is a Go func, so the only way to answer it has been to draw
// huh prompts in a terminal; the studio therefore ran each step with its defaults
// and never showed a control. A plugin that also implements OptionStepper hands
// the studio a plain description of each step — its type, its choices, their
// defaults — so the studio can draw a real form and post the chosen values back
// through the plugin's ordinary Apply.
//
// It is deliberately separate from Stepper rather than a change to it: every
// existing consumer of Stepper keeps compiling, and a plugin adopts the schema
// only when it wants a studio form.
type OptionStepper interface {
	// OptionSteps returns one schema per wizard step, computed for this project so
	// the choices match what the interactive prompt would offer. The order matches
	// Stepper.Steps so a client can pair a schema with its step by ID.
	OptionSteps(ctx context.Context, p Project) ([]OptionSchema, error)
}

// OptionSchema is one wizard step described as data: enough for a headless client
// to draw a control and collect an answer, with no keel or terminal dependency.
// The json tags are a contract — the studio reads this over HTTP.
type OptionSchema struct {
	// ID matches the Step.ID this schema describes, so a client can post the
	// chosen values back to the right step's Apply.
	ID    string `json:"id"`
	Label string `json:"label"`
	Help  string `json:"help,omitempty"`
	// Type is how the client should render it: "select" for one choice, "multi"
	// for several. Kept small on purpose; a client that meets an unknown type can
	// fall back to the defaults, which is what running headless already does.
	Type    string         `json:"type"`
	Choices []OptionChoice `json:"choices"`
}

// OptionChoice is one selectable value in an OptionSchema.
type OptionChoice struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Default     bool   `json:"default,omitempty"`
}

// --- events -----------------------------------------------------------------

// Event is a moment in keel's lifecycle a plugin can subscribe to. The set is
// closed: an open one means every plugin invents its own name and nothing can
// be documented or checked.
type Event string

const (
	// EventProjectCreated fires after a project is scaffolded, before the summary.
	EventProjectCreated Event = "project.created"
	// EventProjectBuilt fires after a build completes.
	EventProjectBuilt Event = "project.built"
	// EventProjectOpened fires when keel is pointed at an existing project.
	EventProjectOpened Event = "project.opened"
	// EventProjectDeleted fires before a project is torn down.
	EventProjectDeleted Event = "project.deleted"
)

// Events is every event keel fires.
var Events = []Event{
	EventProjectCreated, EventProjectBuilt, EventProjectOpened, EventProjectDeleted,
}

// Listener subscribes to lifecycle events. Subscriptions are declared in
// config/event.yaml so they can be read without running anything, and the
// handler is matched to them by name.
type Listener interface {
	Listeners() map[Event]func(ctx context.Context, io IO, p Project) error
}

// --- storage ----------------------------------------------------------------

// Installer is implemented by a plugin that stores anything: its own state, or
// tables in the project's database.
//
// keel records the installed version per plugin per project in
// .keel/plugins.yaml, so Install runs once and Upgrade runs only when the
// recorded version is behind the plugin's. Without that record a plugin has to
// make its own setup idempotent, which is where WordPress plugins go wrong.
type Installer interface {
	// Install runs the first time this plugin is used in a project.
	Install(ctx context.Context, io IO, p Project, db DB) error
	// Upgrade runs when the recorded version is behind Meta().Version. from is
	// what was recorded, so a plugin can step through its own migrations.
	Upgrade(ctx context.Context, io IO, p Project, db DB, from string) error
	// Uninstall runs when the plugin is removed from a project. Leaving data
	// behind is a choice a plugin makes deliberately, not by omission.
	Uninstall(ctx context.Context, io IO, p Project, db DB) error
}

// DB is the project's database, opened by keel from the project's own
// configuration. A plugin never parses .env or guesses a DSN.
//
// It is an interface rather than *sql.DB so a plugin can be tested without a
// database, and so keel can refuse a write when it knows the project has no
// database configured.
type DB interface {
	// Driver is "mysql", "postgres", "sqlite" or "" when the project has none.
	// A plugin supporting several writes different DDL per driver, which is the
	// honest thing to do rather than pretending one dialect fits all.
	Driver() string
	// Exec runs a statement. Arguments are bound, never interpolated.
	Exec(ctx context.Context, query string, args ...any) error
	// Query returns rows for a read.
	Query(ctx context.Context, query string, args ...any) (Rows, error)
	// Available reports whether the project has a usable database. A plugin
	// that needs one should say so rather than failing at the first Exec.
	Available() bool
}

// Rows is a minimal cursor over a query result.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
	Err() error
}

// Store is a plugin's own key-value state, kept under
// .keel/plugins/<name>/ in the project. Use it for anything that does not
// belong in the project's database: a last-run timestamp, a cached result.
//
// It exists so a plugin does not invent a file format and a location, and so
// `keel delete` can remove a project's plugin state along with everything else.
type Store interface {
	Get(key string) (string, bool)
	Set(key, value string) error
	Delete(key string) error
	Keys() []string
}
