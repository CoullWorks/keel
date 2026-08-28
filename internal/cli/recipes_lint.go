package cli

import (
	"fmt"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/spf13/cobra"
)

func recipesLintCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "lint",
		Short: "Check every loaded recipe for problems that would only show up at build time",
		Long: "Validates the whole catalogue, including your own recipes and any installed\n" +
			"packs: a {{token}} nothing defines, an alias that shadows a real recipe id,\n" +
			"an appliesTo naming a framework that does not exist, a config recipe whose\n" +
			"condition can never match.\n\n" +
			"These are the failures that otherwise stay quiet. A token nothing defines\n" +
			"reaches the shell literally, and a step whose token renders empty does\n" +
			"nothing at all while the build still reports success.",
		Args: cobra.NoArgs,
		Example: "  keel recipes lint\n" +
			"  keel recipes lint && keel recipes verify   # lint first, then really boot it",
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := catalog.Registry()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			findings := recipe.Lint(reg)
			if len(findings) == 0 {
				fmt.Fprintf(out, "✓ %d recipes, no problems found\n", reg.Len())
				return nil
			}
			for _, f := range findings {
				fmt.Fprintf(out, "✗ %s\n", f)
			}
			return fmt.Errorf("%d problem(s) in %d recipes", len(findings), reg.Len())
		},
	}
}
