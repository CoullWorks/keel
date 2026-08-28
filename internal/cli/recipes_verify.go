package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
	"github.com/spf13/cobra"
)

func recipesVerifyCmd() *cobra.Command {
	var envID, keep string
	var with []string
	c := &cobra.Command{
		Use:   "verify [framework]",
		Short: "Build a stack in a temp dir and run its smoke steps - proves it boots (needs the real tools)",
		Long: "Composes a framework's default stack, runs the real installers in a throwaway\n" +
			"directory, then runs the recipe smoke checks. This is what keel's CI uses to\n" +
			"prove every recipe actually boots, not just that it composes. Requires the\n" +
			"stack's toolchain (Docker/DDEV/composer/uv/node) on the host.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			reg, err := catalog.Registry()
			if err != nil {
				return err
			}
			var frameworks []recipe.Recipe
			if len(args) == 1 {
				fw, ok := reg.Get(args[0])
				if !ok || fw.Kind != recipe.Framework {
					return fmt.Errorf("%q is not a framework", args[0])
				}
				frameworks = []recipe.Recipe{fw}
			} else {
				frameworks = reg.OfKind(recipe.Framework)
			}

			failed := 0
			for _, fw := range frameworks {
				env := chooseEnv(reg, fw.ID, envID)
				if env == "" {
					fmt.Fprintf(out, "• %s: no env, skipped\n", fw.ID)
					continue
				}
				if err := verifyOne(cmd.Context(), out, reg, fw.ID, env, with, keep); err != nil {
					fmt.Fprintf(out, "✗ %s / %s: %v\n", fw.ID, env, err)
					failed++
				} else {
					label := env
					if len(with) > 0 {
						label += " +" + fmt.Sprint(with)
					}
					fmt.Fprintf(out, "✓ %s / %s booted\n", fw.ID, label)
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d stack(s) failed to verify", failed)
			}
			fmt.Fprintln(out, "✓ all stacks verified")
			return nil
		},
	}
	c.Flags().StringVar(&envID, "env", "", "env recipe id to verify (default: the framework's default env)")
	c.Flags().StringSliceVar(&with, "with", nil, "extra recipe ids to include (e.g. tailwind add-ons)")
	c.Flags().StringVar(&keep, "keep", "", "directory to build in and keep (default: a temp dir that's removed)")
	return c
}

func chooseEnv(reg *recipe.Registry, fw, prefer string) string {
	envs := reg.ForFramework(fw, recipe.Env)
	if prefer != "" {
		for _, e := range envs {
			if e.ID == prefer {
				return e.ID
			}
		}
	}
	for _, e := range envs {
		if e.Default {
			return e.ID
		}
	}
	if len(envs) > 0 {
		return envs[0].ID
	}
	return ""
}

// verifyOne builds fw+env (+ default db + default services + any --with ids) and runs smoke steps.
func verifyOne(ctx context.Context, out io.Writer, reg *recipe.Registry, fw, env string, with []string, keep string) error {
	// The same seeding `keel new` does, through the same function - not a second
	// copy of the rule. The copy that used to live here picked databases with
	// IsDefaultFor instead of CompatibleDefault, so for several frameworks it
	// verified a stack with no database, whose compose.yaml still referenced the
	// database vars and was not valid YAML.
	ids := resolver.SeedDefaults(reg, []string{fw, env}, fw)
	ids = append(ids, with...)
	plan, err := resolver.Resolve(reg, ids)
	if err != nil {
		return fmt.Errorf("resolve: %w", err)
	}

	dir := keep
	throwaway := keep == ""
	if throwaway {
		dir, err = os.MkdirTemp("", "keel-verify-"+fw+"-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
	}
	dir = filepath.Join(dir, fw)

	// Bring down whatever this starts, unless the caller asked to keep the
	// project (--keep means they want it running).
	//
	// Without this, verify deleted its temp directory and left the containers
	// running against a bind mount to a path that no longer existed - the exact
	// state the `down` verb was added for. It also held the ports: the native
	// database publishes a fixed 127.0.0.1:55432, so verifying a second
	// framework failed with "port is already allocated", which says nothing
	// about the recipe being verified and looks like its fault.
	//
	// Registered after the RemoveAll above so it runs before it: defers unwind
	// last-in-first-out, and the containers have to go before the directory
	// they mount.
	//
	// The plan's own `down`, because this code has the plan. Detecting the
	// environment from the directory would miss the native ones, whose database
	// lives in docker-compose.db.yml.
	if throwaway {
		if down := engine.DownCommand(plan, filepath.Base(dir)); down != "" {
			defer func() {
				// A fresh context, not the caller's: the build below runs under a
				// timeout, and the run that most needs tearing down is the one
				// that hit it. Cancelling the teardown with the thing it is
				// cleaning up after would leave exactly the mess this prevents.
				fmt.Fprintf(out, "  down: %s\n", down)
				_ = (engine.ExecRunner{Out: out}).Run(context.Background(), dir, down)
			}()
		}
	}

	bctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	if err := engine.Build(bctx, plan, engine.Options{Dir: dir, Out: out, Trusted: true, DockerUp: DockerRunning}); err != nil {
		return fmt.Errorf("build: %w", err)
	}
	for _, s := range engine.SmokeSteps(plan, filepath.Base(dir)) {
		fmt.Fprintf(out, "  smoke: %s\n", s)
		if err := (engine.ExecRunner{Out: out}).Run(bctx, dir, s); err != nil {
			return fmt.Errorf("smoke failed (%s): %w", s, err)
		}
	}
	return nil
}
