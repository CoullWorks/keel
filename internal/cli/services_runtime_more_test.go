package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestServiceListDDEV: a ddev env reads `ddev describe -j`, lists each service
// with its state, and reports that per-service control is unavailable.
func TestServiceListDDEV(t *testing.T) {
	wd := isolate(t)
	writeManifest(t, wd, "laravel", "ddev")
	stubRuntime(t, map[string]bool{"ddev": true}, func(argv []string) (string, error) {
		if strings.Contains(strings.Join(argv, " "), "describe -j") {
			return `{"raw":{"status":"running","services":{"db":{"status":"running","type":"db"},"web":{"status":"running","type":"web"}}}}`, nil
		}
		return "", nil
	})
	out, err := runRoot(t, "service")
	if err != nil {
		t.Fatalf("keel service (ddev): %v", err)
	}
	mustContain(t, out, "ddev", "db", "web", "up")
	mustContain(t, out, "per-service control is not available")
}

// TestServiceListDDEVNotInstalled: a ddev project with no ddev binary reports a
// calm message, not an error.
func TestServiceListDDEVNotInstalled(t *testing.T) {
	wd := isolate(t)
	writeManifest(t, wd, "laravel", "ddev")
	stubRuntime(t, map[string]bool{}, func(argv []string) (string, error) { return "", nil })
	out, err := runRoot(t, "service")
	if err != nil {
		t.Fatalf("keel service: %v", err)
	}
	if !strings.Contains(out, "DDEV is not installed") {
		t.Errorf("missing ddev should be a calm message, got:\n%s", out)
	}
}

// TestServiceListComposeDaemonDown: the compose file is readable but the daemon
// is not, so every defined service shows down with an explanatory message.
func TestServiceListComposeDaemonDown(t *testing.T) {
	wd := isolate(t)
	writeManifest(t, wd, "laravel", "sail")
	stubRuntime(t, map[string]bool{"docker": true}, func(argv []string) (string, error) {
		line := strings.Join(argv, " ")
		if strings.Contains(line, "config --services") {
			return "app\ndb\n", nil
		}
		return "", errors.New("cannot connect to the docker daemon")
	})
	out, err := runRoot(t, "service")
	if err != nil {
		t.Fatalf("keel service: %v", err)
	}
	mustContain(t, out, "down", "app", "db")
	mustContain(t, out, "Docker is not reachable")
}

// TestParseComposePS covers both output shapes docker emits + the empty case.
func TestParseComposePS(t *testing.T) {
	// Array shape.
	rows, err := parseComposePS(`[{"Service":"db","State":"running"},{"Service":"app","State":"exited"}]`)
	if err != nil || len(rows) != 2 {
		t.Fatalf("array parse: rows=%d err=%v", len(rows), err)
	}
	// NDJSON shape.
	rows, err = parseComposePS(composePSJSON("db", "running", "postgres") + "\n" + composePSJSON("app", "exited", "app"))
	if err != nil || len(rows) != 2 {
		t.Fatalf("ndjson parse: rows=%d err=%v", len(rows), err)
	}
	// Empty is a valid empty result, not an error.
	if rows, err := parseComposePS("   "); err != nil || rows != nil {
		t.Fatalf("empty parse should be (nil,nil), got rows=%v err=%v", rows, err)
	}
	// Malformed is an error.
	if _, err := parseComposePS("{not json"); err == nil {
		t.Fatal("malformed compose ps should error")
	}
}

// TestProjectEnvRecipeUnknownEnv: a manifest naming an env recipe that is not in
// the catalog yields the clear "not a known recipe" error.
func TestProjectEnvRecipeUnknownEnv(t *testing.T) {
	wd := isolate(t)
	writeManifest(t, wd, "laravel", "not-a-real-env")
	_, err := projectEnvRecipe(wd)
	if err == nil || !strings.Contains(err.Error(), "not a known recipe") {
		t.Fatalf("unknown env should be a clear error, got: %v", err)
	}
}

// TestSafeServiceName rejects the shapes that could carry a flag or path.
func TestSafeServiceName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"db", true},
		{"my-service_1.x", true},
		{"", false},
		{"-rf", false},
		{"a b", false},
		{"a/b", false},
		{"a;rm", false},
		{strings.Repeat("x", 61), false},
	}
	for _, tc := range tests {
		if got := safeServiceName(tc.name); got != tc.want {
			t.Errorf("safeServiceName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestCaptureCmdSeamDefault exercises the real captureCmd body once (a trivial
// argv that echoes), so the seam's default is not the one uncovered line.
func TestCaptureCmdSeamDefault(t *testing.T) {
	out, err := captureCmd(context.Background(), t.TempDir(), "printf", "hi")
	if err != nil {
		t.Skipf("printf unavailable on this host: %v", err)
	}
	if !strings.Contains(out, "hi") {
		t.Errorf("captureCmd default should echo command output, got %q", out)
	}
}
