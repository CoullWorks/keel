package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coullworks/keel/internal/plugins"
	"github.com/coullworks/keel/internal/pluginstore"
	"github.com/coullworks/keel/plugin"
)

// registry is the plugin registry as the studio last read it.
//
// It is cached because loading scans and validates and several handlers ask for
// it per request, and it is dropped after any plugin action rather than held for
// the life of the process. The studio is long-lived and can install a plugin
// into itself: caching it once meant a plugin installed from the Plugins screen
// was reported as "enabled, but keel did not register it" until the studio was
// restarted, and its screens never appeared.
var (
	regMu     sync.Mutex
	regLoaded *plugins.Registry
)

func registry() *plugins.Registry {
	regMu.Lock()
	defer regMu.Unlock()
	if regLoaded == nil {
		regLoaded = plugins.Load()
	}
	return regLoaded
}

// reloadRegistry drops the cached registry so the next read rescans.
func reloadRegistry() {
	regMu.Lock()
	regLoaded = nil
	regMu.Unlock()
}

// handleScreens lists the screens plugins registered, so the studio can draw a
// tab for each without knowing what plugins exist.
func handleScreens(w http.ResponseWriter, r *http.Request) {
	reg := registry()
	type screenDTO struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Icon  string `json:"icon,omitempty"`
		// Plugin is the owning plugin, so a component screen's keel.call bridge posts
		// back to the right plugin (and so it can be threaded the project dir).
		Plugin string `json:"plugin"`
		// Component is the /plugin-assets URL of a built ES module the studio mounts
		// as a React component (the component tier); empty for data/render screens.
		Component string `json:"component,omitempty"`
	}
	// Iterate plugins (not reg.Screens()) so each screen knows its owning plugin —
	// a component screen's bridge and asset URL both need it, mirroring
	// handlePluginPages.
	out := []screenDTO{}
	for _, p := range reg.Plugins() {
		scr, ok := p.(plugin.Screener)
		if !ok {
			continue
		}
		for _, s := range scr.Screens() {
			d := screenDTO{ID: s.ID, Title: s.Title, Icon: s.Icon, Plugin: p.Meta().Name}
			if s.Component != "" {
				d.Component = "/plugin-assets/" + p.Meta().Name + "/" + s.Component
			}
			out = append(out, d)
		}
	}

	problems := make([]string, 0, len(reg.Problems()))
	for _, err := range reg.Problems() {
		problems = append(problems, err.Error())
	}
	// Surfaced rather than swallowed: a plugin that failed to register is
	// exactly the thing someone needs told about, and the studio is where they
	// are looking.
	writeJSON(w, map[string]any{"screens": out, "problems": problems})
}

