package cli

import (
	"context"
	"strings"
	"testing"
)

// stubRuntime swaps hasRuntime + captureCmd for the test and restores them, so a
// service/status test drives docker/ddev output without a real runtime. install
// is the set of "installed" binaries; respond maps an argv (joined by spaces) to
// its stdout.
func stubRuntime(t *testing.T, install map[string]bool, respond func(argv []string) (string, error)) {
	t.Helper()
	oldHas, oldCap := hasRuntime, captureCmd
	hasRuntime = func(tool string) bool { return install[tool] }
	captureCmd = func(_ context.Context, _ string, argv ...string) (string, error) { return respond(argv) }
	t.Cleanup(func() { hasRuntime, captureCmd = oldHas, oldCap })
}

// composePSJSON is one docker compose ps --format json line.
func composePSJSON(service, state, image string) string {
	return `{"Service":"` + service + `","State":"` + state + `","Image":"` + image + `"}`
}

// TestServiceListCompose: a compose env lists its DEFINED services with state,
// overlaying the ps view, and offers a start hint.
func TestServiceListCompose(t *testing.T) {
	wd := isolate(t)
	writeManifest(t, wd, "laravel", "sail")
	stubRuntime(t, map[string]bool{"docker": true}, func(argv []string) (string, error) {
		line := strings.Join(argv, " ")
		switch {
		case strings.Contains(line, "config --services"):
			return "app\ndb\nredis\n", nil
		case strings.Contains(line, "ps --all"):
			return composePSJSON("db", "running", "postgres:16"), nil
		}
		return "", nil
	})
	out, err := runRoot(t, "service")
	if err != nil {
		t.Fatalf("keel service: %v", err)
	}
	// db is up (from ps), app + redis are defined-but-down.
	mustContain(t, out, "up", "db", "down", "app", "redis")
	mustContain(t, out, "keel service start")
}

// TestServiceStartShellsCompose: `keel service start <svc>` runs the exact
// compose argv (up -d --no-deps <svc>) with the name as one argument.
func TestServiceStartShellsCompose(t *testing.T) {
	wd := isolate(t)
	writeManifest(t, wd, "laravel", "sail")
	var got []string
	stubRuntime(t, map[string]bool{"docker": true}, func(argv []string) (string, error) {
		got = argv
		return "", nil
	})
	out, err := runRoot(t, "service", "start", "db")
	if err != nil {
		t.Fatalf("service start db: %v", err)
	}
	want := []string{"docker", "compose", "up", "-d", "--no-deps", "db"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("argv = %v, want %v", got, want)
	}
	mustContain(t, out, "starting service db", "done")
}

// TestServiceRejectsBadName: a service name that is not a safe identifier is
// refused before any command runs (argv guard).
func TestServiceRejectsBadName(t *testing.T) {
	wd := isolate(t)
	writeManifest(t, wd, "laravel", "sail")
	ran := false
	stubRuntime(t, map[string]bool{"docker": true}, func(argv []string) (string, error) {
		ran = true
		return "", nil
	})
	_, err := runRoot(t, "service", "start", "--rm -rf")
	if err == nil {
		t.Fatal("a dangerous service name should be refused")
	}
	if ran {
		t.Error("no command should run for an invalid service name")
	}
}

// TestServiceDDEVRefusesPerService: ddev has no first-class per-service control,
// so start is refused with a pointer at the whole-env run.
func TestServiceDDEVRefusesPerService(t *testing.T) {
	wd := isolate(t)
	writeManifest(t, wd, "laravel", "ddev")
	stubRuntime(t, map[string]bool{"ddev": true}, func(argv []string) (string, error) { return "", nil })
	_, err := runRoot(t, "service", "start", "db")
	if err == nil {
		t.Fatal("ddev per-service start should be refused")
	}
	if !strings.Contains(err.Error(), "DDEV manages its services together") {
		t.Errorf("error should explain ddev is whole-env: %v", err)
	}
}

// TestServiceNativeHasNoServices: a local env has nothing to control.
func TestServiceNativeHasNoServices(t *testing.T) {
	wd := isolate(t)
	writeManifest(t, wd, "nextjs", "nestjs-local")
	out, err := runRoot(t, "service")
	if err != nil {
		t.Fatalf("keel service (native): %v", err)
	}
	if !strings.Contains(out, "runs natively") {
		t.Errorf("native env should say it has no containers, got:\n%s", out)
	}
}

// TestServiceRefusesOutsideProject: no manifest = refuse (don't invent an env).
func TestServiceRefusesOutsideProject(t *testing.T) {
	isolate(t)
	_, err := runRoot(t, "service")
	if err == nil {
		t.Fatal("service should refuse outside a keel project")
	}
	if !mentionsAProject(err.Error()) {
		t.Errorf("error should explain there is no project: %v", err)
	}
}
