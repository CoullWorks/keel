package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/coullworks/keel/internal/profile"
	"github.com/coullworks/keel/internal/project"
)

// makeManagedProject creates a keel-managed project dir (with a .keel manifest
// and a local — non-docker — env so teardown is a no-op) and returns its path.
func makeManagedProject(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(dir, ".keel"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Local env → DetectEnv returns "local", so teardownEnv shells out to nothing.
	manifest := "framework: django\nenv: local\nrecipes: [django, local]\n"
	if err := os.WriteFile(filepath.Join(dir, ".keel", "manifest.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manage.py"), []byte("# django\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// delete --yes on a keel-managed path tears down (no-op for local), removes the
// files, and untracks it.
func TestDeleteManagedProject(t *testing.T) {
	isolate(t)
	dir := makeManagedProject(t, "shopA")

	out, err := runRoot(t, "delete", dir, "--yes")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	mustContain(t, out, "deleted", "untracked")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("project dir should be gone, stat err = %v", err)
	}
}

// --keep-files tears down + untracks but leaves the files.
func TestDeleteKeepFiles(t *testing.T) {
	isolate(t)
	dir := makeManagedProject(t, "shopKeep")

	out, err := runRoot(t, "delete", dir, "--yes", "--keep-files")
	if err != nil {
		t.Fatalf("delete --keep-files: %v", err)
	}
	mustContain(t, out, "untracked")
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("files should remain: %v", err)
	}
}

// delete with no arg and no tracked projects gives a helpful error.
func TestDeleteNoArgsNoProjects(t *testing.T) {
	isolate(t)
	_, err := runRoot(t, "delete")
	if err == nil {
		t.Fatal("expected error with no arg and no tracked projects")
	}
	mustContain(t, err.Error(), "no project given")
}

// delete with no arg but tracked projects lists them and asks which.
func TestDeleteNoArgsWithProjects(t *testing.T) {
	isolate(t)
	dir := makeManagedProject(t, "shopList")
	reg, _ := project.Load()
	if _, err := reg.Add(dir); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	_, err := runRoot(t, "delete")
	if err == nil {
		t.Fatal("expected a which-project error")
	}
	mustContain(t, err.Error(), "which project", "shopList")
}

// delete rejects an unknown target.
func TestDeleteUnknownTarget(t *testing.T) {
	isolate(t)
	_, err := runRoot(t, "delete", "not-a-real-project", "--yes")
	if err == nil {
		t.Fatal("expected error for unknown target")
	}
	mustContain(t, err.Error(), "not a keel-managed")
}

// The catastrophic-path guard blocks deleting root or home.
func TestUnsafeDeleteTarget(t *testing.T) {
	if unsafeDeleteTarget("/") == "" {
		t.Error("root should be flagged unsafe")
	}
	home, _ := os.UserHomeDir()
	if home != "" && unsafeDeleteTarget(home) == "" {
		t.Error("home should be flagged unsafe")
	}
	if unsafeDeleteTarget("/some/safe/project") != "" {
		t.Error("a normal path should be safe")
	}
}

// resolveProjectDir accepts a tracked name, a tracked path, and an untracked but
// keel-managed dir.
func TestResolveProjectDir(t *testing.T) {
	isolate(t)
	managed := makeManagedProject(t, "resolveMe")
	reg := &project.Registry{}

	// Untracked but keel-managed → resolvable by path.
	if _, _, ok := resolveProjectDir(reg, managed); !ok {
		t.Error("expected untracked keel-managed dir to resolve")
	}
	// Track it, then resolve by name.
	reg.Projects = []project.Project{{Name: "byname", Path: managed}}
	if dir, _, ok := resolveProjectDir(reg, "byname"); !ok || dir != managed {
		t.Errorf("resolve by name failed: dir=%q ok=%v", dir, ok)
	}
	// A path with no manifest and not tracked → not resolvable.
	if _, _, ok := resolveProjectDir(reg, filepath.Join(t.TempDir(), "empty")); ok {
		t.Error("empty dir should not resolve")
	}
}

// resolveProjectDir resolves a tracked project by its stored path (not just its
// name), covering the by-path branch.
func TestResolveProjectDirByPath(t *testing.T) {
	isolate(t)
	managed := makeManagedProject(t, "bypath")
	reg := &project.Registry{Projects: []project.Project{{Name: "bypath", Path: managed}}}
	dir, p, ok := resolveProjectDir(reg, managed)
	if !ok || dir != managed || p.Name != "bypath" {
		t.Errorf("resolve by path: dir=%q name=%q ok=%v", dir, p.Name, ok)
	}
}

// delete --yes on a tracked project (resolved by name) tears down + removes it.
func TestDeleteByTrackedName(t *testing.T) {
	isolate(t)
	dir := makeManagedProject(t, "byname")
	reg, _ := project.Load()
	if _, err := reg.Add(dir); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "delete", "byname", "--yes")
	if err != nil {
		t.Fatalf("delete byname: %v", err)
	}
	mustContain(t, out, "deleted", "untracked")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("dir should be gone, err=%v", err)
	}
}

// reset --yes restores the default profile.
func TestResetDefaults(t *testing.T) {
	isolate(t)
	// Dirty the profile first.
	p, _ := profile.Load()
	p.Defaults["framework"] = "totally-custom"
	if err := p.Save(); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "reset", "--yes")
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	mustContain(t, out, "profile reset")
	// The default profile should no longer carry our custom value.
	p2, _ := profile.Load()
	if p2.Defaults["framework"] == "totally-custom" {
		t.Error("reset did not restore defaults")
	}
}

// reset --projects also clears the tracked-projects list.
func TestResetProjects(t *testing.T) {
	isolate(t)
	dir := makeManagedProject(t, "shopReset")
	reg, _ := project.Load()
	if _, err := reg.Add(dir); err != nil {
		t.Fatal(err)
	}
	_ = reg.Save()

	out, err := runRoot(t, "reset", "--yes", "--projects")
	if err != nil {
		t.Fatalf("reset --projects: %v", err)
	}
	mustContain(t, out, "cleared tracked projects")
	reg2, _ := project.Load()
	if len(reg2.Projects) != 0 {
		t.Errorf("tracked projects not cleared: %+v", reg2.Projects)
	}
}
