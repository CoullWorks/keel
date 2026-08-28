package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ErrCancelled is returned when the user quits a wizard.
var ErrCancelled = errors.New("cancelled")

// Choice is a selectable option in a wizard step.
type Choice struct {
	Key, Label, Desc string
	Selected         bool // pre-checked (multi) or the default (single)
}

// Step is one screen of the wizard.
type Step struct {
	Title, Help string
	Multi       bool
	Options     []Choice
	// Dynamic, if set, rebuilds Options when the step is entered, from the
	// selections made so far (so e.g. the env list depends on the framework).
	Dynamic func(prior [][]string) []Choice
}

// Fixed header height, so mouse Y maps to option rows exactly:
//
//	0 blank · 1 title · 2 intro · 3 progress · 4 blank · 5 step title · 6 help · 7 blank · 8… options
const optionsTop = 8

type wizardModel struct {
	title, intro  string
	steps         []Step
	step, cursor  int
	sel           [][]string
	width, height int
	done, cancel  bool
}

// cloneSteps returns a deep copy of steps whose per-step Options slices are the
// model's own. The model toggles Selected in place; without this the wizard would
// mutate the caller's Choice values through the shared backing array. Dynamic is a
// func value (shared by reference) — resolveOptions replaces Options wholesale
// when it runs, so a rebuilt step gets a fresh slice anyway.
func cloneSteps(steps []Step) []Step {
	out := make([]Step, len(steps))
	for i, s := range steps {
		s.Options = append([]Choice(nil), s.Options...)
		out[i] = s
	}
	return out
}

func firstSelected(opts []Choice) int {
	for i, o := range opts {
		if o.Selected {
			return i
		}
	}
	return 0
}

func (m *wizardModel) resolveOptions(step int) {
	if m.steps[step].Dynamic != nil {
		m.steps[step].Options = m.steps[step].Dynamic(m.sel)
	}
}

// autoSelect records the forced value for a single-select step that is being
// skipped (its sole option, or nothing when it has none).
func (m *wizardModel) autoSelect(step int) {
	if len(m.steps[step].Options) == 1 {
		m.sel[step] = []string{m.steps[step].Options[0].Key}
	} else {
		m.sel[step] = nil
	}
}

// skippable: a single-select step that resolves to 0 or 1 option has nothing to
// choose, so the wizard auto-selects and skips it (e.g. a framework with only
// one setup variant, or an env with a single option).
func (m *wizardModel) skippable(step int) bool {
	return !m.steps[step].Multi && len(m.steps[step].Options) <= 1
}

func (m *wizardModel) enter(step int) {
	m.resolveOptions(step)
	m.step = step
	m.cursor = firstSelected(m.steps[step].Options)
}

func (m wizardModel) cur() *Step { return &m.steps[m.step] }

func (m wizardModel) Init() tea.Cmd { return nil }

// toggle flips the Selected flag of the i-th option on the current step. It is a
// pointer receiver because it mutates state the model owns: the value-receiver
// Update copies the model, but its steps slice shares a backing array, so a
// value-receiver toggle would reach back into that shared array by accident.
// Wizard deep-copies each step's Options up front so this mutation is confined to
// the model and never touches the caller's slice.
func (m *wizardModel) toggle(i int) {
	m.steps[m.step].Options[i].Selected = !m.steps[m.step].Options[i].Selected
}

func (m wizardModel) advance() (tea.Model, tea.Cmd) {
	st := m.cur()
	if st.Multi {
		var keys []string
		for _, o := range st.Options {
			if o.Selected {
				keys = append(keys, o.Key)
			}
		}
		m.sel[m.step] = keys
	} else if len(st.Options) > 0 {
		m.sel[m.step] = []string{st.Options[m.cursor].Key}
	}
	// Advance to the next step that actually needs input, auto-selecting and
	// skipping single-select steps that resolve to 0 or 1 option.
	next := m.step + 1
	for next < len(m.steps) {
		m.resolveOptions(next)
		if !m.skippable(next) {
			break
		}
		m.autoSelect(next)
		next++
	}
	if next >= len(m.steps) {
		m.done = true
		return m, tea.Quit
	}
	m.step = next
	m.cursor = firstSelected(m.steps[next].Options)
	return m, nil
}

func (m wizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.cur().Options)-1 {
				m.cursor++
			}
		case " ":
			if m.cur().Multi && len(m.cur().Options) > 0 {
				m.toggle(m.cursor)
			}
		case "enter", "right":
			return m.advance()
		case "backspace", "left":
			// Step back over any auto-skipped single-option steps.
			prev := m.step - 1
			for prev > 0 {
				m.resolveOptions(prev)
				if !m.skippable(prev) {
					break
				}
				prev--
			}
			if prev >= 0 {
				m.enter(prev)
			}
		case "q", "esc", "ctrl+c":
			m.cancel = true
			return m, tea.Quit
		default:
			if n, err := strconv.Atoi(msg.String()); err == nil && n >= 1 && n <= len(m.cur().Options) {
				m.cursor = n - 1
				if m.cur().Multi {
					m.toggle(m.cursor)
				}
			}
		}
	case tea.MouseMsg:
		opts := m.cur().Options
		off, vis := m.offset(), m.visN()
		rel := msg.Y - optionsTop // row within the visible window
		// Wheel scrolls the cursor (the window follows it).
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case tea.MouseButtonWheelDown:
			if m.cursor < len(opts)-1 {
				m.cursor++
			}
			return m, nil
		}
		// Hover: highlight the option under the pointer.
		if msg.Action == tea.MouseActionMotion && rel >= 0 && rel < vis {
			m.cursor = off + rel
		}
		// Click: select (single) or toggle (multi); Continue row advances.
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			switch {
			case rel >= 0 && rel < vis:
				m.cursor = off + rel
				if m.cur().Multi {
					m.toggle(m.cursor)
				}
			case rel == vis+1: // the Continue / Finish row (after one blank line)
				return m.advance()
			}
		}
	}
	return m, nil
}