// handleScreen renders one screen for one project.
//
// A screen returns data and the studio draws it. That is why this endpoint
// returns JSON rather than markup: a plugin cannot break the studio's layout or
// script into the page.
func handleScreen(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID  string `json:"id"`
		Dir string `json:"dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		badRequest(w, "bad request: "+err.Error())
		return
	}
	if body.ID == "" {
		writeJSON(w, map[string]string{"error": "no screen id"})
		return
	}
	screen, ok := registry().Screen(body.ID)
	if !ok {
		writeJSON(w, map[string]string{"error": "no such screen: " + body.ID})
		return
	}
	if screen.Render == nil {
		// A component screen has no data View: the studio mounts it from its bundle
		// (via the component URL in the screen list), it is never rendered here.
		// Guard rather than call the nil Render, which would crash the studio.
		writeJSON(w, map[string]string{"error": "screen " + body.ID + " is a component screen; mount it from its component bundle"})
		return
	}

	// projectDir is what keeps a screen inside a tracked project. A plugin is
	// not trusted with an arbitrary path from a browser.
	dir, err := projectDir(body.Dir)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	p := projectFor(dir)

	// A screen that hangs would hang the studio. A render is meant to be a read,
	// so it gets a short leash.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	view, rerr := screen.Render(ctx, p)
	if rerr != nil {
		writeJSON(w, map[string]string{"error": rerr.Error()})
		return
	}
	writeJSON(w, view)
}

// handlePluginOptions describes, as data the browser can render, the wizard
// options the applicable plugins would ask during a build — so the studio's
// Build flow can draw a real "choose your AI assistant skills" form instead of
// silently running each plugin with its defaults.
//
// It is keyed by ?framework=<fw> for the new-stack path (the project does not
// exist yet, so there is nothing to read a manifest from) and falls back to
// ?dir=<dir> for a rebuild, resolving the framework from the tracked project's
// manifest. A dir is validated exactly like every other studio path: only a
// tracked project, never an arbitrary one from the request. The schema comes
// from registry.OptionSchemasFor, the same source the CLI wizard uses, so the
// studio and `keel new` offer identical choices.
func handlePluginOptions(w http.ResponseWriter, r *http.Request) {
	fw := strings.TrimSpace(r.URL.Query().Get("framework"))
	p := plugin.Project{Framework: fw}
	if dir := strings.TrimSpace(r.URL.Query().Get("dir")); dir != "" {
		abs, err := projectDir(dir)
		if err != nil {
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		p = projectFor(abs)
		if fw != "" {
			p.Framework = fw
		}
	}
	if p.Framework == "" {
		writeJSON(w, map[string]any{"schemas": []plugin.OptionSchema{}})
		return
	}
	// A schema computation is a read, so it gets the same short leash a screen
	// render does: a plugin that hangs must not hang the studio.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	schemas, errs := registry().OptionSchemasFor(ctx, p)
	if schemas == nil {
		schemas = []plugin.OptionSchema{}
	}
	problems := make([]string, 0, len(errs))
	for _, err := range errs {
		problems = append(problems, err.Error())
	}
	writeJSON(w, map[string]any{"schemas": schemas, "problems": problems})
}

// handleOverview gathers the overview sections every applicable plugin
// contributes for a project, so the studio can draw one project summary without
// knowing which plugins exist. A read that takes a project, so it is a POST guarded
// like the others; the project is validated to a tracked directory, never an
// arbitrary path from the browser.
func handleOverview(w http.ResponseWriter, r *http.Request) {
	p, ok := projectFromBody(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	secs, errs := registry().OverviewSectionsFor(ctx, p)
	if secs == nil {
		secs = []plugin.Section{}
	}
	writeJSON(w, map[string]any{"sections": secs, "problems": problemStrings(errs)})
}

// handlePluginActions lists the actions the applicable plugins offer on a project,
// each tagged with the plugin that owns it, so the studio can draw the buttons and
// post one back to the right RunAction. An action that needs a capability the
// plugin never declared is already dropped by ActionsFor.
func handlePluginActions(w http.ResponseWriter, r *http.Request) {
	p, ok := projectFromBody(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	actions, errs := registry().ActionsFor(ctx, p)
	if actions == nil {
		actions = []plugins.PluginAction{}
	}
	writeJSON(w, map[string]any{"actions": actions, "problems": problemStrings(errs)})
}

// handlePluginAction runs one plugin action against a project.
//
// It is the trust gate for actions. An action that declares a capability (net,
// secrets, exec) runs only when the plugin declared it AND the user has trusted
// the plugin — keel is offline-by-design, so a plugin reaching the network, the
// shell or secrets is a consented decision, not something a button acquires by
// being clicked. capabilityGranted answers that from the pluginstore's trust flag.
func handlePluginAction(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Plugin string            `json:"plugin"`
		ID     string            `json:"id"`
		Dir    string            `json:"dir"`
		Args   map[string]string `json:"args"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		badRequest(w, "bad request: "+err.Error())
		return
	}
	if body.Plugin == "" || body.ID == "" {
		writeJSON(w, map[string]string{"error": "an action needs a plugin and an id"})
		return
	}
	dir, err := projectDir(body.Dir)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	p := projectFor(dir)

	// An action can take a while (a deploy, an audit), but not forever.
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	io := &captureActionIO{}
	granted := capabilityGranted(body.Plugin)
	if err := registry().RunAction(ctx, io, p, body.Plugin, body.ID, body.Args, granted); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error(), "output": io.lines})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "output": io.lines})
}

