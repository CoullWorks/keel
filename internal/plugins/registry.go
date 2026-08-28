// Package plugins is keel's side of the plugin contract: it registers the
// plugins compiled into this build, validates each one, and dispatches to them.
//
// Registration is explicit. A plugin is added to the list in All below, which
// means the set of installed plugins is one readable file rather than a pile of
// init() side effects nobody can trace.
package plugins

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/pluginstore"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/plugin"
)

// SupportedSchema is the register-file version this keel understands.
const SupportedSchema = 1

// init wires this package's Reciper contributions into the recipe catalog. The
// dependency runs plugins -> catalog (never the reverse) so catalog stays free of
// the TUI the plugin registry pulls in, which is what avoids a test-time import
// cycle. Linking the plugin registry — which the binary always does — is therefore
// what turns the plugin-recipe fold on; a pure recipe test that never imports this
// package gets a catalog with no plugin fold, which is correct for it.
func init() {
	catalog.RegisterPluginRecipes(func() []recipe.Recipe {
		return append(Load().Recipes(), installedPluginRecipes()...)
	})
}

// installedPluginRecipes folds in the recipes an enabled, discovered plugin ships
// as data in its recipes/ directory. A discovered plugin cannot be compiled in, so
// it can't implement Reciper in Go; shipping recipe YAML in recipes/ is how it
// contributes stacks. Disabled, broken or untrusted-irrelevant plugins are
// skipped, and a plugin with no recipes/ dir simply adds nothing.
func installedPluginRecipes() []recipe.Recipe {
	items, err := pluginstore.List()
	if err != nil {
		return nil
	}
	var out []recipe.Recipe
	for _, it := range items {
		if !it.Enabled || it.Problem != "" {
			continue
		}
		rdir := filepath.Join(it.Dir, "recipes")
		if fi, err := os.Stat(rdir); err != nil || !fi.IsDir() {
			continue
		}
		reg := recipe.NewRegistry()
		if err := recipe.LoadInto(reg, os.DirFS(rdir), "plugin:"+it.Name, it.Name); err != nil {
			continue
		}
		for _, rec := range reg.All() {
			rec.Source = "plugin:" + it.Name
			rec.Pack = it.Name
			out = append(out, rec)
		}
	}
	return out
}

// constructor is what every plugin package exposes.
type constructor func() (plugin.Plugin, error)

// builtin is one compiled-in plugin: how to construct it, and whether it is off
// until the user turns it on. Most built-ins are on by default; DefaultOff is for
// a plugin that ships as a demonstration rather than a working feature, so it is
// present to be studied and enabled but does not clutter a real project until
// asked for.
type builtin struct {
	new        constructor
	DefaultOff bool
}

// All is the set of plugins compiled into keel. It is deliberately empty: keel
// ships zero bundled plugins. Every plugin — ai-core, sonar, the example — is its
// own repository, discovered wherever it is cloned (the managed dir or any root on
// KEEL_PLUGIN_PATH) and enabled on demand. Nothing runs by default. The builtin
// type and this map remain so a future first-party plugin could be compiled in if
// there were ever a reason to, but the model is discovery, not bundling.
var All = map[string]builtin{}

// Registry holds the plugins that registered successfully, and what each one
// contributes.
type Registry struct {
	plugins  []plugin.Plugin
	commands []plugin.Command
	screens  []plugin.Screen
	steps    []plugin.Step
	events   map[plugin.Event][]handler
	problems []error
	// disabledBuiltins are compiled-in plugins the user switched off. They are
	// not registered, so they contribute nothing, but they are remembered here so
	// a listing can still show them as present-but-off rather than vanishing — a
	// plugin that disappears reads as a plugin that was never there.
	disabledBuiltins []plugin.Plugin
}

type handler struct {
	plugin string
	fn     func(context.Context, plugin.IO, plugin.Project) error
}

