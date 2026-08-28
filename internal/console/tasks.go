package console

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coullworks/keel/internal/project"
)

// taskFlow is a menu of things keel can do, for the areas whose whole job is
// "pick one and run it": Generate, Data, Run & Logs, Packs.
//
// Most of those act on a project, so the flow asks which one first. It used to
// run them in whatever directory keel was launched from, which makes `keel db
// migrate` or `keel gen model` meaningless unless you happened to start keel
// inside the project you meant.
type taskFlow struct {
	title string
	help  string
	tasks []consoleTask

	// scoped marks a menu whose tasks act on a project.
	scoped   bool
	projects []project.Project
	pcur     int
	picked   bool

	cur  int
	note string

	// naming is the text prompt for a task that takes an argument, and name is
	// what has been typed so far.
	naming bool
	name   string
}

type consoleTask struct {
	desc string
	argv []string
	// arg is what to call the argument this task needs, or "" when it needs
	// none. `keel gen model` with no name is an error, so every Generate entry
	// failed the moment it ran until the console started asking.
	arg string
}

func newTaskFlow(title, help string, scoped bool, tasks []consoleTask) *taskFlow {
	f := &taskFlow{title: title, help: help, scoped: scoped, tasks: tasks}
	f.reload()
	return f
}

// reload re-reads the project list, so a project built moments ago is offered
// without leaving and re-entering the screen.
func (f *taskFlow) reload() {
	if !f.scoped {
		return
	}
	reg, err := project.Load()
	if err != nil {
		f.note = err.Error()
		return
	}
	reg.Refresh()
	f.projects = reg.Projects
	if f.pcur >= len(f.projects) {
		f.pcur = max(0, len(f.projects)-1)
	}
}

// dir is the project the task will run in, or "" for an unscoped menu.
func (f *taskFlow) dir() string {
	if !f.scoped || f.pcur >= len(f.projects) {
		return ""
	}
	return f.projects[f.pcur].Path
}

// run builds the action for the highlighted task.
func (f *taskFlow) run() Action {
	t := f.tasks[f.cur]
	argv := append([]string(nil), t.argv...)
	if t.arg != "" {
		argv = append(argv, strings.Fields(f.name)...)
	}
	return Action{Kind: "argv", Argv: argv, Dir: f.dir()}
}

func (f *taskFlow) update(msg tea.KeyMsg) (Action, bool) {
	switch {
	case f.naming:
		return f.updateName(msg)
	case f.scoped && !f.picked:
		return f.updateProject(msg)
	default:
		return f.updateTask(msg)
	}
}

func (f *taskFlow) updateProject(msg tea.KeyMsg) (Action, bool) {
	switch msg.String() {
	case "up", "k":
		if f.pcur > 0 {
			f.pcur--
		}
	case "down", "j":
		if f.pcur < len(f.projects)-1 {
			f.pcur++
		}
	case "enter", "right", "l":
		if len(f.projects) > 0 {
			f.picked, f.cur, f.note = true, 0, ""
		}
	case "r":
		f.reload()
		f.note = "rescanned"
	case "esc", "left", "h":
		return Action{}, true
	}
	return Action{}, false
}

func (f *taskFlow) updateTask(msg tea.KeyMsg) (Action, bool) {
	switch msg.String() {
	case "up", "k":
		if f.cur > 0 {
			f.cur--
		}
	case "down", "j":
		if f.cur < len(f.tasks)-1 {
			f.cur++
		}
	case "enter":
		if f.cur >= len(f.tasks) {
			break
		}
		if f.tasks[f.cur].arg != "" {
			f.naming, f.name = true, ""
			return Action{}, false
		}
		// The menu stays open: you are far more likely to run a second task on
		// the same project than to be finished with keel.
		return f.run(), false
	case "esc", "left", "h":
		if f.scoped {
			f.picked = false
			return Action{}, false
		}
		return Action{}, true
	}
	return Action{}, false
}

func (f *taskFlow) updateName(msg tea.KeyMsg) (Action, bool) {
	switch msg.String() {
	case "enter":
		if strings.TrimSpace(f.name) == "" {
			f.note = "give it a name"
			return Action{}, false
		}
		act := f.run()
		f.naming, f.note = false, ""
		return act, false
	case "esc":
		f.naming, f.name = false, ""
	case "backspace":
		if r := []rune(f.name); len(r) > 0 {
			f.name = string(r[:len(r)-1])
		} else {
			f.naming = false
		}
	default:
		if s := msg.String(); len(s) == 1 || s == " " {
			f.name += s
		}
	}
	return Action{}, false
}

