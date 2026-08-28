package console

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// newModel builds a console model with a saved (non-cold-start) profile and a
// sane size, so tests drive the navigable UI rather than the onboarding stepper.
// KEEL_CONFIG_DIR is pointed at a temp dir with a profile already written.
func newModel(t *testing.T) model {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("KEEL_CONFIG_DIR", dir)
	// Write a profile so Exists() is true => no cold-start onboarding.
	if err := writeMinimalProfile(dir); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	m := New()
	m.w, m.h = 100, 32
	return m
}

func writeMinimalProfile(dir string) error {
	return writeFile(dir+"/profile.yaml",
		"defaults:\n  framework: laravel\n  env: ddev\n  database: postgres\n  editor: code\n  hosting: compose\ngit:\n  name: Dan\n")
}

func send(m model, msg tea.Msg) model {
	next, _ := m.Update(msg)
	return next.(model)
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "ctrl+k":
		return tea.KeyMsg{Type: tea.KeyCtrlK}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func TestInitReturnsNil(t *testing.T) {
	if cmd := newModel(t).Init(); cmd != nil {
		t.Fatalf("Init should be nil, got %T", cmd)
	}
}

func TestWindowSizeStored(t *testing.T) {
	m := newModel(t)
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	if m.w != 120 || m.h != 40 {
		t.Fatalf("window size not stored: %dx%d", m.w, m.h)
	}
}

func TestQuitKeys(t *testing.T) {
	for _, k := range []string{"q", "ctrl+c"} {
		m := newModel(t)
		next, cmd := m.Update(key(k))
		if !next.(model).quitting {
			t.Fatalf("%q did not set quitting", k)
		}
		if cmd == nil {
			t.Fatalf("%q did not return a quit cmd", k)
		}
	}
	// Quitting hides the whole view.
	m := newModel(t)
	m.quitting = true
	if m.View() != "" {
		t.Fatal("a quitting model should render an empty view")
	}
}

func TestHelpToggle(t *testing.T) {
	m := newModel(t)
	m = send(m, key("?"))
	if !m.showHelp {
		t.Fatal("? should toggle help on")
	}
	if v := m.View(); !strings.Contains(v, "jump") || !strings.Contains(v, "command") {
		t.Fatalf("help bar not rendered:\n%s", v)
	}
	m = send(m, key("?"))
	if m.showHelp {
		t.Fatal("? should toggle help off")
	}
}

func TestTabFlipsFocus(t *testing.T) {
	m := newModel(t)
	if m.focus != 0 {
		t.Fatalf("initial focus=%d want 0", m.focus)
	}
	m = send(m, key("tab"))
	if m.focus != 1 {
		t.Fatalf("tab focus=%d want 1", m.focus)
	}
	m = send(m, key("tab"))
	if m.focus != 0 {
		t.Fatalf("tab back focus=%d want 0", m.focus)
	}
}

func TestSidebarNavigation(t *testing.T) {
	m := newModel(t)
	// down/j move the nav selection; clamps at ends.
	m = send(m, key("down"))
	if m.nav != 1 {
		t.Fatalf("down nav=%d want 1", m.nav)
	}
	m = send(m, key("j"))
	if m.nav != 2 {
		t.Fatalf("j nav=%d want 2", m.nav)
	}
	m = send(m, key("up"))
	if m.nav != 1 {
		t.Fatalf("up nav=%d want 1", m.nav)
	}
	m = send(m, key("k"))
	if m.nav != 0 {
		t.Fatalf("k nav=%d want 0", m.nav)
	}
	// up at the top is a no-op.
	m = send(m, key("up"))
	if m.nav != 0 {
		t.Fatalf("up at top nav=%d want 0", m.nav)
	}
	// jump to the last area and try to go past it.
	last := len(m.areas) - 1
	for i := 0; i < last+3; i++ {
		m = send(m, key("down"))
	}
	if m.nav != last {
		t.Fatalf("down clamps at nav=%d want %d", m.nav, last)
	}
}

func TestNumberJump(t *testing.T) {
	m := newModel(t)
	m = send(m, key("3"))
	if m.nav != 2 {
		t.Fatalf("'3' nav=%d want 2", m.nav)
	}
	// Out-of-range digit is ignored (only 1..len(areas)).
	m = send(m, key("9"))
	if m.nav != 2 {
		t.Fatalf("out-of-range digit moved nav to %d", m.nav)
	}
}

func TestEnterOpensAreaAndFocusesMain(t *testing.T) {
	m := newModel(t)
	m = send(m, key("2")) // Build a stack
	m = send(m, key("enter"))
	if m.focus != 1 {
		t.Fatalf("enter should focus main, focus=%d", m.focus)
	}
	if m.setup {
		t.Fatal("opening a non-settings area should not start setup")
	}
}

func TestEnterOnSettingsStartsSetup(t *testing.T) {
	m := newModel(t)
	m.nav = m.indexOf("settings")
	m = send(m, key("enter"))
	if !m.setup || m.focus != 1 {
		t.Fatalf("enter on Settings should start setup+focus main: setup=%v focus=%d", m.setup, m.focus)
	}
	if m.setupStep != 0 {
		t.Fatalf("setup should reset to step 0, got %d", m.setupStep)
	}
}

func TestEachAreaRenders(t *testing.T) {
	m := newModel(t)
	for i, a := range m.areas {
		m.nav = i
		out := a.render(&m, 60, 20)
		if strings.TrimSpace(out) == "" {
			t.Fatalf("area %q rendered empty", a.key)
		}
	}
	// Spot-check distinctive content per area.
	m.nav = m.indexOf("projects")
	if !strings.Contains(m.cur().render(&m, 60, 20), "keel adopt") {
		t.Fatal("Projects area missing 'keel adopt'")
	}
	m.nav = m.indexOf("build")
	if !strings.Contains(m.cur().render(&m, 60, 20), "keel new") {
		t.Fatal("Build area missing 'keel new'")
	}
	m.nav = m.indexOf("generate")
	if !strings.Contains(m.cur().render(&m, 60, 20), "keel gen") {
		t.Fatal("Generate area missing 'keel gen'")
	}
	m.nav = m.indexOf("data")
	if !strings.Contains(m.cur().render(&m, 60, 20), "keel db migrate") {
		t.Fatal("Data area missing 'keel db migrate'")
	}
	m.nav = m.indexOf("logs")
	if !strings.Contains(m.cur().render(&m, 60, 20), "ddev start") {
		t.Fatal("Logs area missing 'ddev start'")
	}
	m.nav = m.indexOf("packs")
	if !strings.Contains(m.cur().render(&m, 60, 20), "recipe packs") {
		t.Fatal("Packs area missing 'recipe packs'")
	}
}

func TestSettingsAreaShowsSavedDefaults(t *testing.T) {
	m := newModel(t)
	m.nav = m.indexOf("settings")
	out := m.cur().render(&m, 60, 24)
	for _, want := range []string{"Settings", "Framework", "laravel", "Dan", "Support keel"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Settings view missing %q:\n%s", want, out)
		}
	}
}

