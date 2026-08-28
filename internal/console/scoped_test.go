package console

import (
	"strings"
	"testing"
)

// Generate, Data and Run & Logs ask which project before offering a task.
//
// They used to run their commands in whatever directory keel was launched from.
// `keel db migrate` or `keel gen model` against the current working directory is
// meaningless unless you happened to start keel inside the project you meant,
// and nothing on the screen said which one it would be.
func TestProjectScopedAreasAskWhichProject(t *testing.T) {
	for _, area := range []string{"generate", "data", "logs"} {
		t.Run(area, func(t *testing.T) {
			m := newModel(t)
			dir := seedProject(t, "shop")
			m.reload()
			m.nav = m.indexOf(area)
			m = send(m, fkey("enter"))

			f, ok := m.flow.(*taskFlow)
			if !ok {
				t.Fatalf("%s opened a %T", area, m.flow)
			}
			if !f.scoped {
				t.Fatalf("%s is not project-scoped", area)
			}
			v := f.view(100)
			if !strings.Contains(v, "shop") {
				t.Errorf("the project list does not offer the project:\n%s", v)
			}
			if strings.Contains(v, "db migrate") || strings.Contains(v, "gen model") {
				t.Errorf("%s offered tasks before a project was chosen:\n%s", area, v)
			}

			// Choosing the project reveals the tasks, and names the project they
			// will run in.
			m = send(m, fkey("enter"))
			f = m.flow.(*taskFlow)
			v = f.view(100)
			if !strings.Contains(v, dir) {
				t.Errorf("the task list does not say which project it acts on:\n%s", v)
			}
			if f.dir() != dir {
				t.Errorf("task dir = %q, want %q", f.dir(), dir)
			}
		})
	}
}

// With no projects registered, the scoped areas say so and offer no task to
// run against nothing.
func TestProjectScopedAreasWithNoProjects(t *testing.T) {
	m := newModel(t)
	m.nav = m.indexOf("data")
	m = send(m, fkey("enter"))

	f := m.flow.(*taskFlow)
	v := f.view(100)
	if !strings.Contains(v, "not tracking any projects") {
		t.Errorf("no explanation for an empty project list:\n%s", v)
	}
	// Enter must not fall through to a task with no project behind it.
	if act, _ := f.update(fkey("enter")); act.Kind != "" {
		t.Errorf("ran %+v with no project chosen", act)
	}
}

// Packs is not project-scoped: a pack is installed once and applies everywhere.
func TestPacksIsNotProjectScoped(t *testing.T) {
	m := newModel(t)
	m.nav = m.indexOf("packs")
	m = send(m, fkey("enter"))

	f := m.flow.(*taskFlow)
	if f.scoped {
		t.Error("Packs should not ask for a project")
	}
	if !strings.Contains(f.view(100), "recipes list") {
		t.Errorf("Packs does not offer its tasks:\n%s", f.view(100))
	}
}

// A task that needs an argument asks for it instead of running a command that
// cannot work. Every Generate entry was `keel gen <component>` with no name,
// which is an error the moment it runs.
func TestTasksThatNeedANameAskForOne(t *testing.T) {
	m := newModel(t)
	dir := seedProject(t, "shop")
	m.reload()
	m.nav = m.indexOf("generate")
	m = send(m, fkey("enter"))
	f := m.flow.(*taskFlow)
	f.update(fkey("enter")) // choose the project

	// The first entry is `gen model`, which takes a name.
	if act, _ := f.update(fkey("enter")); act.Kind != "" {
		t.Fatalf("gen model ran without a name: %+v", act)
	}
	if !f.naming {
		t.Fatal("no name was asked for")
	}
	// An empty name is refused rather than run.
	if act, _ := f.update(fkey("enter")); act.Kind != "" {
		t.Fatalf("an empty name should not run: %+v", act)
	}
	for _, r := range "Order" {
		f.update(fkey(string(r)))
	}
	act, _ := f.update(fkey("enter"))
	if got := strings.Join(act.Argv, " "); got != "gen model Order" {
		t.Errorf("argv = %q, want `gen model Order`", got)
	}
	if act.Dir != dir {
		t.Errorf("action dir = %q, want the chosen project %q", act.Dir, dir)
	}
}
