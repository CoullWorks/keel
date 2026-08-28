package cli

import (
	"context"

	"github.com/coullworks/keel/internal/selfupdate"
	"github.com/spf13/cobra"
)

func selfUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use: "self-update",
		Long: "Replaces this binary with the latest GitHub release. The download is verified\n" +
			"against its published checksum and refused outright if that is missing or does\n" +
			"not match.\n",
		Args:    cobra.NoArgs,
		Aliases: []string{"upgrade"},
		Short:   "Update keel to the latest release",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := selfupdate.Update(context.Background(), repo, Version, cmd.OutOrStdout())
			return err
		},
	}
}
