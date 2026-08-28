package pluginstore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/coullworks/keel/plugin"
	"gopkg.in/yaml.v3"
)

// maxPluginOutput bounds how much stdout keel reads from a plugin subprocess. A
// plugin (buggy or hostile) that prints without end must not be able to exhaust
// keel's memory — and a render/overview path fires automatically when the studio
// draws a project, so this is not gated behind a user action. Output past the cap
// is discarded; the run still returns what fit.
const maxPluginOutput = 16 << 20 // 16 MB

// pluginEnvAllow is the set of environment variables a plugin subprocess inherits
// from keel: operational settings a tool legitimately needs (PATH, locale, proxy,
// temp), but NOT the ambient secrets a developer exports in their shell
// (GITHUB_TOKEN, AWS_*, DATABASE_URL, API keys, …). A plugin that needs a secret
// must be granted the `secrets` capability and read it through keel, not lift it
// from keel's own environment. This is what stops a trusted plugin — whose render
// path fires automatically — from harvesting the operator's ambient credentials.
// Keys are compared upper-cased so the Windows/lower-case spellings match too.
var pluginEnvAllow = map[string]bool{
	"PATH": true, "HOME": true, "TMPDIR": true, "TMP": true, "TEMP": true,
	"LANG": true, "LC_ALL": true, "LC_CTYPE": true, "TZ": true, "TERM": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true,
	"SYSTEMROOT": true, "USERPROFILE": true, // Windows PATH/tooling essentials
}

// pluginEnv returns the environment a plugin subprocess runs with: the KEEL_*
// project vars keel provides by contract, plus the operational (non-secret)
// vars from pluginEnvAllow. It deliberately does NOT inherit keel's full
// environment, so ambient secrets never reach a plugin that was not granted them.
func pluginEnv(extra ...string) []string {
	env := append([]string{}, extra...)
	for _, kv := range os.Environ() {
		if k, _, ok := strings.Cut(kv, "="); ok && pluginEnvAllow[strings.ToUpper(k)] {
			env = append(env, kv)
		}
	}
	return env
}

// cappedWriter writes at most n bytes to w and silently discards the rest, always
// reporting a full write so the child process is never killed by a short write.
type cappedWriter struct {
	w io.Writer
	n int
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	if c.n > 0 {
		w := p
		if len(w) > c.n {
			w = w[:c.n]
		}
		nn, err := c.w.Write(w)
		c.n -= nn
		if err != nil {
			return nn, err
		}
	}
	return len(p), nil
}

// runCapped runs cmd capturing stdout (and stderr too when combined is set) into a
// buffer bounded to maxPluginOutput, so plugin output can never exhaust memory.
func runCapped(cmd *exec.Cmd, combined bool) ([]byte, error) {
	var buf bytes.Buffer
	cw := &cappedWriter{w: &buf, n: maxPluginOutput}
	cmd.Stdout = cw
	if combined {
		cmd.Stderr = cw
	}
	err := cmd.Run()
	return buf.Bytes(), err
}

// manifest is config/register.yaml in full: the identity keel already read, plus
// what the plugin contributes.
//
// Contributions are declared as data rather than written in Go, because an
// installed plugin cannot be compiled into this binary. A screen is a list of
// sections, a wizard step is a list of options, and a command is a name and an
// optional executable. That covers the extension points without keel ever having
// to load foreign code into its own process.
type manifest struct {
	plugin.Meta `yaml:",inline"`

	Commands []declaredCommand `yaml:"commands,omitempty"`
	Screens  []declaredScreen  `yaml:"screens,omitempty"`
	Steps    []declaredStep    `yaml:"steps,omitempty"`
	Actions  []declaredAction  `yaml:"actions,omitempty"`
	Pages    []declaredPage    `yaml:"pages,omitempty"`
	// Overview is argv keel runs to get the sections this plugin adds to a
	// project's studio overview, printed as View JSON (project in the environment).
	Overview []string `yaml:"overview,omitempty"`
}

