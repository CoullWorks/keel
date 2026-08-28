// Package console is keel's full-screen, multi-panel terminal UI and the ONE
// shell for every screen — hero bar · left-nav sidebar · swappable main panel ·
// footer (contextual keys + a "Support keel" CTA). Even first-run "set your
// defaults" renders inside the main panel; nothing bypasses the frame. Built on
// bubbletea v1 + lipgloss. See docs/research/v1-tui-console.md.
package console

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/coullworks/keel/internal/art"
	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/pack"
	"github.com/coullworks/keel/internal/plugins"
	"github.com/coullworks/keel/internal/profile"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/selfupdate"
	"github.com/coullworks/keel/internal/tui"
)

const sponsorURL = "github.com/sponsors/coullworks"

// Version and Repo are set by the CLI at start-up. The console does not own
// keel's version number; it only needs one to know whether a newer release
// exists.
var (
	Version = "dev"
	Repo    = "coullworks/keel"
)

var (
	// The brand accent is single-sourced from the shared theme (tui.Orange) so it
	// never drifts from the rest of keel. The remaining shades are tuned for the
	// console surface — a full-screen interactive TUI, not the rendered tables
	// tui/theme.go styles — and stay local by design (different surface, different
	// reason to change), rather than being force-merged into the table palette.
	cOrange = tui.Orange
	cHead   = lipgloss.Color("#e7ebf3")
	cDim    = lipgloss.Color("#7f8794")
	cLine   = lipgloss.Color("#2a2f3a")
	cSelBg  = lipgloss.Color("#1e2430")
	cGreen  = lipgloss.Color("#4ade80")

	styWord  = lipgloss.NewStyle().Foreground(cOrange).Bold(true)
	styDim   = lipgloss.NewStyle().Foreground(cDim)
	styHead  = lipgloss.NewStyle().Foreground(cHead)
	styCrumb = lipgloss.NewStyle().Foreground(cDim)
	styPill  = lipgloss.NewStyle().Foreground(cGreen)

	navItem = lipgloss.NewStyle().Foreground(cHead).Padding(0, 2)
	navSel  = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Background(cSelBg).Bold(true).Padding(0, 2)
	navBar  = lipgloss.NewStyle().Foreground(cOrange)

	mainTitle = lipgloss.NewStyle().Foreground(cHead).Bold(true)
	sponsor   = lipgloss.NewStyle().Foreground(cOrange)
	keyStyle  = lipgloss.NewStyle().Foreground(cHead)
	keyDesc   = lipgloss.NewStyle().Foreground(cDim)
)

type choice struct{ key, label string }

var editors = []choice{
	{"code", "VS Code"}, {"pstorm", "PhpStorm"}, {"cursor", "Cursor"},
	{"zed", "Zed"}, {"nvim", "Neovim"}, {"subl", "Sublime Text"}, {"", "None"},
}

// hostingChoices mirrors the studio onboarding's hosting options (single source
// in the profile package) so both onboardings offer the same targets.
var hostingChoices = func() []choice {
	out := make([]choice, 0, len(profile.HostingOptions))
	for _, h := range profile.HostingOptions {
		out = append(out, choice{h.Key, h.Label})
	}
	return out
}()

// setupStepKey is the onboarding order (same fields as the studio wizard):
// who you are, where projects go, your default stack, then default hosting.
var setupStepKey = []string{"name", "projects_dir", "framework", "env", "database", "editor", "hosting"}

// setupTextSteps are free-text (not pick-a-choice) steps.
var setupTextSteps = map[string]bool{"name": true, "projects_dir": true}

type area struct {
	key, icon, title string
	render           func(m *model, w, h int) string
}

