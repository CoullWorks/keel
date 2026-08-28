// Package plugintest builds discovered-plugin fixtures for tests.
//
// keel ships zero built-in plugins: every plugin is its own repository,
// discovered wherever it is cloned. So a test that needs a plugin to exist —
// to exercise discovery, the declared-adapter subprocess path, screens,
// actions, overview, wizard steps or plugin-contributed recipes — writes one to
// disk here and points keel at it with KEEL_PLUGIN_PATH. This is the real path a
// shipped plugin takes, not a compiled-in shortcut, so the tests prove the thing
// users actually run.
//
// The fixture is written in plain shell, like the reference plugin, to make the
// same point the reference does: keel talks to a plugin over a subprocess + JSON
// protocol and never loads its code. It declares every extension point (a
// command, a static and a live screen, a multi-select wizard step, an action, an
// overview and a recipe) so one fixture serves every kind of test; a test simply
// asserts on the parts it cares about.
package plugintest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/coullworks/keel/internal/pluginstore"
	"github.com/coullworks/keel/plugin"
)

// Fixture is a plugin written to disk and discoverable by keel for the duration
// of a test.
type Fixture struct {
	Name string // the plugin name (its command, and the key for enable/trust)
	Dir  string // the plugin directory, containing config/register.yaml
	Root string // the search root placed on KEEL_PLUGIN_PATH
	t    *testing.T
}

// The ids and titles the fixture declares, exported so a test asserts against a
// constant rather than a magic string that could drift from the register file.
const (
	CommandName    = "demo"    // when installed under the default name
	StaticScreenID = "-static" // suffix; full id is <name> + this
	LiveScreenID   = "-live"   // suffix
	StepID         = "-step"   // suffix
	ActionID       = "-action" // suffix
	PageID         = "-page"   // suffix
	StaticTitle    = "Demo (static)"
	LiveTitle      = "Demo (live)"
	StepTitle      = "Demo extras"
	PageTitle      = "Demo Page"
)

// Install writes the fixture plugin under an isolated search root and points keel
// at it, returning the Fixture. The caller must have isolated KEEL_CONFIG_DIR
// first (so trust/enable state lands in a temp dir); Install sets
// KEEL_PLUGIN_PATH to the fixture's root.
//
// The plugin is discovered and enabled by default but untrusted: its data
// (identity, static screen, command/step/action declarations) is available
// immediately, while its executables — the live screen, the action, the
// overview, live step options — do not run until the test calls Trust. That is
// exactly the boundary a real user crosses with `keel plugins trust`.
func Install(t *testing.T, name string) *Fixture {
	t.Helper()
	if name == "" {
		name = CommandName
	}
	// Isolate HOME to an empty temp dir. Discovery now walks the user's home
	// directory by default (pluginstore.defaultRoots), so without this the walk
	// would traverse the developer's real home — slow, and liable to pick up their
	// real plugin repos. The fixture is discovered via KEEL_PLUGIN_PATH below, not
	// via home.
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	// The directory is named keel-plugin-<name> while the manifest name is <name>,
	// mirroring how a real plugin repo is cloned. This is deliberate: keel keys a
	// plugin's trust/enable/grant state by its manifest name, and a fixture that
	// named the directory <name> would never exercise the mismatch every real repo
	// has (see pluginstore.TestStateSticksWhenDirNameDiffersFromManifestName).
	dir := filepath.Join(root, "keel-plugin-"+name)
	writeFile(t, filepath.Join(dir, "config", "register.yaml"), register(name))
	writeExec(t, filepath.Join(dir, "bin", "hello"), scriptHello)
	writeExec(t, filepath.Join(dir, "bin", "screen"), scriptScreen)
	writeExec(t, filepath.Join(dir, "bin", "overview"), scriptOverview)
	writeExec(t, filepath.Join(dir, "bin", "action"), scriptAction)
	writeExec(t, filepath.Join(dir, "bin", "apply"), scriptApply)
	writeExec(t, filepath.Join(dir, "bin", "options"), scriptOptions)
	writeExec(t, filepath.Join(dir, "bin", "page"), scriptPage)
	writeFile(t, filepath.Join(dir, "recipes", name+"-stack.yaml"), recipeYAML(name))

	t.Setenv("KEEL_PLUGIN_PATH", root)
	// List memoizes its result for a short TTL, so a result cached under a
	// previous test's search roots would otherwise leak into this one and hide the
	// fixture just written. Clear it now that KEEL_PLUGIN_PATH points here.
	pluginstore.Invalidate()
	f := &Fixture{Name: name, Dir: dir, Root: root, t: t}
	// The plugin must be discoverable now, or every later assertion is testing
	// nothing.
	if _, ok := pluginstore.Get(name); !ok {
		t.Fatalf("plugintest: fixture %q was not discovered under %s", name, root)
	}
	return f
}

// Trust marks the plugin trusted and grants exec + net, so its executables run.
// A test that renders the live screen or overview, runs the action, or reads
// framework-dependent step options must call this first — keel is offline by
// default and will not run a plugin's code otherwise.
func (f *Fixture) Trust() {
	f.t.Helper()
	if err := pluginstore.SetTrusted(f.Name, true); err != nil {
		f.t.Fatalf("plugintest: trust %q: %v", f.Name, err)
	}
	for _, c := range []plugin.Capability{plugin.CapExec, plugin.CapNet} {
		if err := pluginstore.SetCapabilityGranted(f.Name, c, true); err != nil {
			f.t.Fatalf("plugintest: grant %s to %q: %v", c, f.Name, err)
		}
	}
}

