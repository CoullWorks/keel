package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/project"
)

// trackDir points keel's config at a temp dir and registers dir as a tracked
// project there, so the write tools' tracked-project gate (resolveWriteDir with
// dirTracked) accepts it. Returns dir for convenience. Isolated per test via
// t.Setenv, so one test's registry never leaks into another.
func trackDir(t *testing.T, dir string) string {
	t.Helper()
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	reg, err := project.Load()
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	if _, err := reg.Add(dir); err != nil {
		t.Fatalf("track %s: %v", dir, err)
	}
	if err := reg.Save(); err != nil {
		t.Fatalf("save registry: %v", err)
	}
	return dir
}

// trackedProject makes a real on-disk directory and registers it as tracked, for
// write-tool tests that need a path the tracked-project gate will accept.
func trackedProject(t *testing.T) string {
	t.Helper()
	return trackDir(t, t.TempDir())
}

// readOpts is a read-only server (no write tools registered).
func readOpts() Options { return Options{Version: "t"} }

// writeOpts returns a write-enabled server whose RunKeel records the last call
// and returns the given output/err.
func writeOpts(out string, err error) (Options, *recordedCall) {
	rec := &recordedCall{}
	opts := Options{Version: "t", Write: true, RunKeel: func(_ context.Context, dir string, args []string) (string, error) {
		rec.dir, rec.args = dir, args
		return out, err
	}}
	return opts, rec
}

type recordedCall struct {
	dir  string
	args []string
}

// call is a small helper to invoke a single tool and return its result map.
func call(t *testing.T, opts Options, name, argsJSON string) map[string]any {
	t.Helper()
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + argsJSON + `}}`
	resps := run(t, opts, req)
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	res, _ := resps[0]["result"].(map[string]any)
	if res == nil {
		t.Fatalf("no result map in response: %v", resps[0])
	}
	return res
}

func TestListRecipesFilters(t *testing.T) {
	// no filter → returns a broad set
	all := resultText(map[string]any{"result": call(t, readOpts(), "keel_list_recipes", `{}`)})
	if !strings.Contains(all, "laravel") {
		t.Errorf("unfiltered list_recipes missing laravel: %s", all)
	}
	// filter by kind=framework → only frameworks, no db recipes leak the "DB" kind
	byKind := resultText(map[string]any{"result": call(t, readOpts(), "keel_list_recipes", `{"kind":"framework"}`)})
	if !strings.Contains(byKind, "laravel") {
		t.Errorf("kind=framework missing laravel: %s", byKind)
	}
	if strings.Contains(byKind, `"Kind": "db"`) {
		t.Errorf("kind=framework should not include db recipes: %s", byKind)
	}
	// filter by framework=laravel → recipes applicable to laravel
	byFw := resultText(map[string]any{"result": call(t, readOpts(), "keel_list_recipes", `{"framework":"laravel"}`)})
	if !strings.Contains(byFw, "laravel") {
		t.Errorf("framework=laravel returned nothing useful: %s", byFw)
	}
	// nonsense framework filter → still valid (likely empty-ish) result, not an error
	none := call(t, readOpts(), "keel_list_recipes", `{"framework":"nope-not-real"}`)
	if none["isError"] == true {
		t.Errorf("framework filter with unknown fw should not error: %v", none)
	}
}

func TestListProjects(t *testing.T) {
	// project.Load returns an empty registry when no profile exists, so this must
	// not error even on a machine with no tracked projects.
	res := call(t, readOpts(), "keel_list_projects", `{}`)
	if res["isError"] == true {
		t.Fatalf("list_projects errored: %v", res)
	}
	if resultText(map[string]any{"result": res}) == "" {
		t.Fatal("list_projects returned empty content")
	}
}

