package console

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coullworks/keel/internal/profile"
)

// coldModel builds a first-run console model (no profile) sitting in the
// set-your-defaults stepper, focused on the main panel.
func coldModel(t *testing.T) model {
	t.Helper()
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	m := New()
	m.w, m.h = 100, 32
	if !m.setup || m.focus != 1 {
		t.Fatalf("cold start should be in setup with main focus: setup=%v focus=%d", m.setup, m.focus)
	}
	return m
}

func TestSetupTextStepTyping(t *testing.T) {
	m := coldModel(t)
	// Step 0 is "name" (a text step).
	if !m.isTextStep() {
		t.Fatal("step 0 should be a text step (name)")
	}
	m = send(m, key("D"))
	m = send(m, key("a"))
	m = send(m, key("n"))
	if m.setupText != "Dan" {
		t.Fatalf("typed text=%q want Dan", m.setupText)
	}
	// Backspace removes the last rune.
	m = send(m, key("backspace"))
	if m.setupText != "Da" {
		t.Fatalf("after backspace text=%q want Da", m.setupText)
	}
	// ctrl+u clears the buffer.
	m = send(m, key("ctrl+u"))
	if m.setupText != "" {
		t.Fatalf("ctrl+u should clear text, got %q", m.setupText)
	}
	// Space is accepted in a text step.
	m = send(m, key(" "))
	if m.setupText != " " {
		t.Fatalf("space should be typed, text=%q", m.setupText)
	}
}

func TestSetupTextEnterAdvancesAndRecords(t *testing.T) {
	m := coldModel(t)
	m.setupText = "Ada Lovelace"
	m = send(m, key("enter"))
	if m.setupSel["name"] != "Ada Lovelace" {
		t.Fatalf("name not recorded: %q", m.setupSel["name"])
	}
	if m.setupStep != 1 {
		t.Fatalf("enter should advance to step 1, got %d", m.setupStep)
	}
}

func TestSetupTextBackspaceOnEmptyGoesBack(t *testing.T) {
	m := coldModel(t)
	m.setupStep = 1 // projects_dir (text step)
	m.setupText = ""
	m = send(m, key("backspace"))
	if m.setupStep != 0 {
		t.Fatalf("backspace on empty text should step back, got %d", m.setupStep)
	}
}

func TestSetupTextEscGoesBack(t *testing.T) {
	m := coldModel(t)
	m.setupStep = 1
	m.loadStepText()
	m = send(m, key("esc"))
	if m.setupStep != 0 {
		t.Fatalf("esc in text step should go back a step, got %d", m.setupStep)
	}
}

func TestSetupEscAtFirstStepBacksOutToSidebar(t *testing.T) {
	m := coldModel(t)
	// On a text step, esc goes back a step; at step 0 backSetup drops focus to sidebar.
	m = send(m, key("esc"))
	if m.focus != 0 {
		t.Fatalf("esc at first step should drop focus to sidebar, focus=%d", m.focus)
	}
}

func TestSetupChoiceStepNavigateAndSelect(t *testing.T) {
	m := coldModel(t)
	m.setupStep = 2 // framework (choice step)
	m.loadStepText()
	if m.isTextStep() {
		t.Fatal("framework step should be a choice step")
	}
	_, opts := m.stepOptions()
	if len(opts) < 2 {
		t.Fatalf("framework step should have options, got %d", len(opts))
	}
	// down moves the cursor; up clamps at 0.
	m = send(m, key("down"))
	if m.setupCur != 1 {
		t.Fatalf("down cur=%d want 1", m.setupCur)
	}
	// up moves back to 0.
	m = send(m, key("up"))
	if m.setupCur != 0 {
		t.Fatalf("up cur=%d want 0", m.setupCur)
	}
	// up at top clamps.
	m = send(m, key("up"))
	if m.setupCur != 0 {
		t.Fatalf("up at top cur=%d want 0", m.setupCur)
	}
	// enter records the selected framework and advances.
	want := opts[m.setupCur].key
	m = send(m, key("enter"))
	if m.setupSel["framework"] != want {
		t.Fatalf("framework not recorded: got %q want %q", m.setupSel["framework"], want)
	}
	if m.setupStep != 3 {
		t.Fatalf("enter should advance to step 3 (env), got %d", m.setupStep)
	}
}

