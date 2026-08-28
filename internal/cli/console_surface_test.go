package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/console"
)

// The console offers menus of keel commands. Nothing checked that the commands
// on those menus were real, or that they would work with the arguments the menu
// gave them, because the console cannot see the CLI: it hands back a command
// line for `keel console` to run. That gap shipped a Generate menu whose four
// entries were all `keel gen <component>` with no name, which is an error the
// moment it runs. These tests close it from the side that can see both.

// Every menu entry names a command that exists.
func TestConsoleMenusOnlyOfferRealCommands(t *testing.T) {
	root := rootCmd()
	for area, menu := range console.MenuCommands() {
		for _, entry := range menu {
			c, _, err := root.Find(entry.Argv)
			if err != nil {
				t.Errorf("%s menu offers `keel %s`, which is not a command: %v",
					area, strings.Join(entry.Argv, " "), err)
				continue
			}
			// Find falls back to the parent when a subcommand is unknown, so a
			// typo would otherwise pass by resolving to its parent.
			if len(entry.Argv) > 1 && c.Name() == root.Name() {
				t.Errorf("%s menu offers `keel %s`, which resolved to the root command",
					area, strings.Join(entry.Argv, " "))
			}
		}
	}
}

// Every menu entry runs with the arguments the menu actually supplies.
//
// An entry that declares no argument must work as written; one that declares an
// argument must work once the console has asked for it. Either way the command
// has to get past argument validation, which is exactly what `keel gen model`
// did not do.
func TestConsoleMenuEntriesRunWithTheArgumentsTheyAreGiven(t *testing.T) {
	for area, menu := range console.MenuCommands() {
		for _, entry := range menu {
			argv := append([]string(nil), entry.Argv...)
			if entry.Arg != "" {
				// What the console appends after prompting.
				argv = append(argv, "Thing")
			}
			name := area + "/" + strings.Join(argv, "_")
			t.Run(name, func(t *testing.T) {
				wd := isolate(t)
				// The console runs project-scoped menus inside the project it
				// asked you to choose, so give the command one to find.
				if err := os.MkdirAll(filepath.Join(wd, ".keel"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(wd, ".keel", "manifest.yaml"),
					[]byte("framework: laravel\nenv: ddev\nrecipes: [laravel, ddev]\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				// Prefer a dry run where the command has one, so nothing here
				// installs or starts anything.
				run := argv
				if c, _, err := rootCmd().Find(argv); err == nil && c.Flags().Lookup("dry-run") != nil {
					run = append(append([]string(nil), argv...), "--dry-run")
				}
				_, err := runRoot(t, run...)
				if err == nil {
					return
				}
				// Without a dry run the command may fail on a missing tool or an
				// absent network, which is not this test's business. What must
				// never happen is failing because the arguments themselves are
				// wrong.
				for _, bad := range []string{
					"unknown command", "unknown flag", "accepts", "requires at least",
					"give at least one name", "invalid argument", "unknown shorthand",
				} {
					if strings.Contains(err.Error(), bad) {
						t.Errorf("`keel %s` cannot run as the %s menu offers it: %v",
							strings.Join(argv, " "), area, err)
					}
				}
			})
		}
	}
}

// The Projects screen's tasks all map to a command keel can run. The mapping
// lives in this package and the task list lives in the console, so nothing but
// a test spanning both can tell when one grows an entry the other lacks.
func TestEveryProjectTaskHasACommand(t *testing.T) {
	root := rootCmd()
	for _, task := range console.ProjectTasks() {
		// Forget is handled inside the console: it edits keel's own project
		// list rather than running anything.
		if task == "forget" {
			continue
		}
		argv, ok := projectTaskArgv[task]
		if !ok {
			t.Errorf("the Projects screen offers %q, which projectTaskArgv cannot run", task)
			continue
		}
		if _, _, err := root.Find(argv); err != nil {
			t.Errorf("project task %q maps to `keel %s`, which is not a command: %v",
				task, strings.Join(argv, " "), err)
		}
	}
	// And nothing in the map is dead: an entry no screen offers is a command
	// nobody can reach.
	offered := map[string]bool{}
	for _, task := range console.ProjectTasks() {
		offered[task] = true
	}
	for task := range projectTaskArgv {
		if !offered[task] {
			t.Errorf("projectTaskArgv can run %q, but no screen offers it", task)
		}
	}
}

// A console action reaches the command it names, in the directory it names.
func TestConsoleActionRunsInTheChosenProject(t *testing.T) {
	wd := isolate(t)
	proj := filepath.Join(wd, "shop")
	if err := os.MkdirAll(filepath.Join(proj, ".keel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, ".keel", "manifest.yaml"),
		[]byte("framework: laravel\nenv: sail\nrecipes: [laravel, sail]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	err := runConsoleAction(console.Action{
		Kind: "argv", Dir: proj, Argv: []string{"gen", "model", "Order", "--dry-run"},
	}, nil, &out, &out)
	if err != nil {
		t.Fatalf("console action: %v", err)
	}
	// It ran against the chosen project, not the working directory.
	if !strings.Contains(out.String(), proj) || !strings.Contains(out.String(), "sail") {
		t.Errorf("the action did not run in the chosen project:\n%s", out.String())
	}
	// And it put the working directory back.
	if got, _ := os.Getwd(); got != wd {
		t.Errorf("working directory left at %q, want %q", got, wd)
	}
}

// An unknown project task is refused rather than silently doing nothing.
func TestConsoleActionRejectsAnUnknownTask(t *testing.T) {
	isolate(t)
	var out strings.Builder
	err := runConsoleAction(console.Action{Kind: "project", Task: "nonsense"}, nil, &out, &out)
	if err == nil {
		t.Fatal("an unknown project task should be an error")
	}
}