// Load builds the registry.
//
// A plugin that fails validation is skipped and its reason recorded, rather
// than taking the others down with it. Problems are returned so the caller can
// surface them: a plugin that silently does not load is the failure this whole
// design exists to avoid.
func Load() *Registry {
	r := &Registry{events: map[plugin.Event][]handler{}}

	names := make([]string, 0, len(All))
	for n := range All {
		names = append(names, n)
	}
	sort.Strings(names)

	// A default-off built-in has no explicit user choice on first run, so its
	// off state is seeded into plugins.yaml before the disabled set is read. Once
	// seeded there is a record, so `keel plugins enable example` (which deletes the
	// record) sticks: the seed only ever writes the initial default, never
	// overrides a decision the user already made. Seeding is best-effort — a
	// read-only config dir must not stop keel loading — and the DefaultOff flag is
	// the fallback below in case it could not be written.
	seedDefaultOff()

	// A built-in the user disabled is honoured the same way an installed one is:
	// it is not registered, so none of its commands, screens, steps or listeners
	// appear. The set lives in plugins.yaml, which is the only place a compiled-in
	// plugin's off switch can be recorded — the plugin's own Go package cannot
	// know a user turned it off.
	disabled := pluginstore.DisabledBuiltins()

	for _, name := range names {
		p, err := All[name].new()
		if err != nil {
			r.problems = append(r.problems, fmt.Errorf("%s: %w", name, err))
			continue
		}
		if err := validate(name, p); err != nil {
			r.problems = append(r.problems, err)
			continue
		}
		if disabled[name] || defaultOffAndUnset(name, disabled) {
			// Remembered but not registered: a disabled built-in is still shown in
			// `keel plugins` as present-but-off, so enabling it is discoverable.
			r.disabledBuiltins = append(r.disabledBuiltins, p)
			continue
		}
		r.add(name, p)
	}

	// Plugins installed at runtime join the same registry as the compiled-in
	// ones, through the same validation. Where a plugin lives is an install
	// detail; what it contributes is not, so `keel plugins`, the studio and the
	// wizard treat both kinds identically.
	installed, problems := pluginstore.Load()
	r.problems = append(r.problems, problems...)
	for _, p := range installed {
		name := p.Meta().Name
		if _, clash := All[name]; clash {
			// A built-in wins. Silently shadowing it would make a keel command
			// mean different things on different machines.
			r.problems = append(r.problems, fmt.Errorf("%s: an installed plugin uses the name of a built-in and was skipped", name))
			continue
		}
		if err := validate(name, p); err != nil {
			r.problems = append(r.problems, err)
			continue
		}
		r.add(name, p)
	}
	return r
}

// validate refuses a plugin whose register file is incomplete or whose
// declarations do not match what it implements.
func validate(key string, p plugin.Plugin) error {
	m := p.Meta()
	switch {
	case m.Schema == 0:
		return fmt.Errorf("%s: config/register.yaml has no schema", key)
	case m.Schema > SupportedSchema:
		return fmt.Errorf("%s: needs plugin schema %d, this keel supports %d", key, m.Schema, SupportedSchema)
	case m.Name == "":
		return fmt.Errorf("%s: config/register.yaml has no name", key)
	case m.Name != key:
		return fmt.Errorf("%s: register.yaml names it %q; the directory and the name must match", key, m.Name)
	case strings.ContainsAny(m.Name, " /\\:"):
		return fmt.Errorf("%s: name may not contain a space, slash or colon: it becomes `keel %s`", key, m.Name)
	case m.Version == "":
		return fmt.Errorf("%s: no version", key)
	case m.Description == "":
		return fmt.Errorf("%s: no description, which is what `keel plugins` shows", key)
	case m.Author == "":
		return fmt.Errorf("%s: no author", key)
	case m.License == "":
		return fmt.Errorf("%s: no license", key)
	}

	// A capability is a security-relevant declaration, so an unknown one is a typo
	// that must fail loudly rather than being silently ignored and leaving a plugin
	// unable to run an action it thought it had asked to.
	for _, c := range m.Capabilities {
		if !plugin.KnownCapability(c) {
			return fmt.Errorf("%s: config/register.yaml declares unknown capability %q (want net, secrets or exec)", key, c)
		}
	}

	if c, ok := p.(plugin.Commander); ok {
		for _, cmd := range c.Commands() {
			if cmd.Name == "" {
				return fmt.Errorf("%s: a command has no name", key)
			}
			if cmd.Run == nil {
				return fmt.Errorf("%s: command %q has nothing to run", key, cmd.Name)
			}
		}
	}
	if s, ok := p.(plugin.Screener); ok {
		for _, sc := range s.Screens() {
			if sc.ID == "" || sc.Title == "" {
				return fmt.Errorf("%s: a screen needs an id and a title", key)
			}
			// A screen must render SOMETHING: a data View (Render) or a mounted
			// React component (Component). A component screen has no Render, so it
			// is valid as long as it names a bundle.
			if sc.Render == nil && sc.Component == "" {
				return fmt.Errorf("%s: screen %q has neither a render nor a component", key, sc.ID)
			}
		}
	}
	if s, ok := p.(plugin.Stepper); ok {
		for _, st := range s.Steps() {
			if st.ID == "" || st.Title == "" {
				return fmt.Errorf("%s: a step needs an id and a title", key)
			}
			if st.Options == nil {
				return fmt.Errorf("%s: step %q offers no options", key, st.ID)
			}
			if st.Apply == nil {
				return fmt.Errorf("%s: step %q does nothing once answered", key, st.ID)
			}
		}
	}

	// A subscription in config/event.yaml with no handler is an event that
	// silently never fires, so it fails here instead.
	type subscriber interface {
		Subscriptions() ([]plugin.Event, error)
	}
	if s, ok := p.(subscriber); ok {
		declared, err := s.Subscriptions()
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		l, hasHandlers := p.(plugin.Listener)
		handlers := map[plugin.Event]bool{}
		if hasHandlers {
			for e := range l.Listeners() {
				handlers[e] = true
			}
		}
		for _, e := range declared {
			if !known(e) {
				return fmt.Errorf("%s: config/event.yaml subscribes to unknown event %q", key, e)
			}
			if !handlers[e] {
				return fmt.Errorf("%s: config/event.yaml subscribes to %q but no handler implements it", key, e)
			}
		}
		for e := range handlers {
			if !contains(declared, e) {
				return fmt.Errorf("%s: handles %q but config/event.yaml does not declare it", key, e)
			}
		}
	}
	return nil
}

