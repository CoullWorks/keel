package e2e

import (
	"os/exec"
	"strings"
	"testing"
)

// TestSweepRemovesLabelledNetworks: the suite swept containers and volumes but
// never networks, so every scenario that brought a compose stack up stranded a
// <project>_default bridge network. They are invisible in the usual places, they
// survive a crashed run, and enough of them exhausts docker's address pool, at
// which point unrelated stacks stop being able to start.
//
// This also pins the safety property that matters more than the cleanup: the
// sweep must only ever touch names carrying labelPrefix. A network belonging to
// someone's real project has to survive it.
func TestSweepRemovesLabelledNetworks(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	ours := labelPrefix + "sweeptest"
	theirs := "keel-sweeptest-not-ours"
	for _, n := range []string{ours, theirs} {
		if out, err := exec.Command("docker", "network", "create", n).CombinedOutput(); err != nil {
			t.Skipf("cannot create network %s: %v: %s", n, err, out)
		}
		defer exec.Command("docker", "network", "rm", n).Run() //nolint:errcheck // best-effort
	}

	sweepOrphans()

	if exists(t, ours) {
		t.Errorf("sweepOrphans left the labelled network %s behind", ours)
	}
	if !exists(t, theirs) {
		t.Errorf("sweepOrphans removed %s, which does not carry %q — it must never touch resources it did not create", theirs, labelPrefix)
	}
}

func exists(t *testing.T, name string) bool {
	t.Helper()
	out, err := exec.Command("docker", "network", "ls", "--format", "{{.Name}}").Output()
	if err != nil {
		t.Fatalf("docker network ls: %v", err)
	}
	for _, l := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(l) == name {
			return true
		}
	}
	return false
}
