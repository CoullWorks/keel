package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// writeWorkspace drops a root-launch pnpm workspace (root dev script + a member
// with its own dev script) into dir.
func writeWorkspace(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		"pnpm-workspace.yaml":   "packages:\n  - 'apps/*'\n",
		"package.json":          `{"name":"root","private":true,"scripts":{"dev":"turbo run dev"}}`,
		"apps/web/package.json": `{"name":"@acme/web","scripts":{"dev":"next dev"},"dependencies":{"next":"15"}}`,
	}
	for rel, body := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// `keel run` at a root-launch workspace root lists ONE task — the root launcher —
// and `keel run dev --dry-run` resolves to that command, never a per-member env.
func TestRunRootLaunchAtWorkspaceRoot(t *testing.T) {
	wd := isolate(t)
	writeWorkspace(t, wd)

	list, err := runRoot(t, "run")
	if err != nil {
		t.Fatalf("run (list): %v", err)
	}
	mustContain(t, list, "launched from the root", "pnpm dev", "keel run dev")

	dev, err := runRoot(t, "run", "dev", "--dry-run")
	if err != nil {
		t.Fatalf("run dev --dry-run: %v", err)
	}
	mustContain(t, dev, "pnpm dev")
}

// `keel run dev` for a MEMBER of a root-launch workspace resolves to the root
// package-manager command scoped to the member (--filter), not a per-member
// docker/env spin-up — even though the member has no keel manifest of its own.
func TestRunRootLaunchMemberResolvesRootCommand(t *testing.T) {
	root := isolate(t)
	writeWorkspace(t, root)
	// Adopt the root so a member inherits the recorded launch.
	if _, err := runRoot(t, "adopt"); err != nil {
		t.Fatalf("adopt root: %v", err)
	}
	// Move into the member and run there.
	member := filepath.Join(root, "apps", "web")
	if err := os.Chdir(member); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "run", "dev", "--dry-run")
	if err != nil {
		t.Fatalf("member run dev --dry-run: %v", err)
	}
	mustContain(t, out, "pnpm --filter @acme/web dev")
	// It must NOT fall through to the manifest path's "not a keel project" error
	// nor try to bring up a per-member environment.
	mustNotContain(t, out, "not a keel project")
}

// After a member is ADOPTED (so it now has its OWN .keel/manifest.yaml), `keel
// run dev` in it must STILL resolve through the parent workspace root's --filter
// command, never a standalone per-member spin-up. This is the "always check the
// parent" rule: adopting a member links it to the root, and run resolution keeps
// walking up to the workspace root even for an adopted member.
func TestRunAdoptedMemberAlwaysChecksParent(t *testing.T) {
	root := isolate(t)
	writeWorkspace(t, root)
	// Adopt the root, then adopt the member itself (it gets its own manifest).
	if _, err := runRoot(t, "adopt"); err != nil {
		t.Fatalf("adopt root: %v", err)
	}
	member := filepath.Join(root, "apps", "web")
	if _, err := runRoot(t, "adopt", member); err != nil {
		t.Fatalf("adopt member: %v", err)
	}
	if err := os.Chdir(member); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "run", "dev", "--dry-run")
	if err != nil {
		t.Fatalf("adopted member run dev --dry-run: %v", err)
	}
	// Resolves through the workspace root, scoped to this member — not the member's
	// own manifest task set, and not a per-member env.
	mustContain(t, out, "pnpm --filter @acme/web dev")
	mustNotContain(t, out, "not a keel project")
}

// `keel run --json` in a root-launch workspace emits the one-task shape the
// studio reads, so the Run tab can label + drive it.
func TestRunRootLaunchJSON(t *testing.T) {
	wd := isolate(t)
	writeWorkspace(t, wd)
	out, err := runRoot(t, "run", "--json")
	if err != nil {
		t.Fatalf("run --json: %v", err)
	}
	mustContain(t, out, `"name":"dev"`, `"command":"pnpm dev"`, `"env":"pnpm"`)
}

// A non-workspace (standalone) project is UNAFFECTED: `keel run` there follows
// the normal manifest-driven path and errors with the usual no-project message
// when there is no manifest — proving the root-launch path did not swallow it.
func TestRunStandaloneUnaffected(t *testing.T) {
	wd := isolate(t)
	// A standalone Next.js project (a package.json with a dev script) but NO
	// workspace markers and no keel manifest.
	if err := os.WriteFile(filepath.Join(wd, "package.json"),
		[]byte(`{"name":"solo","scripts":{"dev":"next dev"},"dependencies":{"next":"15"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := runRoot(t, "run", "dev")
	if err == nil {
		t.Fatal("a standalone project with no manifest should error, not be treated as root-launch")
	}
	if err.Error() != errNoProject.Error() {
		t.Errorf("want the no-project error, got %v", err)
	}
}
