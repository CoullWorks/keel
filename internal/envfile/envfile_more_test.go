package envfile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestParseEdgeCases covers the malformed-line, quote and comment branches of
// Parse: single quotes, no-equals lines, a leading = (eq < 1), whitespace around
// the key, an unbalanced quote (kept verbatim), and a mid-value '#'.
func TestParseEdgeCases(t *testing.T) {
	src := strings.Join([]string{
		"# a comment",
		"   # indented comment",
		"",
		"   ",
		"export SINGLE='quoted value'",
		"NOEQUALS",         // no '=' -> skipped
		"=leadingeq",       // eq==0 -> skipped
		"  SPACED  =  hi ", // key/value trimmed
		`UNBALANCED="oops`, // only a leading quote -> not stripped
		"MID=a#b",          // '#' inside a value is kept
		"EMPTYQ=\"\"",      // empty quoted value -> ""
	}, "\n")
	f := Parse([]byte(src))

	cases := map[string]string{
		"SINGLE":     "quoted value",
		"SPACED":     "hi",
		"UNBALANCED": `"oops`,
		"MID":        "a#b",
		"EMPTYQ":     "",
	}
	for k, want := range cases {
		if !f.Has(k) {
			t.Fatalf("expected key %q to be parsed", k)
		}
		if got := f.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if f.Has("NOEQUALS") {
		t.Error("a line without '=' must be skipped")
	}
	if f.Has("") || f.Has("leadingeq") {
		t.Error("a line beginning with '=' must be skipped")
	}
}

// TestParseDuplicateKeyLastWins exercises the set() update-existing branch:
// a repeated key keeps its declaration position but takes the later value.
func TestParseDuplicateKeyLastWins(t *testing.T) {
	f := Parse([]byte("A=1\nB=2\nA=3\n"))
	if got := f.Get("A"); got != "3" {
		t.Errorf("A = %q, want last-wins 3", got)
	}
	if got := f.Keys(); !reflect.DeepEqual(got, []string{"A", "B"}) {
		t.Errorf("duplicate must not add a new key: keys = %v", got)
	}
}

// TestGetAbsent covers Get's not-found branch.
func TestGetAbsent(t *testing.T) {
	f := Parse([]byte("A=1\n"))
	if got := f.Get("NOPE"); got != "" {
		t.Errorf("Get(absent) = %q, want empty", got)
	}
}

// TestSetOnZeroValueFile covers Set initialising a nil index (a File built with
// a bare struct literal rather than Parse).
func TestSetOnZeroValueFile(t *testing.T) {
	var f File // zero value: index is nil
	f.Set("KEY", "v1")
	if got := f.Get("KEY"); got != "v1" {
		t.Fatalf("Set on zero File failed: %q", got)
	}
	f.Set("KEY", "v2") // update path
	if got := f.Get("KEY"); got != "v2" {
		t.Errorf("Set update = %q, want v2", got)
	}
}

// TestLoadRoundTrip covers Load (present file) and Render, and proves a
// parse -> render round trip is stable in declaration order.
func TestLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	body := "APP_NAME=Keel\nDB_HOST=localhost\nEMPTY=\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := f.Render(); got != body {
		t.Errorf("round trip:\n got  %q\n want %q", got, body)
	}
}

// TestLoadMissingFileIsEmpty covers Load's os.IsNotExist branch.
func TestLoadMissingFileIsEmpty(t *testing.T) {
	f, err := Load(filepath.Join(t.TempDir(), "does-not-exist.env"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if len(f.Keys()) != 0 {
		t.Errorf("missing file should yield empty File, got %v", f.Keys())
	}
	// The returned File must be usable (non-nil index).
	f.Set("X", "1")
	if f.Get("X") != "1" {
		t.Error("File from missing-Load should be writable")
	}
}

// TestLoadReadError covers Load's generic (non-not-exist) error branch by
// pointing at a directory, which os.ReadFile refuses.
func TestLoadReadError(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil {
		t.Error("expected an error reading a directory as a file")
	}
}

// TestSecretClampAndEntropy covers Secret's nbytes<16 clamp and the happy path.
func TestSecretClampAndEntropy(t *testing.T) {
	// Below the floor: clamped up to 16 bytes -> a non-empty token.
	small, err := Secret(1)
	if err != nil {
		t.Fatal(err)
	}
	if small == "" {
		t.Error("clamped secret should be non-empty")
	}
	// RawURLEncoding must not contain padding or unsafe chars.
	if strings.ContainsAny(small, "=+/") {
		t.Errorf("secret is not URL-safe: %q", small)
	}
	big, _ := Secret(48)
	if big == small {
		t.Error("distinct sizes/randomness should differ")
	}
}

// TestMergeReturnsNilWhenNothingAdded covers Merge with no drift.
func TestMergeReturnsNilWhenNothingAdded(t *testing.T) {
	cur := Parse([]byte("A=1\nB=2\n"))
	ex := Parse([]byte("A=x\nB=y\n"))
	if added := cur.Merge(ex); added != nil {
		t.Errorf("no keys should be added, got %v", added)
	}
}

// TestEmptyKeysPlaceholders covers each placeholder token EmptyKeys recognises.
func TestEmptyKeysPlaceholders(t *testing.T) {
	f := Parse([]byte(strings.Join([]string{
		"BLANK=",
		"SPACES=   ",
		"CHANGEME=changeme",
		"DASH=change-me",
		"TODO=TODO",
		"XXX=xxx",
		"SEC=secret",
		"YOURS=your-secret",
		"YOURHERE=your_secret_here",
		"REAL=actual-value",
	}, "\n")))
	got := f.EmptyKeys()
	want := []string{"BLANK", "CHANGEME", "DASH", "SEC", "SPACES", "TODO", "XXX", "YOURHERE", "YOURS"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EmptyKeys =\n %v\nwant\n %v", got, want)
	}
	// A genuine value is never an empty key.
	for _, k := range got {
		if k == "REAL" {
			t.Error("a real value must not be reported empty")
		}
	}
}