// handlePluginPages lists the global studio pages plugins contribute, so the
// studio can draw a nav entry for each. A page stands outside any one project, so
// this takes none.
func handlePluginPages(w http.ResponseWriter, r *http.Request) {
	reg := registry()
	type pageDTO struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Icon  string `json:"icon,omitempty"`
		// HTML marks an own-UI page the studio hosts in a sandboxed iframe rather
		// than drawing a View — the studio reads this to choose how to render it.
		HTML bool `json:"html,omitempty"`
		// Plugin is the owning plugin, so the webview's keel.call bridge posts back
		// to the right plugin.
		Plugin string `json:"plugin"`
		// Component is the /plugin-assets URL of a built ES module the studio mounts
		// as a React component (the component tier); empty for data + HTML pages.
		Component string `json:"component,omitempty"`
	}
	out := []pageDTO{}
	for _, p := range reg.Plugins() {
		pgr, ok := p.(plugin.Pager)
		if !ok {
			continue
		}
		for _, pg := range pgr.Pages() {
			d := pageDTO{ID: pg.ID, Title: pg.Title, Icon: pg.Icon, HTML: pg.HTML, Plugin: p.Meta().Name}
			if pg.Component != "" {
				d.Component = "/plugin-assets/" + p.Meta().Name + "/" + pg.Component
			}
			out = append(out, d)
		}
	}
	writeJSON(w, map[string]any{"pages": out})
}

// handlePluginPage renders one global page by id. An own-UI page returns its own
// HTML (which the studio hosts in a sandboxed iframe); a data page returns a View.
func handlePluginPage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		badRequest(w, "bad request: "+err.Error())
		return
	}
	page, ok := registry().Page(body.ID)
	if !ok {
		writeJSON(w, map[string]string{"error": "no such page: " + body.ID})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if page.HTML {
		if page.RenderHTML == nil {
			writeJSON(w, map[string]any{"html": ""})
			return
		}
		html, rerr := page.RenderHTML(ctx)
		if rerr != nil {
			writeJSON(w, map[string]string{"error": rerr.Error()})
			return
		}
		writeJSON(w, map[string]any{"html": html})
		return
	}
	if page.Render == nil {
		// A component page has no data View: the studio mounts it from its bundle
		// (via the component URL in the page list), it is never rendered here. Guard
		// rather than call the nil Render, which would crash the studio.
		writeJSON(w, map[string]string{"error": "page " + body.ID + " is a component page; mount it from its component bundle"})
		return
	}
	view, rerr := page.Render(ctx)
	if rerr != nil {
		writeJSON(w, map[string]string{"error": rerr.Error()})
		return
	}
	writeJSON(w, view)
}

// handlePluginCall is keel's bridge for a plugin's own UI: the sandboxed webview
// posts {plugin, action, args}, and keel runs that plugin's action in its own
// process and returns the JSON result. The work (a scan, a fetch) happens in the
// plugin; keel enforces trust + the action's capability and passes the result
// back. This is the only channel from a webview to its plugin, so the webview
// never touches keel's API or token directly.
func handlePluginCall(w http.ResponseWriter, r *http.Request) {
	// A simple cross-site form post cannot set application/json without tripping a
	// CORS preflight, so requiring it on the one endpoint that runs a plugin's own
	// code adds a CSRF layer on top of the loopback + token + same-origin guards.
	// The studio's own client always sends it (see api.ts).
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		badRequest(w, "Content-Type must be application/json")
		return
	}
	// Bound the request body: a call payload is a small {plugin, action, args, dir}
	// object, so a 1 MB ceiling is generous and stops an unbounded read.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body struct {
		Plugin string            `json:"plugin"`
		Action string            `json:"action"`
		Args   map[string]string `json:"args"`
		// Dir is set by a project-scoped surface (a component SCREEN) so the action
		// runs against that project; a global page omits it and the call runs with no
		// project. Empty when absent.
		Dir string `json:"dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		badRequest(w, "bad request: "+err.Error())
		return
	}
	if strings.TrimSpace(body.Plugin) == "" || strings.TrimSpace(body.Action) == "" {
		badRequest(w, "a plugin and an action are required")
		return
	}
	// A dir, when given, is validated to a tracked project — a component screen is
	// no more trusted with an arbitrary path from the browser than any other
	// surface. Absent, the call runs with no project.
	var pdir string
	if strings.TrimSpace(body.Dir) != "" {
		dir, err := projectDir(body.Dir)
		if err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		pdir = dir
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	out, err := pluginstore.Call(ctx, body.Plugin, body.Action, body.Args, pdir)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	// The plugin prints JSON; forward it under "result" so the webview gets
	// structured data, not a string. It is validated first: a plugin that prints
	// non-JSON (or partial JSON) would otherwise produce a malformed response body
	// the browser's response.json() throws on — so a misbehaving plugin degrades to
	// a clean error instead of a broken call.
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		trimmed = "null"
	}
	if !json.Valid([]byte(trimmed)) {
		writeJSON(w, map[string]any{"ok": false, "error": "plugin " + body.Plugin + " did not return valid JSON from action " + body.Action})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true,"result":`))
	_, _ = w.Write([]byte(trimmed))
	_, _ = w.Write([]byte("}"))
}

