package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/coullworks/keel/internal/project"
)

// TestTrackListsWithoutManifest is the parity guarantee behind `keel track`: it
// lists an existing project and detects its stack (the CLI twin of the studio's
// "Add an existing project"), but writes NO manifest. That last part is the
// difference from `keel adopt`, and it's what lets the console offer the studio's
// track-only add.
func TestTrackListsWithoutManifest(t *testing.T) {
	wd := isolate(t)
	scaffoldDjango(t, wd)

	out, err := runRoot(t, "track")
	if err != nil {
		t.Fatalf("track: %v", err)
	}
	mustContain(t, out, "tracking", "django")

	// track must NOT adopt: no manifest is written.
	if _, err := os.Stat(filepath.Join(wd, ".keel", "manifest.yaml")); err == nil {
		t.Fatal("track wrote a .keel/manifest.yaml — that is adopt's job, not track's")
	}

	// The project is now in the registry, detected as django.
	reg, err := project.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Projects) != 1 || reg.Projects[0].Framework != "django" {
		t.Fatalf("expected one tracked django project, got %+v", reg.Projects)
	}
	if reg.Projects[0].Managed {
		t.Fatal("a tracked (not adopted) project must not be marked keel-managed")
	}
}

// TestTrackUndetectableStillTracks: unlike adopt (which refuses an undetectable
// dir), track lists whatever directory you point it at — the studio's add does
// the same, so a project with no recognised stack still appears in the list.
func TestTrackUndetectableStillTracks(t *testing.T) {
	wd := isolate(t)
	out, err := runRoot(t, "track")
	if err != nil {
		t.Fatalf("track should not fail on an undetectable dir: %v", err)
	}
	mustContain(t, out, "tracking")
	reg, _ := project.Load()
	if len(reg.Projects) != 1 || reg.Projects[0].Path != mustAbs(wd) {
		t.Fatalf("expected the current dir tracked, got %+v", reg.Projects)
	}
}
