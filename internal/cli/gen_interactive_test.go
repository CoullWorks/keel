package cli

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/coullworks/keel/internal/tui"
)

// stubGenWizard swaps the gen picker's wizard for canned selections.
func stubGenWizard(t *testing.T, res [][]string, err error) {
	t.Helper()
	old := genWizard
	genWizard = func(_, _ string, _ []tui.Step) ([][]string, error) {
		return res, err
	}
	t.Cleanup(func() { genWizard = old })
}

// pickComponents returns the wizard's first-step selection on success.
func TestPickComponentsSuccess(t *testing.T) {
	stubGenWizard(t, [][]string{{"model", "event"}}, nil)
	keys, err := pickComponents("i", "t", "h", []tui.Choice{{Key: "model", Label: "Model"}})
	if err != nil {
		t.Fatalf("pickComponents: %v", err)
	}
	if len(keys) != 2 || keys[0] != "model" || keys[1] != "event" {
		t.Errorf("keys = %v, want [model event]", keys)
	}
}

// Cancel maps to (nil, nil) — no selection, no error — so callers just do nothing.
func TestPickComponentsCancelled(t *testing.T) {
	stubGenWizard(t, nil, tui.ErrCancelled)
	keys, err := pickComponents("i", "t", "h", nil)
	if err != nil {
		t.Fatalf("cancel should not error: %v", err)
	}
	if keys != nil {
		t.Errorf("cancel should return nil keys, got %v", keys)
	}
}

// A non-cancel wizard error propagates.
func TestPickComponentsError(t *testing.T) {
	stubGenWizard(t, nil, errors.New("boom"))
	if _, err := pickComponents("i", "t", "h", nil); err == nil {
		t.Fatal("expected the wizard error to propagate")
	}
}

// runLaravelInteractive with an empty picker selection skips the inline name
// prompts entirely and reaches runCmds(io.Discard, []) — "nothing to generate". This covers
// the interactive function's wiring (option build + pickComponents + fall-through)
// offline, without touching huh.NewInput or Docker.
func TestRunLaravelInteractiveEmptySelection(t *testing.T) {
	wd := isolate(t)
	stubGenWizard(t, [][]string{{}}, nil)
	// The writer is a parameter now, so the test reads the buffer directly.
	var buf bytes.Buffer
	if err := runLaravelInteractive(context.Background(), &buf, genTarget{Dir: wd, Framework: "laravel", Env: "ddev"}, true); err != nil {
		t.Fatalf("runLaravelInteractive (empty): %v", err)
	}
	mustContain(t, buf.String(), "nothing to generate")
}

// `keel gen` with no args and a cancelled picker is the interactive entrypoint:
// runLaravelGen → runLaravelInteractive → pickComponents(cancel) → runCmds(io.Discard, nil).
func TestGenNoArgsInteractiveCancel(t *testing.T) {
	genProject(t, "laravel", "ddev")
	stubGenWizard(t, nil, tui.ErrCancelled)
	out, err := runRoot(t, "gen", "-f", "laravel", "--dry-run")
	if err != nil {
		t.Fatalf("gen (interactive cancel): %v", err)
	}
	mustContain(t, out, "nothing to generate")
}