// Enable turns the plugin on or off, the way a user toggles it in the listing.
func (f *Fixture) Enable(on bool) {
	f.t.Helper()
	if err := pluginstore.SetEnabled(f.Name, on); err != nil {
		f.t.Fatalf("plugintest: set enabled %v on %q: %v", on, f.Name, err)
	}
}

// Screen ids and step/action ids for the installed name, so tests need not
// concatenate strings themselves.
func (f *Fixture) StaticScreen() string { return f.Name + StaticScreenID }
func (f *Fixture) LiveScreen() string   { return f.Name + LiveScreenID }
func (f *Fixture) Step() string         { return f.Name + StepID }
func (f *Fixture) Action() string       { return f.Name + ActionID }
func (f *Fixture) Page() string         { return f.Name + PageID }

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeExec(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

// register is the fixture's config/register.yaml, declaring every extension
// point. It is a template only in the plugin name; everything else is fixed so a
// test asserts against the constants above.
func register(name string) string {
	return `schema: 1
name: ` + name + `
version: 1.0.0
description: A discovered test plugin covering every extension point.
author: CoullWorks
license: MIT
homepage: https://example.test/` + name + `

capabilities:
  - exec
  - net

commands:
  - name: ` + name + `
    summary: Report what the test plugin sees about this project
    run: ["bin/hello"]

screens:
  - id: ` + name + `-static
    title: ` + StaticTitle + `
    sections:
      - kind: text
        title: About
        items:
          - label: What this is
            value: A studio screen declared entirely in register.yaml.
  - id: ` + name + `-live
    title: ` + LiveTitle + `
    render: ["bin/screen"]

steps:
  - id: ` + name + `-step
    title: ` + StepTitle + `
    help: A wizard step contributed by the test plugin.
    multi: true
    optionsRender: ["bin/options"]
    apply: ["bin/apply"]

actions:
  - id: ` + name + `-action
    label: Run the test action
    help: Runs the plugin's own executable against the project.
    group: Plugins
    run: ["bin/action"]

pages:
  - id: ` + name + `-page
    title: ` + PageTitle + `
    icon: "◈"
    render: ["bin/page"]

overview: ["bin/overview"]
`
}

const scriptHello = `#!/bin/sh
echo "test plugin — project: ${KEEL_PROJECT_DIR:-.}"
echo "framework: ${KEEL_FRAMEWORK:-unknown}, env: ${KEEL_ENV:-unknown}"
`

// scriptScreen is the live screen. Besides a stat section, it lists the step
// options NOT yet installed in an "Available (not installed)" section, so keel can
// derive installed state generically: a value the screen does not list as
// available is installed. bin/apply records the chosen values in .demo-state (the
// working directory is the project dir for both scripts), so this reflects real
// applied state, not a fixed answer.
const scriptScreen = `#!/bin/sh
state=".demo-state"
avail=""
for v in greeting analytics docs; do
  if ! grep -qx "$v" "$state" 2>/dev/null; then
    avail="$avail{\"label\":\"$v\",\"value\":\"$v\"},"
  fi
done
avail="${avail%,}"
cat <<JSON
{"sections":[
  {"kind":"stat","title":"This project","items":[
    {"label":"Framework","value":"${KEEL_FRAMEWORK:-unknown}"},
    {"label":"Environment","value":"${KEEL_ENV:-unknown}"}
  ]},
  {"kind":"list","title":"Available (not installed)","items":[$avail]}
]}
JSON
`

const scriptOverview = `#!/bin/sh
cat <<JSON
{"sections":[{"kind":"stat","title":"Test plugin","items":[{"label":"status","value":"active"}]}]}
JSON
`

// scriptPage renders the plugin's global page (a top-level "Extend" destination).
// A page has no project, so it prints a fixed View.
const scriptPage = `#!/bin/sh
cat <<JSON
{"sections":[{"kind":"text","title":"Demo page","items":[{"label":"what","value":"a global plugin page under Extend"}]}]}
JSON
`

const scriptAction = `#!/bin/sh
echo "action ran for ${KEEL_FRAMEWORK:-unknown}"
`

// scriptApply reconciles: it records exactly the chosen values (overwrite, so an
// empty apply removes everything the step installed). The working directory is the
// project dir, and scriptScreen reads .demo-state back, so an apply is reflected
// in the plugin's installed state — the two-way sync the studio form relies on.
const scriptApply = `#!/bin/sh
state=".demo-state"
: > "$state"
for v in "$@"; do
  echo "$v" >> "$state"
done
echo "applied: $*"
`

// scriptOptions computes step options live: more choices when a framework is
// known, so a headless form is framework-aware the way the interactive step is.
const scriptOptions = `#!/bin/sh
if [ -n "$KEEL_FRAMEWORK" ]; then
cat <<JSON
{"options":[
  {"value":"greeting","label":"Add a greeting","default":true},
  {"value":"analytics","label":"Add analytics"},
  {"value":"docs","label":"Add docs"}
]}
JSON
else
cat <<JSON
{"options":[{"value":"greeting","label":"Add a greeting"}]}
JSON
fi
`

// recipeYAML is a recipe the plugin ships in its recipes/ directory, which keel
// folds into the catalog when the plugin is enabled — the "a pack is a plugin
// that only ships recipes" mechanism, proven end to end.
func recipeYAML(name string) string {
	return `id: ` + name + `-stack
kind: extra
label: A recipe shipped by the test plugin
provides: [` + name + `-stack]
files:
  - path: ` + name + `_PLUGIN.md
    content: |
      # Added by the test plugin
      This file was written by the ` + name + `-stack recipe.
`
}
