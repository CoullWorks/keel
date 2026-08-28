package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// newModel builds a wizardModel over the given steps with a sane default size,
// entered at step 0 (options resolved, cursor on the pre-selected option).
func newModel(steps ...Step) wizardModel {
	m := wizardModel{title: "t", intro: "i", steps: steps, sel: make([][]string, len(steps)), width: 80, height: 24}
	m.enter(0)
	return m
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// send drives one message through Update and returns the concrete model back.
func send(m wizardModel, msg tea.Msg) (wizardModel, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(wizardModel), cmd
}

func TestInitReturnsNil(t *testing.T) {
	if cmd := newModel(Step{Title: "x"}).Init(); cmd != nil {
		t.Fatalf("Init should return nil, got %T", cmd)
	}
}

func TestFirstSelected(t *testing.T) {
	opts := []Choice{{Key: "a"}, {Key: "b", Selected: true}, {Key: "c"}}
	if got := firstSelected(opts); got != 1 {
		t.Fatalf("firstSelected=%d want 1", got)
	}
	// None selected => 0.
	if got := firstSelected([]Choice{{Key: "a"}, {Key: "b"}}); got != 0 {
		t.Fatalf("firstSelected(none)=%d want 0", got)
	}
}

func TestCursorMovement(t *testing.T) {
	m := newModel(Step{Title: "s", Options: []Choice{{Key: "a"}, {Key: "b"}, {Key: "c"}}})
	// up at top is a no-op.
	m, _ = send(m, key("up"))
	if m.cursor != 0 {
		t.Fatalf("up at top moved cursor to %d", m.cursor)
	}
	m, _ = send(m, key("down"))
	m, _ = send(m, key("j")) // vim down
	if m.cursor != 2 {
		t.Fatalf("cursor=%d want 2 after two downs", m.cursor)
	}
	// down at bottom is a no-op.
	m, _ = send(m, key("down"))
	if m.cursor != 2 {
		t.Fatalf("down at bottom moved cursor to %d", m.cursor)
	}
	m, _ = send(m, key("k")) // vim up
	if m.cursor != 1 {
		t.Fatalf("cursor=%d want 1 after k", m.cursor)
	}
}

func TestNumberShortcutSingle(t *testing.T) {
	m := newModel(Step{Title: "s", Options: []Choice{{Key: "a"}, {Key: "b"}, {Key: "c"}}})
	m, _ = send(m, key("3"))
	if m.cursor != 2 {
		t.Fatalf("number '3' -> cursor %d want 2", m.cursor)
	}
	// Out-of-range number is ignored.
	m, _ = send(m, key("9"))
	if m.cursor != 2 {
		t.Fatalf("out-of-range number moved cursor to %d", m.cursor)
	}
}

func TestNumberShortcutMultiToggles(t *testing.T) {
	m := newModel(Step{Title: "s", Multi: true, Options: []Choice{{Key: "a"}, {Key: "b"}}})
	m, _ = send(m, key("2")) // selects cursor 1 and toggles it on
	if m.cursor != 1 || !m.steps[0].Options[1].Selected {
		t.Fatalf("number toggle failed: cursor=%d selected=%v", m.cursor, m.steps[0].Options[1].Selected)
	}
}

func TestSpaceTogglesMultiOnly(t *testing.T) {
	// Multi: space toggles the option under the cursor.
	m := newModel(Step{Title: "s", Multi: true, Options: []Choice{{Key: "a"}, {Key: "b"}}})
	m, _ = send(m, key(" "))
	if !m.steps[0].Options[0].Selected {
		t.Fatal("space did not toggle multi option on")
	}
	m, _ = send(m, key(" "))
	if m.steps[0].Options[0].Selected {
		t.Fatal("second space did not toggle multi option off")
	}
	// Single-select: space is a no-op (nothing to toggle).
	s := newModel(Step{Title: "s", Options: []Choice{{Key: "a"}, {Key: "b"}}})
	s, _ = send(s, key(" "))
	if s.steps[0].Options[0].Selected {
		t.Fatal("space toggled a single-select option (should not)")
	}
}

func TestAdvanceSingleRecordsSelection(t *testing.T) {
	m := newModel(
		Step{Title: "one", Options: []Choice{{Key: "a"}, {Key: "b"}}},
		Step{Title: "two", Options: []Choice{{Key: "c"}, {Key: "d"}}},
	)
	m, _ = send(m, key("down")) // cursor -> b
	m, cmd := send(m, key("enter"))
	if cmd != nil {
		t.Fatal("advancing to a middle step should not quit")
	}
	if m.step != 1 {
		t.Fatalf("step=%d want 1", m.step)
	}
	if got := m.sel[0]; len(got) != 1 || got[0] != "b" {
		t.Fatalf("sel[0]=%v want [b]", got)
	}
}

func TestAdvanceMultiRecordsAllSelected(t *testing.T) {
	m := newModel(
		Step{Title: "one", Multi: true, Options: []Choice{{Key: "a", Selected: true}, {Key: "b"}, {Key: "c", Selected: true}}},
		Step{Title: "two", Options: []Choice{{Key: "z"}}},
	)
	m, _ = send(m, key("right")) // right == enter/advance
	if got := m.sel[0]; len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Fatalf("multi sel[0]=%v want [a c]", got)
	}
}

