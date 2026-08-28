package merge

import (
	"strings"
	"testing"
)

func TestFastPaths(t *testing.T) {
	if r, c := ThreeWay("a\nb\n", "a\nb\n", "a\nb\n"); r != "a\nb\n" || c != 0 {
		t.Errorf("identical: %q %d", r, c)
	}
	if r, c := ThreeWay("a\nb\n", "a\nb\n", "a\nB\n"); r != "a\nB\n" || c != 0 {
		t.Errorf("only-theirs: %q %d", r, c)
	}
	if r, c := ThreeWay("a\nb\n", "a\nB\n", "a\nb\n"); r != "a\nB\n" || c != 0 {
		t.Errorf("only-ours: %q %d", r, c)
	}
}

func TestNonOverlappingChangesMergeClean(t *testing.T) {
	// ours edits the top, theirs edits the bottom → both kept, no conflict.
	base := "line1\nline2\nline3\nline4\n"
	ours := "LINE1\nline2\nline3\nline4\n"
	theirs := "line1\nline2\nline3\nLINE4\n"
	r, c := ThreeWay(base, ours, theirs)
	if c != 0 {
		t.Fatalf("expected clean merge, got %d conflicts:\n%s", c, r)
	}
	if r != "LINE1\nline2\nline3\nLINE4\n" {
		t.Errorf("merged wrong:\n%q", r)
	}
}

func TestBothInsertDifferentLines(t *testing.T) {
	base := "a\nb\nc\n"
	ours := "a\nX\nb\nc\n"   // insert X after a
	theirs := "a\nb\nY\nc\n" // insert Y after b
	r, c := ThreeWay(base, ours, theirs)
	if c != 0 {
		t.Fatalf("independent inserts should not conflict:\n%s", r)
	}
	if r != "a\nX\nb\nY\nc\n" {
		t.Errorf("got:\n%q", r)
	}
}

func TestOverlappingConflict(t *testing.T) {
	base := "a\nb\nc\n"
	ours := "a\nOURS\nc\n"
	theirs := "a\nTHEIRS\nc\n"
	r, c := ThreeWay(base, ours, theirs)
	if c != 1 {
		t.Fatalf("expected 1 conflict, got %d:\n%s", c, r)
	}
	for _, want := range []string{markOurs, "OURS", markSplit, "THEIRS", markTheirs} {
		if !strings.Contains(r, want) {
			t.Errorf("conflict output missing %q:\n%s", want, r)
		}
	}
	// the unchanged surroundings survive
	if !strings.HasPrefix(r, "a\n") || !strings.HasSuffix(r, "c\n") {
		t.Errorf("surroundings lost:\n%q", r)
	}
}

func TestBothSameChange(t *testing.T) {
	base, ours, theirs := "a\nb\n", "a\nB\n", "a\nB\n"
	if r, c := ThreeWay(base, ours, theirs); c != 0 || r != "a\nB\n" {
		t.Errorf("same change should be clean: %q %d", r, c)
	}
}
