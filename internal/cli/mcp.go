package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/coullworks/keel/internal/mcp"
	"github.com/spf13/cobra"
)

func mcpCmd() *cobra.Command {
	var write bool
	c := &cobra.Command{
		Use:   "mcp",
		Args:  cobra.NoArgs,
		Short: "Run keel as an MCP server so AI coding agents can drive it",
		Long: "Speaks the Model Context Protocol over stdio. Read-only by default:\n" +
			"agents can list frameworks/recipes, resolve/preview a plan, list projects,\n" +
			"list what's generatable in a project, and run the optimize scan. Pass\n" +
			"--write to also expose scaffold/adopt/db/deploy and generate (which run\n" +
			"the real installers/generators).\n\n" +
			"Register keel with your MCP client. Its server command is `keel mcp`, e.g.\n" +
			"add to your client's mcp config: { \"command\": \"keel\", \"args\": [\"mcp\"] }",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := mcp.Options{Version: Version, Write: write}
			if write {
				opts.RunKeel = runKeel
			}
			// Real stdin/stdout on purpose: MCP is a stdio JSON-RPC protocol and
			// the process's streams ARE the transport. Do not route this through
			// cmd.OutOrStdout().
			return mcp.Serve(context.Background(), os.Stdin, os.Stdout, opts)
		},
	}
	c.Flags().BoolVar(&write, "write", false, "also expose write tools (scaffold, adopt, db, deploy)")
	return c
}

// runKeel re-execs this keel binary for MCP write tools, returning combined
// output. Reuses every command's real logic without an mcp↔cli import cycle.
func runKeel(ctx context.Context, dir string, args []string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		// os.Executable is near-infallible, but if it fails fall back to a keel on
		// PATH and say so clearly when there is none, rather than handing the caller
		// a raw "executable file not found" from the re-exec below.
		if p, lerr := exec.LookPath("keel"); lerr == nil {
			exe = p
		} else {
			return "", fmt.Errorf("cannot locate the keel binary to run %q: %w", args, err)
		}
	}
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err = cmd.Run()
	return buf.String(), err
}
