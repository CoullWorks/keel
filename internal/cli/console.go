package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/console"
	"github.com/spf13/cobra"
)

func consoleCmd() *cobra.Command {
	return &cobra.Command{
		Use: "console",
		Long: "The single shell for every keel screen: projects, build, settings and the\n" +
			"recipe catalogue, without leaving the terminal. Running keel with no\n" +
			"arguments opens this.\n\n" +
			"Everything you pick runs inside the console and returns to it. The only\n" +
			"way out is q.\n",
		Args:  cobra.NoArgs,
		Short: "Open the keel console (full-screen multi-panel UI)",
		RunE:  func(cmd *cobra.Command, args []string) error { return runConsole(cmd) },
	}
}

// runConsole opens the console, having told it how to carry out what the user
// picks.
//
// The console suspends itself while an action runs, so a build's composer, npm
// and docker output reaches the real terminal exactly as it did before, and the
// console comes back afterwards. It used to get that readability by quitting:
// `keel console` ran the chosen action on the way out, so picking anything at
// all ended the session.
func runConsole(cmd *cobra.Command) error {
	// The console shows the same upgrade notice the CLI does, so it needs to
	// know which build it is.
	console.Version, console.Repo = Version, repo
	console.Runner = runConsoleAction
	return console.Run()
}

// runConsoleAction carries out one console action against the real terminal.
func runConsoleAction(a console.Action, in io.Reader, out, errw io.Writer) error {
	// Every action names the project it acts on, and the commands behind them
	// operate on the working directory, so move there rather than teaching each
	// one to take a path.
	if a.Dir != "" && a.Kind != "build" {
		restore, err := chdir(a.Dir)
		if err != nil {
			return err
		}
		defer restore()
	}

	switch a.Kind {
	case "build":
		reg, err := catalog.Registry()
		if err != nil {
			return err
		}
		// The same build() that `keel new` calls, so the console is a different
		// way into one code path rather than a second implementation of it. The
		// console TUI owns its own run loop and signal handling, and ActionRunner
		// carries no context, so a fresh background context is the honest value
		// here (the request-scoped cancellation lives at the cobra command layer).
		return build(context.Background(), out, reg, a.Recipes, a.Brand, a.Dir, false, false, false)
	case "project":
		argv, ok := projectTaskArgv[a.Task]
		if !ok {
			return fmt.Errorf("unknown project task %q", a.Task)
		}
		return runConsoleKeel(in, out, errw, argv)
	case "argv":
		// A menu entry is the command line it shows, run as typed.
		return runConsoleKeel(in, out, errw, a.Argv)
	}
	return nil
}

// projectTaskArgv maps the Projects screen's tasks onto the commands that
// already implement them.
//
// Reusing the cobra commands rather than calling their internals keeps one
// definition of what "optimize" means: the console, the flag form and the studio
// all end up in the same place.
var projectTaskArgv = map[string][]string{
	"doctor":   {"doctor"},
	"optimize": {"optimize"},
	"secrets":  {"secrets", "sync"},
	"db":       {"db", "migrate"},
	"deploy":   {"deploy"},
}

// runConsoleKeel runs a keel command line in this process, wired to the given streams.
func runConsoleKeel(in io.Reader, out, errw io.Writer, argv []string) error {
	root := rootCmd()
	if in != nil {
		root.SetIn(in)
	}
	root.SetOut(out)
	root.SetErr(errw)
	root.SetArgs(argv)
	return root.Execute()
}

// chdir moves to dir and returns the function that moves back.
func chdir(dir string) (func(), error) {
	prev, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	if err := os.Chdir(dir); err != nil {
		return nil, err
	}
	return func() { _ = os.Chdir(prev) }, nil
}