func TestAdvanceOnLastStepQuits(t *testing.T) {
	m := newModel(Step{Title: "only", Options: []Choice{{Key: "a"}}})
	m, cmd := send(m, key("enter"))
	if !m.done {
		t.Fatal("advancing off the last step should set done")
	}
	if cmd == nil {
		t.Fatal("last-step advance should return a quit cmd")
	}
}

func TestAdvanceSkipsSingleOptionSteps(t *testing.T) {
	// Step 1 has a single option => auto-selected and skipped; land on step 2.
	m := newModel(
		Step{Title: "a", Options: []Choice{{Key: "a0"}, {Key: "a1"}}},
		Step{Title: "b", Options: []Choice{{Key: "only"}}},
		Step{Title: "c", Options: []Choice{{Key: "c0"}, {Key: "c1"}}},
	)
	m, _ = send(m, key("enter"))
	if m.step != 2 {
		t.Fatalf("step=%d want 2 (single-option step 1 should auto-skip)", m.step)
	}
	if got := m.sel[1]; len(got) != 1 || got[0] != "only" {
		t.Fatalf("auto-selected sel[1]=%v want [only]", got)
	}
}

func TestAdvanceSkipsEmptySingleStepToFinish(t *testing.T) {
	// A trailing single-select step with zero options is skippable and finishes.
	m := newModel(
		Step{Title: "a", Options: []Choice{{Key: "a0"}}},
		Step{Title: "empty", Options: nil},
	)
	m, cmd := send(m, key("enter"))
	if !m.done || cmd == nil {
		t.Fatalf("should finish through the empty trailing step: done=%v cmd=%v", m.done, cmd)
	}
	if m.sel[1] != nil {
		t.Fatalf("empty step autoSelect should be nil, got %v", m.sel[1])
	}
}

func TestBackspaceReturnsToPreviousStep(t *testing.T) {
	m := newModel(
		Step{Title: "a", Options: []Choice{{Key: "a0"}, {Key: "a1"}}},
		Step{Title: "b", Options: []Choice{{Key: "b0"}, {Key: "b1"}}},
	)
	m, _ = send(m, key("enter")) // to step 1
	if m.step != 1 {
		t.Fatalf("precondition: step=%d want 1", m.step)
	}
	m, _ = send(m, key("backspace"))
	if m.step != 0 {
		t.Fatalf("backspace step=%d want 0", m.step)
	}
	// left also goes back.
	m, _ = send(m, key("enter"))
	m, _ = send(m, key("left"))
	if m.step != 0 {
		t.Fatalf("left step=%d want 0", m.step)
	}
}

