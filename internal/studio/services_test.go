package studio

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/recipe"
)

// stubRunCapture swaps the argv-exec seam for a canned output/error and restores
// it, so the compose/ddev enumeration can be exercised with no docker or ddev on
// the host. It records the argv the handler built so a test can assert the exact
// command shape.
func stubRunCapture(t *testing.T, out string, err error) *[]string {
	t.Helper()
	var got []string
	prev := runCapture
	runCapture = func(ctx context.Context, dir string, argv ...string) (string, error) {
		got = argv
		return out, err
	}
	t.Cleanup(func() { runCapture = prev })
	return &got
}

// stubRuntime forces the "is docker/ddev installed?" seam to a fixed answer, so
// the not-installed branch is testable on a host that does have them (and the
// happy path is testable on one that does not).
func stubRuntime(t *testing.T, present bool) {
	t.Helper()
	prev := hasRuntime
	hasRuntime = func(string) bool { return present }
	t.Cleanup(func() { hasRuntime = prev })
}

// --- parseComposePS: both docker CLI output shapes --------------------------

func TestParseComposePS(t *testing.T) {
	tests := []struct {
		name    string
		out     string
		wantN   int
		wantErr bool
	}{
		{"empty is no services, not an error", "  \n ", 0, false},
		{
			name:  "newline-delimited objects (docker CLI default)",
			out:   `{"Service":"app","State":"running","Image":"app:dev"}` + "\n" + `{"Service":"db","State":"exited","Image":"postgres:16"}`,
			wantN: 2,
		},
		{
			name:  "json array shape",
			out:   `[{"Service":"app","State":"running"},{"Service":"redis","State":"running"}]`,
			wantN: 2,
		},
		{"garbage is an error", `{not json`, 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := parseComposePS(tc.out)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got rows=%v", rows)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(rows) != tc.wantN {
				t.Fatalf("rows = %d, want %d", len(rows), tc.wantN)
			}
		})
	}
}

// --- parseComposeUptime: the uptime widget's source -------------------------

// A running compose container's uptime is parsed from the runtime's own Status
// string ("Up 2 hours"), so the dashboard shows how long each service has been up
// without a second probe. Anything that is not an "Up …" line (exited, created)
// yields no uptime — the row is not running, so there is no honest age to show.
func TestParseComposeUptime(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   string
	}{
		{"plain hours", "Up 2 hours", "2 hours"},
		{"about a minute", "Up About a minute", "About a minute"},
		{"days with health", "Up 3 days (healthy)", "3 days"},
		{"seconds with starting health", "Up 12 seconds (health: starting)", "12 seconds"},
		{"less than a second", "Up Less than a second", "Less than a second"},
		{"lowercase up", "up 5 minutes", "5 minutes"},
		{"exited has no uptime", "Exited (0) 5 minutes ago", ""},
		{"created has no uptime", "Created", ""},
		{"restarting has no uptime", "Restarting (1) 3 seconds ago", ""},
		{"blank has no uptime", "   ", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseComposeUptime(tc.status); got != tc.want {
				t.Errorf("parseComposeUptime(%q) = %q, want %q", tc.status, got, tc.want)
			}
		})
	}
}

// A running compose service carries its parsed uptime onto its dashboard row; an
// exited one does not — the badge only draws for a container that is genuinely up.
func TestListServicesCarriesUptime(t *testing.T) {
	stubRuntime(t, true)
	stubRunCapture(t, `[{"Service":"app","State":"running","Status":"Up 2 hours","Image":"app:dev"},`+
		`{"Service":"db","State":"exited","Status":"Exited (0) 5 minutes ago","Image":"postgres:16"}]`, nil)

	res := listServices(context.Background(), t.TempDir(), recipe.Recipe{EnvFamily: recipe.FamilyCompose})
	byName := map[string]service{}
	for _, s := range res.Services {
		byName[s.Name] = s
	}
	if byName["app"].Uptime != "2 hours" {
		t.Errorf("a running service should carry its uptime: got %q, want 2 hours", byName["app"].Uptime)
	}
	if byName["db"].Uptime != "" {
		t.Errorf("an exited service has no uptime, got %q", byName["db"].Uptime)
	}
}

// --- listServices: compose up, running-state mapping ------------------------

