package console

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ActionRunner carries out an Action against the real terminal.
//
// It is injected rather than imported: the console cannot reach the command
// package that owns build, db and deploy without an import cycle, and the
// console's job is to choose what happens, not to know how it happens.
type ActionRunner func(a Action, stdin io.Reader, stdout, stderr io.Writer) error

// Runner is installed by `keel console` before the program starts.
var Runner ActionRunner

// actionExec runs an Action with bubbletea suspended, then hands the screen
// back to the console.
//
// Long jobs — composer, npm, docker, minutes of output — have to reach the real
// terminal to stay readable and interruptible. The console used to get that by
// quitting: it returned an Action and `keel console` ran it on the way out, so
// choosing anything from a menu ended the session and dropped you at a shell
// prompt. tea.Exec gives the same real terminal and then gives it back, so
// nothing leaves keel except a deliberate quit.
type actionExec struct {
	act Action
	in  io.Reader
	out io.Writer
	err io.Writer
}

func (e *actionExec) SetStdin(r io.Reader)  { e.in = r }
func (e *actionExec) SetStdout(w io.Writer) { e.out = w }
func (e *actionExec) SetStderr(w io.Writer) { e.err = w }

func (e *actionExec) Run() error {
	if Runner == nil {
		return errors.New("no action runner installed")
	}
	// While the action holds the terminal, Ctrl-C belongs to what it is running
	// rather than to keel. The child is in this terminal's foreground process
	// group and receives the interrupt from the tty directly, so catching it
	// here and doing nothing stops the build and returns to the console, instead
	// of taking the console down with it. Without this, interrupting a build
	// begun inside the console ends the session, which is the behaviour the
	// console was just fixed to stop having.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt)
	defer signal.Stop(sigs)

	err := Runner(e.act, e.in, e.out, e.err)
	if err != nil {
		fmt.Fprintf(e.err, "\n%v\n", err)
	}
	// The console repaints over this the moment it resumes, so the run's output
	// is only readable while we wait here. Without the pause a failed build
	// would flash past and be gone.
	fmt.Fprint(e.out, "\npress enter to return to keel ")
	if e.in != nil {
		_, _ = bufio.NewReader(e.in).ReadString('\n')
	}
	// The error was reported above and belongs to the action, not to the
	// console: returning it would tear the program down, which is the behaviour
	// this whole type exists to remove.
	return nil
}

// actionDoneMsg reports a finished action back into the console's update loop.
type actionDoneMsg struct{ act Action }

// actionSummary describes an action in a few words, for the footer: what is
// running now, and what the last one did.

func actionSummary(a Action) string {
	switch a.Kind {
	case "build":
		if a.Dir != "" {
			return "build in " + a.Dir
		}
		return "build"
	case "project":
		return a.Task
	case "argv":
		return "keel " + strings.Join(a.Argv, " ")
	}
	return ""
}

// runAction suspends the console, runs a, and resumes.
func runAction(a Action) tea.Cmd {
	return tea.Exec(&actionExec{act: a}, func(error) tea.Msg {
		return actionDoneMsg{act: a}
	})
}