func known(e plugin.Event) bool { return contains(plugin.Events, e) }

func contains(list []plugin.Event, e plugin.Event) bool {
	for _, x := range list {
		if x == e {
			return true
		}
	}
	return false
}

func (r *Registry) add(name string, p plugin.Plugin) {
	r.plugins = append(r.plugins, p)
	if c, ok := p.(plugin.Commander); ok {
		r.commands = append(r.commands, c.Commands()...)
	}
	if s, ok := p.(plugin.Screener); ok {
		r.screens = append(r.screens, s.Screens()...)
	}
	if s, ok := p.(plugin.Stepper); ok {
		r.steps = append(r.steps, s.Steps()...)
	}
	if l, ok := p.(plugin.Listener); ok {
		for e, fn := range l.Listeners() {
			r.events[e] = append(r.events[e], handler{plugin: name, fn: fn})
		}
	}
}

// Plugins returns everything that registered.
func (r *Registry) Plugins() []plugin.Plugin { return r.plugins }

// DisabledBuiltins returns the compiled-in plugins the user switched off. They
// did not register, so they add nothing; a listing shows them as off so turning
// one back on is discoverable rather than a guess.
func (r *Registry) DisabledBuiltins() []plugin.Plugin { return r.disabledBuiltins }

// IsBuiltin reports whether name is a plugin compiled into this keel. It is the
// one fact only this package holds, so the CLI asks it before deciding whether an
// enable/disable/remove targets a built-in or an installed plugin.
func IsBuiltin(name string) bool {
	_, ok := All[name]
	return ok
}

// seedDefaultOff records the initial off state for every default-off built-in
// that has no record yet, so its default persists in plugins.yaml and enabling it
// (which deletes the record) is not undone on the next run. It only ever writes
// the absent initial default: a built-in that already has a record — because the
// user enabled or disabled it — is left exactly as they left it.
//
// Errors are swallowed on purpose. A read-only config dir must not stop keel from
// loading; the DefaultOff flag is honoured directly by defaultOffAndUnset in that
// case, so the plugin is still off, just not remembered as such.
func seedDefaultOff() {
	known := pluginstore.KnownBuiltins()
	for name, b := range All {
		if b.DefaultOff && !known[name] {
			_ = pluginstore.SetBuiltinEnabled(name, false)
		}
	}
}

// defaultOffAndUnset reports whether a built-in is default-off and the user has
// made no explicit choice. It is the fallback for when seeding could not write a
// record (a read-only config dir): the plugin is still treated as off. disabled
// is the recorded disabled set, so a built-in already recorded either way is not
// second-guessed here.
func defaultOffAndUnset(name string, disabled map[string]bool) bool {
	b, ok := All[name]
	if !ok || !b.DefaultOff {
		return false
	}
	// If it is recorded disabled, the disabled[name] branch already caught it; if it
	// is recorded enabled, the user turned it on and we must honour that. Only the
	// unrecorded case falls through to the default.
	return !disabled[name] && !pluginstore.HasBuiltinRecord(name)
}