func (m wizardModel) width0() int {
	if m.width > 0 {
		return m.width
	}
	return 80
}

func (m wizardModel) height0() int {
	if m.height > 0 {
		return m.height
	}
	return 24
}

// maxVisible is how many option rows fit under the fixed header, leaving room
// for the blank + Continue row + blank + hint footer.
func (m wizardModel) maxVisible() int {
	v := m.height0() - optionsTop - 4
	if v < 3 {
		v = 3
	}
	return v
}

// visN is the number of option rows actually shown (window height).
func (m wizardModel) visN() int {
	if n := len(m.cur().Options); n < m.maxVisible() {
		return n
	}
	return m.maxVisible()
}

// offset is the first visible option index, derived from the cursor so the
// selection stays centred and in view. Deterministic, so View() and the mouse
// hit-test compute the same window.
func (m wizardModel) offset() int {
	opts := m.cur().Options
	mv := m.maxVisible()
	if len(opts) <= mv {
		return 0
	}
	off := m.cursor - mv/2
	if off < 0 {
		off = 0
	}
	if last := len(opts) - mv; off > last {
		off = last
	}
	return off
}

func (m wizardModel) View() string {
	st := m.steps[m.step]
	clipStyle := clipTo(m.width0() - 1)
	clip := func(s string) string { return clipStyle.Render(s) }

	var b strings.Builder
	b.WriteString("\n")                                      // 0
	b.WriteString(" " + styTitle.Render(m.title) + "\n")     // 1
	b.WriteString(" " + styDim.Render(clip(m.intro)) + "\n") // 2
	dots := ""                                               // 3
	for i := range m.steps {
		switch {
		case i < m.step:
			dots += styOK.Render("●") + " "
		case i == m.step:
			dots += styAccent.Render("●") + " "
		default:
			dots += styDim.Render("○") + " "
		}
	}
	b.WriteString(" " + styDim.Render(fmt.Sprintf("Step %d of %d   ", m.step+1, len(m.steps))) + dots + "\n")
	b.WriteString("\n")                                  // 4
	b.WriteString(" " + styHead.Render(st.Title) + "\n") // 5
	off, vis := m.offset(), m.visN()
	help := st.Help
	if len(st.Options) > vis { // scroll indicator when the list is windowed
		help += fmt.Sprintf("   (%d-%d of %d ↑↓)", off+1, off+vis, len(st.Options))
	}
	b.WriteString(" " + styDim.Render(help) + "\n") // 6
	b.WriteString("\n")                             // 7
	for r := 0; r < vis; r++ {                      // 8 ... (only the visible window)
		i := off + r
		o := st.Options[i]
		var marker string
		switch {
		case st.Multi && o.Selected:
			marker = styOK.Render("◉ ")
		case st.Multi:
			marker = styDim.Render("○ ")
		case i == m.cursor:
			marker = styAccent.Render("▸ ")
		default:
			marker = "  "
		}
		label := o.Label
		if i == m.cursor {
			label = styAccent.Render(o.Label)
		} else {
			label = styHead.Render(o.Label)
		}
		row := " " + marker + label
		if o.Desc != "" {
			row += "  " + styDim.Render(o.Desc)
		}
		b.WriteString(clip(row) + "\n") // MaxWidth truncates, never wraps -> one row each
	}
	b.WriteString("\n")
	cont := "Continue →"
	if m.step == len(m.steps)-1 {
		cont = "Finish →"
	}
	b.WriteString(" " + styAccent.Render(cont) + "\n\n")
	b.WriteString(" " + styDim.Render("↑↓ or hover · space toggle · enter/click Continue · ← back · q quit"))
	return b.String()
}

// Wizard runs a stepped, mouse-and-keyboard selection wizard. intro is a short
// one-line explanation shown under the title on every step.
// runProgram runs a bubbletea program to completion. It's a test seam so the
// wizard's result/cancel/error handling (and the callers that map its output)
// can be covered without a real terminal.
var runProgram = func(m tea.Model) (tea.Model, error) {
	return tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseAllMotion()).Run()
}

func Wizard(title, intro string, steps []Step) ([][]string, error) {
	m := wizardModel{title: title, intro: intro, steps: cloneSteps(steps), sel: make([][]string, len(steps))}
	m.enter(0)
	res, err := runProgram(m)
	if err != nil {
		return nil, err
	}
	// Bubbletea returns the final model, which is always the wizardModel we passed
	// in; the comma-ok guards a swapped test seam (or a future model change) so a
	// wrong type is a clear error, never a panic in library code.
	fm, ok := res.(wizardModel)
	if !ok {
		return nil, fmt.Errorf("wizard returned an unexpected model type %T", res)
	}
	if fm.cancel || !fm.done {
		return nil, ErrCancelled
	}
	return fm.sel, nil
}