// declaredPage is a top-level studio page (a nav destination under "Extend"),
// unlike a screen which is a per-project tab. It has no project because a page is
// not scoped to one; keel runs its render executable and draws the View it prints.
type declaredPage struct {
	ID    string `yaml:"id"`
	Title string `yaml:"title"`
	Icon  string `yaml:"icon,omitempty"`
	// UI marks an own-HTML page (a "webview"): render prints the plugin's own HTML,
	// which keel hosts in a sandboxed iframe. Without it, render prints a View that
	// keel draws.
	UI bool `yaml:"ui,omitempty"`
	// Component is a built ES module (a path inside the plugin dir) the studio
	// serves at /plugin-assets/<name>/<Component> and mounts as a React component —
	// the no-iframe own-UI tier. Takes precedence over UI when both are set.
	Component string   `yaml:"component,omitempty"`
	Render    []string `yaml:"render,omitempty"` // argv, relative to the plugin dir
}

type declaredAction struct {
	ID    string   `yaml:"id"`
	Label string   `yaml:"label"`
	Help  string   `yaml:"help,omitempty"`
	Group string   `yaml:"group,omitempty"`
	Needs string   `yaml:"needs,omitempty"` // capability: net | secrets | exec
	Run   []string `yaml:"run,omitempty"`   // argv, relative to the plugin dir; input values are appended as key=value
}

type declaredCommand struct {
	Name         string   `yaml:"name"`
	Summary      string   `yaml:"summary"`
	NeedsProject bool     `yaml:"needsProject,omitempty"`
	Run          []string `yaml:"run,omitempty"` // argv, relative to the plugin dir
}

type declaredScreen struct {
	ID    string `yaml:"id"`
	Title string `yaml:"title"`
	Icon  string `yaml:"icon,omitempty"`
	// Component is a built ES module (a path inside the plugin dir) the studio
	// serves at /plugin-assets/<name>/<Component> and mounts as a React component —
	// the no-iframe own-UI tier for a per-project screen, mirroring a page's
	// Component. Takes precedence over render/sections when set.
	Component string `yaml:"component,omitempty"`
	// Static sections, or a live one: with `render` set, keel runs the plugin's own
	// executable and reads the View it prints as JSON, so an installed plugin can
	// show live data (a scan, a status) a static manifest never could.
	Sections []declaredSection `yaml:"sections,omitempty"`
	Render   []string          `yaml:"render,omitempty"` // argv, relative to the plugin dir
}

type declaredSection struct {
	Kind  string         `yaml:"kind"` // stat | list | text
	Title string         `yaml:"title,omitempty"`
	Items []declaredItem `yaml:"items,omitempty"`
}

type declaredItem struct {
	Label string `yaml:"label"`
	Value string `yaml:"value,omitempty"`
	Href  string `yaml:"href,omitempty"`
}

type declaredStep struct {
	ID      string           `yaml:"id"`
	Title   string           `yaml:"title"`
	Help    string           `yaml:"help,omitempty"`
	Multi   bool             `yaml:"multi,omitempty"`
	Order   int              `yaml:"order,omitempty"`
	Options []declaredOption `yaml:"options,omitempty"`
	// OptionsRender is argv keel runs to compute the options live (project in the
	// environment), for a step whose choices depend on the project. It prints
	// {"options":[{value,label,description,default}]} as JSON. Falls back to the
	// static Options when unset.
	OptionsRender []string `yaml:"optionsRender,omitempty"`
	// Apply runs after the user chooses. The chosen keys arrive as arguments.
	Apply []string `yaml:"apply,omitempty"`
}

type declaredOption struct {
	Key      string `yaml:"key"`
	Label    string `yaml:"label"`
	Desc     string `yaml:"desc,omitempty"`
	Selected bool   `yaml:"selected,omitempty"`
}

// readManifest parses the whole register file, not just the identity block.
func readManifest(dir string) (manifest, error) {
	var mf manifest
	b, err := os.ReadFile(filepath.Join(dir, "config", "register.yaml"))
	if err != nil {
		return mf, fmt.Errorf("no config/register.yaml: %w", err)
	}
	if err := yaml.Unmarshal(b, &mf); err != nil {
		return mf, fmt.Errorf("config/register.yaml: %w", err)
	}
	return mf, validate(mf.Meta)
}

// RunsCode reports whether a plugin declares anything keel would execute. It is
// how the install path can warn before, not after.
func RunsCode(dir string) bool {
	mf, err := readManifest(dir)
	if err != nil {
		return false
	}
	for _, c := range mf.Commands {
		if len(c.Run) > 0 {
			return true
		}
	}
	for _, s := range mf.Steps {
		if len(s.Apply) > 0 {
			return true
		}
	}
	for _, sc := range mf.Screens {
		if len(sc.Render) > 0 {
			return true
		}
	}
	for _, ac := range mf.Actions {
		if len(ac.Run) > 0 {
			return true
		}
	}
	for _, pg := range mf.Pages {
		if len(pg.Render) > 0 {
			return true
		}
	}
	if len(mf.Overview) > 0 {
		return true
	}
	return false
}