func TestSettingsAreaUnsetDefaults(t *testing.T) {
	// A model whose profile has no defaults shows "unset"/"current dir".
	dir := t.TempDir()
	t.Setenv("KEEL_CONFIG_DIR", dir)
	if err := writeFile(dir+"/profile.yaml", "defaults: {}\ngit: {}\n"); err != nil {
		t.Fatal(err)
	}
	m := New()
	m.w, m.h = 100, 32
	m.nav = m.indexOf("settings")
	out := m.cur().render(&m, 60, 24)
	if !strings.Contains(out, "unset") {
		t.Fatalf("empty defaults should show 'unset':\n%s", out)
	}
	if !strings.Contains(out, "current dir") {
		t.Fatalf("empty projects_dir should show 'current dir':\n%s", out)
	}
}

func TestIndexOf(t *testing.T) {
	m := newModel(t)
	if got := m.indexOf("build"); got < 0 {
		t.Fatal("indexOf(build) not found")
	}
	// Case-insensitive title match.
	if got := m.indexOf("Settings"); got != m.indexOf("settings") {
		t.Fatal("indexOf title match should equal key match")
	}
	if got := m.indexOf("nope"); got != -1 {
		t.Fatalf("indexOf(nope)=%d want -1", got)
	}
}

func TestDefaultSizeFallback(t *testing.T) {
	m := newModel(t)
	m.w, m.h = 0, 0
	if m.w0() != 96 || m.h0() != 30 {
		t.Fatalf("size fallback w0=%d h0=%d want 96x30", m.w0(), m.h0())
	}
	// View still renders at the fallback size without panicking.
	if v := m.View(); !strings.Contains(v, "keel") {
		t.Fatalf("fallback-size view missing wordmark:\n%s", v)
	}
}

func TestDevIcon(t *testing.T) {
	// Keyword hits return their specific glyph; an unknown id gets the fallback.
	if devIcon("laravel", "Laravel") == devIcon("zzz-unknown", "Nothing") {
		t.Fatal("laravel should not share the fallback glyph")
	}
	if devIcon("postgres", "Postgres") == devIcon("zzz-unknown", "Nothing") {
		t.Fatal("postgres should map to a specific glyph")
	}
	// The fallback is stable.
	if devIcon("aaa", "Bbb") != devIcon("ccc", "Ddd") {
		t.Fatal("unknown ids should all get the same fallback glyph")
	}
}