// projectFromBody decodes a {dir} body, validates it to a tracked project and
// returns the plugin.Project, writing the error response itself on failure. It is
// the shared front of the read-a-project-then-gather handlers.
func projectFromBody(w http.ResponseWriter, r *http.Request) (plugin.Project, bool) {
	var body struct {
		Dir string `json:"dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		badRequest(w, "bad request: "+err.Error())
		return plugin.Project{}, false
	}
	dir, err := projectDir(body.Dir)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return plugin.Project{}, false
	}
	return projectFor(dir), true
}

// capabilityGranted returns whether the user has consented to a specific
// capability for a plugin.
//
// Trust and grant are two separate gates and both must pass. Trusted answers
// "may keel run this plugin's executables at all"; the granted SET answers "and
// is THIS power (net / secrets / exec) allowed". This is the fix for the earlier
// all-or-nothing behaviour, where trusting a plugin blanket-granted every
// capability it declared — a plugin the user trusted enough to run could then
// reach the network or read secrets without that ever being a separate, visible
// decision. Now an untrusted plugin is refused every capability regardless of
// its grants, and a trusted plugin is allowed only the capabilities explicitly
// granted. A built-in has no store record, so it is refused every capability —
// the deploy integrations that need this are installed plugins.
func capabilityGranted(name string) func(plugin.Capability) bool {
	p, ok := pluginstore.Get(name)
	if !ok || !p.Trusted {
		return func(plugin.Capability) bool { return false }
	}
	return func(cap plugin.Capability) bool { return p.GrantedCaps[cap] }
}

// grantDeclaredCaps grants every capability a plugin's manifest declares. It is
// called on trust so trusting is a single informed decision (the card shows the
// declared powers next to the Trust button), while leaving each power revocable
// afterwards. A plugin that declares nothing grants nothing — the forward-looking
// case for today's built-ins, which declare no capabilities.
func grantDeclaredCaps(name string) error {
	p, ok := pluginstore.Get(name)
	if !ok {
		return nil
	}
	for _, c := range p.Meta.Capabilities {
		if err := pluginstore.SetCapabilityGranted(name, c, true); err != nil {
			return err
		}
	}
	return nil
}

// problemStrings flattens gather errors for a JSON response.
func problemStrings(errs []error) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Error())
	}
	return out
}

// captureActionIO collects an action's output as plain lines for the JSON
// response. An action streaming live to the browser is a studio-wave concern; the
// backend here returns the finished output.
type captureActionIO struct{ lines []string }

func (c *captureActionIO) Title(t string)     { c.lines = append(c.lines, t) }
func (c *captureActionIO) Detail(l, v string) { c.lines = append(c.lines, l+": "+v) }
func (c *captureActionIO) Note(t string)      { c.lines = append(c.lines, t) }
func (c *captureActionIO) OK(t string)        { c.lines = append(c.lines, "✓ "+t) }
func (c *captureActionIO) Warn(t string)      { c.lines = append(c.lines, "⚠ "+t) }
func (c *captureActionIO) Bad(t string)       { c.lines = append(c.lines, "✗ "+t) }
func (c *captureActionIO) List(t string, i []string) {
	c.lines = append(c.lines, t+": "+strings.Join(i, ", "))
}

// handlePlugins is the studio's half of plugin management, and it exists so the
// console and the studio are the same tool rather than two with different
// abilities. GET lists what is installed; POST installs, enables, disables,
// trusts or removes one.
//
// missingPluginArg names the required field an action is missing, or "" when the
// request carries everything its action needs. It is the single place the
// /api/plugins contract is stated, so a blank field is refused with a clear 400
// before any store/git call turns it into a cryptic error. An unknown action is
// left to the switch's default (which already answers 400) so this stays purely
// about presence, not about which actions exist.
func missingPluginArg(action, name, source, cap string) string {
	switch action {
	case "add":
		if strings.TrimSpace(source) == "" {
			return "add needs a plugin source (a directory or a git repo)"
		}
	case "grant", "ungrant":
		if strings.TrimSpace(name) == "" {
			return action + " needs a plugin name"
		}
		if strings.TrimSpace(cap) == "" {
			return action + " needs a capability (net, secrets or exec)"
		}
	case "enable", "disable", "trust", "untrust", "remove":
		if strings.TrimSpace(name) == "" {
			return action + " needs a plugin name"
		}
	}
	return ""
}

// Installing takes a source that reaches git or the filesystem, so it is guarded
// exactly like /api/build: loopback host, same-origin, session token, POST only.
func handlePlugins(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		// Wrapped in {plugins:[…]}, the same shape the POST branch returns, because
		// every reader on the page unwraps d.plugins. Returning a bare array here
		// (a Round-2 regression) made the reader see no .plugins field and render
		// "Nothing installed yet" on a build with three plugins compiled in.
		writeJSON(w, map[string]any{"plugins": listPlugins()})
		return
	}
	var body struct {
		Action string `json:"action"` // add | enable | disable | trust | untrust | grant | ungrant | remove
		Name   string `json:"name"`
		Source string `json:"source"`
		Ref    string `json:"ref"`
		// Cap is the capability a grant/ungrant action targets (net | secrets |
		// exec). Ignored by the other actions.
		Cap string `json:"cap"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// Reject a request missing the field its action needs BEFORE dispatching, so a
	// blank name/source/cap yields a clear 400 the page shows inline rather than a
	// cryptic downstream git/store error (or, for a built-in toggle, a garbage
	// empty-named index record). add needs a source; grant/ungrant a cap; every
	// per-plugin action a name.
	if reason := missingPluginArg(body.Action, body.Name, body.Source, body.Cap); reason != "" {
		http.Error(w, reason, http.StatusBadRequest)
		return
	}
	var err error
	switch body.Action {
	case "add":
		_, err = pluginstore.Install(r.Context(), body.Source, body.Ref)
	case "enable":
		err = setPluginEnabled(body.Name, true)
	case "disable":
		err = setPluginEnabled(body.Name, false)
	case "trust":
		// Trust is run-code consent. The UI shows the plugin's declared powers
		// alongside the Trust button (see the plugin card), so trusting is an
		// informed decision that also grants the declared set — never a silent
		// acquisition. The user can then ungrant any individual power afterwards,
		// which trust alone can no longer re-widen.
		if err = pluginstore.SetTrusted(body.Name, true); err == nil {
			err = grantDeclaredCaps(body.Name)
		}
	case "untrust":
		// Untrusting revokes the run-code consent; the granted powers become
		// inert (capabilityGranted refuses everything without trust) but are kept
		// so re-trusting restores the user's earlier choices rather than silently
		// resetting them.
		err = pluginstore.SetTrusted(body.Name, false)
	case "grant":
		err = pluginstore.SetCapabilityGranted(body.Name, plugin.Capability(body.Cap), true)
	case "ungrant":
		err = pluginstore.SetCapabilityGranted(body.Name, plugin.Capability(body.Cap), false)
	case "remove":
		err = pluginstore.Remove(body.Name)
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	// Whatever just happened changed what is on disk, so the next read rescans.
	// Installing, enabling and removing all alter which plugins register and
	// which screens exist.
	reloadRegistry()
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error(), "plugins": listPlugins()})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "plugins": listPlugins()})
}