func areaList() []area {
	return []area{
		{"projects", "\uf07b", "Projects", areaProjects},
		{"build", "\uf0ad", "Build a stack", areaBuild},
		{"generate", "\uf0d0", "Generate", areaGenerate},
		{"data", "\uf1c0", "Data", areaData},
		{"logs", "\uf120", "Run & Logs", areaLogs},
		{"packs", "\uf1b2", "Packs", areaPacks},
		{"plugins", "\uf1e6", "Plugins", areaPlugins},
		{"settings", "\uf013", "Settings", areaSettings},
	}
}

// devIcon returns a Nerd Font glyph for a framework/service/db by keyword.
// (Requires a Nerd Font in the terminal — standard for dev setups.)
func devIcon(id, label string) string {
	s := strings.ToLower(id + " " + label)
	for _, p := range [][2]string{
		{"woo", "\uf19a"}, {"wordpress", "\uf19a"},
		{"magento", "\uf07a"}, {"laravel", "\ue73f"}, {"django", "\ue71d"}, {"next", "\ue74e"},
		{"fastapi", "\ue73c"}, {"node", "\ue718"},
		{"docker", "\ue7b0"}, {"ddev", "\ue7b0"}, {"sail", "\ue7b0"}, {"local", "\uf120"},
		{"postgres", "\ue76e"}, {"mysql", "\ue704"}, {"mariadb", "\ue704"}, {"sqlite", "\ue7c4"}, {"redis", "\ue76d"},
		{"opensearch", "\uf002"}, {"elastic", "\uf002"}, {"meili", "\uf002"}, {"minio", "\uf1c0"},
		{"rabbit", "\uf0e7"}, {"mongo", "\ue7a4"}, {"nginx", "\ue776"}, {"apache", "\uf48c"},
		{"varnish", "\uf0a0"}, {"php", "\ue73d"}, {"python", "\ue73c"},
	} {
		if strings.Contains(s, p[0]) {
			return p[1]
		}
	}
	return "\uf111"
}

type model struct {
	w, h  int
	nav   int // selected sidebar area
	focus int // 0 = sidebar, 1 = main
	areas []area

	reg         *recipe.Registry
	prof        *profile.Profile
	recipeCount int
	pluginRows  []tui.PluginRow
	packCount   int

	// first-run / edit "set your defaults" stepper (lives in the main panel)
	setup     bool
	setupStep int
	setupCur  int
	setupSel  map[string]string
	setupText string // buffer for the current free-text step (name, projects_dir)

	// flow is the interactive screen running inside the main panel. Nil means the
	// area is showing its overview. This is what makes an area something you can
	// act in rather than a page describing commands to retype.
	flow panelFlow

	// action is what is running or last ran, busy is set while it holds the
	// terminal, and status is what it left behind. The console stays alive
	// throughout: an action happens inside keel, not on the way out of it.
	action Action
	busy   bool
	status string

	// upgrade is the "a newer keel exists" line, or "" when there is nothing to
	// say. Rendered in the footer rather than printed, because this program owns
	// the screen.
	upgrade string

	cmd      bool
	cmdBuf   string
	showHelp bool
	quitting bool
}

// New builds the console. On first run (no profile) it opens on Settings with the
// set-your-defaults stepper active, inside the frame.
func New() model {
	m := model{areas: areaList(), setupSel: map[string]string{}}
	m.reload()
	// The same once-a-day cached check the CLI uses. It reads a file, so it
	// cannot delay the console opening.
	m.upgrade = selfupdate.Notice(Repo, Version)
	if !profile.Exists() {
		m.nav = m.indexOf("settings")
		m.focus = 1
		m.setup = true
		m.loadStepText()
	}
	return m
}

// reload re-reads everything the console renders from disk. It runs at start-up
// and again after any action, because a build adds a project and installing a
// pack or a plugin changes the catalogue: the screens should show what is there
// now, not what was there when the console opened.
func (m *model) reload() {
	if reg, err := catalog.Registry(); err == nil {
		m.reg = reg
		m.recipeCount = reg.Len()
	}
	if reg, err := pack.Load(); err == nil {
		m.packCount = len(reg.Packs)
	}
	m.prof, _ = profile.Load()
	m.pluginRows = plugins.Rows(plugins.Load())
	if m.flow != nil {
		m.flow.reload()
	}
}