// ErrUntrusted is returned when a plugin asks keel to run one of its
// executables and the user has not trusted it.
var ErrUntrusted = errors.New("this plugin is not trusted, so keel will not run its executables (keel plugins trust <name>)")

// adapter turns a declared manifest into the interfaces keel's registry expects.
// Only the interfaces a plugin actually declares are satisfied, so `keel plugins`
// reports what it really adds.
type adapter struct {
	mf      manifest
	dir     string
	trusted bool
}

func (a *adapter) Meta() plugin.Meta { return a.mf.Meta }

// exec runs one of the plugin's own executables, streaming its output through
// keel's IO so it is themed like everything else rather than printed raw.
//
// The argv comes from the manifest and is resolved inside the plugin directory:
// a plugin cannot name /bin/sh or reach outside its own files, and keel never
// passes it through a shell, so nothing in the arguments is interpreted.
func (a *adapter) exec(ctx context.Context, io plugin.IO, dir string, argv, args []string) error {
	if !a.trusted {
		return ErrUntrusted
	}
	if len(argv) == 0 {
		return nil
	}
	bin, err := a.resolve(argv[0])
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, bin, append(append([]string{}, argv[1:]...), args...)...)
	cmd.Dir = dir
	// A scrubbed environment (no ambient secrets) and a bounded output: the plugin
	// gets keel's operational env plus its project dir, never the shell's secrets,
	// and cannot flood keel's memory.
	cmd.Env = pluginEnv("KEEL_PROJECT_DIR=" + dir)
	out, err := runCapped(cmd, true)
	for s := bufio.NewScanner(strings.NewReader(string(out))); s.Scan(); {
		if line := strings.TrimRight(s.Text(), "\r"); line != "" {
			io.Note(line)
		}
	}
	return err
}

// resolve refuses any path that leaves the plugin directory. Without this a
// manifest could name ../../../bin/sh and keel would run it.
func (a *adapter) resolve(rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("%q is an absolute path: a plugin may only run its own files", rel)
	}
	full := filepath.Join(a.dir, rel)
	root, err := filepath.Abs(a.dir)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	if abs != root && !strings.HasPrefix(abs, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("%q resolves outside the plugin directory", rel)
	}
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("%q is declared but not present in the plugin", rel)
	}
	// A textual containment check is not enough: a symlink inside the plugin dir
	// could point outside it, and keel would then run a file the plugin does not
	// own — defeating the "a plugin may only run its own files" guarantee. Resolve
	// symlinks on both the target and the root and re-check, so a symlink escape is
	// refused too. Both paths exist here (abs was just Stat'd), so EvalSymlinks is
	// well-defined.
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	realAbs, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	if realAbs != realRoot && !strings.HasPrefix(realAbs, realRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("%q resolves outside the plugin directory", rel)
	}
	return abs, nil
}

func (a *adapter) Commands() []plugin.Command {
	out := make([]plugin.Command, 0, len(a.mf.Commands))
	for _, dc := range a.mf.Commands {
		out = append(out, plugin.Command{
			Name:         dc.Name,
			Summary:      dc.Summary,
			NeedsProject: dc.NeedsProject,
			Run: func(ctx context.Context, io plugin.IO, p plugin.Project, args []string) error {
				if len(dc.Run) == 0 {
					io.Warn(dc.Name + " declares no command to run")
					return nil
				}
				return a.exec(ctx, io, p.Dir, dc.Run, args)
			},
		})
	}
	return out
}

