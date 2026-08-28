package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func mouse(x, y int, action tea.MouseAction, button tea.MouseButton) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: action, Button: button}
}

func TestMouseWheelMovesCursor(t *testing.T) {
	var opts []Choice
	for i := 0; i < 6; i++ {
		opts = append(opts, Choice{Key: string(rune('a' + i))})
	}
	m := newModel(Step{Title: "s", Options: opts})
	m.cursor = 3
	// Wheel up moves the cursor up.
	m, _ = send(m, mouse(0, 0, tea.MouseActionPress, tea.MouseButtonWheelUp))
	if m.cursor != 2 {
		t.Fatalf("wheel up cursor=%d want 2", m.cursor)
	}
	// Wheel down moves it down.
	m, _ = send(m, mouse(0, 0, tea.MouseActionPress, tea.MouseButtonWheelDown))
	if m.cursor != 3 {
		t.Fatalf("wheel down cursor=%d want 3", m.cursor)
	}
	// Wheel up at the top is a no-op.
	m.cursor = 0
	m, _ = send(m, mouse(0, 0, tea.MouseActionPress, tea.MouseButtonWheelUp))
	if m.cursor != 0 {
		t.Fatalf("wheel up at top moved cursor to %d", m.cursor)
	}
	// Wheel down at the bottom is a no-op.
	m.cursor = len(opts) - 1
	m, _ = send(m, mouse(0, 0, tea.MouseActionPress, tea.MouseButtonWheelDown))
	if m.cursor != len(opts)-1 {
		t.Fatalf("wheel down at bottom moved cursor to %d", m.cursor)
	}
}

func TestMouseHoverHighlights(t *testing.T) {
	m := newModel(Step{Title: "s", Options: []Choice{{Key: "a"}, {Key: "b"}, {Key: "c"}}})
	// Row 2 within the option window (optionsTop + 2) -> cursor 2.
	m, _ = send(m, mouse(4, optionsTop+2, tea.MouseActionMotion, tea.MouseButtonNone))
	if m.cursor != 2 {
		t.Fatalf("hover cursor=%d want 2", m.cursor)
	}
	// A motion above the option window (rel < 0) does not move the cursor.
	m.cursor = 1
	m, _ = send(m, mouse(4, 0, tea.MouseActionMotion, tea.MouseButtonNone))
	if m.cursor != 1 {
		t.Fatalf("hover above window moved cursor to %d", m.cursor)
	}
}

func TestMouseClickSelectsSingle(t *testing.T) {
	m := newModel(Step{Title: "s", Options: []Choice{{Key: "a"}, {Key: "b"}, {Key: "c"}}})
	m, _ = send(m, mouse(4, optionsTop+1, tea.MouseActionPress, tea.MouseButtonLeft))
	if m.cursor != 1 {
		t.Fatalf("click cursor=%d want 1", m.cursor)
	}
}

func TestMouseClickTogglesMulti(t *testing.T) {
	m := newModel(Step{Title: "s", Multi: true, Options: []Choice{{Key: "a"}, {Key: "b"}}})
	m, _ = send(m, mouse(4, optionsTop+0, tea.MouseActionPress, tea.MouseButtonLeft))
	if m.cursor != 0 || !m.steps[0].Options[0].Selected {
		t.Fatalf("multi click did not toggle: cursor=%d selected=%v", m.cursor, m.steps[0].Options[0].Selected)
	}
}

func TestMouseClickContinueRowAdvances(t *testing.T) {
	m := newModel(
		Step{Title: "one", Options: []Choice{{Key: "a"}, {Key: "b"}}},
		Step{Title: "two", Options: []Choice{{Key: "c"}, {Key: "d"}}},
	)
	// The Continue row is at optionsTop + visN + 1 (one blank line after the list).
	contRow := optionsTop + m.visN() + 1
	m, cmd := send(m, mouse(4, contRow, tea.MouseActionPress, tea.MouseButtonLeft))
	if m.step != 1 {
		t.Fatalf("clicking Continue advanced to step %d want 1", m.step)
	}
	if cmd != nil {
		t.Fatal("mid-wizard Continue click should not quit")
	}
}

func TestMouseClickBelowNothingHappens(t *testing.T) {
	m := newModel(Step{Title: "one", Options: []Choice{{Key: "a"}, {Key: "b"}}})
	before := m.cursor
	// A click far below the Continue row falls through all cases.
	m, cmd := send(m, mouse(4, optionsTop+50, tea.MouseActionPress, tea.MouseButtonLeft))
	if m.cursor != before || cmd != nil || m.done {
		t.Fatalf("stray click had an effect: cursor=%d cmd=%v done=%v", m.cursor, cmd, m.done)
	}
}