func (m model) Init() tea.Cmd { return nil }
func (m model) cur() area     { return m.areas[m.nav] }

func (m model) indexOf(key string) int {
	for i, a := range m.areas {
		if a.key == key || strings.EqualFold(a.title, key) {
			return i
		}
	}
	return -1
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case actionDoneMsg:
		// Back in the console with the terminal returned. A build adds a
		// project and a pack or plugin action changes the counts, so what the
		// screens show is re-read rather than left stale.
		m.busy = false
		m.status = actionSummary(msg.act)
		m.reload()
		return m, nil
	case tea.MouseMsg:
		return m.updateMouse(msg)
	case tea.KeyMsg:
		if m.cmd {
			return m.updateCmd(msg)
		}
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case ":", "ctrl+k":
			m.cmd, m.cmdBuf = true, ""
			return m, nil
		case "?":
			m.showHelp = !m.showHelp
			return m, nil
		case "tab":
			m.focus = 1 - m.focus
			return m, nil
		}
		return m.routeKey(msg)
	}
	return m, nil
}

// updateMouse handles the pointer. The console asks the terminal for mouse
// reporting, so every click it ignores is a click the user believes worked.
//
// A click on the sidebar used to move the highlight and stop there, and a click
// anywhere in the main panel did nothing at all: the only way into a screen was
// the keyboard, on a UI that had spent the whole session tracking the mouse.
func (m model) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		return m.routeKey(fakeKey("up"))
	case tea.MouseButtonWheelDown:
		return m.routeKey(fakeKey("down"))
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}
	row := msg.Y - m.heroHeight() - 1
	if msg.X < m.sidebarWidth() {
		if row < 0 || row >= len(m.areas) {
			return m, nil
		}
		// A click means open, not merely highlight. Clicking the area you are
		// already in re-opens it, which is what a second click reads as.
		m.nav = row
		return m.routeKey(fakeKey("enter"))
	}
	// In the panel: give it focus so the wheel and the keys act on the screen
	// being looked at rather than on the sidebar behind it.
	if m.flow != nil || m.setup {
		m.focus = 1
	}
	return m, nil
}

// fakeKey makes the keypress a mouse gesture stands for, so a click and Enter
// take exactly one path instead of two that drift.
func fakeKey(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// routeKey dispatches a keypress to whatever has focus. Clicks and wheel
// gestures come through here too, so a mouse and the keyboard take one path
// rather than two that drift apart.
func (m model) routeKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.focus == 1 && m.flow != nil {
		act, closed := m.flow.update(k)
		if closed {
			m.flow, m.focus = nil, 0
		}
		// An action runs inside the console, on the real terminal, and the
		// console comes back afterwards. It used to quit and let the CLI run the
		// action on the way out, so picking anything ended the session.
		if act.Kind != "" {
			m.action, m.busy, m.status = act, true, ""
			return m, runAction(act)
		}
		return m, nil
	}
	if m.focus == 1 && m.setup {
		return m.updateSetup(k), nil
	}
	return m.updateSidebar(k), nil
}

func (m model) updateSidebar(msg tea.KeyMsg) model {
	switch msg.String() {
	case "up", "k":
		if m.nav > 0 {
			m.nav--
		}
	case "down", "j":
		if m.nav < len(m.areas)-1 {
			m.nav++
		}
	case "enter", "right", "l":
		m.focus = 1
		switch m.cur().key {
		case "settings":
			m.setup, m.setupStep, m.setupCur = true, 0, 0
			m.loadStepText()
		case "build":
			// The same steps `keel new` walks, rendered in this panel.
			if m.reg != nil {
				m.flow = newBuildFlow(tui.BuildSteps(m.reg, m.prof))
			}
		case "plugins":
			m.flow = newPluginFlow()
		case "projects":
			m.flow = newProjectFlow()
		case "generate", "data", "logs", "packs":
			if f := areaTasks(m.cur().key); f != nil {
				m.flow = f
			}
		}
	default:
		if len(msg.String()) == 1 {
			if n := int(msg.String()[0]) - '1'; n >= 0 && n < len(m.areas) {
				m.nav = n
			}
		}
	}
	return m
}