func (a *adapter) Screens() []plugin.Screen {
	out := make([]plugin.Screen, 0, len(a.mf.Screens))
	for _, ds := range a.mf.Screens {
		ds := ds // capture per iteration
		if ds.Component != "" {
			// A component screen: the studio serves this built ES module at
			// /plugin-assets/<name>/<Component> and mounts it as a React component
			// scoped to the project. No subprocess render — the module drives itself
			// through keel.call, threading the project dir.
			out = append(out, plugin.Screen{ID: ds.ID, Title: ds.Title, Icon: ds.Icon, Component: ds.Component})
			continue
		}
		out = append(out, plugin.Screen{
			ID:    ds.ID,
			Title: ds.Title,
			Icon:  ds.Icon,
			Render: func(ctx context.Context, p plugin.Project) (plugin.View, error) {
				if len(ds.Render) > 0 {
					return a.renderScreen(ctx, p, ds.Render)
				}
				v := plugin.View{}
				for _, sec := range ds.Sections {
					items := make([]plugin.Item, 0, len(sec.Items))
					for _, it := range sec.Items {
						items = append(items, plugin.Item{Label: it.Label, Value: it.Value, Href: it.Href})
					}
					v.Sections = append(v.Sections, plugin.Section{Kind: sec.Kind, Title: sec.Title, Items: items})
				}
				return v, nil
			},
		})
	}
	return out
}

// Pages returns the global studio pages this plugin declares — top-level nav
// destinations under "Extend", not per-project tabs. Each is rendered by running
// the plugin's own executable and reading the View it prints, with no project
// because a page is not scoped to one. This is what lets a discovered plugin add
// a whole page to the studio, not only a per-project screen; the base adapter
// always satisfies Pager, so a plugin with no pages simply contributes none.
func (a *adapter) Pages() []plugin.Page {
	out := make([]plugin.Page, 0, len(a.mf.Pages))
	for _, dp := range a.mf.Pages {
		dp := dp // capture per iteration
		if dp.Component != "" {
			// A component page: the studio serves this built ES module at
			// /plugin-assets/<name>/<Component> and mounts it as a React component.
			// No subprocess render — the module drives itself through keel.call.
			out = append(out, plugin.Page{ID: dp.ID, Title: dp.Title, Icon: dp.Icon, Component: dp.Component})
			continue
		}
		if dp.UI {
			// An own-HTML page: keel hosts the plugin's HTML in a sandboxed iframe.
			out = append(out, plugin.Page{
				ID: dp.ID, Title: dp.Title, Icon: dp.Icon, HTML: true,
				RenderHTML: func(ctx context.Context) (string, error) {
					if len(dp.Render) == 0 {
						return "", nil
					}
					return a.execCapture(ctx, plugin.Project{}, dp.Render, nil)
				},
			})
			continue
		}
		out = append(out, plugin.Page{
			ID:    dp.ID,
			Title: dp.Title,
			Icon:  dp.Icon,
			Render: func(ctx context.Context) (plugin.View, error) {
				if len(dp.Render) == 0 {
					return plugin.View{}, nil
				}
				// A page has no project, so it renders with an empty one — the same
				// trust-gated, sandboxed subprocess a live screen uses.
				return a.renderScreen(ctx, plugin.Project{}, dp.Render)
			},
		})
	}
	return out
}

// execCapture runs one of the plugin's executables and returns its raw stdout,
// trust-gated and sandboxed to the plugin directory like every other executable a
// plugin runs. It backs an own-HTML surface (which prints HTML) and a bridged
// call (which prints JSON); extra key=value arguments are appended after the
// declared argv. The project, if any, is passed in the environment.
func (a *adapter) execCapture(ctx context.Context, p plugin.Project, argv, extra []string) (string, error) {
	if !a.trusted {
		return "", ErrUntrusted
	}
	if len(argv) == 0 {
		return "", nil
	}
	bin, err := a.resolve(argv[0])
	if err != nil {
		return "", err
	}
	full := append(append([]string{}, argv[1:]...), extra...)
	cmd := exec.CommandContext(ctx, bin, full...)
	cmd.Dir = a.dir
	cmd.Env = pluginEnv(
		"KEEL_PROJECT_DIR="+p.Dir,
		"KEEL_FRAMEWORK="+p.Framework,
		"KEEL_ENV="+p.Env,
	)
	b, err := runCapped(cmd, false)
	if err != nil {
		return "", fmt.Errorf("%s: %w", argv[0], err)
	}
	return string(b), nil
}

// jsonView is the on-the-wire shape a live screen prints on stdout: the same data
// a static screen declares, so a plugin author can move from static to live
// without keel importing the plugin or the plugin importing keel.
type jsonView struct {
	Sections []struct {
		Kind  string `json:"kind"`
		Title string `json:"title"`
		Items []struct {
			Label string `json:"label"`
			Value string `json:"value"`
			Href  string `json:"href"`
		} `json:"items"`
	} `json:"sections"`
}

