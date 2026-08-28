package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/coullworks/keel/internal/envfile"
)

func TestPatchFile(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	os.WriteFile(env, []byte("APP_NAME=Demo\nDB_CONNECTION=sqlite\n"), 0o644)

	ok, err := PatchFile(env, map[string]string{"DB_CONNECTION": "pgsql", "DB_HOST": "db", "DB_PORT": "5432"})
	if err != nil || !ok {
		t.Fatalf("patch: ok=%v err=%v", ok, err)
	}
	f, _ := envfile.Load(env)
	if f.Get("DB_CONNECTION") != "pgsql" {
		t.Errorf("DB_CONNECTION not updated: %q", f.Get("DB_CONNECTION"))
	}
	if f.Get("DB_HOST") != "db" || f.Get("DB_PORT") != "5432" {
		t.Errorf("new keys not added: host=%q port=%q", f.Get("DB_HOST"), f.Get("DB_PORT"))
	}
	if f.Get("APP_NAME") != "Demo" {
		t.Error("existing key clobbered")
	}
}

func TestPatchMissingFileIsNoop(t *testing.T) {
	ok, err := PatchFile(filepath.Join(t.TempDir(), "nope.env"), map[string]string{"X": "1"})
	if err != nil || ok {
		t.Errorf("missing file should be a silent no-op: ok=%v err=%v", ok, err)
	}
}