func TestOptimizeCleanAndDirty(t *testing.T) {
	// clean dir → no error, summary present
	clean := t.TempDir()
	res := call(t, readOpts(), "keel_optimize", `{"path":"`+clean+`"}`)
	txt := resultText(map[string]any{"result": res})
	if res["isError"] == true {
		t.Fatalf("optimize errored on clean dir: %v", res)
	}
	if !strings.Contains(txt, "summary") || !strings.Contains(txt, "findings") {
		t.Errorf("optimize result missing summary/findings: %s", txt)
	}

	// dirty dir → a committed secret should be reported as an error finding
	dirty := t.TempDir()
	// a hardcoded AWS access key id (matches the optimize scanner) in a source file
	if err := os.WriteFile(filepath.Join(dirty, "config.py"), []byte("KEY = \"AKIAIOSFODNN7EXAMPLE\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res2 := call(t, readOpts(), "keel_optimize", `{"path":"`+dirty+`"}`)
	txt2 := resultText(map[string]any{"result": res2})
	if !strings.Contains(txt2, "aws-access-key") {
		t.Errorf("optimize should flag the hardcoded AWS key: %s", txt2)
	}
}

func TestOptimizeUsesManifestFramework(t *testing.T) {
	// When a .keel/manifest.yaml exists, optimize reads the framework from it
	// (the ReadManifest success branch) rather than sniffing the directory.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".keel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".keel", "manifest.yaml"), []byte("framework: nextjs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := call(t, readOpts(), "keel_optimize", `{"path":"`+dir+`"}`)
	if res["isError"] == true {
		t.Fatalf("optimize errored with a manifest present: %v", res)
	}
	if !strings.Contains(resultText(map[string]any{"result": res}), "summary") {
		t.Errorf("optimize (manifest path) missing summary: %v", res)
	}
}

func TestOptimizeDefaultPath(t *testing.T) {
	// empty path defaults to "." — must not error.
	res := call(t, readOpts(), "keel_optimize", `{}`)
	if res["isError"] == true {
		t.Fatalf("optimize with default path errored: %v", res)
	}
}

func TestUnknownToolCall(t *testing.T) {
	res := call(t, readOpts(), "keel_does_not_exist", `{}`)
	if res["isError"] != true {
		t.Errorf("expected isError for unknown tool, got %v", res)
	}
	if !strings.Contains(resultText(map[string]any{"result": res}), "unknown tool") {
		t.Errorf("unknown-tool message missing: %v", res)
	}
}

// --- write tools ---

func TestScaffoldBuildsArgsAndRuns(t *testing.T) {
	opts, rec := writeOpts("scaffolded ok", nil)
	res := call(t, opts, "keel_scaffold", `{"framework":"laravel","name":"shop","with":["ddev","postgres"]}`)
	if res["isError"] == true {
		t.Fatalf("scaffold errored: %v", res)
	}
	// scaffoldArgs always runs from the cwd (dirAny resolves "." to the absolute
	// working dir) with new <fw> <name> --with a,b --yes.
	cwd, _ := os.Getwd()
	if rec.dir != filepath.Clean(cwd) {
		t.Errorf("scaffold dir = %q, want cwd %q", rec.dir, cwd)
	}
	want := []string{"new", "laravel", "shop", "--with", "ddev,postgres", "--yes"}
	if strings.Join(rec.args, " ") != strings.Join(want, " ") {
		t.Errorf("scaffold args = %v, want %v", rec.args, want)
	}
	if !strings.Contains(resultText(map[string]any{"result": res}), "scaffolded ok") {
		t.Errorf("scaffold output not surfaced: %v", res)
	}
}

func TestScaffoldMinimalArgs(t *testing.T) {
	// no name, no with → just new <fw> --yes (exercises the skipped branches)
	opts, rec := writeOpts("", nil)
	call(t, opts, "keel_scaffold", `{"framework":"django"}`)
	want := []string{"new", "django", "--yes"}
	if strings.Join(rec.args, " ") != strings.Join(want, " ") {
		t.Errorf("scaffold minimal args = %v, want %v", rec.args, want)
	}
}

func TestAdoptRunDBDeployCommerce(t *testing.T) {
	// Each tool acts on a tracked project (adopt only needs the dir to exist), so
	// the tracked-project gate passes and the argv/dir are what reaches RunKeel.
	cases := []struct {
		tool, mkArgs string // %s is substituted with the (tracked) project dir
		wantArgs     []string
		tracked      bool // adopt needs an existing dir, the rest a tracked one
	}{
		{"keel_adopt", `{"path":%q}`, []string{"adopt"}, false},
		{"keel_run", `{"path":%q,"task":"test"}`, []string{"run", "test"}, true},
		{"keel_db", `{"path":%q,"action":"migrate"}`, []string{"db", "migrate"}, true},
		{"keel_commerce_ready", `{"path":%q}`, []string{"commerce", "ready"}, true},
		{"keel_deploy", `{"path":%q,"target":"fly"}`, []string{"deploy", "fly"}, true},
		{"keel_deploy", `{"path":%q}`, []string{"deploy"}, true}, // no target
	}
	for _, c := range cases {
		t.Run(c.tool+" "+strings.Join(c.wantArgs, "_"), func(t *testing.T) {
			dir := t.TempDir()
			if c.tracked {
				trackDir(t, dir)
			}
			args := fmt.Sprintf(c.mkArgs, dir)
			opts, rec := writeOpts("done", nil)
			res := call(t, opts, c.tool, args)
			if res["isError"] == true {
				t.Fatalf("%s errored: %v", c.tool, res)
			}
			if strings.Join(rec.args, " ") != strings.Join(c.wantArgs, " ") {
				t.Errorf("%s args = %v, want %v", c.tool, rec.args, c.wantArgs)
			}
			if rec.dir != filepath.Clean(dir) {
				t.Errorf("%s ran in %q, want %q", c.tool, rec.dir, dir)
			}
		})
	}
}

func TestAdoptExpandsHome(t *testing.T) {
	// A leading ~ must be expanded to an absolute path before RunKeel is invoked.
	// Point HOME at a real dir so ~/proj exists (adopt requires an existing dir).
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "proj"), 0o755); err != nil {
		t.Fatal(err)
	}
	opts, rec := writeOpts("", nil)
	res := call(t, opts, "keel_adopt", `{"path":"~/proj"}`)
	if res["isError"] == true {
		t.Fatalf("adopt errored: %v", res)
	}
	if strings.HasPrefix(rec.dir, "~") {
		t.Errorf("adopt dir not expanded: %q", rec.dir)
	}
	if !filepath.IsAbs(rec.dir) {
		t.Errorf("adopt dir not absolute after expand: %q", rec.dir)
	}
}