// renderScreen runs a screen's own executable and parses the View it prints as
// JSON. Trust-gated and sandboxed to the plugin directory like every other
// executable a plugin declares; the project it renders for is passed in the
// environment so there is no fragile positional-argument contract.
func (a *adapter) renderScreen(ctx context.Context, p plugin.Project, argv []string) (plugin.View, error) {
	if !a.trusted {
		return plugin.View{}, ErrUntrusted
	}
	if len(argv) == 0 {
		return plugin.View{}, nil
	}
	bin, err := a.resolve(argv[0])
	if err != nil {
		return plugin.View{}, err
	}
	cmd := exec.CommandContext(ctx, bin, argv[1:]...)
	cmd.Dir = p.Dir
	cmd.Env = pluginEnv(
		"KEEL_PROJECT_DIR="+p.Dir,
		"KEEL_FRAMEWORK="+p.Framework,
		"KEEL_ENV="+p.Env,
	)
	b, err := runCapped(cmd, false)
	if err != nil {
		return plugin.View{}, fmt.Errorf("%s: %w", argv[0], err)
	}
	var jv jsonView
	if err := json.Unmarshal(b, &jv); err != nil {
		return plugin.View{}, fmt.Errorf("%s: screen did not print valid View JSON: %w", argv[0], err)
	}
	v := plugin.View{}
	for _, s := range jv.Sections {
		items := make([]plugin.Item, 0, len(s.Items))
		for _, it := range s.Items {
			items = append(items, plugin.Item{Label: it.Label, Value: it.Value, Href: it.Href})
		}
		v.Sections = append(v.Sections, plugin.Section{Kind: s.Kind, Title: s.Title, Items: items})
	}
	return v, nil
}

// renderStepOptions runs a step's own executable to compute its options live, for
// a step whose choices depend on the project. Trust-gated and sandboxed like every
// other executable a plugin runs.
func (a *adapter) renderStepOptions(ctx context.Context, p plugin.Project, argv []string) ([]plugin.Option, error) {
	if !a.trusted {
		return nil, ErrUntrusted
	}
	if len(argv) == 0 {
		return nil, nil
	}
	bin, err := a.resolve(argv[0])
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, argv[1:]...)
	cmd.Dir = p.Dir
	cmd.Env = pluginEnv("KEEL_PROJECT_DIR="+p.Dir, "KEEL_FRAMEWORK="+p.Framework, "KEEL_ENV="+p.Env)
	b, err := runCapped(cmd, false)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", argv[0], err)
	}
	var jo struct {
		Options []struct {
			Value       string `json:"value"`
			Label       string `json:"label"`
			Description string `json:"description"`
			Default     bool   `json:"default"`
		} `json:"options"`
	}
	if err := json.Unmarshal(b, &jo); err != nil {
		return nil, fmt.Errorf("%s: step did not print valid options JSON: %w", argv[0], err)
	}
	opts := make([]plugin.Option, 0, len(jo.Options))
	for _, o := range jo.Options {
		opts = append(opts, plugin.Option{Value: o.Value, Label: o.Label, Description: o.Description, Default: o.Default})
	}
	return opts, nil
}

func (a *adapter) Steps() []plugin.Step {
	out := make([]plugin.Step, 0, len(a.mf.Steps))
	for _, dst := range a.mf.Steps {
		out = append(out, plugin.Step{
			ID:    dst.ID,
			Title: dst.Title,
			Help:  dst.Help,
			Multi: dst.Multi,
			Order: dst.Order,
			Options: func(ctx context.Context, p plugin.Project) ([]plugin.Option, error) {
				if len(dst.OptionsRender) > 0 {
					return a.renderStepOptions(ctx, p, dst.OptionsRender)
				}
				opts := make([]plugin.Option, 0, len(dst.Options))
				for _, o := range dst.Options {
					opts = append(opts, plugin.Option{Value: o.Key, Label: o.Label, Description: o.Desc, Default: o.Selected})
				}
				return opts, nil
			},
			Apply: func(ctx context.Context, io plugin.IO, p plugin.Project, values []string) error {
				if len(dst.Apply) == 0 {
					return nil
				}
				return a.exec(ctx, io, p.Dir, dst.Apply, values)
			},
		})
	}
	return out
}

