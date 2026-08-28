package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/coullworks/keel/internal/proxy"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/project"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/spf13/cobra"
)

func runCmd() *cobra.Command {
	var dryRun, asJSON bool
	c := &cobra.Command{
		Use: "run [task]",
		Example: "  keel run                                        # list the tasks this stack has\n" +
			"  keel run dev\n" +
			"  keel run test\n",
		Short: "Run a project task (dev, test, lint, typecheck, build) - one command across every stack",
		Long: "keel exposes a single task vocabulary regardless of stack: `keel run test`\n" +
			"works whether the project is Laravel, Django, FastAPI or Next.js. Tasks run\n" +
			"through the project's env (DDEV/Sail/Docker/Local). `keel run` lists them.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			// A member of a root-launch workspace (pnpm/yarn/npm-workspace/turbo
			// with a root dev script) has no runtime of its own: the whole
			// workspace comes up from ONE root command. Resolve to that command
			// before the manifest lookup, so a member with no manifest of its own
			// runs via the root instead of failing "not a keel project", and a
			// member that WAS adopted does not spin up a per-member env/Docker.
			if handled, err := runRootLaunch(cmd, args, dryRun, asJSON); handled {
				return err
			}
			m, err := engine.ReadManifest(".")
			if err != nil {
				return manifestErr(err)
			}
			reg, err := catalog.Registry()
			if err != nil {
				return err
			}
			fw, ok := reg.Get(m.Framework)
			if !ok || len(fw.Tasks) == 0 {
				return fmt.Errorf("no tasks defined for %s", m.Framework)
			}
			env, _ := reg.Get(m.Env)
			if len(args) == 0 {
				if asJSON {
					// The shape editors read: task name plus the command it
					// resolves to under this project's environment.
					type task struct {
						Name    string `json:"name"`
						Command string `json:"command"`
					}
					out := struct {
						Framework string `json:"framework"`
						Env       string `json:"env"`
						Tasks     []task `json:"tasks"`
					}{Framework: m.Framework, Env: m.Env}
					for _, name := range taskNames(fw) {
						out.Tasks = append(out.Tasks, task{name, renderTask(fw.Tasks[name], env.Commands, projectName("."))})
					}
					return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
				}
				fmt.Fprintf(out, "tasks for %s:\n", m.Framework)
				for _, name := range taskNames(fw) {
					fmt.Fprintf(out, "  %-11s %s\n", name, renderTask(fw.Tasks[name], env.Commands, projectName(".")))
				}
				fmt.Fprintln(out, "\nrun one with: keel run <task>")
				return nil
			}
			// Two different failures, and they used to share one message. A task
			// that exists but renders empty was reported as "no task \"dev\" for
			// fastapi (have: dev, lint, test, typecheck)" - which names the task
			// it has just claimed not to have, and sends you looking for a typo
			// that is not there.
			raw, ok := fw.Tasks[args[0]]
			if !ok {
				return fmt.Errorf("no task %q for %s (have: %s)", args[0], m.Framework, strings.Join(taskNames(fw), ", "))
			}
			full := strings.TrimSpace(renderTask(raw, env.Commands, projectName(".")))
			if full == "" {
				return fmt.Errorf("task %q is defined as %q, which is empty under the %s environment: "+
					"it uses a command this environment does not provide", args[0], raw, m.Env)
			}
			fmt.Fprintln(out, "→", full)
			if dryRun {
				return nil
			}
			// `dev` is the long-running one, and the only task worth a name. It
			// takes whatever port the kernel offers and publishes it, so the
			// proxy can route to it and nothing has to agree on a number in
			// advance. A container env brings its own routing and is left alone.
			if args[0] == "dev" && !containerEnv(m.Env) {
				return runDev(cmd, full, projectName("."))
			}
			return engine.ExecRunner{Out: out}.Run(context.Background(), ".", full)
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "print the command without running it")
	c.Flags().BoolVar(&asJSON, "json", false, "list the tasks as JSON, for editors and scripts")
	return c
}

