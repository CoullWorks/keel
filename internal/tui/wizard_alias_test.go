package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The wizard toggles Choice.Selected in place as the user checks options. It must
// do so on its OWN copy of the caller's steps, never through the shared backing
// array — otherwise running a wizard would silently mutate the slice the caller
// still holds. Wizard deep-copies each step's Options for exactly this reason;
// this test drives a real toggle and asserts the caller's Choice is untouched.
func TestWizardDoesNotAliasCallerSteps(t *testing.T) {
	orig := runProgram
	defer func() { runProgram = orig }()

	// runProgram simulates the user pressing space (toggle option 0) then enter
	// (finish) on a single multi-select step.
	runProgram = func(m tea.Model) (tea.Model, error) {
		cur, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
		cur, _ = cur.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return cur, nil
	}

	steps := []Step{{
		Title: "pick",
		Multi: true,
		Options: []Choice{
			{Key: "a", Label: "A", Selected: false},
			{Key: "b", Label: "B", Selected: false},
		},
	}}

	sel, err := Wizard("t", "i", steps)
	if err != nil {
		t.Fatalf("Wizard: %v", err)
	}

	// The wizard reports "a" selected...
	if len(sel) != 1 || len(sel[0]) != 1 || sel[0][0] != "a" {
		t.Fatalf("selection = %v, want [[a]]", sel)
	}
	// ...but the caller's own Choice must be unchanged: the toggle happened on the
	// wizard's copy, not the slice we passed in.
	if steps[0].Options[0].Selected {
		t.Error("Wizard mutated the caller's steps: Options[0].Selected flipped to true")
	}
}

// toggle is a pointer receiver, so a value-receiver Update whose model it mutates
// must still see the change (the model owns its Options after cloneSteps). This
// pins the toggle actually flips the model's own state.
func TestToggleFlipsModelState(t *testing.T) {
	m := wizardModel{
		steps: cloneSteps([]Step{{
			Multi:   true,
			Options: []Choice{{Key: "a"}, {Key: "b"}},
		}}),
	}
	m.toggle(0)
	if !m.steps[0].Options[0].Selected {
		t.Error("toggle(0) did not set Options[0].Selected")
	}
	m.toggle(0)
	if m.steps[0].Options[0].Selected {
		t.Error("toggle(0) twice did not clear Options[0].Selected")
	}
}