func TestSetupChoiceDownClampsAtEnd(t *testing.T) {
	m := coldModel(t)
	m.setupStep = 5 // editor (fixed-length choice list)
	m.loadStepText()
	_, opts := m.stepOptions()
	for i := 0; i < len(opts)+3; i++ {
		m = send(m, key("down"))
	}
	if m.setupCur != len(opts)-1 {
		t.Fatalf("down should clamp at last option %d, got %d", len(opts)-1, m.setupCur)
	}
}

func TestSetupChoiceLeftGoesBack(t *testing.T) {
	m := coldModel(t)
	m.setupStep = 2
	m.loadStepText()
	m = send(m, key("left"))
	if m.setupStep != 1 {
		t.Fatalf("left in choice step should go back, got %d", m.setupStep)
	}
}

func TestSetupChoiceEscDropsFocus(t *testing.T) {
	m := coldModel(t)
	m.setupStep = 2
	m.loadStepText()
	m = send(m, key("esc"))
	if m.focus != 0 {
		t.Fatalf("esc in a choice step should drop focus to sidebar, focus=%d", m.focus)
	}
}

func TestSetupFullRunSavesProfile(t *testing.T) {
	m := coldModel(t)
	m.setupSel["name"] = "Grace"
	// Walk every step to the end using enter; text steps take the buffer, choice
	// steps take the current option. Advancing off the last step saves.
	for i := 0; i < len(setupStepKey); i++ {
		if m.isTextStep() {
			m.setupText = "val-" + setupStepKey[m.setupStep]
		}
		m = send(m, key("enter"))
	}
	if m.setup {
		t.Fatal("finishing the last step should end setup")
	}
	// The profile was persisted with the name we set.
	p, err := profile.Load()
	if err != nil {
		t.Fatalf("load saved profile: %v", err)
	}
	if p.Git.Name == "" {
		t.Fatal("saved profile should carry a git name")
	}
	if p.Defaults["framework"] == "" {
		t.Fatal("saved profile should carry a framework default")
	}
}

func TestAdvanceAndBackSetupHelpers(t *testing.T) {
	m := coldModel(t)
	m.advanceSetup()
	if m.setupStep != 1 || m.setupCur != 0 {
		t.Fatalf("advanceSetup step=%d cur=%d want 1,0", m.setupStep, m.setupCur)
	}
	m.backSetup()
	if m.setupStep != 0 {
		t.Fatalf("backSetup step=%d want 0", m.setupStep)
	}
	// backSetup at step 0 drops focus to the sidebar.
	m.backSetup()
	if m.focus != 0 {
		t.Fatalf("backSetup at 0 focus=%d want 0", m.focus)
	}
}

func TestLoadStepTextPrefill(t *testing.T) {
	m := coldModel(t)
	// A prior answer wins.
	m.setupSel["name"] = "Prior"
	m.setupStep = 0
	m.loadStepText()
	if m.setupText != "Prior" {
		t.Fatalf("loadStepText should prefer a prior answer, got %q", m.setupText)
	}
	// projects_dir with no prior answer falls back to the profile default (empty here).
	delete(m.setupSel, "projects_dir")
	m.setupStep = 1
	m.loadStepText()
	// A choice step leaves the buffer untouched (early return).
	m.setupStep = 2
	before := m.setupText
	m.loadStepText()
	if m.setupText != before {
		t.Fatalf("loadStepText on a choice step should not touch the buffer")
	}
}

func TestStepOptionsFrameworkFiltersEnvDB(t *testing.T) {
	m := coldModel(t)
	// With no framework chosen, env/db options may be empty.
	m.setupSel["framework"] = "laravel"
	m.setupStep = 3 // env
	title, envOpts := m.stepOptions()
	if title == "" {
		t.Fatal("env step should have a title")
	}
	m.setupStep = 4 // database
	if _, dbOpts := m.stepOptions(); len(dbOpts) == 0 && len(envOpts) == 0 {
		t.Skip("catalog has no env/db recipes for laravel in this build")
	}
	// editor + hosting are fixed lists.
	m.setupStep = 5
	if _, e := m.stepOptions(); len(e) != len(editors) {
		t.Fatalf("editor options=%d want %d", len(e), len(editors))
	}
	m.setupStep = 6
	if _, h := m.stepOptions(); len(h) != len(hostingChoices) {
		t.Fatalf("hosting options=%d want %d", len(h), len(hostingChoices))
	}
}