func TestListServicesComposeUp(t *testing.T) {
	stubRuntime(t, true)
	stubRunCapture(t, `[{"Service":"app","State":"running","Image":"app:dev"},{"Service":"db","State":"exited","Image":"postgres:16"}]`, nil)

	res := listServices(context.Background(), t.TempDir(), recipe.Recipe{EnvFamily: recipe.FamilyCompose})
	if res.Family != recipe.FamilyCompose {
		t.Fatalf("family = %q, want compose", res.Family)
	}
	if !res.Controls {
		t.Error("compose services should support per-service controls")
	}
	if len(res.Services) != 2 {
		t.Fatalf("want 2 services, got %d: %+v", len(res.Services), res.Services)
	}
	// "running" maps to up, anything else to down.
	byName := map[string]bool{}
	for _, s := range res.Services {
		byName[s.Name] = s.Running
	}
	if !byName["app"] {
		t.Error("app is running and should map to up")
	}
	if byName["db"] {
		t.Error("db is exited and should map to down")
	}
	if !res.Up {
		t.Error("at least one service is running, so the env is up")
	}
}

// A compose env that was never started returns an empty ps output — a NORMAL
// down state with a clear message, never an error or a blank.
func TestListServicesComposeDownIsCalm(t *testing.T) {
	stubRuntime(t, true)
	stubRunCapture(t, "", nil)

	res := listServices(context.Background(), t.TempDir(), recipe.Recipe{EnvFamily: recipe.FamilyCompose})
	if res.Up {
		t.Error("no services means the env is not up")
	}
	if len(res.Services) != 0 {
		t.Fatalf("want no services, got %+v", res.Services)
	}
	if res.Message == "" {
		t.Error("a down env must carry a clear inline message, not a blank")
	}
}

// Docker not installed is a clear, non-blank state — controls off, no hang.
func TestListServicesComposeNotInstalled(t *testing.T) {
	stubRuntime(t, false)
	// runCapture must not even be reached; a stub that fails proves it.
	stubRunCapture(t, "SHOULD NOT RUN", nil)

	res := listServices(context.Background(), t.TempDir(), recipe.Recipe{EnvFamily: recipe.FamilyCompose})
	if res.Controls {
		t.Error("with no docker there is nothing to control")
	}
	if !strings.Contains(res.Message, "Docker is not installed") {
		t.Errorf("message should name the missing runtime: %q", res.Message)
	}
}

// A wedged/erroring docker (ps returns an error) degrades to a calm message, not
// a leaked error string or a hang.
func TestListServicesComposeErrorIsCalm(t *testing.T) {
	stubRuntime(t, true)
	stubRunCapture(t, "Cannot connect to the Docker daemon", context.DeadlineExceeded)

	res := listServices(context.Background(), t.TempDir(), recipe.Recipe{EnvFamily: recipe.FamilyCompose})
	if res.Up || len(res.Services) != 0 {
		t.Errorf("an errored ps is a down state: %+v", res)
	}
	if !strings.Contains(res.Message, "Docker") {
		t.Errorf("message should be actionable: %q", res.Message)
	}
	if strings.Contains(res.Message, "DeadlineExceeded") {
		t.Errorf("the calm message must not leak the raw error: %q", res.Message)
	}
}

// --- listServices: ddev -----------------------------------------------------

func TestListServicesDdev(t *testing.T) {
	stubRuntime(t, true)
	stubRunCapture(t, `{"raw":{"status":"running","services":{"web":{"status":"running","type":"web"},"db":{"status":"running","type":"db"}}}}`, nil)

	res := listServices(context.Background(), t.TempDir(), recipe.Recipe{EnvFamily: recipe.FamilyDDEV})
	if res.Family != recipe.FamilyDDEV {
		t.Fatalf("family = %q, want ddev", res.Family)
	}
	if res.Controls {
		t.Error("ddev manages services as one unit — per-service controls must be off")
	}
	if len(res.Services) != 2 {
		t.Fatalf("want 2 ddev services, got %d: %+v", len(res.Services), res.Services)
	}
	if !res.Up {
		t.Error("ddev reported status running, so the env is up")
	}
}

func TestListServicesDdevNotInstalled(t *testing.T) {
	stubRuntime(t, false)
	res := listServices(context.Background(), t.TempDir(), recipe.Recipe{EnvFamily: recipe.FamilyDDEV})
	if !strings.Contains(res.Message, "DDEV is not installed") {
		t.Errorf("message should name the missing runtime: %q", res.Message)
	}
}