// bundled is the set of compiled-in plugins that are separate COULLWORKS tools
// rather than first-party keel features: keel would label them as separate tools
// with their own author and homepage. It is empty because keel ships zero
// built-ins — sonar and ai-core are now their own repositories, discovered and
// enabled like any other plugin, so nothing is bundled into the binary. The map
// and IsBundled remain as the seam for a future first-party bundled tool, but the
// model is discovery, not bundling.
var bundled = map[string]bool{}

// IsBundled reports whether a built-in is a separate COULLWORKS tool shipped with
// keel rather than a first-party keel feature. The front ends use it to show
// whose tool it is instead of implying keel wrote it.
func IsBundled(name string) bool { return bundled[name] }

// Problems returns the plugins that failed to register, and why.
func (r *Registry) Problems() []error { return r.problems }

// Commands returns every command, for the CLI to mount.
func (r *Registry) Commands() []plugin.Command { return r.commands }

// Command finds one by name.
func (r *Registry) Command(name string) (plugin.Command, bool) {
	for _, c := range r.commands {
		if c.Name == name {
			return c, true
		}
	}
	return plugin.Command{}, false
}

// Screens returns every studio screen.
func (r *Registry) Screens() []plugin.Screen { return r.screens }

// Screen finds one by id.
func (r *Registry) Screen(id string) (plugin.Screen, bool) {
	for _, s := range r.screens {
		if s.ID == id {
			return s, true
		}
	}
	return plugin.Screen{}, false
}

// StepsFor returns the wizard steps that apply to a framework, in the order
// they should be asked.
func (r *Registry) StepsFor(fw string) []plugin.Step {
	var out []plugin.Step
	for _, p := range r.plugins {
		if !p.Meta().AppliesToFramework(fw) {
			continue
		}
		if s, ok := p.(plugin.Stepper); ok {
			out = append(out, s.Steps()...)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

// OptionSchemasFor returns every applicable plugin's wizard steps described as
// declarative schema, for a headless client that renders its own form rather than
// keel's interactive prompts. Only plugins that implement OptionStepper appear;
// the rest are still driven interactively (or by defaults) through StepsFor.
//
// A plugin whose schema cannot be computed for this project is skipped with its
// reason recorded, so one plugin's trouble does not deny the client the others'
// schema — the same tolerance the interactive path already has.
func (r *Registry) OptionSchemasFor(ctx context.Context, p plugin.Project) ([]plugin.OptionSchema, []error) {
	var out []plugin.OptionSchema
	var errs []error
	for _, pl := range r.plugins {
		if !pl.Meta().AppliesToFramework(p.Framework) {
			continue
		}
		os, ok := pl.(plugin.OptionStepper)
		if !ok {
			continue
		}
		schemas, err := os.OptionSteps(ctx, p)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", pl.Meta().Name, err))
			continue
		}
		out = append(out, schemas...)
	}
	return out, errs
}

// Emit fires an event at every subscriber.
//
// One listener failing does not stop the others, and never fails the build that
// triggered it: a plugin's opinion about a finished project is not a reason to
// undo the project. Errors are returned for the caller to report.
func (r *Registry) Emit(ctx context.Context, e plugin.Event, io plugin.IO, p plugin.Project) []error {
	var errs []error
	for _, h := range r.events[e] {
		if err := h.fn(ctx, io, p); err != nil {
			errs = append(errs, fmt.Errorf("%s handling %s: %w", h.plugin, e, err))
		}
	}
	return errs
}

// plugin returns the registered plugin with this name, so a gather method can
// map an action or a capability back to the plugin that owns it.
func (r *Registry) plugin(name string) (plugin.Plugin, bool) {
	for _, p := range r.plugins {
		if p.Meta().Name == name {
			return p, true
		}
	}
	return nil, false
}

// OverviewSectionsFor gathers the overview sections every applicable plugin
// contributes for a project, so the studio can draw one project summary without
// knowing which plugins exist. A plugin whose sections cannot be computed is
// skipped with its reason recorded — one plugin's trouble does not deny the
// others' contribution, the same tolerance every other gather method has.
func (r *Registry) OverviewSectionsFor(ctx context.Context, p plugin.Project) ([]plugin.Section, []error) {
	var out []plugin.Section
	var errs []error
	for _, pl := range r.plugins {
		if !pl.Meta().AppliesToFramework(p.Framework) {
			continue
		}
		ov, ok := pl.(plugin.Overviewer)
		if !ok {
			continue
		}
		secs, err := ov.OverviewSections(ctx, p)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", pl.Meta().Name, err))
			continue
		}
		out = append(out, secs...)
	}
	return out, errs
}

// ActionsFor gathers the actions every applicable plugin offers on a project,
// each tagged with the plugin that owns it so the studio can post it back to the
// right RunAction. An action that needs a capability the plugin never declared is
// dropped: offering a button that can never run is worse than not offering it.
func (r *Registry) ActionsFor(ctx context.Context, p plugin.Project) ([]PluginAction, []error) {
	var out []PluginAction
	var errs []error
	for _, pl := range r.plugins {
		if !pl.Meta().AppliesToFramework(p.Framework) {
			continue
		}
		ac, ok := pl.(plugin.Actioner)
		if !ok {
			continue
		}
		actions, err := ac.Actions(ctx, p)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", pl.Meta().Name, err))
			continue
		}
		for _, a := range actions {
			if a.Needs != "" && !pl.Meta().HasCapability(a.Needs) {
				errs = append(errs, fmt.Errorf("%s: action %q needs capability %q the plugin did not declare", pl.Meta().Name, a.ID, a.Needs))
				continue
			}
			out = append(out, PluginAction{Plugin: pl.Meta().Name, Action: a})
		}
	}
	return out, errs
}

