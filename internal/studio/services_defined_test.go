package studio

// Tests for the Issue-1 fix: the services dashboard must list the DEFINED
// services (from `docker compose config --services`), each with its running
// state, so a stopped or never-`up`'d env still shows every service on/off —
// never a blank dashboard. These drive the same runCapture seam the existing
// services tests use, but the seam here must answer TWO different commands
// (config --services and ps), so it dispatches on the argv.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/recipe"
)

// stubComposeSeam swaps runCapture for one that answers `config --services` and
// `ps` separately, so a test can drive the merge of defined + running. Either
// arm may carry an error to exercise the daemon-down / no-file paths.
func stubComposeSeam(t *testing.T, configOut string, configErr error, psOut string, psErr error) {
	t.Helper()
	prev := runCapture
	runCapture = func(ctx context.Context, dir string, argv ...string) (string, error) {
		joined := strings.Join(argv, " ")
		switch {
		case strings.Contains(joined, "config --services"):
			return configOut, configErr
		case strings.Contains(joined, "compose ps"):
			return psOut, psErr
		default:
			return "", errors.New("unexpected argv: " + joined)
		}
	}
	t.Cleanup(func() { runCapture = prev })
}

// A compose env that was NEVER brought up: `config --services` lists the defined
// services, `ps` returns empty. Every defined service must still appear, all off,
// with per-service controls on — the concrete "start a service you can see is
// off" fix.
func TestComposeDefinedServicesShowWhenDown(t *testing.T) {
	stubRuntime(t, true)
	stubComposeSeam(t, "app\ndb\nredis\n", nil, "", nil)

	res := listServices(context.Background(), t.TempDir(), recipe.Recipe{EnvFamily: recipe.FamilyCompose})
	if len(res.Services) != 3 {
		t.Fatalf("all 3 DEFINED services must show even with the env down, got %d: %+v", len(res.Services), res.Services)
	}
	for _, s := range res.Services {
		if s.Running {
			t.Errorf("service %q should be off when nothing is running", s.Name)
		}
	}
	if res.Up {
		t.Error("no service running means the env is not up")
	}
	if !res.Controls {
		t.Error("compose must keep per-service controls so each off service can be started")
	}
	// Deterministic order (sorted): app, db, redis.
	if res.Services[0].Name != "app" || res.Services[1].Name != "db" || res.Services[2].Name != "redis" {
		t.Errorf("services should be sorted by name: %+v", res.Services)
	}
}

// A partly-up env: `config` lists three, `ps` reports one running. The defined
// list is the spine; the running row overlays its state + kind onto it.
func TestComposeDefinedMergedWithRunning(t *testing.T) {
	stubRuntime(t, true)
	stubComposeSeam(t,
		"app\ndb\nredis\n", nil,
		`[{"Service":"app","State":"running","Image":"app:dev"},{"Service":"db","State":"exited","Image":"postgres:16"}]`, nil)

	res := listServices(context.Background(), t.TempDir(), recipe.Recipe{EnvFamily: recipe.FamilyCompose})
	if len(res.Services) != 3 {
		t.Fatalf("defined list of 3 is the spine, got %d: %+v", len(res.Services), res.Services)
	}
	byName := map[string]service{}
	for _, s := range res.Services {
		byName[s.Name] = s
	}
	if !byName["app"].Running {
		t.Error("app is running per ps and must map to up")
	}
	if byName["db"].Running {
		t.Error("db is exited and must map to down")
	}
	if _, ok := byName["redis"]; !ok || byName["redis"].Running {
		t.Error("redis is defined-but-not-created and must show, off")
	}
	if byName["app"].Kind != "app:dev" {
		t.Errorf("the running row's image should carry over as the kind: %+v", byName["app"])
	}
	if !res.Up {
		t.Error("app is running, so the env is up")
	}
}

// The daemon is down but the compose FILE is readable: `config` succeeds, `ps`
// errors. Every defined service must still show (all off) with a clear note that
// docker is unreachable — never a blank dashboard.
func TestComposeDefinedShowWhenDaemonDown(t *testing.T) {
	stubRuntime(t, true)
	stubComposeSeam(t, "app\ndb\n", nil, "Cannot connect to the Docker daemon", context.DeadlineExceeded)

	res := listServices(context.Background(), t.TempDir(), recipe.Recipe{EnvFamily: recipe.FamilyCompose})
	if len(res.Services) != 2 {
		t.Fatalf("the defined services must show even with the daemon down, got %d: %+v", len(res.Services), res.Services)
	}
	if res.Up {
		t.Error("a down daemon means nothing is running")
	}
	if res.Message == "" || !strings.Contains(res.Message, "Docker") {
		t.Errorf("a daemon-down state must carry a clear note: %q", res.Message)
	}
	if strings.Contains(res.Message, "DeadlineExceeded") {
		t.Errorf("the note must not leak the raw error: %q", res.Message)
	}
}

// Both the compose file and the daemon are unreadable: the only genuinely blank
// case degrades to a calm message with controls off.
func TestComposeBothUnreadable(t *testing.T) {
	stubRuntime(t, true)
	stubComposeSeam(t, "", errors.New("no configuration file"), "", context.DeadlineExceeded)

	res := listServices(context.Background(), t.TempDir(), recipe.Recipe{EnvFamily: recipe.FamilyCompose})
	if len(res.Services) != 0 {
		t.Fatalf("with neither file nor daemon there is nothing to list: %+v", res.Services)
	}
	if res.Controls {
		t.Error("nothing to control when neither the file nor the daemon is readable")
	}
	if res.Message == "" {
		t.Error("the blank case must still carry a message")
	}
}

// When the compose file cannot be read but the daemon answers, fall back to the
// running containers alone (pre-fix behaviour) rather than showing nothing.
func TestComposeFileUnreadableFallsBackToRunning(t *testing.T) {
	stubRuntime(t, true)
	stubComposeSeam(t, "", errors.New("no configuration file"),
		`[{"Service":"app","State":"running","Image":"app:dev"}]`, nil)

	res := listServices(context.Background(), t.TempDir(), recipe.Recipe{EnvFamily: recipe.FamilyCompose})
	if len(res.Services) != 1 || res.Services[0].Name != "app" {
		t.Fatalf("with no file, the running containers are the fallback: %+v", res.Services)
	}
	if !res.Up {
		t.Error("app is running, so the env is up")
	}
}

// A defined-services line that could carry a flag/path is refused by
// safeServiceName, so a malformed compose output cannot inject an argv element.
func TestComposeDefinedRejectsUnsafeNames(t *testing.T) {
	stubRuntime(t, true)
	stubComposeSeam(t, "app\n--rm\nsvc/../etc\ndb\n", nil, "", nil)

	res := listServices(context.Background(), t.TempDir(), recipe.Recipe{EnvFamily: recipe.FamilyCompose})
	names := map[string]bool{}
	for _, s := range res.Services {
		names[s.Name] = true
	}
	if !names["app"] || !names["db"] {
		t.Errorf("the safe names must survive: %+v", res.Services)
	}
	if names["--rm"] || names["svc/../etc"] {
		t.Errorf("unsafe names must be dropped: %+v", res.Services)
	}
}