func (m model) isTextStep() bool { return setupTextSteps[setupStepKey[m.setupStep]] }

// loadStepText seeds the text buffer for the current step from a prior answer or
// the existing profile, so editing pre-fills instead of starting blank.
func (m *model) loadStepText() {
	key := setupStepKey[m.setupStep]
	if !setupTextSteps[key] {
		return
	}
	if v, ok := m.setupSel[key]; ok {
		m.setupText = v
		return
	}
	if m.prof != nil {
		if key == "name" {
			m.setupText = m.prof.Git.Name
		} else {
			m.setupText = m.prof.Defaults[key]
		}
		return
	}
	m.setupText = ""
}

func (m *model) advanceSetup() {
	if m.setupStep >= len(setupStepKey)-1 {
		m.saveSetup()
		return
	}
	m.setupStep++
	m.setupCur = 0
	m.loadStepText()
}

func (m *model) backSetup() {
	if m.setupStep > 0 {
		m.setupStep--
		m.setupCur = 0
		m.loadStepText()
		return
	}
	m.closeSetup()
}

// closeSetup leaves the defaults stepper without saving, putting Settings back
// to showing the defaults as they stand.
//
// Clearing m.setup is the whole point: leaving it set meant the panel kept
// rendering the stepper and the sidebar kept calling the area "Set defaults"
// after esc, with no key that would put either back.
func (m *model) closeSetup() {
	m.setup, m.setupStep, m.setupCur = false, 0, 0
	m.setupText = ""
	m.focus = 0
}

func (m model) updateSetup(msg tea.KeyMsg) model {
	if m.isTextStep() {
		return m.updateSetupText(msg)
	}
	_, opts := m.stepOptions()
	switch msg.String() {
	case "up", "k":
		if m.setupCur > 0 {
			m.setupCur--
		}
	case "down", "j":
		if m.setupCur < len(opts)-1 {
			m.setupCur++
		}
	case "enter", "right", " ":
		if len(opts) > 0 {
			m.setupSel[setupStepKey[m.setupStep]] = opts[m.setupCur].key
		}
		m.advanceSetup()
	case "left", "backspace", "h":
		m.backSetup()
	case "esc":
		m.closeSetup()
	}
	return m
}

// updateSetupText handles a free-text step (name, projects folder).
func (m model) updateSetupText(msg tea.KeyMsg) model {
	switch msg.String() {
	case "enter":
		m.setupSel[setupStepKey[m.setupStep]] = strings.TrimSpace(m.setupText)
		m.advanceSetup()
	case "esc":
		m.backSetup()
	case "backspace":
		if r := []rune(m.setupText); len(r) > 0 {
			m.setupText = string(r[:len(r)-1])
		} else {
			m.backSetup()
		}
	case "ctrl+u":
		m.setupText = ""
	default:
		if s := msg.String(); len(s) == 1 || s == " " {
			m.setupText += s
		}
	}
	return m
}

func (m *model) saveSetup() {
	p := m.prof
	if p == nil {
		p, _ = profile.Load() // returns a usable profile
	}
	if p.Defaults == nil {
		p.Defaults = map[string]string{}
	}
	for k, v := range m.setupSel {
		if k == "name" {
			p.Git.Name = v
			continue
		}
		p.Defaults[k] = v
	}
	_ = p.Save()
	m.prof = p
	m.setup = false
	m.focus = 0
}

