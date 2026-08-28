package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/engine"
)

// TestStatusComposeUpWithDB: status prints framework/env, each service up/down,
// and reads DB reachability from a running db service.
func TestStatusComposeUpWithDB(t *testing.T) {
	wd := isolate(t)
	writeManifest(t, wd, "laravel", "sail")
	stubRuntime(t, map[string]bool{"docker": true}, func(argv []string) (string, error) {
		line := strings.Join(argv, " ")
		switch {
		case strings.Contains(line, "config --services"):
			return "app\ndb\n", nil
		case strings.Contains(line, "ps --all"):
			return composePSJSON("db", "running", "postgres:16"), nil
		}
		return "", nil
	})
	out, err := runRoot(t, "status")
	if err != nil {
		t.Fatalf("keel status: %v", err)
	}
	mustContain(t, out, "framework: laravel", "env:       sail")
	mustContain(t, out, "services:", "up", "db", "down", "app")
	// The db service is running, so the database reads as up.
	mustContain(t, out, "database:", "up (db service running)")
}

// TestStatusDockerDownGraceful: when docker is not installed, status still prints
// a well-formed overview with a calm message, not an error.
func TestStatusDockerDownGraceful(t *testing.T) {
	wd := isolate(t)
	writeManifest(t, wd, "laravel", "sail")
	stubRuntime(t, map[string]bool{}, func(argv []string) (string, error) { return "", nil })
	out, err := runRoot(t, "status")
	if err != nil {
		t.Fatalf("status should not error when docker is down: %v", err)
	}
	mustContain(t, out, "Docker is not installed")
}

// TestStatusFrameworkStats: cheap per-framework stats are counted from the tree
// (migrations here) and shown; an empty count renders as a dash.
func TestStatusFrameworkStats(t *testing.T) {
	wd := isolate(t)
	writeManifest(t, wd, "laravel", "nestjs-local") // local env: no services, fast
	// Two Laravel migration files.
	md := filepath.Join(wd, "database", "migrations")
	if err := os.MkdirAll(md, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"2024_01_01_000000_create_users.php", "2024_01_02_000000_create_posts.php"} {
		if err := os.WriteFile(filepath.Join(md, n), []byte("<?php\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := runRoot(t, "status")
	if err != nil {
		t.Fatalf("keel status: %v", err)
	}
	mustContain(t, out, "stats:", "migrations", "2")
	// No routes dir -> routes is a dash, never a fabricated number.
	mustContain(t, out, "routes", "-")
}

// TestStatusRefusesOutsideProject: no manifest = refuse.
func TestStatusRefusesOutsideProject(t *testing.T) {
	isolate(t)
	_, err := runRoot(t, "status")
	if err == nil {
		t.Fatal("status should refuse outside a keel project")
	}
	if !mentionsAProject(err.Error()) {
		t.Errorf("error should explain there is no project: %v", err)
	}
}

// TestDBReachabilityHeuristic exercises the cheap DB signal directly across the
// cases the heuristic must get right.
func TestDBReachabilityHeuristic(t *testing.T) {
	tests := []struct {
		name string
		svcs []svcState
		env  string
		want string
	}{
		{"db up", []svcState{{Name: "db", Running: true}}, "sail", "up"},
		{"db down", []svcState{{Name: "db", Running: false}}, "sail", "down"},
		{"postgres image", []svcState{{Name: "database", Running: true, Kind: "postgres:16"}}, "sail", "up"},
		{"no db service", []svcState{{Name: "app", Running: true}}, "sail", "no database service"},
		{"native", nil, "nestjs-local", "not probed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := dbReachability(svcListing{Services: tc.svcs}, &engine.Manifest{Env: tc.env})
			if !strings.Contains(got, tc.want) {
				t.Errorf("dbReachability = %q, want substring %q", got, tc.want)
			}
		})
	}
}
