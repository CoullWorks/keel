package pack

import (
	"os"
	"path/filepath"
	"testing"
)

// Uninstall deletes a pack's files and its packs.yaml entry, and reports whether
// the pack was installed — the studio Remove button and `keel recipes remove`
// both go through it.
func TestUninstallRemovesFilesAndEntry(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())

	dir := Dir("demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keel.pack.yaml"),
		[]byte("schema_version: 1\nname: demo\nversion: 1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, _ := Load()
	r.Upsert(Installed{Name: "demo", Version: "1.0.0"})
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}

	// A pack that is not installed: false, no error, nothing done.
	if ok, err := Uninstall("nope"); err != nil || ok {
		t.Errorf("uninstalling a missing pack should be (false,nil), got (%v,%v)", ok, err)
	}

	// The installed pack: files and entry both gone.
	ok, err := Uninstall("demo")
	if err != nil || !ok {
		t.Fatalf("uninstall demo = (%v,%v), want (true,nil)", ok, err)
	}
	if _, e := os.Stat(dir); !os.IsNotExist(e) {
		t.Error("uninstall did not delete the pack's files")
	}
	r2, _ := Load()
	if _, found := r2.Get("demo"); found {
		t.Error("uninstall did not remove the packs.yaml entry")
	}
}