// stepOptions returns the current setup step's title + options, filtered so env/db
// depend on the chosen framework.
func (m model) stepOptions() (string, []choice) {
	fw := m.setupSel["framework"]
	toChoices := func(rs []recipe.Recipe) []choice {
		out := make([]choice, 0, len(rs))
		for _, r := range rs {
			out = append(out, choice{r.ID, r.Label})
		}
		return out
	}
	if m.reg == nil {
		return "", nil
	}
	switch setupStepKey[m.setupStep] {
	case "name":
		return "Your name (git author)", nil
	case "projects_dir":
		return "Default projects folder  (~ ok · blank = current dir)", nil
	case "framework":
		return "Default framework", toChoices(m.reg.OfKind(recipe.Framework))
	case "env":
		return "Default local dev environment", toChoices(m.reg.ForFramework(fw, recipe.Env))
	case "database":
		return "Default database", toChoices(m.reg.ForFramework(fw, recipe.DB))
	case "editor":
		return "Editor", editors
	case "hosting":
		return "Default hosting / deploy target", hostingChoices
	}
	return "", nil
}

func (m model) updateCmd(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.cmd = false
	case "enter":
		cmd := strings.TrimSpace(m.cmdBuf)
		m.cmd = false
		switch cmd {
		case "q", "quit", "exit":
			m.quitting = true
			return m, tea.Quit
		case "sponsor":
			m.nav = m.indexOf("settings")
		default:
			if i := m.indexOf(cmd); i >= 0 {
				m.nav, m.focus = i, 1
			}
		}
	case "backspace":
		if len(m.cmdBuf) > 0 {
			m.cmdBuf = m.cmdBuf[:len(m.cmdBuf)-1]
		}
	default:
		if len(msg.String()) == 1 {
			m.cmdBuf += msg.String()
		}
	}
	return m, nil
}

func (m model) w0() int {
	if m.w > 0 {
		return m.w
	}
	return 96
}
func (m model) h0() int {
	if m.h > 0 {
		return m.h
	}
	return 30
}
func (m model) sidebarWidth() int { return 26 }
func (m model) heroHeight() int   { return lipgloss.Height(m.hero()) }

func (m model) hero() string {
	logo := art.Anchor(16)
	right := "\n" + styWord.Render(" keel") + styDim.Render("  console") + "\n" +
		" " + styCrumb.Render("keel › "+m.cur().title) + "\n" +
		" " + styPill.Render("● ready") + "\n"
	bar := lipgloss.JoinHorizontal(lipgloss.Top, logo, right)
	return bar + "\n" + navBar.Render(strings.Repeat("─", m.w0()))
}

func (m model) sidebar(h int) string {
	var b strings.Builder
	// The sidebar names the eight areas and nothing else. It used to relabel
	// Settings as "Set defaults" while the stepper was open, so the one fixed
	// landmark on the screen moved under you.
	for i, a := range m.areas {
		row := fmt.Sprintf("%s  %s", a.icon, a.title)
		if i == m.nav {
			b.WriteString(navBar.Render("▸") + navSel.Render(row) + "\n")
		} else {
			b.WriteString(" " + navItem.Render(row) + "\n")
		}
	}
	return lipgloss.NewStyle().Width(m.sidebarWidth()).Height(h).
		BorderStyle(lipgloss.NormalBorder()).BorderRight(true).BorderForeground(cLine).Render(b.String())
}

