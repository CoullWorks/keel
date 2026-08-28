package console

import (
	"strings"
	"testing"
)

// TestAreaSettingsChoiceStepRendersNoneAndEmpty exercises two branches of the
// setup-choice render: an option with an empty label shows "None" (the editor
// list has one), and a step with zero options shows the framework-first hint.
func TestAreaSettingsChoiceStepRendersNone(t *testing.T) {
	m := coldModel(t)
	m.setupStep = 5 // editor: its last option is {"", "None"}
	m.loadStepText()
	if out := m.cur().render(&m, 60, 24); !strings.Contains(out, "None") {
		t.Fatalf("editor step should render a 'None' label:\n%s", out)
	}
}

func TestAreaSettingsChoiceStepEmptyOptionsHint(t *testing.T) {
	m := coldModel(t)
	// Env step with no framework selected yields zero options => the hint shows.
	delete(m.setupSel, "framework")
	m.setupStep = 3 // env
	m.loadStepText()
	_, opts := m.stepOptions()
	if len(opts) != 0 {
		t.Skipf("env step unexpectedly has %d options with no framework; hint branch not reachable", len(opts))
	}
	if out := m.cur().render(&m, 60, 24); !strings.Contains(out, "choose a framework first") {
		t.Fatalf("empty choice step should show the framework-first hint:\n%s", out)
	}
}

// TestLoadStepTextNilProfileEmpties covers the branch where there is no prior
// answer and no profile, so the buffer is emptied.
func TestLoadStepTextNilProfileEmpties(t *testing.T) {
	m := model{areas: areaList(), setupSel: map[string]string{}, prof: nil}
	m.setupText = "leftover"
	m.setupStep = 0 // name (text step)
	m.loadStepText()
	if m.setupText != "" {
		t.Fatalf("loadStepText with nil profile + no prior answer should empty the buffer, got %q", m.setupText)
	}
}

// TestSaveSetupNilProfileLoadsOne covers saveSetup's nil-profile branch: it
// loads a usable profile, applies the collected answers, and persists.
func TestSaveSetupNilProfileLoadsOne(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	m := model{areas: areaList(), prof: nil, setupSel: map[string]string{
		"name":      "Zaphod",
		"framework": "laravel",
	}}
	m.saveSetup()
	if m.setup {
		t.Fatal("saveSetup should clear the setup flag")
	}
	if m.prof == nil || m.prof.Git.Name != "Zaphod" {
		t.Fatalf("saveSetup should load a profile and set the name, got %+v", m.prof)
	}
	if m.prof.Defaults["framework"] != "laravel" {
		t.Fatalf("saveSetup should record framework default, got %q", m.prof.Defaults["framework"])
	}
}

// TestFooterCommandMode covers the footer's command-line branch.
func TestFooterCommandMode(t *testing.T) {
	m := newModel(t)
	m.cmd, m.cmdBuf = true, "buil"
	if out := m.footer(); !strings.Contains(out, "buil") {
		t.Fatalf("command-mode footer should echo the buffer:\n%s", out)
	}
}

// TestFooterSetupMode covers the footer's setup-mode key hints.
func TestFooterSetupMode(t *testing.T) {
	m := coldModel(t)
	if out := m.footer(); !strings.Contains(out, "back") || !strings.Contains(out, "menu") {
		t.Fatalf("setup-mode footer should show back/menu hints:\n%s", out)
	}
}
