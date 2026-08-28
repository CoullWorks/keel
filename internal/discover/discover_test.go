package discover

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// mark creates dir (and parents) and drops an empty marker file inside it, so the
// directory looks like an extension of that kind to the walk.
func mark(t *testing.T, root, rel, marker string) string {
	t.Helper()
	dir := filepath.Join(root, rel)
	mfile := filepath.Join(dir, marker)
	if err := os.MkdirAll(filepath.Dir(mfile), 0o755); err != nil { // covers nested markers (config/register.yaml)
		t.Fatal(err)
	}
	if err := os.WriteFile(mfile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestWalkFindsMarkersAndStops verifies the two rules the whole discovery model
// leans on: every directory carrying the marker is found, and the walk does not
// descend into a match (an extension's own subdirectories are never separate
// extensions), so a marker nested inside another extension is invisible.
func TestWalkFindsMarkersAndStops(t *testing.T) {
	root := t.TempDir()
	const marker = "keel.pack.yaml"

	a := mark(t, root, "a", marker)
	b := mark(t, root, "nested/deep/b", marker)
	// A marker inside an already-matched directory must not be reported: the walk
	// stops at the parent.
	mark(t, root, "a/inner", marker)

	got := Walk(marker, []string{root})
	want := []string{a, b}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("Walk = %v, want %v", got, want)
	}
}

// TestWalkSkipsHeavyTrees verifies the walk never descends into dependency,
// build, module-cache or dot directories — the trees that make a naive home-scan
// slow and that never hold a keel extension.
func TestWalkSkipsHeavyTrees(t *testing.T) {
	root := t.TempDir()
	const marker = "config/register.yaml"

	real := mark(t, root, "plugins/real", marker)
	for _, skipped := range []string{"node_modules/x", ".cache/y", "vendor/z", "go/pkg/mod/m"} {
		mark(t, root, skipped, marker)
	}

	got := Walk(marker, []string{root})
	if len(got) != 1 || got[0] != real {
		t.Fatalf("Walk = %v, want only %q (heavy trees must be skipped)", got, real)
	}
}

// TestWalkBoundsDepth verifies a marker buried past MaxDepth is not found, so a
// deep or huge home directory cannot make discovery slow.
func TestWalkBoundsDepth(t *testing.T) {
	root := t.TempDir()
	const marker = "keel.pack.yaml"

	deep := ""
	for i := 0; i <= MaxDepth+1; i++ {
		deep = filepath.Join(deep, "d")
	}
	mark(t, root, deep, marker)

	if got := Walk(marker, []string{root}); len(got) != 0 {
		t.Fatalf("Walk found %v past MaxDepth=%d, want none", got, MaxDepth)
	}
}

// TestHasMarker is the single definition of "this directory is an extension".
func TestHasMarker(t *testing.T) {
	root := t.TempDir()
	dir := mark(t, root, "p", "keel.pack.yaml")
	if !HasMarker(dir, "keel.pack.yaml") {
		t.Error("HasMarker false for a directory that carries the marker")
	}
	if HasMarker(root, "keel.pack.yaml") {
		t.Error("HasMarker true for a directory that does not carry the marker")
	}
}
