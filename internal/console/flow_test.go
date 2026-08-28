package console

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func fkey(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// TestEnteringBuildOpensRealOptions is the regression guard for the console's
// original defect: every area rendered a paragraph of help text listing commands
// to retype, and pressing enter only moved the focus. Nothing opened.
//
// Entering "Build a stack" must now start a flow whose first screen offers the
// actual language choices from the catalogue.
func TestEnteringBuildOpensRealOptions(t *testing.T) {
	m := New()
	m.w, m.h = 120, 40
	m.setup = false // no profile on a clean CI runner would gate on first-run setup; test the flow itself
	if m.reg == nil {
		t.Skip("no catalogue in this environment")
	}
	m.nav = m.indexOf("build")
	if m.nav < 0 {
		t.Fatal("no build area")
	}

	got, _ := m.Update(fkey("enter"))
	m = got.(model)
	if m.flow == nil {
		t.Fatal("enter on Build did not open anything — the area is still static text")
	}
	bf, ok := m.flow.(*stepFlow)
	if !ok {
		t.Fatalf("Build opened a %T, expected the step flow", m.flow)
	}
	opts := bf.options()
	if len(opts) == 0 {
		t.Fatal("Build opened with no options to choose from")
	}
	view := bf.view(100)
	for _, o := range opts {
		if !strings.Contains(view, o.Label) {
			t.Errorf("option %q is offered but not rendered", o.Label)
		}
	}
}

// TestBuildFlowReachesAnAction walks the whole flow with Enter and asserts it
// ends in a build Action carrying real recipe ids. Enter alone must be enough:
// every step is pre-seeded from the profile or the recipe defaults.
func TestBuildFlowReachesAnAction(t *testing.T) {
	m := New()
	m.w, m.h = 120, 40
	m.setup = false // no profile on a clean CI runner would gate on first-run setup; test the flow itself
	if m.reg == nil {
		t.Skip("no catalogue in this environment")
	}
	m.nav = m.indexOf("build")
	got, _ := m.Update(fkey("enter"))
	m = got.(model)

	// One Enter per step, plus one for the directory prompt. The bound is
	// generous; the assertion is that it terminates in an action, not when.
	bf, ok := m.flow.(*stepFlow)
	if !ok {
		t.Fatalf("Build opened a %T, expected the step flow", m.flow)
	}
	for i := 0; i < len(bf.steps)+2 && m.action.Kind == ""; i++ {
		got, _ = m.Update(fkey("enter"))
		m = got.(model)
		if m.flow == nil {
			break
		}
	}
	if m.action.Kind != "build" {
		t.Fatalf("walking the flow with enter never produced a build action, got %q", m.action.Kind)
	}
	if len(m.action.Recipes) == 0 {
		t.Fatal("build action carries no recipes")
	}
	// The framework itself must be in there: a plan without one cannot resolve.
	var hasFramework bool
	for _, id := range m.action.Recipes {
		if r, ok := m.reg.Get(id); ok && string(r.Kind) == "framework" {
			hasFramework = true
		}
	}
	if !hasFramework {
		t.Errorf("no framework among the chosen recipes: %v", m.action.Recipes)
	}
}

// TestEscLeavesTheFlow: esc on the first step returns to the sidebar rather than
// trapping you in a screen with no way out.
func TestEscLeavesTheFlow(t *testing.T) {
	m := New()
	m.w, m.h = 120, 40
	m.setup = false // no profile on a clean CI runner would gate on first-run setup; test the flow itself
	if m.reg == nil {
		t.Skip("no catalogue in this environment")
	}
	m.nav = m.indexOf("build")
	got, _ := m.Update(fkey("enter"))
	m = got.(model)
	if m.flow == nil {
		t.Fatal("build did not open")
	}
	got, _ = m.Update(fkey("esc"))
	m = got.(model)
	if m.flow != nil {
		t.Error("esc on the first step did not leave the flow")
	}
	if m.focus != 0 {
		t.Error("leaving the flow did not return focus to the sidebar")
	}
}

// TestEveryAreaOpensSomething is the general form of the reported defect: you
// could navigate the sidebar, press enter, and nothing happened, because only
// Settings had an interactive screen and the rest were paragraphs listing
// commands to retype.
//
// Every area must now open either a flow or the setup stepper. A new area added
// without one fails here rather than being discovered by a user.
func TestEveryAreaOpensSomething(t *testing.T) {
	base := New()
	if base.reg == nil {
		t.Skip("no catalogue in this environment")
	}
	for i, a := range base.areas {
		m := New()
		m.w, m.h = 120, 40
		m.setup = false // no profile on a clean CI runner would gate on first-run setup; test the flow itself
		// Start from the sidebar every time: the first-run stepper would
		// otherwise swallow the keypress.
		m.setup, m.focus, m.nav = false, 0, i

		got, _ := m.Update(fkey("enter"))
		m = got.(model)
		if m.flow == nil && !m.setup {
			t.Errorf("area %q (%s) opened nothing when entered", a.key, a.title)
			continue
		}
		if m.flow != nil {
			if v := m.flow.view(100); strings.TrimSpace(v) == "" {
				t.Errorf("area %q opened an empty screen", a.key)
			}
		}
	}
}
