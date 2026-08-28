package envfile

import (
	"reflect"
	"testing"
)

func TestParseAndGet(t *testing.T) {
	f := Parse([]byte("# comment\nexport APP_NAME=\"Keel App\"\nDB_HOST=localhost\n\nEMPTY=\n"))
	if got := f.Get("APP_NAME"); got != "Keel App" {
		t.Errorf("APP_NAME = %q", got)
	}
	if got := f.Get("DB_HOST"); got != "localhost" {
		t.Errorf("DB_HOST = %q", got)
	}
	if !f.Has("EMPTY") {
		t.Error("EMPTY should exist")
	}
	if got := f.Keys(); !reflect.DeepEqual(got, []string{"APP_NAME", "DB_HOST", "EMPTY"}) {
		t.Errorf("keys = %v", got)
	}
}

func TestMergePreservesExisting(t *testing.T) {
	cur := Parse([]byte("APP_NAME=Mine\n"))
	ex := Parse([]byte("APP_NAME=Example\nDB_HOST=db\nDB_PORT=5432\n"))
	added := cur.Merge(ex)
	if !reflect.DeepEqual(added, []string{"DB_HOST", "DB_PORT"}) {
		t.Errorf("added = %v", added)
	}
	if cur.Get("APP_NAME") != "Mine" {
		t.Error("Merge overwrote an existing value")
	}
	if cur.Get("DB_PORT") != "5432" {
		t.Error("Merge did not seed new value")
	}
}

func TestMissingAndEmpty(t *testing.T) {
	ex := Parse([]byte("A=1\nB=2\nC=3\n"))
	cur := Parse([]byte("A=1\nB=changeme\nD=\n"))
	if got := cur.MissingFrom(ex); !reflect.DeepEqual(got, []string{"C"}) {
		t.Errorf("missing = %v", got)
	}
	if got := cur.EmptyKeys(); !reflect.DeepEqual(got, []string{"B", "D"}) {
		t.Errorf("empty = %v", got)
	}
}

func TestSecretUnique(t *testing.T) {
	a, err := Secret(32)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Secret(32)
	if a == b || len(a) < 32 {
		t.Errorf("weak secret: %q / %q", a, b)
	}
}
