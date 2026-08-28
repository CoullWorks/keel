package studio

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The studio auto-starts MCP by default: newMux with MCP options mounts POST /mcp
// behind the same guardAPI as every /api route, so an agent gets a live endpoint
// in the studio's own process.
func TestStudioMountsMCPWhenEnabled(t *testing.T) {
	o := mcpOptions()
	mux := newMux(testTok, &o)

	// A JSON-RPC initialize with the session token succeeds through the guard.
	req := httptest.NewRequest("POST", "http://127.0.0.1/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	req.Host = "127.0.0.1"
	req.Header.Set(tokenHeader, testTok)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("guarded /mcp initialize: want 200, got %d: %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "protocolVersion") {
		t.Errorf("initialize response missing protocolVersion: %s", w.Body)
	}
}

// --no-mcp leaves /mcp unmounted: newMux with nil options must not serve it.
func TestStudioOmitsMCPWhenDisabled(t *testing.T) {
	mux := newMux(testTok, nil)
	req := httptest.NewRequest("POST", "http://127.0.0.1/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	req.Host = "127.0.0.1"
	req.Header.Set(tokenHeader, testTok)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	// With no /mcp route the default mux handler serves index.html at "/", but a
	// POST to /mcp is not "/", so it 404s. Either way it is NOT a 200 MCP reply.
	if w.Code == http.StatusOK && strings.Contains(w.Body.String(), "protocolVersion") {
		t.Errorf("/mcp answered an MCP request while --no-mcp was set")
	}
}

// The MCP mount is behind the session token: a request with no token is refused
// exactly like every /api route, which is stronger than stdio's blanket --write.
func TestMCPEndpointRefusesWithoutToken(t *testing.T) {
	o := mcpOptions()
	mux := newMux(testTok, &o)
	req := httptest.NewRequest("POST", "http://127.0.0.1/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	req.Host = "127.0.0.1" // loopback host, but no X-Keel-Token
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("/mcp without a token: want 403, got %d", w.Code)
	}
}

// The MCP mount refuses a GET (the transport is POST-only), from the guard's
// method allowlist.
func TestMCPEndpointRefusesGet(t *testing.T) {
	o := mcpOptions()
	mux := newMux(testTok, &o)
	req := httptest.NewRequest("GET", "http://127.0.0.1/mcp", nil)
	req.Host = "127.0.0.1"
	req.Header.Set(tokenHeader, testTok)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /mcp: want 405, got %d", w.Code)
	}
}
