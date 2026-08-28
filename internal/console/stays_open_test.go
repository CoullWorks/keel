package console

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coullworks/keel/internal/project"
)

// Nothing you pick inside the console ends the session.
//
// The console used to answer every action by setting quitting and returning
// tea.Quit, so `keel console` could run the action after the program had torn
// down. Choosing a task, a build, or anything from a menu therefore dropped you
// back at a shell prompt, and the only way back in was to start keel again.
// Actions now suspend the console and hand the terminal back afterwards, so the
// only thing that quits is q.
func TestNoActionEverQuitsTheConsole(t *testing.T) {
	for _, area := range []string{"generate", "data", "logs", "packs", "plugins", "projects", "build"} {
		t.Run(area, func(t *testing.T) {
			m := newModel(t)
			// The project-scoped areas ask which project first, and an empty
			// list has nothing to press Enter on, so they would never reach the
			// action at all and the test would pass without testing anything.
			seedProject(t, "shop")
			m.reload()
			m.nav = m.indexOf(area)
			if m.nav < 0 {
				t.Fatalf("there is no %s area", area)
			}
			m = send(m, fkey("enter"))

			// Walk the screen with Enter. Whatever it reaches - a project list,
			// a task menu, a name prompt, the end of a wizard - none of it may
			// quit.
			for i := 0; i < 12; i++ {
				got, cmd := m.Update(fkey("enter"))
				m = got.(model)
				if m.quitting {
					t.Fatalf("enter #%d in %s quit the console", i+1, area)
				}
				if isQuit(cmd) {
					t.Fatalf("enter #%d in %s returned tea.Quit", i+1, area)
				}
			}
		})
	}
}

// q is the way out, and it still works.
func TestQuitStillQuits(t *testing.T) {
	m := newModel(t)
	got, cmd := m.Update(fkey("q"))
	m = got.(model)
	if !m.quitting || !isQuit(cmd) {
		t.Fatal("q should quit the console")
	}
}

// A finished action puts the console back in charge and says what happened.
func TestFinishedActionReturnsToTheConsole(t *testing.T) {
	m := newModel(t)
	m.busy = true
	m.action = Action{Kind: "argv", Argv: []string{"doctor"}}
	got, _ := m.Update(actionDoneMsg{act: m.action})
	m = got.(model)
	if m.busy {
		t.Error("the console is still marked busy after the action finished")
	}
	if m.quitting {
		t.Error("a finished action quit the console")
	}
	if !strings.Contains(m.footer(), "doctor") {
		t.Errorf("the console does not say what just ran:\n%s", m.footer())
	}
}

// seedProject registers a real keel project so the project-scoped screens have
// something to choose.
func seedProject(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(dir, ".keel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".keel", "manifest.yaml"),
		[]byte("framework: laravel\nenv: ddev\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := project.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Add(dir); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	return dir
}

// isQuit reports whether a tea.Cmd is tea.Quit, by running it and looking at
// the message. Every other cmd the console returns is cheap and side-effect
// free, and a nil cmd is not a quit.
func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}