// OptionSteps describes this plugin's wizard steps as schema for a headless
// client (the studio), computed for the project so the choices match what the
// interactive step would offer. It is the declarative adapter's half of
// OptionStepper: a discovered plugin's steps show as a real studio form, not
// only as terminal prompts, so an installed plugin is as first-class in the
// builder as a compiled-in one ever was.
//
// The choices come from the same place Steps().Options reads — the static
// manifest options, or the step's own optionsRender executable — so the two
// front ends can never drift. A live-options step is trust- and sandbox-gated
// exactly as everywhere else, because computing it runs the plugin.
func (a *adapter) OptionSteps(ctx context.Context, p plugin.Project) ([]plugin.OptionSchema, error) {
	out := make([]plugin.OptionSchema, 0, len(a.mf.Steps))
	for _, dst := range a.mf.Steps {
		var opts []plugin.Option
		if len(dst.OptionsRender) > 0 {
			var err error
			if opts, err = a.renderStepOptions(ctx, p, dst.OptionsRender); err != nil {
				return nil, fmt.Errorf("%s: %w", dst.ID, err)
			}
		} else {
			for _, o := range dst.Options {
				opts = append(opts, plugin.Option{Value: o.Key, Label: o.Label, Description: o.Desc, Default: o.Selected})
			}
		}
		typ := "select"
		if dst.Multi {
			typ = "multi"
		}
		choices := make([]plugin.OptionChoice, 0, len(opts))
		for _, o := range opts {
			choices = append(choices, plugin.OptionChoice{Value: o.Value, Label: o.Label, Description: o.Description, Default: o.Default})
		}
		out = append(out, plugin.OptionSchema{ID: dst.ID, Label: dst.Title, Help: dst.Help, Type: typ, Choices: choices})
	}
	return out, nil
}

// actionsList returns the actions this plugin declares, as data the studio draws
// as buttons. Static from the manifest, so listing them never runs the plugin.
func (a *adapter) actionsList(ctx context.Context, p plugin.Project) ([]plugin.Action, error) {
	out := make([]plugin.Action, 0, len(a.mf.Actions))
	for _, da := range a.mf.Actions {
		out = append(out, plugin.Action{
			ID:    da.ID,
			Label: da.Label,
			Help:  da.Help,
			Group: da.Group,
			Needs: plugin.Capability(da.Needs),
		})
	}
	return out, nil
}

// runAction runs one declared action by id, appending the studio's collected
// input values as key=value arguments. Trust- and sandbox-gated by exec.
func (a *adapter) runAction(ctx context.Context, io plugin.IO, p plugin.Project, id string, args map[string]string) error {
	for _, da := range a.mf.Actions {
		if da.ID == id {
			if len(da.Run) == 0 {
				return nil
			}
			extra := make([]string, 0, len(args))
			for k, v := range args {
				extra = append(extra, k+"="+v)
			}
			return a.exec(ctx, io, p.Dir, da.Run, extra)
		}
	}
	return fmt.Errorf("no action %q", id)
}

// overviewSections runs the plugin's overview executable and returns the sections
// it prints as View JSON, so an installed plugin can add live project-overview
// sections to the studio.
func (a *adapter) overviewSections(ctx context.Context, p plugin.Project) ([]plugin.Section, error) {
	if len(a.mf.Overview) == 0 {
		return nil, nil
	}
	v, err := a.renderScreen(ctx, p, a.mf.Overview)
	if err != nil {
		return nil, err
	}
	return v.Sections, nil
}

// The base adapter satisfies Commander, Screener and Stepper (each reported by
// content, so an empty one is not shown). Actioner and Overviewer are added by a
// wrapper only when declared, because keel detects them by presence, not content
// (an overview runs the plugin, so it must not be probed just to list it). Each
// wrapper embeds *adapter exactly once, keeping Meta unambiguous.
type actionAdapter struct{ *adapter }

func (a actionAdapter) Actions(ctx context.Context, p plugin.Project) ([]plugin.Action, error) {
	return a.adapter.actionsList(ctx, p)
}
func (a actionAdapter) RunAction(ctx context.Context, io plugin.IO, p plugin.Project, id string, args map[string]string) error {
	return a.adapter.runAction(ctx, io, p, id, args)
}

type overviewAdapter struct{ *adapter }