func TestStepOptionsNilRegistry(t *testing.T) {
	// stepOptions guards a nil registry (returns empty).
	m := model{}
	if title, opts := m.stepOptions(); title != "" || opts != nil {
		t.Fatalf("nil-reg stepOptions should be empty, got %q %v", title, opts)
	}
}

// --- command palette (":" line) ---

func TestCommandPaletteOpensAndTypes(t *testing.T) {
	m := newModel(t)
	m = send(m, key(":"))
	if !m.cmd {
		t.Fatal("':' should open the command line")
	}
	m = send(m, key("b"))
	m = send(m, key("u"))
	m = send(m, key("i"))
	m = send(m, key("l"))
	m = send(m, key("d"))
	if m.cmdBuf != "build" {
		t.Fatalf("cmdBuf=%q want build", m.cmdBuf)
	}
	// Enter routes to the matching area and closes the palette.
	m = send(m, key("enter"))
	if m.cmd {
		t.Fatal("enter should close the command line")
	}
	if m.nav != m.indexOf("build") {
		t.Fatalf("':build' should jump to Build, nav=%d", m.nav)
	}
}

func TestCommandPaletteCtrlKOpens(t *testing.T) {
	m := newModel(t)
	m = send(m, key("ctrl+k"))
	if !m.cmd {
		t.Fatal("ctrl+k should open the command line")
	}
}

func TestCommandPaletteBackspaceAndEsc(t *testing.T) {
	m := newModel(t)
	m = send(m, key(":"))
	m = send(m, key("a"))
	m = send(m, key("b"))
	m = send(m, key("backspace"))
	if m.cmdBuf != "a" {
		t.Fatalf("backspace cmdBuf=%q want a", m.cmdBuf)
	}
	m = send(m, key("esc"))
	if m.cmd {
		t.Fatal("esc should close the command line")
	}
}

func TestCommandPaletteQuit(t *testing.T) {
	for _, c := range []string{"q", "quit", "exit"} {
		m := newModel(t)
		m = send(m, key(":"))
		for _, r := range c {
			m = send(m, key(string(r)))
		}
		next, cmd := m.Update(key("enter"))
		if !next.(model).quitting || cmd == nil {
			t.Fatalf("':%s' should quit", c)
		}
	}
}

func TestCommandPaletteSponsorJumpsToSettings(t *testing.T) {
	m := newModel(t)
	m = send(m, key(":"))
	for _, r := range "sponsor" {
		m = send(m, key(string(r)))
	}
	m = send(m, key("enter"))
	if m.nav != m.indexOf("settings") {
		t.Fatalf("':sponsor' should jump to Settings, nav=%d", m.nav)
	}
}

func TestCommandPaletteUnknownCommandNoop(t *testing.T) {
	m := newModel(t)
	before := m.nav
	m = send(m, key(":"))
	for _, r := range "zzznope" {
		m = send(m, key(string(r)))
	}
	m = send(m, key("enter"))
	if m.nav != before {
		t.Fatalf("unknown command should not move nav, nav=%d want %d", m.nav, before)
	}
}

// --- mouse ---

