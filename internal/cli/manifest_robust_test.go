package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/coullworks/keel/internal/engine"
)

// TestManifestErr classifies a ReadManifest error into the right user message: a
// missing manifest is "not a keel project", a corrupt one keeps its own message.
func TestManifestErr(t *testing.T) {
	tests := []struct {
		name       string
		in         error
		wantNoProj bool
	}{
		{"missing", os.ErrNotExist, true},
		{"malformed", engine.ErrManifestMalformed, false},
		{"other", errors.New("permission denied"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := manifestErr(tc.in)
			if tc.wantNoProj && !errors.Is(got, errNoProject) {
				t.Errorf("manifestErr(%v) = %v, want errNoProject", tc.in, got)
			}
			if !tc.wantNoProj && errors.Is(got, errNoProject) {
				t.Errorf("manifestErr(%v) should NOT be errNoProject", tc.in)
			}
		})
	}
}

// TestCorruptManifestNotMisreported is the end-to-end claim: a hand-edited,
// invalid .keel/manifest.yaml must NOT be reported as "not a keel project" (which
// would send the user editing the wrong thing). `keel run` should surface that the
// manifest is malformed and name the file.
func TestCorruptManifestNotMisreported(t *testing.T) {
	wd := isolate(t)
	kd := filepath.Join(wd, ".keel")
	if err := os.MkdirAll(kd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kd, "manifest.yaml"), []byte("framework: [unterminated"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := runRoot(t, "run")
	if err == nil {
		t.Fatal("keel run on a corrupt manifest should error")
	}
	if errors.Is(err, errNoProject) {
		t.Fatalf("a corrupt manifest was mis-reported as 'not a keel project': %v", err)
	}
	if !errors.Is(err, engine.ErrManifestMalformed) {
		t.Fatalf("expected a malformed-manifest error, got %v", err)
	}
}

// TestMissingManifestStillNoProject guards the other side: with no manifest at
// all, `keel run` still says "not a keel project".
func TestMissingManifestStillNoProject(t *testing.T) {
	isolate(t)
	_, err := runRoot(t, "run")
	if err == nil {
		t.Fatal("keel run with no project should error")
	}
	if !errors.Is(err, errNoProject) {
		t.Fatalf("a missing manifest should be errNoProject, got %v", err)
	}
}
