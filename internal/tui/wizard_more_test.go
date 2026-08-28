package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// longStep builds a step with n plain options.
func longStep(n int) Step {
	var opts []Choice
	for i := 0; i < n; i++ {
		opts = append(opts, Choice{Key: string(rune('a' + i))})
	}
	return Step{Title: "long", Options: opts}
}

// TestOffsetClampsAtTop covers the off<0 clamp: with the cursor near the top of
// a windowed (longer-than-visible) list, the centred offset would go negative
// and must clamp to 0.
func TestOffsetClampsAtTop(t *testing.T) {
	m := wizardModel{steps: []Step{longStep(20)}, sel: make([][]string, 1), height: 20, width: 80}
	m.enter(0)
	m.cursor = 1 // near the top => cursor - mv/2 < 0
	if got := m.offset(); got != 0 {
		t.Fatalf("offset near top=%d want 0 (clamped)", got)
	}
}

// TestViewShowsCompletedStepDot covers the "step already completed" dot branch
// of View: rendering while on a later step marks earlier steps with the OK dot.
func TestViewShowsCompletedStepDot(t *testing.T) {
	m := newModel(
		Step{Title: "one", Options: []Choice{{Key: "a"}, {Key: "b"}}},
		Step{Title: "two", Options: []Choice{{Key: "c"}, {Key: "d"}}},
		Step{Title: "three", Options: []Choice{{Key: "e"}, {Key: "f"}}},
	)
	m, _ = send(m, key("enter")) // advance to step 1 (step 0 now completed)
	v := m.View()
	// The completed (●, OK-styled) and future (○) markers both appear.
	if !strings.Contains(v, "●") || !strings.Contains(v, "○") {
		t.Fatalf("View at a later step should show completed + pending dots:\n%s", v)
	}
	if !strings.Contains(v, "Step 2 of 3") {
		t.Fatalf("View should show 'Step 2 of 3':\n%s", v)
	}
}

// TestViewScrollHintAndWindowing covers the windowed-list branch in View where
// the scroll indicator "(a-b of N)" is appended to the help line.
func TestViewScrollHintWhenWindowed(t *testing.T) {
	m := wizardModel{title: "t", intro: "i",
		steps: []Step{{Title: "big", Help: "pick", Options: optsN(14)}},
		sel:   make([][]string, 1), height: 20, width: 80}
	m.enter(0)
	m.cursor = 10
	if v := m.View(); !strings.Contains(v, " of 14") {
		t.Fatalf("windowed View should carry a scroll hint:\n%s", v)
	}
}

func optsN(n int) []Choice {
	var out []Choice
	for i := 0; i < n; i++ {
		out = append(out, Choice{Key: string(rune('a' + i)), Label: string(rune('A' + i))})
	}
	return out
}

// TestMouseClickContinueTogglesNothingOnSingle exercises the click path where a
// left-press lands on an option row in a single-select step (selects, no toggle)
// versus a multi step (toggles) — complementing the existing mouse tests to
// cover both arms of the click branch.
func TestMouseClickOptionRowSingleVsMulti(t *testing.T) {
	// Single-select: clicking an option row moves the cursor but toggles nothing.
	s := newModel(Step{Title: "s", Options: []Choice{{Key: "a"}, {Key: "b"}, {Key: "c"}}})
	s, _ = send(s, mouse(4, optionsTop+2, tea.MouseActionPress, tea.MouseButtonLeft))
	if s.cursor != 2 {
		t.Fatalf("single click cursor=%d want 2", s.cursor)
	}
	// Multi: clicking an option row toggles it.
	mm := newModel(Step{Title: "s", Multi: true, Options: []Choice{{Key: "a"}, {Key: "b"}, {Key: "c"}}})
	mm, _ = send(mm, mouse(4, optionsTop+1, tea.MouseActionPress, tea.MouseButtonLeft))
	if mm.cursor != 1 || !mm.steps[0].Options[1].Selected {
		t.Fatalf("multi click did not toggle row 1: cursor=%d sel=%v", mm.cursor, mm.steps[0].Options[1].Selected)
	}
}

// TestBackspaceStopsAtNonSkippableIntermediate covers the `break` in the
// backspace loop: stepping back from a later step must stop on the first
// non-skippable step it meets (not run all the way to step 0). Steps: 0 choose,
// 1 choose (stop here), 2 single->skipped, 3 choose. From step 3, backspace
// skips step 2 and lands on the non-skippable step 1.
func TestBackspaceStopsAtNonSkippableIntermediate(t *testing.T) {
	m := newModel(
		Step{Title: "a", Options: []Choice{{Key: "a0"}, {Key: "a1"}}},
		Step{Title: "b", Options: []Choice{{Key: "b0"}, {Key: "b1"}}},
		Step{Title: "c", Options: []Choice{{Key: "only"}}}, // single -> auto-skipped
		Step{Title: "d", Options: []Choice{{Key: "d0"}, {Key: "d1"}}},
	)
	m, _ = send(m, key("enter")) // 0 -> 1
	m, _ = send(m, key("enter")) // 1 -> 3 (skips single step 2)
	if m.step != 3 {
		t.Fatalf("precondition step=%d want 3", m.step)
	}
	m, _ = send(m, key("backspace")) // 3 -> (skip 2) -> break at 1
	if m.step != 1 {
		t.Fatalf("backspace should stop at non-skippable step 1, got %d", m.step)
	}
}

// TestUpdateUnhandledKeyIsNoop covers the default arm of the key switch for a
// key that is neither a shortcut nor a number: nothing changes.
func TestUpdateUnhandledKeyIsNoop(t *testing.T) {
	m := newModel(Step{Title: "s", Options: []Choice{{Key: "a"}, {Key: "b"}}})
	before := m.cursor
	m, cmd := send(m, key("x")) // 'x' is not a shortcut and not a digit
	if m.cursor != before || cmd != nil {
		t.Fatalf("unhandled key had an effect: cursor=%d cmd=%v", m.cursor, cmd)
	}
}

// TestUpdateNonMouseNonKeyMsgNoop covers the outer switch's fall-through for a
// message type the wizard does not handle.
func TestUpdateUnknownMsgNoop(t *testing.T) {
	m := newModel(Step{Title: "s", Options: []Choice{{Key: "a"}}})
	type customMsg struct{}
	next, cmd := m.Update(customMsg{})
	if cmd != nil {
		t.Fatalf("unknown msg returned a cmd: %v", cmd)
	}
	if _, ok := next.(wizardModel); !ok {
		t.Fatal("Update should return the model unchanged for an unknown msg")
	}
}