func (o overviewAdapter) OverviewSections(ctx context.Context, p plugin.Project) ([]plugin.Section, error) {
	return o.adapter.overviewSections(ctx, p)
}

type overviewActionAdapter struct{ *adapter }

func (x overviewActionAdapter) OverviewSections(ctx context.Context, p plugin.Project) ([]plugin.Section, error) {
	return x.adapter.overviewSections(ctx, p)
}
func (x overviewActionAdapter) Actions(ctx context.Context, p plugin.Project) ([]plugin.Action, error) {
	return x.adapter.actionsList(ctx, p)
}
func (x overviewActionAdapter) RunAction(ctx context.Context, io plugin.IO, p plugin.Project, id string, args map[string]string) error {
	return x.adapter.runAction(ctx, io, p, id, args)
}

// Call runs one of a plugin's actions on behalf of its own UI — the keel.call
// bridge from a sandboxed webview or a mounted component — and returns the
// action's JSON output. The work runs in the plugin's process; keel only proxies
// the call, gated by trust and the action's declared capability. Args arrive as
// key=value pairs the plugin reads. This is the one path a plugin's front-end
// reaches its own back-end through, so keel can enforce trust + capability on
// every call.
//
// projectDir is the project the call runs against, threaded through from a
// project-scoped surface (a component SCREEN) so its action sees the right
// project; it is empty for a global surface (a page), which runs with no project
// exactly as before.
func Call(ctx context.Context, name, action string, args map[string]string, projectDir string) (string, error) {
	it, ok := Get(name)
	if !ok {
		return "", fmt.Errorf("no such plugin: %s", name)
	}
	if !it.Trusted {
		return "", ErrUntrusted
	}
	mf, err := readManifest(it.Dir)
	if err != nil {
		return "", err
	}
	for _, da := range mf.Actions {
		if da.ID != action {
			continue
		}
		if da.Needs != "" {
			if !plugin.KnownCapability(plugin.Capability(da.Needs)) {
				return "", fmt.Errorf("action %q declares unknown capability %q", action, da.Needs)
			}
			if !it.GrantedCaps[plugin.Capability(da.Needs)] {
				return "", fmt.Errorf("action %q needs the %q capability, which is not granted", action, da.Needs)
			}
		}
		a := &adapter{mf: mf, dir: it.Dir, trusted: it.Trusted}
		extra := make([]string, 0, len(args))
		for k, v := range args {
			extra = append(extra, k+"="+v)
		}
		// A page passes no dir and runs with an empty project; a component screen
		// passes its project dir so the action runs against the right project. The
		// dir is already path-validated by the studio before it reaches here.
		var p plugin.Project
		if projectDir != "" {
			p = plugin.Project{Dir: projectDir}
		}
		return a.execCapture(ctx, p, da.Run, extra)
	}
	return "", fmt.Errorf("no action %q", action)
}

// Load returns the enabled, valid installed plugins as keel plugins.
//
// A plugin is skipped rather than half-loaded when its manifest is bad; the
// reason is already visible in `keel plugins`, which lists the directory with
// its problem attached.
func Load() ([]plugin.Plugin, []error) {
	items, err := List()
	if err != nil {
		return nil, []error{err}
	}
	var out []plugin.Plugin
	var problems []error
	for _, it := range items {
		if !it.Enabled {
			continue
		}
		if it.Problem != "" {
			problems = append(problems, fmt.Errorf("%s: %s", it.Name, it.Problem))
			continue
		}
		mf, err := readManifest(it.Dir)
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", it.Name, err))
			continue
		}
		out = append(out, build(&adapter{mf: mf, dir: it.Dir, trusted: it.Trusted}))
	}
	return out, problems
}

// build returns a plugin that satisfies exactly the presence-detected interfaces
// this manifest declares. The base adapter always carries Commander/Screener/
// Stepper (reported by content, so an empty one shows nothing); Actioner and
// Overviewer are added by a wrapper only when declared, since keel detects those
// by presence.
func build(a *adapter) plugin.Plugin {
	hasA, hasO := len(a.mf.Actions) > 0, len(a.mf.Overview) > 0
	switch {
	case hasA && hasO:
		return overviewActionAdapter{a}
	case hasA:
		return actionAdapter{a}
	case hasO:
		return overviewAdapter{a}
	default:
		return a
	}
}