func (m model) footer() string {
	var left string
	switch {
	case m.cmd:
		left = " " + styWord.Render(":") + m.cmdBuf + styDim.Render("▌")
	case m.busy:
		left = " " + styWord.Render("● ") + keyStyle.Render("running "+actionSummary(m.action))
	case m.status != "":
		// What the last action did, until the next keypress moves on.
		left = " " + styPill.Render("✓ ") + keyDesc.Render(m.status) + styDim.Render("  ·  ") +
			keyStyle.Render("q") + keyDesc.Render(" quit")
	case m.focus == 1 && m.setup:
		left = " " + keyStyle.Render("↑↓") + keyDesc.Render(" choose") + styDim.Render("  ·  ") +
			keyStyle.Render("enter") + keyDesc.Render(" next") + styDim.Render("  ·  ") +
			keyStyle.Render("←") + keyDesc.Render(" back") + styDim.Render("  ·  ") +
			keyStyle.Render("esc") + keyDesc.Render(" menu")
	default:
		left = " " + keyStyle.Render("↑↓/jk") + keyDesc.Render(" move") + styDim.Render("  ·  ") +
			keyStyle.Render("enter") + keyDesc.Render(" open") + styDim.Render("  ·  ") +
			keyStyle.Render(":") + keyDesc.Render(" cmd") + styDim.Render("  ·  ") +
			keyStyle.Render("?") + keyDesc.Render(" help") + styDim.Render("  ·  ") +
			keyStyle.Render("q") + keyDesc.Render(" quit")
	}
	// An available upgrade displaces the sponsor line: it is more useful, and
	// two competing calls to action in one footer is one too many.
	right := sponsor.Render("♥ Support keel") + styDim.Render("  "+sponsorURL) + " "
	short := sponsor.Render("♥ Support keel") + " "
	if m.upgrade != "" {
		right = styWord.Render("↑ ") + styHead.Render(m.upgrade) + " "
		short = right
	}
	// The two halves are dropped back rather than allowed to collide.
	//
	// The gap used to be clamped to two and the line printed anyway, so at 100
	// columns the keys, the heart and the sponsors URL came to 107. lipgloss
	// pads every other line of the frame out to the widest one, so one long
	// footer widened the whole console past the terminal and wrapped it.
	fit := func(r string) int { return m.w0() - lipgloss.Width(left) - lipgloss.Width(r) }
	if fit(right) < 2 {
		right = short
	}
	if fit(right) < 2 {
		right = "" // the keys matter more than the plea
	}
	gap := fit(right)
	if gap < 0 {
		gap = 0
	}
	return navBar.Render(strings.Repeat("─", m.w0())) + "\n" + left + strings.Repeat(" ", gap) + right
}

func (m model) View() string {
	if m.quitting {
		return ""
	}
	hero := m.hero()
	footer := m.footer()
	bodyH := m.h0() - lipgloss.Height(hero) - lipgloss.Height(footer)
	if bodyH < 3 {
		bodyH = 3
	}
	mainW := m.w0() - m.sidebarWidth() - 2
	// An active flow owns the panel: the area's overview is what you see before
	// you enter, not while you are answering it.
	main := m.cur().render(&m, mainW, bodyH)
	if m.flow != nil {
		main = m.flow.view(mainW)
	}
	mainPanel := lipgloss.NewStyle().Width(mainW).Height(bodyH).Padding(0, 2).Render(main)
	body := lipgloss.JoinHorizontal(lipgloss.Top, m.sidebar(bodyH), mainPanel)
	if m.showHelp {
		return lipgloss.JoinVertical(lipgloss.Left, hero, body, helpBar(m.w0()))
	}
	return lipgloss.JoinVertical(lipgloss.Left, hero, body, footer)
}

func helpBar(w int) string {
	return navBar.Render(strings.Repeat("─", w)) + "\n" +
		styHead.Render(" keys  ") + styDim.Render(trimTo("↑↓/jk move · 1-8 jump · tab focus · enter open · : command · ? help · q quit", w-8))
}

// Run launches the console (full-screen, mouse) and returns when the user quits
// it.
//
// Everything the user picks runs inside this call, through Runner: the console
// used to return an Action for `keel console` to perform on the way out, which
// meant choosing anything at all ended the session.
func Run() error {
	_, err := tea.NewProgram(New(), tea.WithAltScreen(), tea.WithMouseAllMotion()).Run()
	return err
}
