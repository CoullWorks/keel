package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// commerce ready scaffolds the portable agentic-commerce starter kit.
func TestCommerceReadyScaffold(t *testing.T) {
	wd := isolate(t)
	out, err := runRoot(t, "commerce", "ready")
	if err != nil {
		t.Fatalf("commerce ready: %v", err)
	}
	mustContain(t, out, "agents.json", "product.jsonld", "For a full readiness audit")
	for _, f := range []string{
		"agentic-commerce/README.md",
		"agentic-commerce/product.jsonld",
		"agentic-commerce/agents.json",
		"agentic-commerce/feed.spec.md",
	} {
		if _, err := os.Stat(filepath.Join(wd, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
}

// commerce ready is idempotent: a second run skips existing files.
func TestCommerceReadyIdempotent(t *testing.T) {
	isolate(t)
	if _, err := runRoot(t, "commerce", "ready"); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "commerce", "ready")
	if err != nil {
		t.Fatalf("commerce ready (2nd): %v", err)
	}
	mustContain(t, out, "already present")
}

// commerce ready [path] scaffolds into a given directory.
func TestCommerceReadyPath(t *testing.T) {
	wd := isolate(t)
	sub := filepath.Join(wd, "store")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runRoot(t, "commerce", "ready", sub); err != nil {
		t.Fatalf("commerce ready path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sub, "agentic-commerce", "agents.json")); err != nil {
		t.Errorf("expected kit under store/: %v", err)
	}
}

// commerce ready --help documents the flow.
func TestCommerceReadyHelp(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "commerce", "ready", "--help")
	if err != nil {
		t.Fatalf("commerce ready --help: %v", err)
	}
	mustContain(t, out, "agents.json", "--tool")
}

// commerce with no subcommand prints its help (usage).
func TestCommerceBare(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "commerce")
	if err != nil {
		t.Fatalf("commerce: %v", err)
	}
	mustContain(t, out, "ready")
}

// readinessTool resolution order: flag > env > profile.
func TestReadinessTool(t *testing.T) {
	isolate(t)
	if got := readinessTool("my-tool"); got != "my-tool" {
		t.Errorf("flag should win, got %q", got)
	}
	t.Setenv("KEEL_COMMERCE_TOOL", "env-tool")
	if got := readinessTool(""); got != "env-tool" {
		t.Errorf("env should win when no flag, got %q", got)
	}
}

// commerce ready --tool <cmd> scaffolds then hands off to the configured tool.
// `true` is a harmless no-op that accepts the dir arg and exits 0.
func TestCommerceReadyRunsTool(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "commerce", "ready", "--tool", "true")
	if err != nil {
		t.Fatalf("commerce ready --tool true: %v", err)
	}
	mustContain(t, out, "running readiness tool")
}

// runReadinessTool shells out to the tool with the dir appended.
func TestRunReadinessTool(t *testing.T) {
	if err := runReadinessTool(context.Background(), "true", t.TempDir()); err != nil {
		t.Errorf("runReadinessTool(true) = %v, want nil", err)
	}
	if err := runReadinessTool(context.Background(), "false", t.TempDir()); err == nil {
		t.Error("runReadinessTool(false) should error (non-zero exit)")
	}
}

// runReadinessTool must fail cleanly, not panic or emit a cryptic errno, on the
// bad-input cases: a whitespace-only tool (empty argv) and a tool that is not on
// PATH.
func TestRunReadinessToolBadInput(t *testing.T) {
	tests := []struct {
		name    string
		tool    string
		wantMsg string
	}{
		{"whitespace only", "   ", "no readiness tool configured"},
		{"empty", "", "no readiness tool configured"},
		{"not installed", "keel-no-such-readiness-tool-xyz", "not installed or not on PATH"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := runReadinessTool(context.Background(), tc.tool, t.TempDir())
			if err == nil {
				t.Fatalf("runReadinessTool(%q) should error", tc.tool)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantMsg)
			}
		})
	}
}

// scaffoldCommerceKit skips existing files unless force, and returns a sorted
// list of what it wrote.
func TestScaffoldCommerceKit(t *testing.T) {
	dir := t.TempDir()
	written, err := scaffoldCommerceKit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 4 {
		t.Fatalf("expected 4 files written, got %v", written)
	}
	// sorted order
	for i := 1; i < len(written); i++ {
		if written[i] < written[i-1] {
			t.Errorf("output not sorted: %v", written)
		}
	}
	// Second run without force writes nothing.
	written, err = scaffoldCommerceKit(dir, false)
	if err != nil || len(written) != 0 {
		t.Errorf("second run wrote %v (err %v)", written, err)
	}
	// With force it re-writes them all.
	written, _ = scaffoldCommerceKit(dir, true)
	if len(written) != 4 {
		t.Errorf("force run wrote %v", written)
	}
}
