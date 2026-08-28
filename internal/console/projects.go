package console

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coullworks/keel/internal/project"
)

// projectTask is something keel can do to an existing project. The list is the
// same set the studio puts on each project card, so both front ends offer the
// same verbs rather than one being a subset of the other.
var projectTasks = []struct{ key, label, desc string }{
	{"doctor", "Doctor", "check the host tools this project needs"},
	{"optimize", "Optimize", "scan for secrets, performance and hygiene issues"},
	{"secrets", "Secrets sync", "create .env and generate application keys"},
	{"db", "Database", "run migrations through this project's env"},
	{"deploy", "Deploy", "generate production deploy artefacts"},
	{"forget", "Forget", "remove it from keel's list (the files stay)"},
}

// projectFlow is the Projects screen: pick a project, then pick what to do to
// it. Tasks are handed back as an Action and run after the console exits,
// because each one is a subprocess whose output belongs on the real terminal.
type projectFlow struct {
	items    []project.Project
	cur      int
	choosing bool // false = picking a project, true = picking a task
	task     int
	note     string
	// adding is the "add an existing project" prompt (the console twin of the
	// studio's "Add an existing project" input), and src is the path being typed.
	// On enter it runs `keel track <path>`, which lists the project without
	// adopting it — the same operation the studio's add endpoint performs.
	adding bool
	src    string
}

func newProjectFlow() *projectFlow {
	f := &projectFlow{}
	f.reload()
	return f
}

func (f *projectFlow) reload() {
	reg, err := project.Load()
	if err != nil {
		f.note = err.Error()
		return
	}
	reg.Refresh()
	f.items = reg.Projects
	if f.cur >= len(f.items) {
		f.cur = max(0, len(f.items)-1)
	}
}

func (f *projectFlow) update(msg tea.KeyMsg) (Action, bool) {
	if f.adding {
		return f.updateAdd(msg)
	}
	if f.choosing {
		switch msg.String() {
		case "up", "k":
			if f.task > 0 {
				f.task--
			}
		case "down", "j":
			if f.task < len(projectTasks)-1 {
				f.task++
			}
		case "enter":
			p := f.items[f.cur]
			t := projectTasks[f.task]
			if t.key == "forget" {
				if reg, err := project.Load(); err == nil {
					reg.Remove(p.Path)
					_ = reg.Save()
				}
				f.choosing = false
				f.reload()
				f.note = "forgot " + p.Name
				return Action{}, false
			}
			return Action{Kind: "project", Dir: p.Path, Task: t.key}, true
		case "esc", "left", "h":
			f.choosing = false
		}
		return Action{}, false
	}

	switch msg.String() {
	case "up", "k":
		if f.cur > 0 {
			f.cur--
		}
	case "down", "j":
		if f.cur < len(f.items)-1 {
			f.cur++
		}
	case "enter", "right", "l":
		if len(f.items) > 0 {
			f.choosing, f.task, f.note = true, 0, ""
		}
	case "a":
		// Add an existing project — the console twin of the studio's add input.
		f.adding, f.src, f.note = true, "", ""
	case "r":
		f.reload()
		f.note = "rescanned"
	case "esc", "left", "h":
		return Action{}, true
	}
	return Action{}, false
}

// updateAdd drives the "add an existing project" path prompt. On enter it runs
// `keel track <path>` (list the project, no manifest) and stays in the flow; the
// console reloads the list when the action returns, so the new project appears.
// It mirrors the Plugins area's install prompt so both read the same way.
func (f *projectFlow) updateAdd(msg tea.KeyMsg) (Action, bool) {
	switch msg.String() {
	case "enter":
		src := strings.TrimSpace(f.src)
		if src == "" {
			f.note = "enter a project path"
			return Action{}, false
		}
		f.adding, f.src, f.note = false, "", ""
		return Action{Kind: "argv", Argv: []string{"track", src}}, false
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

func (f *projectFlow) view(w int) string {
	var b strings.Builder
	b.WriteString(mainTitle.Render("Projects") + "\n")

	if f.adding {
		b.WriteString(styHead.Render("Add an existing project") + "\n")
		b.WriteString(styDim.Render("Type or paste a path. keel detects the stack and lists it;\nrun adopt afterwards to make it keel-managed.") + "\n\n")
		b.WriteString("  " + styWord.Render("> ") + f.src + styWord.Render("_") + "\n")
		if f.note != "" {
			b.WriteString("\n" + styDim.Render(f.note) + "\n")
		}
		b.WriteString("\n" + styDim.Render("enter track · esc cancel"))
		return b.String()
	}

	if len(f.items) == 0 {
		b.WriteString(styDim.Render("keel is not tracking any projects yet.") + "\n\n")
		b.WriteString(cmd(w, "a", "add an existing project (track it, like the studio)"))
		b.WriteString(cmd(w, "keel new", "build a new one"))
		return b.String()
	}

	if f.choosing {
		p := f.items[f.cur]
		b.WriteString(styDim.Render(trimTo(p.Path, w)) + "\n\n")
		b.WriteString(styHead.Render("What would you like to do?") + "\n\n")
		for i, t := range projectTasks {
			d := trimTo(t.desc, w-14-8)
			line := fmt.Sprintf("  %-14s %s", t.label, styDim.Render(d))
			if i == f.task {
				line = navSel.Render(fmt.Sprintf(" %-14s %s ", t.label, d))
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n" + styDim.Render("enter run · esc back"))
		return b.String()
	}

	b.WriteString(styDim.Render("Pick a project, then choose what to do to it.") + "\n\n")
	for i, p := range f.items {
		// The tag is coloured, so its width is not its length: measure the word.
		tagText := "adopted"
		tag := styDim.Render(tagText)
		if p.Managed {
			tagText, tag = "managed", styPill.Render("managed")
		}
		meta := strings.TrimSpace(p.Framework + " " + p.Env)
		// Sized from the panel: 22 and 18 fixed columns plus the tag do not fit
		// the 52 an 80-column terminal leaves here.
		nameW, metaW := colWidths(w-len([]rune(tagText))-8, 22, 18)
		n, mt := trimTo(p.Name, nameW), trimTo(meta, metaW)
		line := fmt.Sprintf("  %-*s %-*s %s", nameW, n, metaW, mt, tag)
		if i == f.cur {
			line = navSel.Render(fmt.Sprintf(" %-*s %-*s ", nameW, n, metaW, mt)) + " " + tag
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	if f.note != "" {
		b.WriteString(styHead.Render(f.note) + "\n\n")
	}
	b.WriteString(styDim.Render(trimTo("enter choose a task · a add an existing project · r rescan · esc back", w)))
	return b.String()
}