// PluginAction is one action paired with the plugin that owns it, so the studio
// knows where to send RunAction and can group the buttons by plugin.
type PluginAction struct {
	Plugin string        `json:"plugin"`
	Action plugin.Action `json:"action"`
}

// RunAction runs one plugin action by plugin name and action id.
//
// It is the trust gate for actions: an action that declares a capability runs
// only when the plugin declared it AND granted reports it consented. keel is
// offline-by-design, so a plugin reaching the network, secrets or the shell is a
// decision the user makes once, not something an action acquires by being called.
// granted answers "has the user consented to this capability for this plugin?";
// the caller supplies it because trust lives in the pluginstore, not here.
func (r *Registry) RunAction(ctx context.Context, io plugin.IO, p plugin.Project, pluginName, id string, args map[string]string, granted func(plugin.Capability) bool) error {
	pl, ok := r.plugin(pluginName)
	if !ok {
		return fmt.Errorf("no such plugin: %s", pluginName)
	}
	ac, ok := pl.(plugin.Actioner)
	if !ok {
		return fmt.Errorf("%s offers no actions", pluginName)
	}
	actions, err := ac.Actions(ctx, p)
	if err != nil {
		return fmt.Errorf("%s: %w", pluginName, err)
	}
	var found *plugin.Action
	for i := range actions {
		if actions[i].ID == id {
			found = &actions[i]
			break
		}
	}
	if found == nil {
		return fmt.Errorf("%s has no action %q", pluginName, id)
	}
	if need := found.Needs; need != "" {
		if !pl.Meta().HasCapability(need) {
			return fmt.Errorf("%s: action %q needs capability %q the plugin did not declare", pluginName, id, need)
		}
		if granted == nil || !granted(need) {
			return fmt.Errorf("%s: action %q needs the %q capability, which you have not granted (keel is offline by default)", pluginName, id, need)
		}
	}
	return ac.RunAction(ctx, io, p, id, args)
}

// Pages gathers the global studio pages every plugin contributes, so the studio
// can draw a nav entry for each without knowing what plugins exist.
func (r *Registry) Pages() []plugin.Page {
	var out []plugin.Page
	for _, p := range r.plugins {
		if pg, ok := p.(plugin.Pager); ok {
			out = append(out, pg.Pages()...)
		}
	}
	return out
}

// Page finds one global page by id.
func (r *Registry) Page(id string) (plugin.Page, bool) {
	for _, p := range r.Pages() {
		if p.ID == id {
			return p, true
		}
	}
	return plugin.Page{}, false
}

// Recipes gathers the recipes every enabled plugin contributes through Reciper,
// each stamped with its owning plugin so the catalog can trust-gate it and a
// listing can say where it came from. A plugin that did not register (disabled or
// broken) is not in r.plugins, so it contributes nothing — the fold honours the
// same on/off switch every other contribution does.
//
// The stamping is done here rather than in the catalog so the one fact only this
// package holds — which plugin returned a recipe — is attached at the source. The
// catalog validates and admits them; it never has to know a plugin was involved.
func (r *Registry) Recipes() []recipe.Recipe {
	var out []recipe.Recipe
	for _, p := range r.plugins {
		rc, ok := p.(plugin.Reciper)
		if !ok {
			continue
		}
		name := p.Meta().Name
		for _, rec := range rc.Recipes() {
			rec.Source = "plugin:" + name
			rec.Pack = name
			out = append(out, rec)
		}
	}
	return out
}