func (f *taskFlow) view(w int) string {
	var b strings.Builder
	b.WriteString(mainTitle.Render(f.title) + "\n")

	if f.scoped && !f.picked {
		b.WriteString(note(w, "Pick the project to work in.") + "\n")
		if len(f.projects) == 0 {
			b.WriteString(note(w, "keel is not tracking any projects yet, and these tasks all act on one.") + "\n")
			b.WriteString(cmd(w, "keel new", "build one"))
			b.WriteString(cmd(w, "keel adopt <path>", "take over an existing project"))
			return b.String()
		}
		for i, p := range f.projects {
			meta := strings.TrimSpace(p.Framework + " " + p.Env)
			n, mt := trimTo(p.Name, 22), trimTo(meta, w-28)
			line := fmt.Sprintf("  %-22s %s", n, styDim.Render(mt))
			if i == f.pcur {
				line = navSel.Render(fmt.Sprintf(" %-22s %s ", n, mt))
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
		if f.note != "" {
			b.WriteString(styHead.Render(f.note) + "\n\n")
		}
		b.WriteString(styDim.Render("enter choose · r rescan · esc back"))
		return b.String()
	}

	if f.help != "" {
		b.WriteString(note(w, f.help))
	}
	// Which project every command below is about to run in. Without it the menu
	// is a list of commands with no stated subject.
	if d := f.dir(); d != "" {
		b.WriteString(styHead.Render(f.projects[f.pcur].Name) + styDim.Render("  "+trimTo(d, w-len([]rune(f.projects[f.pcur].Name))-4)) + "\n")
	}
	b.WriteString("\n")

	if f.naming {
		t := f.tasks[f.cur]
		b.WriteString(styHead.Render(strings.Join(t.argv, " ")+": "+t.arg) + "\n\n")
		b.WriteString("  " + styHead.Render(f.name) + styDim.Render("▌") + "\n\n")
		if f.note != "" {
			b.WriteString(styHead.Render(f.note) + "\n\n")
		}
		b.WriteString(styDim.Render("enter run · esc back"))
		return b.String()
	}

	for i, t := range f.tasks {
		shown := strings.Join(t.argv, " ")
		if t.arg != "" {
			shown += " <" + t.arg + ">"
		}
		shown = trimTo(shown, 28)
		d := trimTo(t.desc, w-28-8)
		line := fmt.Sprintf("  %-28s %s", shown, styDim.Render(d))
		if i == f.cur {
			line = navSel.Render(fmt.Sprintf(" %-28s %s ", shown, d))
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	if f.note != "" {
		b.WriteString(styHead.Render(f.note) + "\n\n")
	}
	if f.scoped {
		b.WriteString(styDim.Render("enter run · esc change project"))
	} else {
		b.WriteString(styDim.Render("enter run · esc back"))
	}
	return b.String()
}

// areaTasks is the menu each of these areas opens into. Keeping them in one
// place means the console and `keel --help` cannot drift apart quietly.
func areaTasks(key string) *taskFlow {
	switch key {
	case "generate":
		return newTaskFlow("Generate", "Scaffold code. Laravel goes through artisan make; Magento is\nwritten from templates.", true, []consoleTask{
			{argv: []string{"gen", "model"}, desc: "a model", arg: "name"},
			{argv: []string{"gen", "controller"}, desc: "a controller", arg: "name"},
			{argv: []string{"gen", "event"}, desc: "an event", arg: "name"},
			{argv: []string{"gen", "listener"}, desc: "a listener", arg: "name"},
			{argv: []string{"gen", "migration"}, desc: "a migration", arg: "name"},
			{argv: []string{"gen"}, desc: "pick from the full list"},
		})
	case "data":
		return newTaskFlow("Data", "Database tasks, run through the project's own env so they work\nthe same under DDEV, Sail, Docker or Local.", true, []consoleTask{
			{argv: []string{"db", "migrate"}, desc: "run migrations"},
			{argv: []string{"db", "seed"}, desc: "seed data"},
			{argv: []string{"db", "status"}, desc: "show migration status"},
			{argv: []string{"db", "reset"}, desc: "drop, re-migrate and re-seed"},
		})
	case "logs":
		return newTaskFlow("Run & Logs", "Start, stop and watch a project.", true, []consoleTask{
			{argv: []string{"run", "dev"}, desc: "start the dev server"},
			{argv: []string{"run", "stop"}, desc: "stop it"},
			{argv: []string{"run", "logs"}, desc: "follow the logs"},
			{argv: []string{"run"}, desc: "list this stack's tasks"},
			{argv: []string{"doctor"}, desc: "check the host tools"},
		})
	case "packs":
		// Not project-scoped: a pack is installed once and applies everywhere.
		return newTaskFlow("Packs", "Recipe packs are shareable bundles of stacks and add-ons. An\ninstalled pack's recipes appear everywhere a built-in one does.", false, []consoleTask{
			{argv: []string{"recipes", "list"}, desc: "list every recipe available"},
			{argv: []string{"recipes", "search"}, desc: "find community packs on GitHub", arg: "query"},
			{argv: []string{"recipes", "add"}, desc: "install a pack", arg: "owner/repo or path"},
			{argv: []string{"recipes", "validate"}, desc: "check the installed catalogue"},
		})
	}
	return nil
}

// MenuCommands is every command line the console's menus can run, keyed by
// area, with the name of the argument each one still needs.
//
// Exported so a test in the CLI package can prove each entry resolves to a real
// keel command that accepts those arguments. The Generate menu shipped four
// entries that were all errors the moment they ran: `keel gen model` with no
// name is "give at least one name", and nothing in this package could have
// known that.
func MenuCommands() map[string][]MenuCommand {
	out := map[string][]MenuCommand{}
	for _, key := range []string{"generate", "data", "logs", "packs"} {
		f := areaTasks(key)
		if f == nil {
			continue
		}
		for _, t := range f.tasks {
			out[key] = append(out[key], MenuCommand{Argv: t.argv, Arg: t.arg})
		}
	}
	return out
}

// MenuCommand is one menu entry: the command line it runs, and the argument the
// console asks for before running it ("" when it needs none).
type MenuCommand struct {
	Argv []string
	Arg  string
}

// ProjectTasks is the Projects screen's task keys, so the CLI side can prove it
// knows how to run every one of them.
func ProjectTasks() []string {
	out := make([]string, 0, len(projectTasks))
	for _, t := range projectTasks {
		out = append(out, t.key)
	}
	return out
}
