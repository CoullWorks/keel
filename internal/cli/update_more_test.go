package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/engine"
)

// update on a freshly-built, untouched project reports "up to date".
func TestUpdateUpToDate(t *testing.T) {
	inTemp(t)
	simulateBuild(t, []string{"fastapi", "fastapi-local", "fastapi-postgres"})
	out, err := runRoot(t, "update")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	mustContain(t, out, "up to date")
}

// update --dry-run reports what would change but writes nothing.
func TestUpdateDryRun(t *testing.T) {
	inTemp(t)
	simulateBuild(t, []string{"fastapi", "fastapi-local", "fastapi-postgres"})
	// Delete a keel-owned file so update would restore it.
	os.Remove(".env.example")
	out, err := runRoot(t, "update", "--dry-run")
	if err != nil {
		t.Fatalf("update --dry-run: %v", err)
	}
	mustContain(t, out, "dry-run")
	// A dry-run must NOT restore the file.
	if _, err := os.Stat(".env.example"); err == nil {
		t.Error("dry-run restored .env.example")
	}
}

// When you edit a keel-owned file and the recipe also changed it (in a different
// region), update runs a 3-way merge and reports completion — covering update's
// "both changed" branch and the final "update complete" summary.
func TestUpdateBothChanged(t *testing.T) {
	inTemp(t)
	simulateBuild(t, []string{"fastapi", "fastapi-local", "fastapi-postgres"})

	current, _ := os.ReadFile("main.py")
	// The recipe "changed" the top of the file: rewind the base to an older
	// version (so base != theirs → recipe changed).
	if err := engine.WriteBase(".", "main.py", "# legacy top\n"+string(current)); err != nil {
		t.Fatal(err)
	}
	// The user appends their own line at the bottom (a different region).
	edited := string(current) + "# my tail\n"
	if err := os.WriteFile("main.py", []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "update")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := os.ReadFile("main.py")
	if !strings.Contains(string(got), "# my tail") {
		t.Errorf("user edit lost:\n%s", got)
	}
	mustContain(t, out, "update complete")
}

// A pre-snapshot project (no .keel/base) that you've edited away from the
// recorded hash gets the new recipe output written alongside as <file>.keel-new
// instead of being clobbered — covering runUpdate's legacy no-base conflict path.
func TestUpdatePreSnapshotConflict(t *testing.T) {
	inTemp(t)
	simulateBuild(t, []string{"fastapi", "fastapi-local", "fastapi-postgres"})
	// Drop the base snapshots to look like a project built before they existed.
	if err := os.RemoveAll(filepath.Join(".keel", "base")); err != nil {
		t.Fatal(err)
	}
	// Edit a keel-owned file so disk != the recorded hash → conflict.
	if err := os.WriteFile("main.py", []byte("# my divergent edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "update")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	mustContain(t, out, "CONFLICT")
	if _, err := os.Stat("main.py.keel-new"); err != nil {
		t.Errorf("expected main.py.keel-new to be written: %v", err)
	}
	// The user's edit is left intact on the real file.
	got, _ := os.ReadFile("main.py")
	if string(got) != "# my divergent edit\n" {
		t.Errorf("user file was clobbered:\n%s", got)
	}
}
