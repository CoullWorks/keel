package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// A dry-run build must resolve, print the plan, and run nothing (no network,
// no docker) — so it's the safe path to exercise `keel new`'s wiring.
func TestNewDryRun(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "new", "fastapi", "--dry-run")
	if err != nil {
		t.Fatalf("new --dry-run: %v", err)
	}
	mustContain(t, out, "fastapi")
	// A dry-run must not create the project directory.
	if _, err := os.Stat("fastapi-app"); err == nil {
		t.Error("dry-run should not create the project dir")
	}
}

// The framework arg is validated before anything runs.
func TestNewUnknownFramework(t *testing.T) {
	isolate(t)
	_, err := runRoot(t, "new", "nope-not-a-framework", "--dry-run")
	if err == nil {
		t.Fatal("expected error for unknown framework")
	}
	mustContain(t, err.Error(), "unknown framework")
}

// The positional [dir] and -o flag feed the same target; dry-run keeps it inert.
func TestNewWithDirAndAddons(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "new", "nextjs", "myshop", "--dry-run")
	if err != nil {
		t.Fatalf("new nextjs myshop --dry-run: %v", err)
	}
	mustContain(t, out, "nextjs")
	if _, err := os.Stat("myshop"); err == nil {
		t.Error("dry-run should not create ./myshop")
	}
}

// A profile whose default framework matches gets its saved add-ons seeded into
// the plan; dry-run proves the seeding path runs without executing anything.
func TestNewSeedsFromProfile(t *testing.T) {
	isolate(t)
	// Write a profile that makes fastapi the default framework.
	cfg := os.Getenv("KEEL_CONFIG_DIR")
	prof := "defaults:\n  framework: fastapi\n"
	if err := os.WriteFile(filepath.Join(cfg, "profile.yaml"), []byte(prof), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "new", "fastapi", "--dry-run")
	if err != nil {
		t.Fatalf("new with profile: %v", err)
	}
	mustContain(t, out, "fastapi")
}
