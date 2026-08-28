package cli

import (
	"github.com/coullworks/keel/internal/studio"
	"github.com/spf13/cobra"
)

func studioCmd() *cobra.Command {
	var addr string
	var noOpen bool
	var noMCP bool
	c := &cobra.Command{
		Use: "studio",
		Long: "Opens keel studio: the same engine as the CLI, driven from a browser. Binds\n" +
			"loopback only and refuses cross-site requests, because it can run real builds\n" +
			"and shell commands on this machine.\n\n" +
			"By default it also auto-starts keel's MCP server on /mcp behind the same\n" +
			"loopback + session-token guard, so an AI agent can drive keel live. The\n" +
			"Settings -> \"Connect keel to your agent\" card shows the connection command.\n" +
			"Pass --no-mcp to run the studio without the agent endpoint.\n",
		Example: "  keel studio\n" +
			"  keel studio --no-open           # print the URL, do not open a browser\n" +
			"  keel studio --no-mcp            # do not auto-start the MCP endpoint\n",
		Args:  cobra.NoArgs,
		Short: "Launch keel studio - a visual stack builder in your browser",
		RunE: func(cmd *cobra.Command, args []string) error {
			// So the studio can show the same upgrade notice the CLI does.
			studio.Version, studio.Repo = Version, repo
			return studio.Serve(cmd.OutOrStdout(), addr, !noOpen, noMCP)
		},
	}
	c.Flags().StringVar(&addr, "addr", "127.0.0.1:7373", "loopback address to bind")
	c.Flags().BoolVar(&noOpen, "no-open", false, "don't open the browser automatically")
	c.Flags().BoolVar(&noMCP, "no-mcp", false, "don't auto-start the MCP endpoint at /mcp")
	return c
}
