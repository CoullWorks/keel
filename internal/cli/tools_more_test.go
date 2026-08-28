package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/resolver"
)

func resolvePlan(t *testing.T, ids ...string) *resolver.Plan {
	t.Helper()
	reg, err := catalog.Registry()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := resolver.Resolve(reg, ids)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

// preflight prints the required-tools checklist. For a containerised Laravel/DDEV
// plan the only non-auto tool is git; docker/ddev are shown as auto-installable
// and never block. git is present in this repo's CI/host, so preflight passes.
func TestPreflightContainerized(t *testing.T) {
	plan := resolvePlan(t, "laravel", "ddev", "postgres")
	var buf bytes.Buffer
	err := preflight(plan, &buf)
	out := buf.String()
	mustContain(t, out, "pre-flight", "git")
	if err != nil {
		// Only acceptable failure is a genuinely missing git on the host.
		mustContain(t, err.Error(), "missing required tool")
	}
}

// preflight blocks when a non-auto-installable tool (e.g. uv for a Local python
// plan) is missing from PATH. We point PATH at an empty dir so uv can't resolve.
func TestPreflightBlocksOnMissingTool(t *testing.T) {
	plan := resolvePlan(t, "fastapi", "fastapi-local", "fastapi-postgres")
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	var buf bytes.Buffer
	err := preflight(plan, &buf)
	if err == nil {
		t.Fatal("expected preflight to block on a missing non-auto tool")
	}
	mustContain(t, err.Error(), "missing required tool")
	mustContain(t, buf.String(), "pre-flight")
}

// neededTools returns docker (+ ddev for a DDEV env) for a containerised plan,
// and nothing for a Local env.
func TestNeededTools(t *testing.T) {
	ddev := neededTools(resolvePlan(t, "laravel", "ddev", "postgres"))
	if len(ddev) == 0 || ddev[0] != "docker" {
		t.Errorf("ddev plan neededTools = %v, want docker first", ddev)
	}
	joined := strings.Join(ddev, ",")
	if !strings.Contains(joined, "ddev") {
		t.Errorf("ddev plan should need ddev, got %v", ddev)
	}
	local := neededTools(resolvePlan(t, "fastapi", "fastapi-local", "fastapi-postgres"))
	if len(local) != 0 {
		t.Errorf("local plan neededTools = %v, want none", local)
	}
}

// ensureTools is a no-op (returns nil) for a plan whose env needs no external
// tools (Local python).
func TestEnsureToolsLocalNoop(t *testing.T) {
	plan := resolvePlan(t, "fastapi", "fastapi-local", "fastapi-postgres")
	if err := ensureTools(context.Background(), io.Discard, plan); err != nil {
		t.Errorf("ensureTools (local) = %v, want nil", err)
	}
}

// The Magento-specific auth flow became the generalized credential collector;
// its behaviour is covered in credentials_test.go.

// runKeel re-execs os.Executable() (the test binary here) with the given args,
// returning combined output. We invoke it with a test-run pattern that matches
// nothing so the nested process exits 0 immediately (no recursion, no installer).
func TestRunKeel(t *testing.T) {
	out, err := runKeel(context.Background(), t.TempDir(), []string{"-test.run=^$KeelRunKeelNoMatch$", "-test.count=1"})
	if err != nil {
		t.Fatalf("runKeel: %v (out %q)", err, out)
	}
	if !strings.Contains(out, "no tests to run") && !strings.Contains(out, "ok") && !strings.Contains(out, "PASS") {
		t.Errorf("runKeel produced unexpected output: %q", out)
	}
}
