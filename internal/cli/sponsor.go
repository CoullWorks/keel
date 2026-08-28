package cli

import (
	"fmt"

	"github.com/coullworks/keel/internal/platform"
	"github.com/spf13/cobra"
)

func sponsorCmd() *cobra.Command {
	return &cobra.Command{
		Use: "sponsor",
		Long: "Where to support keel's development. keel is free and always will be; this is\n" +
			"for people who want it to keep being maintained.\n",
		Args:  cobra.NoArgs,
		Short: "Support keel's development (GitHub Sponsors)",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			url := "https://github.com/sponsors/coullworks"
			fmt.Fprintln(out, "\n♥ keel is free and open source, built in the open by COULLWORKS.")
			fmt.Fprintln(out, "  If it saves you time, you can support its development:")
			fmt.Fprintln(out, "  "+url)
			_ = platform.OpenURL(url)
			return nil
		},
	}
}
