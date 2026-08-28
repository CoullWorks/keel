package console

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coullworks/keel/internal/tui"
)

// Action is what the console asks its caller to do once it exits.
//
// The console chooses; the caller acts. A build is minutes of composer, npm and
// docker output, and that does not belong inside an alt-screen program that
// owns the terminal — it would either be swallowed or fight the renderer. So
// the flow collects the answers, quits, and `keel console` runs the same
// build() that `keel new` runs, with normal streaming output.
type Action struct {
	Kind    string // "" = nothing, "build" = scaffold, "project" = run a task on one
	Recipes []string
	Brand   [][]string // the raw wizard result, carrying the brand-colour choice
	Dir     string
	Task    string   // for Kind "project": doctor, optimize, secrets, db, deploy
	Argv    []string // for Kind "argv": a keel command line to run as typed
}

// panelFlow is an interactive screen running inside the main panel. Areas differ
// in what they ask, not in how they are hosted, so the console knows only this.
type panelFlow interface {
	// update handles a keypress, returning an Action to run and whether the flow
	// is finished with the panel.
	update(tea.KeyMsg) (Action, bool)
	view(w int) string
	// reload re-reads whatever the flow lists. An action can change it — a build
	// adds a project, an install adds a plugin — and the flow is still on screen
	// when the action finishes.
	reload()
}

// stepFlow drives a []tui.Step inside the console's main panel.
//
// The steps come from tui.BuildSteps, the same list `keel new` walks, so the
// console asks exactly the same questions in the same order. Adding a step
// there adds it here, which is the point: the console and the CLI wizard are
// one thing with two renderers, not two implementations that drift.
type stepFlow struct {
	title string
	steps []tui.Step
	res   [][]string // chosen keys, one entry per completed step
	step  int
	cur   int
	marks map[string]bool // multi-select toggles for the step being shown
	dir   string          // project directory, asked for after the last step
	// asking for the directory is a text step that follows the choice steps
	naming bool
	done   bool
}

func newBuildFlow(steps []tui.Step) *stepFlow {
	f := &stepFlow{title: "Build a stack", steps: steps}
	f.reset()
	return f
}

// reset seeds the per-step state, pre-ticking whatever the step defaults to so
// a profile default is one Enter away rather than something to re-pick.
func (f *stepFlow) reset() {
	f.cur, f.marks = 0, map[string]bool{}
	for i, o := range f.options() {
		if o.Selected {
			f.marks[o.Key] = true
			if !f.steps[f.step].Multi && f.cur == 0 {
				f.cur = i
			}
		}
	}
}

// options resolves the current step's choices. A Dynamic step is a function of
// the answers so far, which is how "Local dev environment" only ever offers the
// envs that apply to the framework picked two steps earlier.
func (f *stepFlow) options() []tui.Choice {
	if f.step >= len(f.steps) {
		return nil
	}
	s := f.steps[f.step]
	if s.Dynamic != nil {
		return s.Dynamic(f.res)
	}
	return s.Options
}

func (f *stepFlow) multi() bool {
	return f.step < len(f.steps) && f.steps[f.step].Multi
}

// commit records the current step's answer and moves on.
func (f *stepFlow) commit() {
	opts := f.options()
	var chosen []string
	if f.multi() {
		for _, o := range opts {
			if f.marks[o.Key] {
				chosen = append(chosen, o.Key)
			}
		}
	} else if f.cur < len(opts) {
		chosen = []string{opts[f.cur].Key}
	}
	if f.step < len(f.res) {
		f.res[f.step] = chosen
	} else {
		f.res = append(f.res, chosen)
	}
	f.step++
	if f.step >= len(f.steps) {
		f.naming = true
		return
	}
	f.reset()
}

func (f *stepFlow) back() {
	if f.naming {
		f.naming = false
		f.step = len(f.steps) - 1
		f.res = f.res[:f.step]
		f.reset()
		return
	}
	if f.step == 0 {
		return
	}
	f.step--
	f.res = f.res[:f.step]
	f.reset()
}