// setPluginEnabled turns a plugin on or off, choosing the right store call by
// what the plugin is: a compiled-in plugin's off switch lives in the built-in
// index (SetBuiltinEnabled), an installed one's in its own record (SetEnabled).
// Routing on IsBuiltin here is what lets the studio enable/disable a built-in
// like sonar, not only installed plugins — the CLI makes the same distinction.
func setPluginEnabled(name string, on bool) error {
	if plugins.IsBuiltin(name) {
		return pluginstore.SetBuiltinEnabled(name, on)
	}
	return pluginstore.SetEnabled(name, on)
}

// pluginDTO is what the page draws. It carries the reason a plugin will not
// load, because hiding that is how someone concludes their plugin does nothing.
type pluginDTO struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	Trusted     bool   `json:"trusted"`
	RunsCode    bool   `json:"runsCode"`
	Source      string `json:"source,omitempty"`
	Problem     string `json:"problem,omitempty"`
	// TrustNote explains why a plugin that the user trusted BY NAME is nonetheless
	// untrusted here: it was trusted at a different path (a moved copy, or a
	// same-named plugin found elsewhere). Surfaced so the page can prompt a
	// re-trust rather than leave the user puzzled that their trusted plugin is off.
	TrustNote string `json:"trustNote,omitempty"`
	Dir       string `json:"dir"`
	// Where each one came from: "built-in", "installed", or the path of a
	// keel-<name> executable on PATH. Only an installed plugin can be trusted or
	// removed, and the screen needs to know that before it offers a button that
	// cannot work. Both built-in and installed plugins can be enabled/disabled.
	Where   string `json:"where"`
	BuiltIn bool   `json:"builtIn"`
	// Bundled narrows BuiltIn: a separate COULLWORKS tool shipped inside the keel
	// binary rather than a first-party keel feature. Author and Homepage are its
	// own provenance, carried only for a bundled tool so the page can say whose
	// tool it is instead of implying keel wrote it.
	Bundled  bool   `json:"bundled"`
	Author   string `json:"author,omitempty"`
	Homepage string `json:"homepage,omitempty"`
	// Capabilities is the set of powers (net / secrets / exec) the plugin's
	// manifest DECLARES it may need, so the page can surface them at trust time
	// rather than a plugin acquiring them silently. Granted is the subset the user
	// has explicitly consented to. The two together let the UI draw a per-power
	// grant toggle: declared-but-not-granted is the visible, actionable gap.
	Capabilities []string `json:"capabilities,omitempty"`
	Granted      []string `json:"granted,omitempty"`
}

