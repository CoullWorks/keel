package studio

// This file backs the studio's interactive plugin option form: the live,
// checkable rendering of a plugin's OptionStepper schemas (ai-core's skills and
// rules) that a person edits per project and applies on the fly.
//
// /api/plugin-options already describes the steps as data (registry.
// OptionSchemasFor) and marks each choice's recommended default. What it could
// not say is what is CURRENTLY installed, so a form drawn from it alone would
// show the recommendations, not the project's real selection. handlePluginState
// answers exactly that: for each OptionStepper step it reports the values a
// project has applied, so the studio pre-checks the form from installed state
// rather than from defaults.
//
// /api/plugin/apply is the reconciling apply the read-only "AI Rules & Skills"
// tab never had. It reuses runProjectSteps — the same code path a build runs — so
// answering the form is the two-way sync the plugin's own Apply performs: it
// writes the newly chosen and removes the no-longer chosen. An entry present but
// empty means "chose none", which reconciles that step to nothing rather than
// falling back to defaults (chosenFor honours that distinction). It is
// loopback-only, POST-only and session-token guarded like every mutating route.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/coullworks/keel/plugin"
)

// handlePluginState reports, per OptionStepper step, the values a project has
// currently applied, so the studio can pre-check the interactive form from the
// project's real selection instead of the recommended defaults. It is a read
// keyed by ?dir= (a tracked project) and ?framework= (override), guarded as a
// GET; a project that has never been touched simply reports nothing installed
// per step, which the form renders as an unchecked (but recommendable) set.
//
// Installed state is derived generically, without the studio importing any one
// plugin: it renders each applicable plugin's Screener (the same View a plugin
// already exposes) and reads back the values it lists as installed. A plugin
// that offers option steps but no screen contributes nothing here, and the form
// falls back to the schema defaults for it — the honest, plugin-agnostic answer.
func handlePluginState(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, map[string]any{"steps": map[string][]string{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	writeJSON(w, map[string]any{"steps": installedByStep(ctx, p)})
}

// installedByStep maps each OptionStepper step ID to the option values the
// project has applied, so the interactive form pre-checks from installed state
// rather than the recommended defaults.
//
// It derives "installed" from a plugin's own Screener, plugin-agnostically: a
// screen lists what a project does NOT have as an "available, not installed"
// section (ai-core's convention), so an option value is installed when it is a
// real choice the screen does not report as available. That inversion is what
// lets the studio read installed state without importing any one plugin's files
// — the screen a plugin already exposes is the source of truth. A plugin with no
// screen contributes nothing here and its form falls back to schema defaults.
func installedByStep(ctx context.Context, p plugin.Project) map[string][]string {
	out := map[string][]string{}
	reg := registry()
	for _, pl := range reg.Plugins() {
		if !pl.Meta().AppliesToFramework(p.Framework) {
			continue
		}
		stepper, ok := pl.(plugin.OptionStepper)
		if !ok {
			continue
		}
		schemas, err := stepper.OptionSteps(ctx, p)
		if err != nil || len(schemas) == 0 {
			continue
		}
		available := availableValues(ctx, pl, p)
		if available == nil {
			// No screen to read installed state from: leave the step to its defaults
			// rather than guessing every choice is installed.
			continue
		}
		for _, s := range schemas {
			var checked []string
			for _, c := range s.Choices {
				if !available[c.Value] {
					checked = append(checked, c.Value)
				}
			}
			if len(checked) > 0 {
				out[s.ID] = checked
			}
		}
	}
	return out
}

// availableValues renders a plugin's Screener and returns the set of item labels
// its "available, not installed" sections list — i.e. exactly what the project
// does NOT have. installedByStep inverts it against the schema's choices to get
// what IS installed. A plugin with no screen returns nil (distinguished from an
// empty set: nil means "cannot tell", empty means "everything is installed").
func availableValues(ctx context.Context, pl plugin.Plugin, p plugin.Project) map[string]bool {
	sc, ok := pl.(plugin.Screener)
	if !ok {
		return nil
	}
	rendered := false
	out := map[string]bool{}
	for _, screen := range sc.Screens() {
		if screen.Render == nil {
			// A component screen renders in React (mounted from its bundle); it has no
			// data View to read installed state from, so it contributes nothing here.
			// Calling its nil Render would panic — a plugin with a component screen is
			// now discoverable from anywhere under home, so this must be guarded.
			continue
		}
		view, err := screen.Render(ctx, p)
		if err != nil {
			continue
		}
		rendered = true
		for _, sec := range view.Sections {
			if !availableOnly(sec.Title) {
				continue
			}
			for _, it := range sec.Items {
				if it.Label != "" {
					out[it.Label] = true
				}
			}
		}
	}
	if !rendered {
		return nil
	}
	return out
}

// availableOnly reports whether a section lists what is NOT installed (available
// to add). The screen convention is a title that says "available" / "not
// installed"; an "Installed" section is the opposite.
func availableOnly(title string) bool {
	t := strings.ToLower(title)
	return strings.Contains(t, "available") || strings.Contains(t, "not installed")
}

// handlePluginApply reconciles a project's plugin option steps to the values the
// interactive form submitted: add the newly chosen, remove the no-longer chosen.
// It makes the read-only "AI Rules & Skills" tab a live editor. Loopback-only,
// POST-only, token-guarded.
//
// It returns JSON with the captured output lines (the same {ok, output} shape
// /api/plugin/action returns) so the UI shows what the reconcile did without a
// second request. Options is keyed by step ID — the IDs /api/plugin-options
// reports — and a step present with an empty list is "chose none", which removes
// everything that step installed.
func handlePluginApply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Dir     string              `json:"dir"`
		Options map[string][]string `json:"options"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		badRequest(w, "bad request: "+err.Error())
		return
	}
	dir, err := projectDir(body.Dir)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	p := projectFor(dir)
	if p.Framework == "" {
		writeJSON(w, map[string]any{"ok": false, "error": "not a keel project: " + dir})
		return
	}
	// A reconcile writes files but does not shell out or reach the network, so it
	// gets the same short leash a screen render does.
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	cap := &captureActionIO{}
	applyProjectSteps(ctx, &captureWriter{io: cap}, pluginsFor(), p, body.Options)
	writeJSON(w, map[string]any{"ok": true, "output": cap.lines})
}

// applyProjectSteps runs every applicable plugin option step's Apply with the
// values the interactive form chose, reconciling the project to that selection.
//
// It is deliberately NOT runProjectSteps: a build SKIPS a step the user answered
// empty (it does not want to run a no-op step during scaffolding), but the
// interactive form's empty answer means "remove everything this step installed",
// which the plugin's reconciling Apply only performs when it is actually called.
// So this loop calls Apply for a step whose key is present even when the chosen
// list is empty, and falls back to the step's declared defaults only for a step
// the form did not mention at all. Values are validated against the step's real
// options (via chosenFor) so a stale or forged value never reaches Apply.
func applyProjectSteps(ctx context.Context, out io.Writer, reg projectPlugins, p plugin.Project, chosen map[string][]string) {
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
		// Absent key -> the step's defaults; present key (even empty) -> exactly the
		// validated picks, so "chose none" reconciles that step to nothing.
		var values []string
		if picked, ok := chosen[s.ID]; ok {
			values = validPicks(opts, picked)
		} else {
			values = stepDefaults(opts)
		}
		if err := s.Apply(ctx, pio, p, values); err != nil {
			fmt.Fprintf(out, "⚠ %s: %v\n", s.ID, err)
		}
	}
}

// validPicks keeps only the picks that are real options of the step, dropping a
// stale or forged value before it reaches Apply. An empty result is honoured (it
// is "chose none"), unlike chosenFor's build path which then skips the step.
func validPicks(opts []plugin.Option, picked []string) []string {
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

// captureWriter adapts a captureActionIO onto io.Writer so runProjectSteps'
// streamIO (which writes newline-terminated lines) is collected as the same
// discrete output lines /api/plugin/action returns. It splits on newlines and
// drops the trailing empty fragment, so a plugin's line is one entry, not a run
// of blanks.
type captureWriter struct {
	io  *captureActionIO
	buf []byte
}

func (c *captureWriter) Write(p []byte) (int, error) {
	c.buf = append(c.buf, p...)
	for {
		i := bytes.IndexByte(c.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(c.buf[:i]), "\r")
		if line != "" {
			c.io.lines = append(c.io.lines, line)
		}
		c.buf = c.buf[i+1:]
	}
	return len(p), nil
}
