package atomicfile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestWriteFileReplacesAtomically proves the core contract: a successful write
// leaves the file with exactly the new bytes and the requested mode, replacing
// any prior content, and leaves no temp litter behind in the directory.
func TestWriteFileReplacesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.yaml")

	if err := os.WriteFile(path, []byte("old, longer content that must be gone"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("content = %q, want %q (old content not fully replaced)", got, "new")
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v, want 0600 — the rename must apply the requested perm", fi.Mode().Perm())
		}
	}

	// No temp files left in the directory (a "."+base+".tmp-*" would be litter).
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.yaml" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("directory has %v, want only state.yaml (temp file leaked)", names)
	}
}

// TestWriteFileTightensAnExistingLoosMode proves the rename replaces a file that
// was previously more open, so a 0600 write lands 0600 even over a 0644 file —
// the property creds.Save relies on to repair permissions without a chmod.
func TestWriteFileTightensAnExistingLooseMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix modes")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.yaml")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
}
