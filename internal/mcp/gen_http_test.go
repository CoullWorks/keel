package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/gen"
)

// laravelProject makes a temp dir carrying a .keel manifest that declares a
// Laravel project, so resolveFramework (which reads the manifest) reports
// laravel — the generate tools then offer Laravel's catalogue and its auth
// stack. It also registers the dir as a tracked project so keel_generate's
// tracked-project gate accepts it (read-only tools ignore the registry, so this
// is harmless there). Returns the dir.
func laravelProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".keel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".keel", "manifest.yaml"), []byte("framework: laravel\nenv: ddev\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return trackDir(t, dir)
}

// --- keel_list_generatables (read) ---

func TestListGeneratables(t *testing.T) {
	dir := laravelProject(t)
	res := call(t, readOpts(), "keel_list_generatables", `{"path":"`+dir+`"}`)
	if res["isError"] == true {
		t.Fatalf("list_generatables errored: %v", res)
	}
	txt := resultText(map[string]any{"result": res})
	// A Laravel project offers its code-blocks (model) and, from the recipe
	// catalogue, its auth stack — with the typed inputs an agent needs.
	for _, want := range []string{`"framework": "laravel"`, `"key": "model"`, `"level": "code-block"`, `"level": "stack"`, `"inputs"`, `"fields"`} {
		if !strings.Contains(txt, want) {
			t.Errorf("list_generatables missing %q in:\n%s", want, txt)
		}
	}
}

func TestListGeneratablesFrameworkOverride(t *testing.T) {
	// An explicit framework override wins over the (here absent) manifest, and a
	// Magento project offers Magento's module component.
	res := call(t, readOpts(), "keel_list_generatables", `{"path":"`+t.TempDir()+`","framework":"magento"}`)
	txt := resultText(map[string]any{"result": res})
	if !strings.Contains(txt, `"framework": "magento"`) || !strings.Contains(txt, `"key": "module"`) {
		t.Errorf("magento override did not surface module: %s", txt)
	}
}

func TestListGeneratablesDefaultPath(t *testing.T) {
	// Empty path defaults to "." and must not error even with no manifest there.
	res := call(t, readOpts(), "keel_list_generatables", `{}`)
	if res["isError"] == true {
		t.Fatalf("list_generatables default path errored: %v", res)
	}
}