// A stopped ddev project (describe errors) and unparseable output both degrade to
// a calm, non-blank message.
func TestListServicesDdevDownAndGarbage(t *testing.T) {
	stubRuntime(t, true)

	stubRunCapture(t, "ddev is not running", context.DeadlineExceeded)
	down := listServices(context.Background(), t.TempDir(), recipe.Recipe{EnvFamily: recipe.FamilyDDEV})
	if down.Up || len(down.Services) != 0 || !strings.Contains(down.Message, "DDEV") {
		t.Errorf("a stopped ddev must be a calm down state: %+v", down)
	}

	stubRunCapture(t, "{not json", nil)
	garbage := listServices(context.Background(), t.TempDir(), recipe.Recipe{EnvFamily: recipe.FamilyDDEV})
	if garbage.Message == "" {
		t.Errorf("unparseable ddev output must carry a message, not a blank: %+v", garbage)
	}

	// describe parses but reports no running services -> down with a message.
	stubRunCapture(t, `{"raw":{"status":"stopped","services":{}}}`, nil)
	empty := listServices(context.Background(), t.TempDir(), recipe.Recipe{EnvFamily: recipe.FamilyDDEV})
	if empty.Up || len(empty.Services) != 0 || empty.Message == "" {
		t.Errorf("a ddev with no services is a down state with a message: %+v", empty)
	}
}

// A compose row with no name is skipped (some ps shapes omit Service on a bare
// container), and a JSON-array parse error is surfaced as a parse error.
func TestComposeEdgeCases(t *testing.T) {
	stubRuntime(t, true)
	stubRunCapture(t, `[{"State":"running"},{"Service":"db","State":"running"}]`, nil)
	res := listServices(context.Background(), t.TempDir(), recipe.Recipe{EnvFamily: recipe.FamilyCompose})
	if len(res.Services) != 1 || res.Services[0].Name != "db" {
		t.Errorf("a nameless row should be skipped, only db kept: %+v", res.Services)
	}

	if _, err := parseComposePS(`[{"State":]`); err == nil {
		t.Error("a malformed JSON array should be a parse error")
	}

	// The falls-through parse error path in composeServices.
	stubRunCapture(t, `{"State":]`, nil)
	bad := listServices(context.Background(), t.TempDir(), recipe.Recipe{EnvFamily: recipe.FamilyCompose})
	if bad.Message == "" {
		t.Errorf("a parse failure must carry a message: %+v", bad)
	}
}

// --- listServices: local (no services) --------------------------------------

func TestListServicesLocal(t *testing.T) {
	res := listServices(context.Background(), t.TempDir(), recipe.Recipe{EnvFamily: recipe.FamilyLocal})
	if len(res.Services) != 0 {
		t.Fatalf("a native env has no services: %+v", res.Services)
	}
	if res.Message == "" {
		t.Error("local must explain there are no containers, not render blank")
	}
	// An env recipe with no family declared is treated as the safe local case.
	res2 := listServices(context.Background(), t.TempDir(), recipe.Recipe{})
	if res2.Family != recipe.FamilyLocal || len(res2.Services) != 0 {
		t.Errorf("an env with no family should degrade to local: %+v", res2)
	}
}

// --- /api/services handler (end to end through the guarded mux) --------------

func TestHandleServicesComposeEndToEnd(t *testing.T) {
	isolateConfig(t)
	stubRuntime(t, true)
	stubRunCapture(t, `[{"Service":"app","State":"running","Image":"app:dev"}]`, nil)

	dir := t.TempDir()
	writeManifest(t, dir, "django-docker", nil)
	trackProject(t, dir)

	w := muxGet(testMux(), "/api/services?dir="+dir)
	if w.Code != http.StatusOK {
		t.Fatalf("services should be 200, got %d: %s", w.Code, w.Body)
	}
	m := decodeJSON(t, w)
	if m["family"] != recipe.FamilyCompose {
		t.Errorf("family = %v, want compose: %s", m["family"], w.Body)
	}
	svcs, _ := m["services"].([]any)
	if len(svcs) != 1 {
		t.Fatalf("want 1 service in the JSON: %s", w.Body)
	}
}

// A non-project dir is a normal answer (200 + message), not a 500.
func TestHandleServicesUntrackedIsCalm(t *testing.T) {
	isolateConfig(t)
	w := muxGet(testMux(), "/api/services?dir="+t.TempDir())
	if w.Code != http.StatusOK {
		t.Fatalf("an untracked dir is a normal 200 answer, got %d", w.Code)
	}
	m := decodeJSON(t, w)
	if _, ok := m["message"].(string); !ok || m["message"] == "" {
		t.Errorf("an untracked dir must carry a message: %s", w.Body)
	}
}

// --- safeServiceName --------------------------------------------------------

func TestSafeServiceName(t *testing.T) {
	ok := []string{"app", "db", "redis", "meili-search", "worker_1", "svc.name"}
	for _, n := range ok {
		if !safeServiceName(n) {
			t.Errorf("%q should be accepted", n)
		}
	}
	bad := []string{"", "-rf", "a b", "app;rm -rf /", "svc/../etc", strings.Repeat("x", 61)}
	for _, n := range bad {
		if safeServiceName(n) {
			t.Errorf("%q should be refused", n)
		}
	}
}