// listPlugins is every plugin keel knows about, however it got there.
//
// It listed only what was installed under the plugins directory, so the studio
// showed nothing at all on a build with three plugins compiled into it. It now
// reads the same builder the CLI and the console read, so the three surfaces
// cannot disagree about what exists.
func listPlugins() []pluginDTO {
	onDisk := map[string]pluginstore.Installed{}
	if items, err := pluginstore.List(); err == nil {
		for _, p := range items {
			onDisk[p.Name] = p
		}
	}
	rows := plugins.Rows(registry())
	out := make([]pluginDTO, 0, len(rows))
	for _, r := range rows {
		d := pluginDTO{
			Name: r.Name, Version: r.Version, Description: r.Description,
			Enabled: r.State == "enabled", Problem: r.Problem,
			Where: r.Where, BuiltIn: r.BuiltIn,
			Bundled: r.Bundled, Author: r.Author, Homepage: r.Homepage,
		}
		if p, ok := onDisk[r.Name]; ok && !r.BuiltIn {
			d.Trusted, d.RunsCode, d.Source, d.Dir = p.Trusted, pluginstore.RunsCode(p.Dir), p.Source, p.Dir
			d.TrustNote = p.TrustNote
			// The powers the manifest declares and the subset the user granted, so
			// the page can draw a per-capability grant toggle instead of hiding what
			// a plugin can reach behind a single trust flag.
			for _, c := range p.Meta.Capabilities {
				d.Capabilities = append(d.Capabilities, string(c))
				if p.GrantedCaps[c] {
					d.Granted = append(d.Granted, string(c))
				}
			}
		}
		out = append(out, d)
	}
	return out
}
