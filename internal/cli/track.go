package cli

import (
	"fmt"

	"github.com/coullworks/keel/internal/project"
	"github.com/spf13/cobra"
)

// trackCmd adds an existing project to keel's project list WITHOUT adopting it.
// It is the CLI twin of the studio's "Add an existing project": the studio calls
// project.Registry.Add (detect the stack, list it, write no manifest), and this
// is the same operation from the terminal. Adopting (`keel adopt`) is the
// separate, heavier step that writes .keel/manifest.yaml to make a tracked
// project keel-managed. Before this, the console's Projects area could only point
// at `keel adopt`, so the two surfaces did not match.
func trackCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "track [path]",
		Short: "Track an existing project so keel lists it (no manifest; run `keel adopt` to manage it)",
		Long: "Adds an existing project to keel's list and detects its stack, exactly like\n" +
			"the studio's \"Add an existing project\". It writes nothing into the project;\n" +
			"run `keel adopt` afterwards to make it keel-managed (db, secrets, deploy and\n" +
			"the studio's per-project tools).",
		Example: "  keel track                 # track the project you are in\n" +
			"  keel track ~/code/myshop",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			reg, err := project.Load()
			if err != nil {
				return err
			}
			// Add handles ~-expansion, validates the directory and detects the stack
			// (or monorepo members) — the same call the studio's add endpoint makes.
			p, err := reg.Add(dir)
			if err != nil {
				return err
			}
			if err := reg.Save(); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			stack := p.Framework
			if stack == "" {
				stack = "unknown stack"
			}
			if p.Managed {
				fmt.Fprintf(out, "✓ tracking %s (%s, already keel-managed)\n", p.Name, stack)
				return nil
			}
			fmt.Fprintf(out, "✓ tracking %s (%s)\n", p.Name, stack)
			fmt.Fprintln(out, "  next: keel adopt   # make it keel-managed (db, secrets, deploy, studio)")
			return nil
		},
	}
}
