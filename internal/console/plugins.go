package console

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coullworks/keel/internal/plugins"
	"github.com/coullworks/keel/internal/pluginstore"
	"github.com/coullworks/keel/internal/tui"
)

// pluginFlow is the Plugins screen: everything keel knows about, where each one
// came from, whether it is on, and what is wrong with any that will not load.
//
// It lists all three kinds — compiled into this build, installed under the
// plugins directory, and keel-<name> executables on PATH — from the same builder
// the CLI listing uses. It used to read only the plugins directory, so a stock
// build with three plugins compiled in reported "nothing installed yet" and
// offered no way to add one.
type pluginFlow struct {
	rows []tui.PluginRow
	cur  int
	err  string
	note string

	// adding is the install prompt, and src is the folder or repository typed
	// into it. Installing was a command to retype somewhere else until now.
	adding bool
	src    string
}

func newPluginFlow() *pluginFlow {
	f := &pluginFlow{}
	f.reload()
	return f
}

// reload re-reads the registry as well as the directory.
//
// The registry is deliberately not cached for the life of the console the way
// the CLI caches it: plugins.Load scans what is installed, and installing one
// from this screen has to change what the screen then shows.
func (f *pluginFlow) reload() {
	f.err = ""
	if _, err := pluginstore.List(); err != nil {
		f.err = err.Error()
	}
	f.rows = plugins.Rows(plugins.Load())
	if f.cur >= len(f.rows) {
		f.cur = max(0, len(f.rows)-1)
	}
}

// installable reports whether the highlighted row is one keel can act on. A
// built-in cannot be removed or disabled — it is part of the binary — and an
// executable on PATH is not keel's to manage either.
func (f *pluginFlow) installable() (tui.PluginRow, bool) {
	if f.cur >= len(f.rows) {
		return tui.PluginRow{}, false
	}
	r := f.rows[f.cur]
	return r, r.Where == "installed"
}

func (f *pluginFlow) update(msg tea.KeyMsg) (Action, bool) {
	if f.adding {
		return f.updateAdd(msg)
	}
	switch msg.String() {
	case "up", "k":
		if f.cur > 0 {
			f.cur--
		}
	case "down", "j":
		if f.cur < len(f.rows)-1 {
			f.cur++
		}
	case "a":
		f.adding, f.src, f.note = true, "", ""
	case " ", "enter":
		r, ok := f.installable()
		if !ok {
			f.note = "only installed plugins can be turned on and off"
			break
		}
		if r.Problem != "" {
			f.note = "cannot enable a plugin that does not load"
			break
		}
		if err := pluginstore.SetEnabled(r.Name, r.State != "enabled"); err != nil {
			f.note = err.Error()
		} else {
			f.note = ""
		}
		f.reload()
	case "x", "delete":
		r, ok := f.installable()
		if !ok {
			f.note = "only installed plugins can be removed"
			break
		}
		if err := pluginstore.Remove(r.Name); err != nil {
			f.note = err.Error()
		} else {
			f.note = "removed " + r.Name
		}
		f.reload()
	case "r":
		f.reload()
		f.note = "rescanned"
	case "esc", "left", "h":
		return Action{}, true
	}
	return Action{}, false
}

// updateAdd handles the install prompt. The install itself goes out as an action
// so it runs on the real terminal: fetching a repository is network work with
// output and errors worth seeing.
func (f *pluginFlow) updateAdd(msg tea.KeyMsg) (Action, bool) {
	switch msg.String() {
	case "enter":
		src := strings.TrimSpace(f.src)
		if src == "" {
			f.note = "give a folder or owner/repo"
			return Action{}, false
		}
		f.adding, f.src, f.note = false, "", ""
		return Action{Kind: "argv", Argv: []string{"plugins", "add", src}}, false
	case "esc":
		f.adding, f.src = false, ""
	case "backspace":
		if r := []rune(f.src); len(r) > 0 {
			f.src = string(r[:len(r)-1])
		} else {
			f.adding = false
		}
	default:
		if s := msg.String(); len(s) == 1 {
			f.src += s
		}
	}
	return Action{}, false
}

