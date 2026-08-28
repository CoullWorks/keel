package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/profile"
)

func TestWizardReturnsSelection(t *testing.T) {
	orig := runProgram
	defer func() { runProgram = orig }()
	runProgram = func(tea.Model) (tea.Model, error) {
		return wizardModel{done: true, sel: [][]string{{"a"}, {"b"}}}, nil
	}
	sel, err := Wizard("t", "i", []Step{{Title: "s"}})
	if err != nil {
		t.Fatalf("Wizard: %v", err)
	}
	if len(sel) != 2 || sel[0][0] != "a" || sel[1][0] != "b" {
		t.Fatalf("unexpected sel: %v", sel)
	}
}

func TestWizardCancelledAndNotDone(t *testing.T) {
	orig := runProgram
	defer func() { runProgram = orig }()
	runProgram = func(tea.Model) (tea.Model, error) { return wizardModel{cancel: true}, nil }
	if _, err := Wizard("t", "i", []Step{{Title: "s"}}); !errors.Is(err, ErrCancelled) {
		t.Fatalf("cancel: want ErrCancelled, got %v", err)
	}
	runProgram = func(tea.Model) (tea.Model, error) { return wizardModel{}, nil } // neither done nor cancel
	if _, err := Wizard("t", "i", []Step{{Title: "s"}}); !errors.Is(err, ErrCancelled) {
		t.Fatalf("incomplete: want ErrCancelled, got %v", err)
	}
}

func TestWizardError(t *testing.T) {
	orig := runProgram
	defer func() { runProgram = orig }()
	runProgram = func(tea.Model) (tea.Model, error) { return nil, errors.New("boom") }
	if _, err := Wizard("t", "i", []Step{{Title: "s"}}); err == nil {
		t.Fatal("program error should propagate")
	}
}

// otherModel is any tea.Model that is NOT a wizardModel, to exercise the type
// assertion guard.
type otherModel struct{}

func (otherModel) Init() tea.Cmd                       { return nil }
func (otherModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return otherModel{}, nil }
func (otherModel) View() string                        { return "" }

// TestWizardUnexpectedModel proves the type assertion is guarded: a seam (or a
// future model change) returning the wrong model type yields a clear error, never
// a panic in library code.
func TestWizardUnexpectedModel(t *testing.T) {
	orig := runProgram
	defer func() { runProgram = orig }()
	runProgram = func(tea.Model) (tea.Model, error) { return otherModel{}, nil }
	_, err := Wizard("t", "i", []Step{{Title: "s"}})
	if err == nil {
		t.Fatal("an unexpected model type should be an error, not a panic")
	}
	if !strings.Contains(err.Error(), "unexpected model type") {
		t.Fatalf("error should name the problem, got %v", err)
	}
}

func TestHomeMapsChoiceCancelAndError(t *testing.T) {
	orig := runProgram
	defer func() { runProgram = orig }()

	runProgram = func(tea.Model) (tea.Model, error) {
		return wizardModel{done: true, sel: [][]string{{"doctor"}}}, nil
	}
	if got, err := Home("summary"); err != nil || got != "doctor" {
		t.Fatalf("Home choice: got %q err %v", got, err)
	}

	runProgram = func(tea.Model) (tea.Model, error) { return wizardModel{cancel: true}, nil }
	if got, err := Home("summary"); err != nil || got != "quit" {
		t.Fatalf("Home cancel: got %q err %v", got, err)
	}

	runProgram = func(tea.Model) (tea.Model, error) { return nil, errors.New("x") }
	if _, err := Home("summary"); err == nil {
		t.Fatal("Home should propagate a non-cancel error")
	}
}

// TestSelectRecipesWalksBuilders drives the wizard with Enter presses so each
// step's resolveOptions fires the Dynamic callbacks (the single/multi/catMulti/
// family/variant option builders), covering SelectRecipes end to end.
func TestSelectRecipesWalksBuilders(t *testing.T) {
	orig := runProgram
	defer func() { runProgram = orig }()
	runProgram = func(m tea.Model) (tea.Model, error) {
		cur := m
		for i := 0; i < 20; i++ {
			wm := cur.(wizardModel)
			if wm.done || wm.cancel {
				break
			}
			cur, _ = cur.Update(tea.KeyMsg{Type: tea.KeyEnter})
		}
		return cur, nil
	}
	reg, err := catalog.Registry()
	if err != nil {
		t.Fatal(err)
	}
	ids, res, err := SelectRecipes(reg, profile.Default())
	if err != nil && !errors.Is(err, ErrCancelled) {
		t.Fatalf("SelectRecipes: %v", err)
	}
	// The raw result is returned alongside the ids (it carries the brand choice
	// the caller threads to ApplyBrand). On a completed walk it mirrors the ids.
	if err == nil {
		if res == nil {
			t.Fatal("SelectRecipes must return the raw wizard result, not nil")
		}
		if len(IDsFromResult(res)) != len(ids) {
			t.Fatalf("ids and IDsFromResult(res) disagree: %v vs %v", ids, IDsFromResult(res))
		}
	}
}