// update handles a keypress. It returns the flow's Action once the user has
// answered everything, and whether the flow wants to close.
func (f *stepFlow) update(msg tea.KeyMsg) (Action, bool) {
	if f.naming {
		switch msg.String() {
		case "enter":
			f.done = true
			return Action{Kind: "build", Recipes: tui.IDsFromResult(f.res), Brand: f.res, Dir: strings.TrimSpace(f.dir)}, true
		case "esc":
			f.back()
		case "backspace":
			if f.dir != "" {
				f.dir = f.dir[:len(f.dir)-1]
			}
		default:
			if s := msg.String(); len(s) == 1 {
				f.dir += s
			}
		}
		return Action{}, false
	}

	opts := f.options()
	switch msg.String() {
	case "up", "k":
		if f.cur > 0 {
			f.cur--
		}
	case "down", "j":
		if f.cur < len(opts)-1 {
			f.cur++
		}
	case " ":
		if f.multi() && f.cur < len(opts) {
			k := opts[f.cur].Key
			f.marks[k] = !f.marks[k]
		}
	case "enter":
		f.commit()
	case "esc", "left", "h":
		// esc on the first step leaves the flow entirely, which is the only way
		// back to the sidebar without answering anything.
		if f.step == 0 {
			return Action{}, true
		}
		f.back()
	}
	return Action{}, false
}

// view renders the flow: a breadcrumb of the steps, the current question, and
// its options. It deliberately looks like the studio's build screen, because it
// is the same screen.
func (f *stepFlow) view(w int) string {
	var b strings.Builder
	b.WriteString(mainTitle.Render(f.title) + "\n")

	// Where you are in the sequence, as a counter and a bar.
	//
	// It used to name every step, joined with separators. A Laravel build has
	// seventeen of them, which came to roughly two hundred columns of titles on
	// one line: it overflowed the panel at every terminal width there is, and
	// the current step's title is already the heading directly underneath.
	total := len(f.steps) + 1 // the questions, plus naming the directory
	at := f.step + 1
	if f.naming {
		at = total
	}
	bar := strings.Repeat("▪", at) + strings.Repeat("▫", total-at)
	b.WriteString(styDim.Render(fmt.Sprintf("step %d of %d  ", at, total)) +
		styPill.Render(trimTo(bar, max(0, w-16))) + "\n\n")

	if f.naming {
		b.WriteString(styHead.Render("Project directory") + "\n")
		b.WriteString(note(w, "Where to create it. Blank uses the framework name.") + "\n")
		b.WriteString("  " + styHead.Render(trimTo(f.dir, w-4)) + styDim.Render("▌") + "\n\n")
		b.WriteString(styDim.Render("enter build · esc back"))
		return b.String()
	}

	s := f.steps[f.step]
	b.WriteString(styHead.Width(w).Render(s.Title) + "\n")
	if s.Help != "" {
		b.WriteString(note(w, s.Help))
	}
	b.WriteString("\n")

	opts := f.options()
	if len(opts) == 0 {
		b.WriteString(note(w, "Nothing to choose here for this stack. Press enter to continue."))
	}
	// A long list (services, add-ons) is windowed around the cursor so the panel
	// never scrolls off the bottom of the frame.
	start, show := 0, 12
	if f.cur >= show {
		start = f.cur - show + 1
	}
	for i := start; i < len(opts) && i < start+show; i++ {
		o := opts[i]
		mark := " "
		if f.multi() {
			mark = "○"
			if f.marks[o.Key] {
				mark = styPill.Render("●")
			}
		} else if i == f.cur {
			mark = styPill.Render("▸")
		}
		lbl := trimTo(o.Label, w-8)
		line := "  " + mark + " " + lbl
		if i == f.cur {
			line = navSel.Render(" " + mark + " " + lbl + " ")
		}
		b.WriteString(line + "\n")
	}
	if len(opts) > start+show {
		b.WriteString(styDim.Render(fmt.Sprintf("  … %d more", len(opts)-(start+show))) + "\n")
	}

	b.WriteString("\n")
	if f.multi() {
		b.WriteString(styDim.Render("space toggle · enter next · esc back"))
	} else {
		b.WriteString(styDim.Render("↑↓ move · enter next · esc back"))
	}
	return b.String()
}

// reload is a no-op: a build flow's questions come from the recipe registry that
// was read when the console opened, and re-asking them mid-flow would move the
// ground under a half-answered wizard.
func (f *stepFlow) reload() {}
