package mcp

// HTTP/SSE transport for the MCP server. This is the second way keel speaks MCP:
// alongside the stdio loop (Serve), ServeHTTP mounts the SAME initialize/tools/
// list/tools/call dispatch on an http.ServeMux so a later studio wave can auto-
// start MCP and mount it behind the studio's own loopback + X-Keel-Token guard.
//
// The endpoint follows MCP's Streamable HTTP shape: a client POSTs one JSON-RPC
// request to /mcp. A request with an id gets its single JSON-RPC response, and —
// when the client offered `Accept: text/event-stream` — that response is
// delivered as one SSE `message` event (the transport's streaming mode) rather
// than a plain JSON body. A notification (no id) is accepted with 202 and no
// body, matching the spec. The dispatch itself is transport-agnostic
// (server.handle), so HTTP and stdio can never diverge.

import (
	"encoding/json"
	"net/http"
	"strings"
)

// ServeHTTP mounts the MCP HTTP transport on mux at POST /mcp. It registers the
// handler only; it deliberately applies no auth of its own — the caller (the
// studio wave) wraps it in the studio's guardAPI + X-Keel-Token, which is a
// stronger gate than stdio's blanket --write. opts configures the tool set
// exactly as Serve does, so the HTTP surface exposes the identical tools
// (read-only unless opts.Write && opts.RunKeel != nil).
func ServeHTTP(mux *http.ServeMux, opts Options) {
	srv := newServer(opts)
	mux.HandleFunc("/mcp", srv.httpHandler)
}

// httpHandler is the POST /mcp handler: decode one JSON-RPC request, dispatch it
// through the shared server.handle, and write the response as JSON or SSE.
func (s *server) httpHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	resp, reply := s.handle(r.Context(), req)
	if !reply { // notification: accepted, nothing to return
		w.WriteHeader(http.StatusAccepted)
		return
	}
	resp.JSONRPC = "2.0"

	// Streamable HTTP: if the client accepts SSE, deliver the response as a single
	// `message` event; otherwise return it as a plain JSON body.
	if wantsSSE(r) {
		writeSSE(w, resp)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// wantsSSE reports whether the client offered the SSE content type in Accept.
func wantsSSE(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}

// writeSSE emits one JSON-RPC response as a single SSE `message` event and
// flushes it. A response body is one line of JSON, so it needs no chunking.
func writeSSE(w http.ResponseWriter, resp rpcResponse) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	b, _ := json.Marshal(resp)
	_, _ = w.Write([]byte("event: message\ndata: "))
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n\n"))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