// runRootLaunch handles `keel run` inside a root-launch workspace. A pnpm/yarn/
// npm-workspace or Turborepo whose root has a dev script is launched from one
// root command, so a member (or the root) runs the workspace via that command
// rather than a per-member env spin-up. It reports handled=true when the cwd is
// such a workspace root, or a member under one; in every other case it returns
// (false, nil) and the normal manifest-driven path takes over unchanged.
//
// It resolves the launch two ways so it works before AND after adoption: from an
// adopted root's recorded manifest (project.EffectiveLaunch), and — for a bare,
// un-adopted workspace — by detecting the root directly. The task vocabulary a
// root-launch member exposes is just the launch itself under the name "dev": the
// members do not each carry keel's per-framework task set, they run together.
func runRootLaunch(cmd *cobra.Command, args []string, dryRun, asJSON bool) (handled bool, err error) {
	cwd, wderr := osGetwd()
	if wderr != nil {
		return false, nil
	}
	rootDir, launch := project.EffectiveLaunch(cwd)
	if launch == nil {
		// Not under an adopted root-launch root — detect a bare workspace at the
		// cwd itself (`keel run` at an un-adopted pnpm workspace root).
		if l := project.RootLaunch(cwd); l.Manager != "" {
			rootDir, launch = cwd, &l
		}
	}
	if launch == nil {
		return false, nil
	}
	out := cmd.OutOrStdout()
	full := project.RootRunCommand(rootDir, launch, cwd)
	if len(args) == 0 { // list: a root-launch member exposes just the launch
		if asJSON {
			// The shape editors + the studio read: one "dev" task = the root
			// command. Framework/env name the launcher so the UI can label it.
			type task struct {
				Name    string `json:"name"`
				Command string `json:"command"`
			}
			payload := struct {
				Framework string `json:"framework"`
				Env       string `json:"env"`
				Tasks     []task `json:"tasks"`
			}{Framework: "workspace", Env: launch.Manager, Tasks: []task{{Name: "dev", Command: full}}}
			return true, json.NewEncoder(out).Encode(payload)
		}
		fmt.Fprintf(out, "tasks for this workspace (launched from the root via %s):\n", launch.Manager)
		fmt.Fprintf(out, "  %-11s %s\n", "dev", full)
		fmt.Fprintln(out, "\nrun it with: keel run dev")
		return true, nil
	}
	if args[0] != "dev" && args[0] != launch.DevScript {
		return true, fmt.Errorf("this workspace is launched from the root: the only task is %q (runs %q)", "dev", full)
	}
	fmt.Fprintln(out, "→", full)
	if dryRun {
		return true, nil
	}
	// The launch is a long-running dev server, but a container/router-owning env
	// is the whole workspace's concern here, not a per-member one: run it through
	// the shell in the member's own directory (pnpm --filter targets by name, so
	// the cwd only needs to be inside the workspace).
	return true, engine.ExecRunner{Out: out}.Run(context.Background(), cwd, full)
}

// osGetwd is a seam so a test can drive runRootLaunch without chdir races.
var osGetwd = os.Getwd

// renderTask substitutes the env vocabulary ({{exec}}, {{artisan}}, {{manage}}…)
// and {{project}} into a task command. Empty tokens (e.g. Local's {{exec}}) drop
// out, so "{{exec}} npm test" becomes "npm test".
func renderTask(cmd string, envCommands map[string]string, project string) string {
	if strings.TrimSpace(cmd) == "" {
		return ""
	}
	vars := map[string]string{"project": project}
	for k, v := range envCommands {
		vars[k] = v
	}
	for k, v := range vars {
		cmd = strings.ReplaceAll(cmd, "{{"+k+"}}", v)
	}
	return strings.TrimSpace(cmd)
}

func taskNames(fw recipe.Recipe) []string {
	names := make([]string, 0, len(fw.Tasks))
	for n := range fw.Tasks {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// projectName returns the base name of dir (for {{project}} in tasks).
func projectName(dir string) string {
	abs, err := os.Getwd()
	if err == nil && dir == "." {
		return baseName(abs)
	}
	return baseName(dir)
}

func baseName(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// portFromEnv reads PORT, returning 0 when it is unset. A PORT that is set but
// unusable is an error rather than something to quietly ignore, because the
// caller who set it will be waiting on that number.
func portFromEnv() (int, error) {
	raw := strings.TrimSpace(os.Getenv("PORT"))
	if raw == "" {
		return 0, nil
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("PORT=%q is not a usable port number", raw)
	}
	return port, nil
}

// containerEnv reports whether the environment routes for itself. DDEV has a
// router and a .ddev.site name; compose publishes its own ports. Allocating a
// host port for either would be meaningless.
func containerEnv(env string) bool {
	for _, s := range []string{"ddev", "sail", "docker", "compose"} {
		if strings.Contains(env, s) {
			return true
		}
	}
	return false
}

// runDev starts the dev server on an allocated port, registers it so
// `keel proxy` can route <name>.localhost to it, and deregisters on the way out
// however the process ends.
func runDev(cmd *cobra.Command, full, name string) error {
	out := cmd.OutOrStdout()

	// An explicit PORT wins. Allocating one is a convenience for the common
	// case, not a licence to override a caller who has already chosen: a test
	// harness, a compose file or a developer with a reason all set PORT and
	// expect the server to be there.
	port, err := portFromEnv()
	if err != nil {
		return err
	}
	if port == 0 {
		if port, err = proxy.FreePort(); err != nil {
			return fmt.Errorf("no free port available: %w", err)
		}
	}

	dir, _ := os.Getwd()
	if err := proxy.Register(name, port, os.Getpid(), dir); err != nil {
		// Not fatal. The dev server is the point; the proxy entry is a
		// convenience, and refusing to start over it would be the wrong trade.
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not register with the proxy: %v\n", err)
	} else {
		defer func() { _ = proxy.Deregister(name) }()
		fmt.Fprintf(out, "  %s (via keel proxy)\n", proxy.Entry{Name: name}.URL(DefaultProxyPort))
	}
	fmt.Fprintf(out, "  http://127.0.0.1:%d/\n\n", port)

	// Interrupt has to reach the dev server, and the deferred deregister has to
	// run, so the signal is handled rather than left to kill the process.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runner := engine.ExecRunner{Out: out, Env: []string{fmt.Sprintf("PORT=%d", port)}}
	return runner.Run(ctx, ".", full)
}