func TestWriteHandlerSurfacesError(t *testing.T) {
	// RunKeel error → tool returns an isError result carrying the output + error.
	dir := trackedProject(t)
	opts, _ := writeOpts("build output here", context.DeadlineExceeded)
	res := call(t, opts, "keel_run", `{"path":"`+dir+`","task":"build"}`)
	if res["isError"] != true {
		t.Fatalf("expected isError when RunKeel fails, got %v", res)
	}
	txt := resultText(map[string]any{"result": res})
	if !strings.Contains(txt, "build output here") {
		t.Errorf("failed run should surface the captured output: %s", txt)
	}
}

// TestWriteToolRejectsUntrackedDir is the security invariant: a write tool that
// mutates an existing project must refuse a directory keel does not track, so a
// token-holding agent cannot run a task / reset a db / deploy in an arbitrary
// path. The dir exists but is not registered — the tool must return an error and
// never reach RunKeel.
func TestWriteToolRejectsUntrackedDir(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir()) // an empty registry: nothing is tracked
	untracked := t.TempDir()
	for _, tc := range []struct{ tool, args string }{
		{"keel_run", `{"path":"` + untracked + `","task":"test"}`},
		{"keel_db", `{"path":"` + untracked + `","action":"reset"}`},
		{"keel_deploy", `{"path":"` + untracked + `","target":"fly"}`},
		{"keel_commerce_ready", `{"path":"` + untracked + `"}`},
		{"keel_generate", `{"path":"` + untracked + `","component":"model","name":"Order","dryRun":false}`},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			ran := false
			opts := Options{Version: "t", Write: true, RunKeel: func(context.Context, string, []string) (string, error) {
				ran = true
				return "", nil
			}}
			res := call(t, opts, tc.tool, tc.args)
			if res["isError"] != true {
				t.Fatalf("%s must reject an untracked dir, got %v", tc.tool, res)
			}
			if !strings.Contains(resultText(map[string]any{"result": res}), "not a tracked keel project") {
				t.Errorf("%s rejection should name the tracked-project rule: %v", tc.tool, res)
			}
			if ran {
				t.Errorf("%s reached RunKeel for an untracked dir — the gate leaked", tc.tool)
			}
		})
	}
}

func TestWriteToolsHiddenWithoutRunKeel(t *testing.T) {
	// Write=true but RunKeel nil → write tools must NOT register (safety gate).
	resps := run(t, Options{Version: "t", Write: true}, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools := resps[0]["result"].(map[string]any)["tools"].([]any)
	for _, tt := range tools {
		if n := tt.(map[string]any)["name"].(string); n == "keel_scaffold" {
			t.Fatal("write tools must not register when RunKeel is nil")
		}
	}
}

func TestPingAndInvalidJSON(t *testing.T) {
	// ping replies with an empty result; a malformed line is skipped silently;
	// a params-less tools/call with bad name still yields a result (unknown tool).
	resps := run(t, readOpts(),
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		`{not valid json`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"","arguments":{}}}`,
	)
	if len(resps) != 2 {
		t.Fatalf("expected 2 responses (ping + call; bad json skipped), got %d: %v", len(resps), resps)
	}
	if _, ok := resps[0]["result"].(map[string]any); !ok {
		t.Errorf("ping should return a result map: %v", resps[0])
	}
	if res, _ := resps[1]["result"].(map[string]any); res["isError"] != true {
		t.Errorf("empty tool name should be an unknown-tool error: %v", resps[1])
	}
}