// keel_generate is write-gated exactly like the other write tools.
func TestGenerateGated(t *testing.T) {
	// read-only server: keel_generate must not be listed.
	resps := run(t, readOpts(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools := resps[0]["result"].(map[string]any)["tools"].([]any)
	for _, tt := range tools {
		if tt.(map[string]any)["name"].(string) == "keel_generate" {
			t.Fatal("keel_generate must not register on a read-only server")
		}
	}
	// Write=true but RunKeel nil: still hidden (the safety gate).
	resps = run(t, Options{Version: "t", Write: true}, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools = resps[0]["result"].(map[string]any)["tools"].([]any)
	for _, tt := range tools {
		if tt.(map[string]any)["name"].(string) == "keel_generate" {
			t.Fatal("keel_generate must not register when RunKeel is nil")
		}
	}
}

// --- keel_generate argv building against a fake RunKeel recorder ---

func TestGenerateArgvTable(t *testing.T) {
	dir := laravelProject(t)
	cases := []struct {
		name     string
		args     string
		wantArgs []string
	}{
		{
			// A model with typed fields → `keel gen model Order --field ...`. dryRun
			// defaults to true, so --dry-run is appended when not overridden.
			name:     "model with fields, default dryRun",
			args:     `{"path":"` + dir + `","component":"model","name":"Order","fields":[{"name":"title","type":"string","index":true,"length":120},{"name":"total","type":"decimal","default":"0"}]}`,
			wantArgs: []string{"gen", "model", "Order", "--field", "title:string,index,len=120", "--field", "total:decimal,default=0", "--dry-run"},
		},
		{
			// dryRun:false drops --dry-run so the generator actually writes.
			name:     "model, dryRun false",
			args:     `{"path":"` + dir + `","component":"model","name":"Post","dryRun":false,"fields":[{"name":"body","type":"text","nullable":true}]}`,
			wantArgs: []string{"gen", "model", "Post", "--field", "body:text,nullable"},
		},
		{
			// A namespaced code-block with no fields.
			name:     "controller namespaced",
			args:     `{"path":"` + dir + `","component":"controller","name":"Admin/UserController","dryRun":false}`,
			wantArgs: []string{"gen", "controller", "Admin/UserController"},
		},
		{
			// The Magento --module flag rides through to the gen argv.
			name:     "with module",
			args:     `{"path":"` + dir + `","component":"model","name":"Post","module":"Acme/Blog","dryRun":false}`,
			wantArgs: []string{"gen", "model", "Post", "--module", "Acme/Blog"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opts, rec := writeOpts("ok", nil)
			res := call(t, opts, "keel_generate", c.args)
			if res["isError"] == true {
				t.Fatalf("generate errored: %v", res)
			}
			if strings.Join(rec.args, " ") != strings.Join(c.wantArgs, " ") {
				t.Errorf("generate argv = %v, want %v", rec.args, c.wantArgs)
			}
			if rec.dir != dir {
				t.Errorf("generate ran in %q, want %q", rec.dir, dir)
			}
		})
	}
}

func TestGenerateStackRunsAdd(t *testing.T) {
	// A stack generatable (auth) is installed via `keel add <recipe...>`, not
	// `keel gen` — "generate auth" is explicit about installing recipes.
	dir := laravelProject(t)
	opts, rec := writeOpts("added", nil)
	res := call(t, opts, "keel_generate", `{"path":"`+dir+`","component":"gen-auth-laravel","name":"auth"}`)
	if res["isError"] == true {
		t.Fatalf("generate auth errored: %v", res)
	}
	want := []string{"add", "laravel-breeze", "--yes"}
	if strings.Join(rec.args, " ") != strings.Join(want, " ") {
		t.Errorf("stack generate argv = %v, want %v", rec.args, want)
	}
}

func TestGenerateSurfacesRunError(t *testing.T) {
	// A generator that fails (RunKeel error) surfaces as an isError result carrying
	// the captured output — the same contract as the other write tools.
	dir := laravelProject(t)
	opts, _ := writeOpts("gen blew up", context.DeadlineExceeded)
	res := call(t, opts, "keel_generate", `{"path":"`+dir+`","component":"model","name":"Order","dryRun":false}`)
	if res["isError"] != true {
		t.Fatalf("expected isError when generation fails, got %v", res)
	}
	if !strings.Contains(resultText(map[string]any{"result": res}), "gen blew up") {
		t.Errorf("failed generate should surface captured output: %v", res)
	}
}

func TestGenerateMissingComponent(t *testing.T) {
	// component is required and validated in-handler before any re-exec. Use a
	// tracked dir so the failure is the component check, not the dir gate.
	dir := laravelProject(t)
	opts, _ := writeOpts("", nil)
	res := call(t, opts, "keel_generate", `{"path":"`+dir+`","name":"Order"}`)
	if res["isError"] != true {
		t.Fatalf("expected isError for missing component, got %v", res)
	}
	if !strings.Contains(resultText(map[string]any{"result": res}), "component is required") {
		t.Errorf("missing-component message wrong: %v", res)
	}
}

func TestGenerateValidationRejects(t *testing.T) {
	dir := laravelProject(t)
	cases := []struct {
		name, args, wantSub string
	}{
		{"bad name", `{"path":"` + dir + `","component":"model","name":"bad;rm -rf"}`, "invalid name"},
		{"empty name", `{"path":"` + dir + `","component":"model","name":""}`, "name is empty"},
		{"bad field type", `{"path":"` + dir + `","component":"model","name":"Order","fields":[{"name":"x","type":"widget"}]}`, "unknown field type"},
		{"bad field name", `{"path":"` + dir + `","component":"model","name":"Order","fields":[{"name":"a b","type":"string"}]}`, "invalid field name"},
		{"duplicate field", `{"path":"` + dir + `","component":"model","name":"Order","fields":[{"name":"a","type":"string"},{"name":"a","type":"int"}]}`, "duplicate field"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// A recorder that must never be called: validation must reject before re-exec.
			called := false
			opts := Options{Version: "t", Write: true, RunKeel: func(context.Context, string, []string) (string, error) {
				called = true
				return "", nil
			}}
			res := call(t, opts, "keel_generate", c.args)
			if res["isError"] != true {
				t.Fatalf("expected isError for %s, got %v", c.name, res)
			}
			if !strings.Contains(resultText(map[string]any{"result": res}), c.wantSub) {
				t.Errorf("%s: message missing %q: %s", c.name, c.wantSub, resultText(map[string]any{"result": res}))
			}
			if called {
				t.Errorf("%s: RunKeel must NOT run when validation fails", c.name)
			}
		})
	}
}

func TestArgHelpers(t *testing.T) {
	// intArg accepts a real int (a caller building args in Go) and a JSON float64
	// (the decode path), and defaults to 0 for anything else.
	if got := intArg(map[string]any{"n": 5}, "n"); got != 5 {
		t.Errorf("intArg(int) = %d, want 5", got)
	}
	if got := intArg(map[string]any{"n": float64(9)}, "n"); got != 9 {
		t.Errorf("intArg(float64) = %d, want 9", got)
	}
	if got := intArg(map[string]any{"n": "x"}, "n"); got != 0 {
		t.Errorf("intArg(string) = %d, want 0", got)
	}
	// fieldsFromArgs skips non-object entries rather than choking on them.
	fs := fieldsFromArgs(map[string]any{"fields": []any{"not-an-object", map[string]any{"name": "a", "type": "string"}}})
	if len(fs) != 1 || fs[0].Name != "a" {
		t.Errorf("fieldsFromArgs skipped/kept wrong: %+v", fs)
	}
	// fieldFlag renders every set attribute in the CLI grammar.
	if got := fieldFlag(gen.Field{Name: "sku", Type: gen.TypeString, Unique: true}); got != "sku:string,unique" {
		t.Errorf("fieldFlag = %q", got)
	}
}

// --- HTTP/SSE transport ---

// postMCP posts one JSON-RPC request to a ServeHTTP-backed mux and returns the
// raw body + response.
func postMCP(t *testing.T, opts Options, accept, body string) (*http.Response, string) {
	t.Helper()
	mux := http.NewServeMux()
	ServeHTTP(mux, opts)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestServeHTTPJSON(t *testing.T) {
	// tools/list over HTTP with a JSON Accept returns a plain JSON-RPC response.
	resp, body := postMCP(t, Options{Version: "t"}, "application/json",
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type = %q, want json", ct)
	}
	var r map[string]any
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, body)
	}
	tools := r["result"].(map[string]any)["tools"].([]any)
	if len(tools) == 0 {
		t.Error("no tools listed over HTTP")
	}
}

func TestServeHTTPSSE(t *testing.T) {
	// Same dispatch, SSE mode: the response arrives as one `message` event.
	resp, body := postMCP(t, Options{Version: "t"}, "text/event-stream",
		`{"jsonrpc":"2.0","id":7,"method":"initialize","params":{}}`)
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("content-type = %q, want event-stream", ct)
	}
	if !strings.Contains(body, "event: message") {
		t.Fatalf("SSE frame missing event line: %s", body)
	}
	data := extractSSEData(t, body)
	var r map[string]any
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		t.Fatalf("SSE data not JSON: %v (%s)", err, data)
	}
	if r["result"].(map[string]any)["protocolVersion"] != protocolVersion {
		t.Errorf("SSE initialize bad protocolVersion: %v", r["result"])
	}
}