// --- /api/service/action handler --------------------------------------------

// A bad verb, a bad service name and an unknown family are each refused with a
// streamed failure, never a real process.
func TestHandleServiceActionRefusesBadInput(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "django-docker", nil)
	trackProject(t, dir)

	// A verb outside the whitelist.
	w := muxPost(testMux(), "/api/service/action", `{"dir":"`+dir+`","service":"app","action":"nuke"}`)
	if !strings.Contains(w.Body.String(), "action not allowed") {
		t.Errorf("a non-whitelisted verb must be refused: %s", w.Body)
	}
	// A service name that could carry a shell fragment.
	w = muxPost(testMux(), "/api/service/action", `{"dir":"`+dir+`","service":"app;rm -rf /","action":"stop"}`)
	if !strings.Contains(w.Body.String(), "invalid service name") {
		t.Errorf("an unsafe service name must be refused: %s", w.Body)
	}
}

// ddev per-service control is not supported — it is refused with a clear message
// pointing at the whole-env Start/Stop, not silently faked.
func TestHandleServiceActionDdevRefused(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "django-ddev", nil)
	trackProject(t, dir)

	w := muxPost(testMux(), "/api/service/action", `{"dir":"`+dir+`","service":"web","action":"stop"}`)
	if !strings.Contains(w.Body.String(), "DDEV manages its services together") {
		t.Errorf("ddev per-service control should be refused with guidance: %s", w.Body)
	}
}

// A native (local) env has no services to control — refused with a clear reason.
func TestHandleServiceActionLocalRefused(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "django-local", nil)
	trackProject(t, dir)

	w := muxPost(testMux(), "/api/service/action", `{"dir":"`+dir+`","service":"app","action":"stop"}`)
	if !strings.Contains(w.Body.String(), "runs natively") {
		t.Errorf("a native env should refuse per-service control: %s", w.Body)
	}
}

// A compose env with no docker installed is refused with a clear reason, not a
// hang or a raw exec error.
func TestHandleServiceActionComposeNoDocker(t *testing.T) {
	isolateConfig(t)
	stubRuntime(t, false)
	dir := t.TempDir()
	writeManifest(t, dir, "django-docker", nil)
	trackProject(t, dir)

	w := muxPost(testMux(), "/api/service/action", `{"dir":"`+dir+`","service":"app","action":"stop"}`)
	if !strings.Contains(w.Body.String(), "Docker is not installed") {
		t.Errorf("no docker should be refused with a clear reason: %s", w.Body)
	}
}

// A malformed request body is a 400, before any project or exec is touched.
func TestHandleServiceActionBadBody(t *testing.T) {
	isolateConfig(t)
	w := muxPost(testMux(), "/api/service/action", `{not json`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a bad body should be 400, got %d: %s", w.Code, w.Body)
	}
}

// An untracked / non-project dir on the control path streams a clear failure.
func TestHandleServiceActionUntracked(t *testing.T) {
	isolateConfig(t)
	w := muxPost(testMux(), "/api/service/action", `{"dir":"`+t.TempDir()+`","service":"app","action":"stop"}`)
	if !strings.Contains(w.Body.String(), "✗") {
		t.Errorf("an untracked dir must stream a failure: %s", w.Body)
	}
}

// projectEnvRecipe surfaces a clear error for a manifest naming an unknown env.
func TestProjectEnvRecipeUnknownEnv(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "no-such-env-recipe", nil)
	if _, err := projectEnvRecipe(dir); err == nil {
		t.Fatal("an unknown env recipe should be an error")
	}
}

// The happy path builds the exact `docker compose <verb> <svc>` argv and streams
// a done frame. The exec is captured through the runCapture seam's sibling — the
// ExecRunner runs argv, so we assert the streamed result rather than the argv.
func TestHandleServiceActionComposeStreams(t *testing.T) {
	isolateConfig(t)
	stubRuntime(t, true)
	dir := t.TempDir()
	writeManifest(t, dir, "django-docker", nil)
	trackProject(t, dir)

	w := muxPost(testMux(), "/api/service/action", `{"dir":"`+dir+`","service":"nope-not-real","action":"restart"}`)
	// docker is not actually invoked with a real daemon here; the handler streams
	// a start line then the exec's result. Either way the response is an SSE
	// stream (never a 4xx/5xx), proving the request reached the exec path.
	if w.Code != http.StatusOK {
		t.Fatalf("a valid service action should stream (200), got %d: %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "restarting service nope-not-real") {
		t.Errorf("the stream should announce the action: %s", w.Body)
	}
}
