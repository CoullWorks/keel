// Package cli wires Keel's command tree (Cobra) to the recipe engine.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/coullworks/keel/internal/selfupdate"
	"github.com/spf13/cobra"
)

// Version is overridable at build time via -ldflags.
var Version = "0.1.0-dev"

// repo is where releases come from, for both `keel self-update` and the
// once-a-day upgrade notice.
const repo = "coullworks/keel"

// upgradeNotice is a seam: tests replace it so the suite never touches the
// network or the developer's real cache file.
var upgradeNotice = func() string { return selfupdate.Notice(repo, Version) }

// errNoProject is the single wording for "you are not inside a keel project".
// Four commands each phrased this differently, so the same situation read as
// four different problems.
var errNoProject = errors.New("not a keel project: no .keel/manifest.yaml here (run keel adopt to take over an existing project)")

// manifestErr turns a ReadManifest error into the right message for the user. A
// missing manifest is "not a keel project" (the common, actionable case); a
// present-but-corrupt one keeps its own message (engine.ErrManifestMalformed
// names the file and says it is not valid YAML) instead of being mis-reported as
// "not a keel project", which would send someone editing the wrong thing.
func manifestErr(err error) error {
	if os.IsNotExist(err) {
		return errNoProject
	}
	return err
}

// requireSubcommand makes a parent command reject an unknown subcommand with an
// error and a non-zero exit, instead of cobra's default of printing help and
// exiting 0. It is the single wording + behaviour for every command that is only
// a namespace for subcommands (secrets, commerce, proxy, recipes, …), so they
// all behave like config/plugins/service rather than each doing its own thing.
//
// With no arguments it prints help (the normal "what can I do here?" case); with
// an argument that did not match a subcommand it errors. cobra calls a parent's
// RunE only when no subcommand matched, so reaching here with args means the
// first arg is an unknown subcommand.
func requireSubcommand(c *cobra.Command) *cobra.Command {
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
	}
	return c
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "keel",
		Short: "A web development studio for any stack",
		Long: "Keel lays the keel of a new project. Pick a framework, add-ons, a local\n" +
			"dev env and services, and it composes a ready-to-run skeleton with the\n" +
			"env and services wired. Recipes are data, so anyone can add a stack.\n" +
			"See docs/ROUTES.md for the catalogue.",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		// Bare `keel` always opens the console — the single shell for every screen,
		// including first-run "set your defaults" (rendered inside the main panel).
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConsole(cmd)
		},
	}
	root.AddCommand(newCmd(), initCmd(), configCmd(), doctorCmd(), genCmd(), recipesCmd(), newRecipeCmd(), studioCmd(), consoleCmd(), sponsorCmd(), dbCmd(), brandCmd(), secretsCmd(), statusCmd(), serviceCmd(), updateCmd(), deployCmd(), adoptCmd(), trackCmd(), optimizeCmd(), mcpCmd(), runCmd(), selfUpdateCmd(), commerceCmd(), pluginsCmd(), deleteCmd(), resetCmd(), proxyCmd(), addCmd(), removeCmd())
	root.AddCommand(pluginCommands()...)
	return root
}

// Execute runs the CLI and returns the process exit code. main is the only
// place that exits, so every path here stays testable and deferred cleanup
// always runs.
func Execute() int {
	root := rootCmd()
	// git-style plugin dispatch: `keel <name>` → keel-<name> on PATH when it isn't
	// a built-in command. This is keel's extension point for other tools.
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") && !isBuiltin(root, os.Args[1]) {
		if handled, code := dispatchPlugin(root.ErrOrStderr(), os.Args[1], os.Args[2:]); handled {
			return code
		}
	}
	// One cancellable context for the whole command tree: Ctrl-C / SIGTERM cancels
	// cmd.Context(), which the build/exec paths thread down to the child processes
	// (exec.CommandContext) so an interrupted `keel new` actually stops composer,
	// rather than leaving it running while keel exits. Individual commands that need
	// their own signal handling (the long-lived dev server in run.go) still layer it.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := root.ExecuteContext(ctx)
	// The upgrade notice goes last and to stderr, so it never lands in the
	// middle of a build's output and never contaminates a --json stdout that
	// something else is parsing. It is also the last thing you read, which is
	// where a "there is a newer version" line is actually useful.
	if msg := upgradeNotice(); msg != "" {
		fmt.Fprintln(root.ErrOrStderr(), msg)
	}
	if err != nil {
		fmt.Fprintln(root.ErrOrStderr(), "error:", err)
		return 1
	}
	return 0
}