func TestBackspaceSkipsAutoSkippedSteps(t *testing.T) {
	// Steps: 0 (choose), 1 (single -> skipped), 2 (choose). Backspace from 2
	// should land on 0, stepping over the skippable step 1.
	m := newModel(
		Step{Title: "a", Options: []Choice{{Key: "a0"}, {Key: "a1"}}},
		Step{Title: "b", Options: []Choice{{Key: "only"}}},
		Step{Title: "c", Options: []Choice{{Key: "c0"}, {Key: "c1"}}},
	)
	m, _ = send(m, key("enter")) // lands on step 2 (skips 1)
	if m.step != 2 {
		t.Fatalf("precondition step=%d want 2", m.step)
	}
	m, _ = send(m, key("backspace"))
	if m.step != 0 {
		t.Fatalf("backspace should skip step 1 back to 0, got %d", m.step)
	}
}

func TestBackspaceAtFirstStepStays(t *testing.T) {
	m := newModel(Step{Title: "a", Options: []Choice{{Key: "a0"}}}, Step{Title: "b", Options: []Choice{{Key: "b0"}}})
	m, _ = send(m, key("backspace"))
	if m.step != 0 {
		t.Fatalf("backspace at first step moved to %d", m.step)
	}
}

func TestQuitKeys(t *testing.T) {
	for _, k := range []string{"q", "esc", "ctrl+c"} {
		m := newModel(Step{Title: "a", Options: []Choice{{Key: "a0"}}})
		m, cmd := send(m, key(k))
		if !m.cancel {
			t.Fatalf("%q did not set cancel", k)
		}
		if cmd == nil {
			t.Fatalf("%q did not return a quit cmd", k)
		}
	}
}

func TestWindowSizeMsg(t *testing.T) {
	m := newModel(Step{Title: "a", Options: []Choice{{Key: "a0"}}})
	m, _ = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	if m.width != 120 || m.height != 40 {
		t.Fatalf("window size not stored: %dx%d", m.width, m.height)
	}
}

func TestToggleHelper(t *testing.T) {
	m := newModel(Step{Title: "a", Multi: true, Options: []Choice{{Key: "a0"}, {Key: "a1", Selected: true}}})
	m.toggle(0)
	m.toggle(1)
	if !m.steps[0].Options[0].Selected || m.steps[0].Options[1].Selected {
		t.Fatalf("toggle failed: %+v", m.steps[0].Options)
	}
}

func TestSkippableAndAutoSelect(t *testing.T) {
	m := newModel(
		Step{Title: "single-one", Options: []Choice{{Key: "x"}}},               // skippable
		Step{Title: "single-multi", Options: []Choice{{Key: "a"}, {Key: "b"}}}, // not skippable
		Step{Title: "multi", Multi: true, Options: []Choice{{Key: "m"}}},       // multi, never skippable
		Step{Title: "empty", Options: nil},                                     // skippable (0 opts)
	)
	if !m.skippable(0) {
		t.Fatal("single-option step should be skippable")
	}
	if m.skippable(1) {
		t.Fatal("two-option single-select should not be skippable")
	}
	if m.skippable(2) {
		t.Fatal("multi step should never be skippable")
	}
	if !m.skippable(3) {
		t.Fatal("empty single-select should be skippable")
	}
	// autoSelect: single option -> that key; empty -> nil.
	m.autoSelect(0)
	if got := m.sel[0]; len(got) != 1 || got[0] != "x" {
		t.Fatalf("autoSelect(single)=%v want [x]", got)
	}
	m.autoSelect(3)
	if m.sel[3] != nil {
		t.Fatalf("autoSelect(empty)=%v want nil", m.sel[3])
	}
}

