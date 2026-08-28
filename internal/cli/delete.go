package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/profile"
	"github.com/coullworks/keel/internal/project"
	"github.com/spf13/cobra"
)

func deleteCmd() *cobra.Command {
	var yes, keepFiles bool
	c := &cobra.Command{
		Use: "delete [project]",
		Example: "  keel delete                                     # pick from your tracked projects\n" +
			"  keel delete myshop --keep-files                 # untrack, leave the files\n",
		Short: "Delete a project: tear down its env, remove it from disk, and untrack it",
		Long: "Tears down the project's local env (ddev delete / sail down / docker compose down -v,\n" +
			"dropping volumes), deletes the project directory, and removes it from keel's tracked\n" +
			"list. Pass a tracked project name or a path. --keep-files tears down + untracks only.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			reg, err := project.Load()
			if err != nil {
				return err
			}
			if len(args) == 0 {
				if len(reg.Projects) == 0 {
					return fmt.Errorf("no project given and none are tracked (try: keel delete <path>)")
				}
				var b strings.Builder
				for _, p := range reg.Projects {
					fmt.Fprintf(&b, "\n  %s\t%s", p.Name, p.Path)
				}
				return fmt.Errorf("which project? pass a name or path:%s", b.String())
			}
			dir, p, ok := resolveProjectDir(reg, args[0])
			if !ok {
				return fmt.Errorf("not a keel-managed or tracked project: %q", args[0])
			}
			if reason := unsafeDeleteTarget(dir); reason != "" {
				return fmt.Errorf("refusing to delete %s (%s)", dir, reason)
			}

			env := project.DetectEnv(dir)
			action := "untrack + tear down " + env
			if !keepFiles {
				action = "DELETE FROM DISK + " + action
			}
			if !yes {
				okc := false
				if err := huh.NewConfirm().
					Title(fmt.Sprintf("%s: %s?", p.Name, action)).
					Description(dir).
					Affirmative("Yes, delete").Negative("Cancel").
					Value(&okc).Run(); err != nil {
					return err
				}
				if !okc {
					fmt.Fprintln(out, "cancelled")
					return nil
				}
			}

			// 1. tear down the env first (best-effort — a stopped/broken env should
			// not block the delete).
			teardownEnv(cmd.Context(), out, dir, env)
			// 2. remove the files.
			if !keepFiles {
				if err := os.RemoveAll(dir); err != nil {
					return fmt.Errorf("delete files: %w", err)
				}
				fmt.Fprintln(out, "✓ deleted", dir)
			}
			// 3. untrack.
			reg.Remove(dir)
			if err := reg.Save(); err != nil {
				return err
			}
			fmt.Fprintln(out, "✓ untracked", p.Name)
			return nil
		},
	}
	c.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	c.Flags().BoolVar(&keepFiles, "keep-files", false, "tear down the env + untrack, but keep the files on disk")
	return c
}

// teardownEnv drops the project's local dev env and its docker volumes. Runs
// through the same clients a developer would use by hand; errors are ignored so
// a half-provisioned env never blocks the delete.
func teardownEnv(ctx context.Context, out io.Writer, dir, env string) {
	run := func(cmd string) { _ = engine.ExecRunner{Out: out}.Run(ctx, dir, cmd) }
	switch env {
	case "ddev":
		run("ddev delete -Oy") // -O: no snapshot, -y: no prompt; removes containers + db + volumes
	case "sail":
		run("./vendor/bin/sail down -v")
	case "docker":
		run("docker compose down -v") // -v drops named volumes
	}
}

// resolveProjectDir maps a name-or-path argument to an absolute dir + its
// Project. It accepts a tracked project's name or path, or an untracked but
// keel-managed directory (one with a .keel/manifest.yaml).
func resolveProjectDir(reg *project.Registry, arg string) (string, project.Project, bool) {
	for _, p := range reg.Projects {
		if p.Name == arg {
			return p.Path, p, true
		}
	}
	abs := mustAbs(project.Expand(arg))
	for _, p := range reg.Projects {
		if p.Path == abs {
			return abs, p, true
		}
	}
	if fi, err := os.Stat(abs); err == nil && fi.IsDir() {
		if _, err := os.Stat(filepath.Join(abs, ".keel", "manifest.yaml")); err == nil {
			return abs, project.Project{Path: abs, Name: filepath.Base(abs)}, true
		}
	}
	return "", project.Project{}, false
}

// unsafeDeleteTarget guards against catastrophic paths (root, home).
func unsafeDeleteTarget(dir string) string {
	if dir == "" || dir == "/" || dir == filepath.VolumeName(dir)+string(filepath.Separator) {
		return "filesystem root"
	}
	if home, _ := os.UserHomeDir(); home != "" && dir == home {
		return "home directory"
	}
	return ""
}

func resetCmd() *cobra.Command {
	var yes, projectsToo bool
	c := &cobra.Command{
		Use:   "reset",
		Args:  cobra.NoArgs,
		Short: "Reset keel to defaults (profile; optionally the tracked-projects list)",
		Long: "Restores keel's default profile (framework/env/db defaults). With --projects it also\n" +
			"clears the tracked-projects list. Never touches your project files.",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if !yes {
				okc := false
				if err := huh.NewConfirm().
					Title("Reset keel to defaults?").
					Description(profile.Path()).
					Value(&okc).Run(); err != nil {
					return err
				}
				if !okc {
					fmt.Fprintln(out, "cancelled")
					return nil
				}
			}
			if err := profile.Default().Save(); err != nil {
				return err
			}
			fmt.Fprintln(out, "✓ profile reset to defaults")
			if projectsToo {
				reg, err := project.Load()
				if err == nil {
					reg.Projects = nil
					err = reg.Save()
				}
				if err != nil {
					return err
				}
				fmt.Fprintln(out, "✓ cleared tracked projects")
			}
			return nil
		},
	}
	c.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	c.Flags().BoolVar(&projectsToo, "projects", false, "also clear the tracked-projects list")
	return c
}
