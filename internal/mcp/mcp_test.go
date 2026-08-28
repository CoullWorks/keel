package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// run feeds newline-delimited JSON-RPC requests through Serve and returns the
// decoded responses in order.
func run(t *testing.T, opts Options, reqs ...string) []map[string]any {
	t.Helper()
	var out strings.Builder
	if err := Serve(context.Background(), strings.NewReader(strings.Join(reqs, "\n")+"\n"), &out, opts); err != nil {
		t.Fatal(err)
	}
	var resps []map[string]any
	sc := bufio.NewScanner(strings.NewReader(out.String()))
	// Match the server's line buffer (mcp.go): a full list_recipes response is a
	// single JSON line well over the 64KB scanner default.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) == nil {
			resps = append(resps, m)
		}
	}
	return resps
}

// resultText digs the text block out of a tools/call result.
func resultText(r map[string]any) string {
	res, _ := r["result"].(map[string]any)
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		return ""
	}
	c0, _ := content[0].(map[string]any)
	s, _ := c0["text"].(string)
	return s
}

func TestInitializeAndToolsList(t *testing.T) {
	resps := run(t, Options{Version: "test"},
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`, // notification: no reply
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	)
	if len(resps) != 2 { // the notification must NOT produce a response
		t.Fatalf("expected 2 responses (notification suppressed), got %d", len(resps))
	}
	init := resps[0]["result"].(map[string]any)
	if init["protocolVersion"] != protocolVersion {
		t.Errorf("bad protocolVersion: %v", init["protocolVersion"])
	}
	// read-only server exposes exactly the read tools, no write tools
	tools := resps[1]["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, tt := range tools {
		names[tt.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"keel_list_frameworks", "keel_list_recipes", "keel_resolve_plan", "keel_list_projects", "keel_optimize"} {
		if !names[want] {
			t.Errorf("missing read tool %q", want)
		}
	}
	if names["keel_scaffold"] || names["keel_db"] {
		t.Error("write tools must NOT appear without --write")
	}
}

func TestWriteToolsGated(t *testing.T) {
	// With Write=true and a RunKeel injected, write tools appear.
	opts := Options{Version: "t", Write: true, RunKeel: func(context.Context, string, []string) (string, error) { return "", nil }}
	resps := run(t, opts, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools := resps[0]["result"].(map[string]any)["tools"].([]any)
	got := map[string]bool{}
	for _, tt := range tools {
		got[tt.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"keel_scaffold", "keel_adopt", "keel_db", "keel_deploy"} {
		if !got[want] {
			t.Errorf("write tool %q missing with --write", want)
		}
	}
}

func TestToolCallResolvePlan(t *testing.T) {
	resps := run(t, Options{Version: "t"},
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"keel_list_frameworks","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"keel_resolve_plan","arguments":{"ids":["fastapi","fastapi-local","fastapi-postgres"]}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"keel_resolve_plan","arguments":{"ids":["laravel","bogus-id"]}}}`,
	)
	if !strings.Contains(resultText(resps[0]), "laravel") {
		t.Errorf("list_frameworks missing laravel: %s", resultText(resps[0]))
	}
	if !strings.Contains(resultText(resps[1]), "fastapi") || !strings.Contains(resultText(resps[1]), "\"steps\"") {
		t.Errorf("resolve_plan bad result: %s", resultText(resps[1]))
	}
	// bad id → isError result, not a crash
	if res, _ := resps[2]["result"].(map[string]any); res["isError"] != true {
		t.Errorf("expected isError for bad recipe id, got %v", resps[2]["result"])
	}
}

func TestUnknownMethod(t *testing.T) {
	resps := run(t, Options{Version: "t"}, `{"jsonrpc":"2.0","id":9,"method":"no/such"}`)
	if resps[0]["error"] == nil {
		t.Error("expected JSON-RPC error for unknown method")
	}
}
