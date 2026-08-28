package cli

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/resolver"
)

// simulateBuild writes a plan's keel-owned files, a manifest recording their
// hashes, and the per-file base snapshots — exactly as a real build would.
func simulateBuild(t *testing.T, ids []string) *resolver.Plan {
	t.Helper()
	reg, err := catalog.Registry()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := resolver.Resolve(reg, ids)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for path, content := range engine.RenderedFiles(plan, ".") {
		if err := engine.WriteFile(".", path, content); err != nil {
			t.Fatal(err)
		}
		if err := engine.WriteBase(".", path, content); err != nil {
			t.Fatal(err)
		}
		files[path] = engine.Sha(content)
	}
	m := &engine.Manifest{Framework: plan.Framework, Recipes: plan.IDs(), Files: files}
	if err := engine.WriteManifestFile(".", m); err != nil {
		t.Fatal(err)
	}
	return plan
}

func inTemp(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
}

// The headline feature: a file the user edited AND the recipe changed is merged
// (3-way), keeping both sets of changes, not overwritten or shunted to .keel-new.
func TestUpdateThreeWayMergeClean(t *testing.T) {
	inTemp(t)
	simulateBuild(t, []string{"fastapi", "fastapi-local", "fastapi-postgres"})

	current, _ := os.ReadFile("main.py") // == the recipe output (theirs)
	// Simulate "the recipe used to have a legacy header line" by rewriting the
	// base snapshot to an older version (so base != theirs → recipe changed).
	if err := engine.WriteBase(".", "main.py", "# legacy header\n"+string(current)); err != nil {
		t.Fatal(err)
	}
	// The user appends their own line at the bottom (a different region).
	edited := string(current) + "# my note\n"
	if err := os.WriteFile("main.py", []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runUpdate(io.Discard, false); err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile("main.py")
	if strings.Contains(string(got), "# legacy header") {
		t.Errorf("recipe's removal of the legacy line was not applied:\n%s", got)
	}
	if !strings.Contains(string(got), "# my note") {
		t.Errorf("user's edit was lost in the merge:\n%s", got)
	}
	if strings.Contains(string(got), "<<<<<<<") {
		t.Errorf("non-overlapping edits should merge without conflict markers:\n%s", got)
	}
	if _, err := os.Stat("main.py.keel-new"); err == nil {
		t.Error("3-way merge should NOT drop a .keel-new file")
	}
}

// Restore a deleted file; keep the user's edit when the recipe is unchanged.
func TestUpdateRestoreAndKeep(t *testing.T) {
	inTemp(t)
	simulateBuild(t, []string{"fastapi", "fastapi-local", "fastapi-postgres"})

	os.Remove(".env.example")                                 // deleted → restore
	os.WriteFile("config.py", []byte("# only mine\n"), 0o644) // edited, recipe unchanged → keep

	if err := runUpdate(io.Discard, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(".env.example"); err != nil {
		t.Errorf("deleted file not restored: %v", err)
	}
	if got, _ := os.ReadFile("config.py"); string(got) != "# only mine\n" {
		t.Errorf("recipe-unchanged file should keep the user's edit, got: %q", got)
	}
}
