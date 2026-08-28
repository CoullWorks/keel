package studio

// This file auto-starts keel's MCP server inside the studio process and mounts
// it behind the studio's own guards, so `keel studio` gives an AI agent a live,
// authenticated MCP endpoint without a second process or a second trust model.
//
// The stdio MCP transport (`keel mcp`) grants a blanket --write for the life of
// the process. The studio's guard is stronger: the same loopback Host check,
// same-origin refusal, per-session X-Keel-Token and POST-only allowlist that
// protect /api/build also protect /mcp — so an agent must present this session's
// token exactly as the page does, and no other page can. Write tools are enabled
// (an agent driving keel is the point) but every write re-execs the real keel
// binary through mcpRunKeel, exactly as the CLI's `keel mcp --write` does.
//
// The mount is wrapped, not registered directly: mcp.ServeHTTP installs its
// handler on a sub-mux, and the main mux delegates /mcp to it through guardAPI.
// That keeps mcp free of any studio dependency (it applies no auth of its own —
// see internal/mcp/http.go) while the studio supplies the gate.

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"os/exec"

	"github.com/coullworks/keel/internal/mcp"
)

// mcpEndpointPath is where the studio serves MCP, printed next to the studio URL
// and shown in the Settings connect card.
const mcpEndpointPath = "/mcp"

// mcpOptions builds the MCP server options the studio mounts: write-enabled (an
// agent driving keel is the whole point) with RunKeel re-execing this binary, so
// a write tool runs the same deterministic installer the CLI does. It mirrors the
// CLI's `keel mcp --write` construction rather than importing it, keeping studio
// free of a cli dependency.
func mcpOptions() mcp.Options {
	return mcp.Options{Version: Version, Write: true, RunKeel: mcpRunKeel}
}

// mcpRunKeel re-execs this keel binary for MCP write tools, returning combined
// output. It is the studio's copy of the CLI's runKeel: reusing every command's
// real logic without an mcp↔cli import cycle. A crafted argv cannot become a
// shell fragment — args reach the child as separate argv elements.
func mcpRunKeel(ctx context.Context, dir string, args []string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		exe = "keel"
	}
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err = cmd.Run()
	return buf.String(), err
}

// mountMCP delegates POST /mcp on the studio mux to keel's MCP HTTP transport,
// behind the studio's guardAPI. mcp.ServeHTTP registers on its own sub-mux (it
// applies no auth of its own); the studio then guards the delegating handler with
// the same loopback + same-origin + session-token + POST-only gate as every /api
// route, which is a stronger gate than stdio's blanket --write.
func mountMCP(mux *http.ServeMux, tok string, opts mcp.Options) {
	inner := http.NewServeMux()
	mcp.ServeHTTP(inner, opts)
	mux.HandleFunc(mcpEndpointPath, guardAPI(tok, []string{http.MethodPost}, inner.ServeHTTP))
}
