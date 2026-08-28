package console

import (
	"strings"
	"testing"
)

// TestConsoleAddExistingProjectPrompt is the console↔studio parity guard: the
// Projects area must offer an interactive "add an existing project" the way the
// studio does. Pressing `a` opens a path prompt; Enter turns the typed path into a
// `keel track <path>` action (list it, no manifest — matching the studio's add).
func TestConsoleAddExistingProjectPrompt(t *testing.T) {
	f := &projectFlow{} // no reload: exercise the flow without touching the real registry

	if act, _ := f.update(fkey("a")); act.Kind != "" {
		t.Fatalf("`a` should open the add prompt, not act: %+v", act)
	}
	if !f.adding {
		t.Fatal("`a` did not open the add-an-existing-project prompt")
	}

	for _, r := range "~/code/shop" {
		f.update(fkey(string(r)))
	}

	act, _ := f.update(fkey("enter"))
	if act.Kind != "argv" || strings.Join(act.Argv, " ") != "track ~/code/shop" {
		t.Fatalf("Enter should run `keel track ~/code/shop`, got %+v", act)
	}
	if f.adding {
		t.Fatal("submitting the path should close the prompt")
	}
}

// Esc cancels the add prompt without acting.
func TestConsoleAddProjectEscCancels(t *testing.T) {
	f := &projectFlow{}
	f.update(fkey("a"))
	f.update(fkey("x"))
	if act, _ := f.update(fkey("esc")); act.Kind != "" {
		t.Fatalf("esc should not act: %+v", act)
	}
	if f.adding {
		t.Fatal("esc should close the add prompt")
	}
}
