package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// This file walks keel's whole command surface rather than testing commands one
// at a time.
//
// Every bug it exists to catch was live in a shipped build while the per-command
// tests were green, because each of those tests set up exactly the conditions
// its command wanted. `keel gen` invented a laravel-on-ddev project wherever it
// was run, and every gen test ran it with -f in a directory it had already
// arranged. Nothing asked the flat question: what does each of these commands do
// when it is pointed at nothing?

// projectScoped is every command that acts on the keel project in the current
// directory, with arguments that make it try. Each must refuse when there is no
// project rather than guessing one.
var projectScoped = map[string][]string{
	"gen":     {"gen", "model", "Order", "--dry-run"},
	"db":      {"db", "migrate", "--dry-run"},
	"run":     {"run", "dev"},
	"update":  {"update", "--dry-run"},
	"deploy":  {"deploy", "compose", "--dry-run"},
	"secrets": {"secrets", "sync"},
	"delete":  {"delete", "--yes"},
	// add/remove edit the manifest of the project in the current directory, so
	// both must refuse when there is none rather than inventing a project.
	"add":    {"add", "redis", "--dry-run"},
	"remove": {"remove", "redis", "--yes"},
	// status and service read the current project's manifest/env, so both must
	// refuse (not invent an env) when run outside a project.
	"status":  {"status"},
	"service": {"service"},
}

// standalone is every command that is meaningful with no project: it configures
// keel itself, describes the catalogue, or takes what it acts on as an argument.
//
// The dividing line is whether the command needs to know the stack. optimize,
// adopt and commerce do not: each takes the directory as an argument, and each
// is a tool you point at something that is not a keel project yet.
var standalone = map[string]bool{
	"console": true, "doctor": true, "recipes": true, "new": true, "new-recipe": true,
	"plugins": true, "studio": true, "sponsor": true, "self-update": true, "mcp": true,
	"version": true, "completion": true, "help": true, "projects": true,
	"init": true, "proxy": true, "reset": true,
	"optimize": true, "adopt": true, "commerce": true, "brand": true,
	// config edits keel's own profile (the same file init writes); it is
	// meaningful anywhere, with no project.
	"config": true,
}

// Every command is classified. A new one has to be put in a bucket, which is the
// point: the question "what does this do outside a project" gets asked once, when
// the command is added, instead of never.
func TestEveryCommandIsClassified(t *testing.T) {
	for _, c := range rootCmd().Commands() {
		name := c.Name()
		if _, ok := projectScoped[name]; ok {
			continue
		}
		if standalone[name] {
			continue
		}
		t.Errorf("command %q is in neither projectScoped nor standalone in surface_test.go: "+
			"decide whether it needs a keel project and say so there", name)
	}
}

// A project-scoped command run outside a project refuses, and says so.
//
// `keel gen model Order` in an empty directory used to succeed and print a plan
// to run `ddev exec php artisan make:model Order` — a command for a project that
// did not exist, aimed at whatever happened to be in the working directory.
func TestProjectCommandsRefuseOutsideAProject(t *testing.T) {
	for name, argv := range projectScoped {
		t.Run(name, func(t *testing.T) {
			isolate(t)
			out, err := runRoot(t, argv...)
			if err == nil {
				t.Fatalf("`keel %s` succeeded with no project in the directory:\n%s",
					strings.Join(argv, " "), out)
			}
			// And it has to say what is wrong, not fail obscurely.
			if !mentionsAProject(err.Error()) {
				t.Errorf("`keel %s` failed without explaining that there is no project: %v",
					strings.Join(argv, " "), err)
			}
		})
	}
}

// Nothing prints a runnable command line for a stack that is not there.
//
// This is the shape of the gen bug stated generally: a dry run in an empty
// directory should not be able to name a container tool, because there is no
// project to tell it which one to name.
func TestNoCommandInventsAStackOutsideAProject(t *testing.T) {
	for name, argv := range projectScoped {
		t.Run(name, func(t *testing.T) {
			isolate(t)
			out, _ := runRoot(t, argv...)
			for _, invented := range []string{"ddev exec", "sail exec", "docker compose", "php artisan", "manage.py"} {
				if strings.Contains(out, invented) {
					t.Errorf("`keel %s` printed %q with no project to get it from:\n%s",
						strings.Join(argv, " "), invented, out)
				}
			}
		})
	}
}

// Every command has help that says what it is for, and help never fails. A
// command whose help errors is a command nobody can discover.
func TestEveryCommandHasWorkingHelp(t *testing.T) {
	var walk func(c *cobra.Command, path string)
	walk = func(c *cobra.Command, path string) {
		if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
			return
		}
		isolate(t)
		args := append(strings.Fields(path), "--help")
		out, err := runRoot(t, args...)
		if err != nil {
			t.Errorf("`keel %s --help` failed: %v", path, err)
			return
		}
		if c.Short == "" {
			t.Errorf("`keel %s` has no Short, so it is blank in `keel --help`", path)
		}
		if !strings.Contains(out, "Usage:") {
			t.Errorf("`keel %s --help` printed no usage:\n%s", path, out)
		}
		for _, sub := range c.Commands() {
			walk(sub, path+" "+sub.Name())
		}
	}
	for _, c := range rootCmd().Commands() {
		walk(c, c.Name())
	}
}

// No user-facing help copy uses a non-ASCII character. The house style is ASCII
// in anything a terminal prints: a fancy dash, an arrow (->), or a bullet (-)
// renders as a box on one machine and the glyph on another, so a hyphen/arrow in
// plain ASCII is better everywhere. This is the single guard for the whole help
// surface (Short/Long/Example of every command, recursively), so a new command
// with a stray Unicode glyph fails here rather than shipping.
func TestCommandHelpIsASCII(t *testing.T) {
	var walk func(c *cobra.Command, path string)
	walk = func(c *cobra.Command, path string) {
		for label, s := range map[string]string{"Short": c.Short, "Long": c.Long, "Example": c.Example} {
			for _, r := range s {
				if r > 127 {
					t.Errorf("`keel %s` %s contains non-ASCII %q (U+%04X): %q", path, label, r, r, s)
					break
				}
			}
		}
		for _, sub := range c.Commands() {
			walk(sub, path+" "+sub.Name())
		}
	}
	for _, c := range rootCmd().Commands() {
		walk(c, c.Name())
	}
}

// mentionsAProject reports whether an error explains that keel could not find a
// project, in whatever words the command chose.
func mentionsAProject(msg string) bool {
	for _, s := range []string{"not a keel project", "no keel project", "keel adopt", "no project", "inside a project"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