func TestResolveOptionsDynamic(t *testing.T) {
	called := false
	m := newModel(
		Step{Title: "a", Options: []Choice{{Key: "php"}}},
		Step{Title: "b", Dynamic: func(prior [][]string) []Choice {
			called = true
			return []Choice{{Key: "built-from-prior"}}
		}},
	)
	m.sel[0] = []string{"php"}
	m.resolveOptions(1)
	if !called {
		t.Fatal("Dynamic was not invoked")
	}
	if m.steps[1].Options[0].Key != "built-from-prior" {
		t.Fatalf("resolveOptions did not rebuild options: %+v", m.steps[1].Options)
	}
	// A step with no Dynamic leaves options untouched.
	before := m.steps[0].Options
	m.resolveOptions(0)
	if len(m.steps[0].Options) != len(before) {
		t.Fatal("resolveOptions mutated a static step")
	}
}

func TestViewRendersStepAndControls(t *testing.T) {
	m := newModel(
		Step{Title: "Framework", Help: "pick", Options: []Choice{
			{Key: "laravel", Label: "Laravel", Desc: "PHP", Selected: true},
			{Key: "magento", Label: "Magento"},
		}},
		Step{Title: "Env", Options: []Choice{{Key: "ddev"}}},
	)
	v := m.View()
	for _, want := range []string{"Framework", "Laravel", "Magento", "Step 1 of 2", "Continue →", "space toggle"} {
		if !strings.Contains(v, want) {
			t.Fatalf("View missing %q:\n%s", want, v)
		}
	}
}

func TestViewFinishOnLastStep(t *testing.T) {
	m := newModel(Step{Title: "only", Options: []Choice{{Key: "a", Label: "A"}}})
	if v := m.View(); !strings.Contains(v, "Finish →") {
		t.Fatalf("last step should show Finish:\n%s", v)
	}
}

func TestViewMultiMarkers(t *testing.T) {
	m := newModel(Step{Title: "Add-ons", Multi: true, Options: []Choice{
		{Key: "a", Label: "A", Selected: true},
		{Key: "b", Label: "B"},
	}})
	v := m.View()
	if !strings.Contains(v, "◉") || !strings.Contains(v, "○") {
		t.Fatalf("multi view should show filled + empty markers:\n%s", v)
	}
}

func TestViewDefaultSize(t *testing.T) {
	// A model with no width/height falls back to 80x24 (width0/height0).
	m := wizardModel{title: "t", intro: "i", steps: []Step{{Title: "a", Options: []Choice{{Key: "x", Label: "X"}}}}, sel: make([][]string, 1)}
	m.enter(0)
	if m.width0() != 80 || m.height0() != 24 {
		t.Fatalf("defaults width0=%d height0=%d", m.width0(), m.height0())
	}
	if v := m.View(); !strings.Contains(v, "X") {
		t.Fatalf("View at default size missing option:\n%s", v)
	}
}

func TestMaxVisibleFloor(t *testing.T) {
	// A tiny terminal clamps the window to a minimum of 3 rows.
	m := wizardModel{steps: []Step{{Title: "a", Options: []Choice{{Key: "x"}}}}, sel: make([][]string, 1), height: 5, width: 40}
	m.enter(0)
	if got := m.maxVisible(); got != 3 {
		t.Fatalf("maxVisible floor=%d want 3", got)
	}
}

func TestOffsetClampsAtEnd(t *testing.T) {
	var opts []Choice
	for i := 0; i < 20; i++ {
		opts = append(opts, Choice{Key: string(rune('a' + i))})
	}
	m := wizardModel{steps: []Step{{Title: "a", Options: opts}}, sel: make([][]string, 1), height: 20, width: 80}
	m.enter(0)
	m.cursor = 19 // last item
	mv := m.maxVisible()
	if got, want := m.offset(), len(opts)-mv; got != want {
		t.Fatalf("offset at end=%d want %d (clamped)", got, want)
	}
	// A short list (fits entirely) => offset 0.
	s := newModel(Step{Title: "a", Options: []Choice{{Key: "x"}, {Key: "y"}}})
	if got := s.offset(); got != 0 {
		t.Fatalf("offset(short list)=%d want 0", got)
	}
}
