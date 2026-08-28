package cli

// service.go is the terminal counterpart to the studio's per-service control:
// start/stop/restart ONE env service, and list every service with its state so a
// user can see what to start. `keel run` brings the whole env up; this is the
// per-service scalpel the studio has and the CLI lacked.
//
// The runtime is shelled the same way the studio does: `docker compose
// start|stop|restart <svc>` for a compose/sail env, with a clear message for
// ddev (no first-class per-service control) and native (no services). The
// service name is validated to a safe shape and passed as one argv element - it
// never reaches a shell.

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/tui"
	"github.com/spf13/cobra"
)

// serviceActions is the closed set of verbs a service control may run. A
// whitelist is the only safe form for a request that ends in a real process.
var serviceActions = map[string]string{
	"start":   "starting",
	"stop":    "stopping",
	"restart": "restarting",
}

// serviceArgv builds the compose command line for an action + validated service
// name, each element a separate argv token (no shell). Start uses
// `up -d --no-deps` rather than `start`: `compose start` only resumes an existing
// container, so a defined-but-never-created service would fail; `up -d --no-deps`
// creates and starts just that one service without dragging its dependencies up.
func serviceArgv(action, svc string) []string {
	switch action {
	case "start":
		return []string{"docker", "compose", "up", "-d", "--no-deps", svc}
	case "stop":
		return []string{"docker", "compose", "stop", svc}
	case "restart":
		return []string{"docker", "compose", "restart", svc}
	}
	return nil
}

func serviceCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "service [start|stop|restart <name>]",
		Short: "Start, stop or restart one env service (keel service alone lists them)",
		Long: "Controls a single service in the project's environment, the terminal version\n" +
			"of the studio's per-service buttons. `keel service` (or `keel service list`)\n" +
			"shows every service with its up/down state; start/stop/restart act on one\n" +
			"named service. Compose and Sail support per-service control; DDEV manages its\n" +
			"services together (use keel run), and a native project has no containers.\n",
		Example: "  keel service                 # list services and their state\n" +
			"  keel service start db\n" +
			"  keel service restart redis\n" +
			"  keel service stop mailpit\n",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServiceList(cmd.Context(), cmd.OutOrStdout(), ".")
		},
	}
	c.AddCommand(serviceListCmd(), serviceActionCmd("start"), serviceActionCmd("stop"), serviceActionCmd("restart"))
	return c
}

func serviceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the project's env services and their state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServiceList(cmd.Context(), cmd.OutOrStdout(), ".")
		},
	}
}

func serviceActionCmd(action string) *cobra.Command {
	return &cobra.Command{
		Use:     action + " <name>",
		Short:   action + " one env service",
		Example: "  keel service " + action + " db\n",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServiceAction(cmd.Context(), cmd.OutOrStdout(), ".", action, args[0])
		},
	}
}

// runServiceList prints each service with an up/down marker so a user can see
// what to start. It refuses outside a keel project (projectEnvRecipe returns the
// manifest error); a down env, an uninstalled runtime and a native env are all
// normal states printed with a calm message, not errors.
func runServiceList(ctx context.Context, out io.Writer, dir string) error {
	env, err := projectEnvRecipe(dir)
	if err != nil {
		return err
	}
	lctx, cancel := context.WithTimeout(ctx, runtimeTimeout)
	defer cancel()
	res := listServices(lctx, dir, env)

	if len(res.Services) == 0 {
		if res.Message != "" {
			fmt.Fprintln(out, res.Message)
		} else {
			fmt.Fprintln(out, "no services")
		}
		return nil
	}
	fmt.Fprintf(out, "services (%s):\n", res.Family)
	rows := make([]tui.ServiceRow, len(res.Services))
	for i, s := range res.Services {
		state := "down"
		if s.Running {
			state = "up"
		}
		rows[i] = tui.ServiceRow{State: state, Name: s.Name, Kind: s.Kind}
	}
	fmt.Fprintln(out, tui.RenderServices(rows))
	// A message can accompany rows too: e.g. the services are defined but the
	// daemon is down, so they all read stopped. Say why, so an all-down list is
	// not mistaken for a truly stopped stack.
	if res.Message != "" {
		fmt.Fprintln(out, res.Message)
	}
	if !res.Controls {
		fmt.Fprintln(out, "\nper-service control is not available for this runtime; use keel run for the whole environment")
	} else {
		fmt.Fprintln(out, "\nstart one with: keel service start <name>")
	}
	return nil
}

// runServiceAction starts/stops/restarts one named service. It validates the
// verb and service name, resolves the runtime family, and shells
// `docker compose <verb> <svc>` for compose/sail. ddev and native are refused
// with a clear reason (there is nothing to act on per-service there). The name
// is one argv element, never concatenated into a shell line.
func runServiceAction(ctx context.Context, out io.Writer, dir, action, svc string) error {
	verb, ok := serviceActions[action]
	if !ok {
		return fmt.Errorf("action not allowed: %s", action)
	}
	if !safeServiceName(svc) {
		return fmt.Errorf("invalid service name: %q", svc)
	}
	env, err := projectEnvRecipe(dir)
	if err != nil {
		return err
	}
	switch runtimeFamily(env) {
	case recipe.FamilyCompose, recipe.FamilySail:
		if !hasRuntime("docker") {
			return fmt.Errorf("docker is not installed or not on PATH")
		}
	case recipe.FamilyDDEV:
		return fmt.Errorf("DDEV manages its services together - use keel run to bring the whole environment up or down")
	default:
		return fmt.Errorf("this project runs natively - there are no services to %s", action)
	}

	fmt.Fprintf(out, "-> %s service %s\n", verb, svc)
	// Through the same seam the listing uses (argv, no shell) so the action is
	// testable and nothing user-supplied ever reaches a shell. The runtime's own
	// output is captured and echoed so the user still sees why a failure happened.
	actx, cancel := context.WithTimeout(ctx, serviceActionTimeout)
	defer cancel()
	runOut, runErr := captureCmd(actx, dir, serviceArgv(action, svc)...)
	if s := strings.TrimSpace(runOut); s != "" {
		fmt.Fprintln(out, s)
	}
	if runErr != nil {
		return runErr
	}
	fmt.Fprintln(out, "done")
	return nil
}

// serviceActionTimeout bounds a start/stop/restart. It is longer than the
// listing leash because a start may pull an image, but still bounded so a wedged
// runtime cannot hang the command forever.
const serviceActionTimeout = 5 * time.Minute