func (f *pluginFlow) view(w int) string {
	var b strings.Builder
	b.WriteString(mainTitle.Render("Plugins") + "\n")

	if f.adding {
		b.WriteString(note(w, "A folder, an owner/repo, or a clone URL. keel copies the files and reads its manifest; it does not run the plugin's code to install it.") + "\n")
		b.WriteString("  " + styHead.Render(f.src) + styDim.Render("▌") + "\n\n")
		if f.note != "" {
			b.WriteString(styHead.Render(f.note) + "\n\n")
		}
		b.WriteString(styDim.Render("enter install · esc cancel"))
		return b.String()
	}

	b.WriteString(note(w, "Plugins add commands, wizard steps and studio screens. Built-in ones ship with keel; installed ones live in the plugins directory.") + "\n")

	if f.err != "" {
		b.WriteString(styHead.Render("could not read the plugins directory: ") + f.err + "\n\n")
	}
	if len(f.rows) == 0 {
		b.WriteString(note(w, "No plugins at all, which means this build has none compiled in either.") + "\n")
		b.WriteString(styDim.Render("a install one · esc back"))
		return b.String()
	}

	built, inst := 0, 0
	for _, r := range f.rows {
		if r.BuiltIn {
			built++
		} else if r.Where == "installed" {
			inst++
		}
	}
	b.WriteString(styDim.Render(fmt.Sprintf("%d built in · %d installed · %d on PATH", built, inst, len(f.rows)-built-inst)) + "\n\n")

	// What is left for the description once the fixed columns and the row's own
	// indent are paid for. Without this the description ran past the panel and
	// wrapped back to column zero, which put the next plugin's name halfway
	// through the previous plugin's sentence.
	//
	// The trailing 6 is the highlighted row's padding: navSel adds two columns
	// either side of the space this code puts inside it, so the selected line is
	// six wider than a plain one and is the one that has to fit.
	const fixed = 14 + 1 + 9 + 1 + 10 + 1 + 6
	rest := w - fixed
	// At a narrow panel there is no room for a description at all: an 80-column
	// terminal leaves 52 here, and the columns alone want 42. Dropping the
	// description beats running past the frame, and the name, where and state are
	// what the table is for.
	showDesc := rest >= 12

	for i, r := range f.rows {
		// Where it came from decides what you can do with it, so it is a column
		// rather than something to infer from the description.
		where := r.Where
		if where != "built-in" && where != "installed" {
			where = "on PATH"
		}
		// The state labels are ASCII, so a plain width verb lines them up and
		// the style is applied after padding, not before: styling first would
		// pad the escape sequences.
		state := styPill.Render(fmt.Sprintf("%-10s", r.State))
		switch r.State {
		case "disabled":
			state = styDim.Render(fmt.Sprintf("%-10s", r.State))
		case "not loaded":
			state = styWord.Render(fmt.Sprintf("%-10s", r.State))
		}
		head := fmt.Sprintf("%-14s %-9s ", tui.Truncate(r.Name, 14), where) + state
		line := head
		if showDesc {
			line += " " + tui.Truncate(r.Description, rest)
			if r.Problem != "" {
				line = head + " " + styDim.Render(tui.Truncate(r.Problem, rest))
			}
		}
		if i == f.cur {
			b.WriteString(navSel.Render(" "+line+" ") + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	b.WriteString("\n" + styDim.Render(trimTo(pluginstore.Dir(), w)) + "\n\n")
	if f.note != "" {
		b.WriteString(styHead.Render(f.note) + "\n\n")
	}
	b.WriteString(styDim.Render(trimTo("a install · space on/off · x remove · r rescan · esc back", w)))
	return b.String()
}

// areaPlugins is the overview shown before you enter the screen. It counts the
// rows the model already read, rather than rescanning on every repaint.
func areaPlugins(m *model, w, h int) string {
	rows := m.pluginRows
	built, inst := 0, 0
	for _, r := range rows {
		if r.BuiltIn {
			built++
		} else if r.Where == "installed" {
			inst++
		}
	}
	return h1("Plugins") +
		desc(w, "Plugins extend keel with commands, wizard steps and studio screens. Some ship with keel; others you install from a folder or a git repository, and are discovered by scanning.") +
		styHead.Render(fmt.Sprintf("%d built in", built)) +
		styDim.Render(fmt.Sprintf("  ·  %d installed  ·  %d on PATH", inst, len(rows)-built-inst)) + "\n\n" +
		note(w, "Press enter to manage them, including installing one.")
}