func TestServeHTTPToolCall(t *testing.T) {
	// A tools/call over HTTP reaches the same handler the stdio loop uses.
	_, body := postMCP(t, Options{Version: "t"}, "application/json",
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"keel_list_frameworks","arguments":{}}}`)
	if !strings.Contains(body, "laravel") {
		t.Errorf("http tools/call did not run list_frameworks: %s", body)
	}
}

func TestServeHTTPNotification(t *testing.T) {
	// A notification (no id) is accepted with 202 and no body.
	resp, body := postMCP(t, Options{Version: "t"}, "application/json",
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("notification status = %d, want 202", resp.StatusCode)
	}
	if strings.TrimSpace(body) != "" {
		t.Errorf("notification should have no body, got %q", body)
	}
}

func TestServeHTTPRejectsNonPost(t *testing.T) {
	mux := http.NewServeMux()
	ServeHTTP(mux, Options{Version: "t"})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /mcp status = %d, want 405", resp.StatusCode)
	}
}

func TestServeHTTPBadJSON(t *testing.T) {
	resp, _ := postMCP(t, Options{Version: "t"}, "application/json", `{not json`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad JSON status = %d, want 400", resp.StatusCode)
	}
}

func TestServeHTTPWriteToolsGated(t *testing.T) {
	// The HTTP transport exposes the identical tool set: write tools appear only
	// with Write && RunKeel, exactly as over stdio.
	opts := Options{Version: "t", Write: true, RunKeel: func(context.Context, string, []string) (string, error) { return "", nil }}
	_, body := postMCP(t, opts, "application/json", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if !strings.Contains(body, "keel_generate") {
		t.Errorf("write-enabled HTTP server should list keel_generate: %s", body)
	}
}

// extractSSEData pulls the JSON payload out of the single `data: ` line of an SSE
// frame.
func extractSSEData(t *testing.T, frame string) string {
	t.Helper()
	for _, line := range strings.Split(frame, "\n") {
		if strings.HasPrefix(line, "data: ") {
			return strings.TrimPrefix(line, "data: ")
		}
	}
	t.Fatalf("no data line in SSE frame: %s", frame)
	return ""
}