// Clicking a sidebar row opens it.
//
// It used to move the highlight and stop, on a UI that asks the terminal for
// mouse reporting and therefore looks clickable: the only way into a screen was
// the keyboard, and a click that highlights without opening reads as a click
// that did not register.
func TestMouseClickSidebarOpensArea(t *testing.T) {
	m := newModel(t)
	row := m.indexOf("plugins")
	y := m.heroHeight() + 1 + row
	m = send(m, tea.MouseMsg{X: 3, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.nav != row {
		t.Fatalf("sidebar click selected %d, want %d", m.nav, row)
	}
	if m.flow == nil || m.focus != 1 {
		t.Fatalf("sidebar click did not open the area: flow=%v focus=%d", m.flow != nil, m.focus)
	}
}

// A click in the main panel gives it focus, so the wheel and the keys act on the
// screen being looked at rather than the sidebar behind it. It must not change
// which area is selected.
func TestMouseClickInThePanelFocusesIt(t *testing.T) {
	m := newModel(t)
	m.nav = m.indexOf("plugins")
	m = send(m, fkey("enter"))
	before := m.nav
	m.focus = 0

	y := m.heroHeight() + 1 + 1
	m = send(m, tea.MouseMsg{X: m.sidebarWidth() + 5, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.nav != before {
		t.Errorf("a panel click changed the selected area to %d", m.nav)
	}
	if m.focus != 1 {
		t.Error("a panel click did not focus the panel")
	}
}

// The wheel moves the selection in whatever has focus.
func TestMouseWheelMovesTheSelection(t *testing.T) {
	m := newModel(t)

	// keel ships no built-in plugins, so the Plugins screen is empty by default —
	// but the panel-scroll half of this test needs at least two rows to move
	// between. Two keel-* executables on PATH give exactly that: Rows() lists any
	// keel-<name> on PATH as a plugin, no fixture files or trust required.
	binDir := t.TempDir()
	for _, name := range []string{"keel-alpha", "keel-beta"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir)

	m.nav = 0
	m = send(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	if m.nav != 1 {
		t.Errorf("wheel down in the sidebar left nav at %d", m.nav)
	}
	m = send(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	if m.nav != 0 {
		t.Errorf("wheel up in the sidebar left nav at %d", m.nav)
	}

	// And inside a screen it moves that screen's cursor, not the sidebar.
	m.nav = m.indexOf("plugins")
	m = send(m, fkey("enter"))
	pf := m.flow.(*pluginFlow)
	if len(pf.rows) < 2 {
		t.Fatalf("the two keel-* PATH plugins should give at least two rows, got %d", len(pf.rows))
	}
	area := m.nav
	m = send(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	if m.flow.(*pluginFlow).cur != 1 {
		t.Errorf("wheel down in the panel left the cursor at %d", m.flow.(*pluginFlow).cur)
	}
	if m.nav != area {
		t.Error("wheel in the panel moved the sidebar as well")
	}
}

func TestHeroHeightPositive(t *testing.T) {
	m := newModel(t)
	if m.heroHeight() <= 0 {
		t.Fatal("heroHeight should be positive")
	}
}

// The sidebar names the same eight areas whatever is happening in the panel.
//
// It used to relabel Settings as "Set defaults" while the stepper was open, so
// the one fixed landmark on the screen moved under you at exactly the moment a
// first-time user is trying to work out where they are.
func TestSidebarKeepsItsLabelsDuringSetup(t *testing.T) {
	m := coldModel(t)
	v := m.View()
	if !m.setup {
		t.Fatal("a cold model should open in setup")
	}
	if !strings.Contains(v, "Settings") {
		t.Errorf("the Settings row lost its name during setup:\n%s", v)
	}
	// The panel still says what it is; the sidebar just does not rename itself.
	if !strings.Contains(v, "Set your defaults") {
		t.Errorf("the setup panel does not say what it is:\n%s", v)
	}
}

// esc leaves the defaults stepper. It used to only drop focus back to the
// sidebar while leaving the stepper mounted, so the panel kept showing it and
// no key would put Settings back to displaying the defaults.
func TestEscLeavesTheSetupStepper(t *testing.T) {
	for _, step := range []int{0, 2} {
		m := coldModel(t)
		m.setupStep = step
		m.focus = 1
		m = send(m, fkey("esc"))
		if m.setup {
			t.Errorf("esc on step %d did not leave the stepper", step)
		}
		if m.focus != 0 {
			t.Errorf("esc on step %d left focus in the panel", step)
		}
		if strings.Contains(m.View(), "Set your defaults") {
			t.Errorf("esc on step %d left the stepper on screen:\n%s", step, m.View())
		}
	}
}
