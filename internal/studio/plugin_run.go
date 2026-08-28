// This file runs a project's plugins from the studio, headless.
//
// The CLI runs a plugin's wizard steps as interactive huh prompts and then fires
// the lifecycle events a plugin listens to. The studio's build endpoint used to
// do neither: it resolved a plan and ran engine.Build and stopped, so a plugin
// like ai-core (a Stepper) never ran and sonar's listener never fired from a
// studio build. That made the studio a thin veneer that silently dropped half of
// what `keel new` does.
//
// A studio build is headless HTTP: there is no terminal to draw huh prompts in.
// The Build flow now fetches each step's options as a schema (/api/plugin-options
// → registry.OptionSchemasFor) and draws real form controls for them, then posts
// the user's selections back with the build. runProjectSteps applies those chosen
// values per step; where the user picked nothing for a step (or a step exposed no
// schema) it falls back to the step's declared DEFAULTS — the same answers
// `keel new --yes` takes — so a build always installs something sensible.
package studio

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/plugin"
)

// projectFor builds the plugin.Project for a directory, reading its manifest for
// the framework and env where it has one. It never fails: an unmanaged directory
// still has a name and path, which is all a project.deleted listener needs.
func projectFor(dir string) plugin.Project {
	p := plugin.Project{Dir: dir, Name: filepath.Base(dir)}
	if m, err := engine.ReadManifest(dir); err == nil {
		p.Framework, p.Env = m.Framework, m.Env
	}
	return p
}

// projectPlugins is the slice of the plugin registry the studio's lifecycle needs:
// the steps that apply to a framework, and the ability to fire an event at every
// subscriber. It is an interface so a test can substitute a recording stub and
// prove a studio build actually runs the steps and emits the events, without a
// real plugin on disk.
type projectPlugins interface {
	StepsFor(fw string) []plugin.Step
	Emit(ctx context.Context, e plugin.Event, io plugin.IO, p plugin.Project) []error
}

// pluginsFor returns the plugin registry the studio acts through. It is a package
// var so tests can swap in a stub; the real body is a one-line adapter over the
// cached registry, which is the only line here left uncovered.
var pluginsFor = func() projectPlugins { return registry() }

// runProjectSteps runs every plugin step that applies to the project, applying
// the user's chosen values for a step where the Build form collected them and
// otherwise the step's declared defaults (the headless equivalent of
// `keel new --yes`). A step that fails to offer its options or to apply is
// reported and skipped: a plugin's trouble after a successful build is not a
// reason to fail the build. It writes through the plugin IO so a step's own
// output lands in the build stream.
func runProjectSteps(ctx context.Context, out io.Writer, reg projectPlugins, p plugin.Project, chosen map[string][]string) {
	steps := reg.StepsFor(p.Framework)
	if len(steps) == 0 {
		return
	}
	pio := newStreamIO(out)
	for _, s := range steps {
		opts, err := s.Options(ctx, p)
		if err != nil {
			fmt.Fprintf(out, "⚠ %s: %v\n", s.ID, err)
			continue
		}
		if len(opts) == 0 {
			continue
		}
		values := chosenFor(s, opts, chosen)
		if len(values) == 0 {
			continue
		}
		if err := s.Apply(ctx, pio, p, values); err != nil {
			fmt.Fprintf(out, "⚠ %s: %v\n", s.ID, err)
		}
	}
}

// chosenFor is the values a step is answered with: the user's picks from the
// Build form when they chose for this step, validated against the step's real
// options so a stale or forged value can never reach Apply, and the step's
// declared defaults otherwise. An entry present but empty means the user
// deliberately chose nothing (e.g. a multi-select cleared), which is honoured
// rather than silently replaced with defaults — the difference between "did not
// answer" and "answered none".
func chosenFor(s plugin.Step, opts []plugin.Option, chosen map[string][]string) []string {
	picked, ok := chosen[s.ID]
	if !ok {
		return stepDefaults(opts)
	}
	valid := make(map[string]bool, len(opts))
	for _, o := range opts {
		valid[o.Value] = true
	}
	out := make([]string, 0, len(picked))
	for _, v := range picked {
		if valid[v] {
			out = append(out, v)
		}
	}
	return out
}

// stepDefaults is the set of options marked default, i.e. what a step is answered
// with when nobody is asked. It mirrors the CLI's defaults() so the studio's
// headless build and `keel new --yes` install the same thing.
func stepDefaults(opts []plugin.Option) []string {
	var out []string
	for _, o := range opts {
		if o.Default {
			out = append(out, o.Value)
		}
	}
	return out
}

// emitProjectEvent fires one lifecycle event at every subscriber and writes any
// listener trouble into the stream as a warning. A listener's opinion never fails
// the caller: an event fired after a build is over cannot undo it.
func emitProjectEvent(ctx context.Context, out io.Writer, reg projectPlugins, e plugin.Event, p plugin.Project) {
	for _, err := range reg.Emit(ctx, e, newStreamIO(out), p) {
		fmt.Fprintf(out, "⚠ %v\n", err)
	}
}

// streamIO adapts plugin.IO onto a plain writer, so a plugin's styled output
// becomes ordinary lines in the studio's build/console stream. The studio draws
// its own theme in the browser, so here a plugin line is just text with a
// leading glyph the browser colours (✓ ok, ✗ bad), matching sseWriter's own
// convention.
type streamIO struct{ w io.Writer }

func newStreamIO(w io.Writer) plugin.IO { return &streamIO{w: w} }

func (s *streamIO) Title(text string)          { fmt.Fprintln(s.w, text) }
func (s *streamIO) Detail(label, value string) { fmt.Fprintf(s.w, "%s: %s\n", label, value) }
func (s *streamIO) Note(text string)           { fmt.Fprintln(s.w, text) }
func (s *streamIO) OK(text string)             { fmt.Fprintln(s.w, "✓ "+text) }
func (s *streamIO) Warn(text string)           { fmt.Fprintln(s.w, "⚠ "+text) }
func (s *streamIO) Bad(text string)            { fmt.Fprintln(s.w, "✗ "+text) }

func (s *streamIO) List(title string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintln(s.w, title+":")
	for _, it := range items {
		fmt.Fprintln(s.w, "  - "+it)
	}
}
