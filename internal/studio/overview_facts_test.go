package studio

// Tests for the Issue-2 fix: the Overview is a data-driven, per-framework
// dashboard. handleOverviewFacts returns real, cheap, framework-aware facts read
// from files — bounded, cached, graceful — and never fabricates a number (an
// unknown value renders empty → "—" in the UI).

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// stubDBUnreachable makes the DB pre-flight fail fast, so a facts test does not
// dial a real database — the reachability row is exercised via the reason path
// rather than a live socket.
func stubDBUnreachable(t *testing.T) {
	t.Helper()
	prev := projectDB
	projectDB = func(ctx context.Context, dir string) (*sql.DB, dbEngine, error) {
		return nil, "", errors.New("could not connect to the project database (is it running?)")
	}
	t.Cleanup(func() { projectDB = prev })
}

// writeFile drops a file (creating parent dirs) so a facts collector has real
// files to count, the way a real project would.
func writeFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A Laravel project's facts count real migrations + route declarations + .env,
// from files — the "real numbers" that make the overview a dashboard.
func TestLaravelFactsFromFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "database/migrations/2024_01_01_create_users.php", "<?php")
	writeFile(t, dir, "database/migrations/2024_01_02_create_posts.php", "<?php")
	writeFile(t, dir, "routes/web.php", "Route::get('/', fn()=>1);\nRoute::post('/x', fn()=>1);")
	writeFile(t, dir, "routes/api.php", "Route::get('/api', fn()=>1);")
	writeFile(t, dir, ".env", "APP_KEY=base64:x")

	facts := frameworkFacts(context.Background(), dir, "laravel")
	got := factMap(facts)
	if got["Migrations"] != "2" {
		t.Errorf("Migrations = %q, want 2", got["Migrations"])
	}
	if got["Routes"] != "3" {
		t.Errorf("Routes = %q, want 3", got["Routes"])
	}
	if got[".env"] != "yes" {
		t.Errorf(".env = %q, want yes", got[".env"])
	}
}

// A Next project's facts count App-Router route files and detect the router kind
// and config, all from the filesystem.
func TestNextFactsFromFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "app/page.tsx", "export default ()=>null")
	writeFile(t, dir, "app/blog/page.tsx", "export default ()=>null")
	writeFile(t, dir, "app/api/hello/route.ts", "export function GET(){}")
	writeFile(t, dir, "next.config.mjs", "export default {}")

	facts := frameworkFacts(context.Background(), dir, "next")
	got := factMap(facts)
	if got["Routes"] != "3" {
		t.Errorf("Routes = %q, want 3", got["Routes"])
	}
	if got["Router"] != "App Router" {
		t.Errorf("Router = %q, want App Router", got["Router"])
	}
	if got["Config"] != "yes" {
		t.Errorf("Config = %q, want yes", got["Config"])
	}
}

// A Django project's facts count apps, migrations, and route declarations.
func TestDjangoFactsFromFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "blog/apps.py", "class BlogConfig: pass")
	writeFile(t, dir, "blog/migrations/0001_initial.py", "# migration")
	writeFile(t, dir, "blog/migrations/0002_add.py", "# migration")
	writeFile(t, dir, "blog/migrations/__init__.py", "")
	writeFile(t, dir, "project/urls.py", "urlpatterns=[path('a',v), path('b',v), re_path('c',v)]")

	facts := frameworkFacts(context.Background(), dir, "django")
	got := factMap(facts)
	if got["Apps"] != "1" {
		t.Errorf("Apps = %q, want 1", got["Apps"])
	}
	if got["Migrations"] != "2" {
		t.Errorf("Migrations = %q, want 2 (__init__ excluded)", got["Migrations"])
	}
	if got["Routes"] != "3" {
		t.Errorf("Routes = %q, want 3", got["Routes"])
	}
}

// A FastAPI project's facts count route decorators across the tree.
func TestFastAPIFactsFromFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.py", "@app.get('/')\ndef r(): ...\n@app.post('/x')\ndef p(): ...")
	writeFile(t, dir, "routers/items.py", "@router.get('/items')\ndef i(): ...")
	writeFile(t, dir, "pyproject.toml", "[project]")

	facts := frameworkFacts(context.Background(), dir, "fastapi")
	got := factMap(facts)
	if got["Routes"] != "3" {
		t.Errorf("Routes = %q, want 3", got["Routes"])
	}
	if got["Deps"] != "yes" {
		t.Errorf("Deps = %q, want yes", got["Deps"])
	}
}

// An empty count renders "—", never a fabricated zero-as-real — the honest
// "keel could not learn this" state.
func TestFactsEmptyRendersDash(t *testing.T) {
	dir := t.TempDir() // no files at all
	facts := frameworkFacts(context.Background(), dir, "laravel")
	got := factMap(facts)
	if got["Migrations"] != "—" {
		t.Errorf("an empty count must be an em dash, got %q", got["Migrations"])
	}
	if got[".env"] != "no" {
		t.Errorf("a missing .env is 'no', got %q", got[".env"])
	}
}

// A framework keel has no cheap signals for returns no rows — the dashboard still
// shows identity + DB + services, but never invents facts.
func TestFactsUnknownFrameworkNoRows(t *testing.T) {
	if rows := frameworkFacts(context.Background(), t.TempDir(), "cobol"); len(rows) != 0 {
		t.Errorf("an unknown framework must yield no fabricated facts: %+v", rows)
	}
}

// buildFacts on a tracked, managed project returns the framework identity and a
// facts payload; a non-project dir returns a calm message, not an error.
func TestHandleOverviewFactsEndToEnd(t *testing.T) {
	isolateConfig(t)
	stubDBUnreachable(t)
	dir := t.TempDir()
	writeManifest(t, dir, "django-docker", nil) // env=django-docker; framework=laravel per helper
	writeFile(t, dir, "routes/web.php", "Route::get('/', fn()=>1);")
	trackProject(t, dir)

	w := muxGet(testMux(), "/api/overview/facts?dir="+dir)
	if w.Code != http.StatusOK {
		t.Fatalf("facts should be 200, got %d: %s", w.Code, w.Body)
	}
	m := decodeJSON(t, w)
	if m["framework"] != "laravel" {
		t.Errorf("framework = %v, want laravel: %s", m["framework"], w.Body)
	}
	if _, ok := m["facts"].([]any); !ok {
		t.Errorf("facts must be an array: %s", w.Body)
	}
}

// A non-project dir is a normal 200 + message, never a 500.
func TestHandleOverviewFactsUntrackedIsCalm(t *testing.T) {
	isolateConfig(t)
	w := muxGet(testMux(), "/api/overview/facts?dir="+t.TempDir())
	if w.Code != http.StatusOK {
		t.Fatalf("an untracked dir is a normal 200, got %d", w.Code)
	}
	m := decodeJSON(t, w)
	if s, _ := m["message"].(string); s == "" {
		t.Errorf("an untracked dir must carry a message: %s", w.Body)
	}
}

// The facts snapshot is cached per dir: a second call within the TTL is served
// from cache (the walk does not re-run). We prove it by mutating the tree after
// the first read and asserting the cached call does not see the change, then that
// ?fresh=1 does.
func TestFactsCached(t *testing.T) {
	isolateConfig(t)
	stubDBUnreachable(t)
	dir := t.TempDir()
	writeManifest(t, dir, "django-docker", nil)
	writeFile(t, dir, "database/migrations/0001.php", "<?php")
	trackProject(t, dir)
	mux := testMux()

	first := decodeJSON(t, muxGet(mux, "/api/overview/facts?dir="+dir))
	_ = first
	// Add a second migration; the cached call must still report the pre-change view.
	writeFile(t, dir, "database/migrations/0002.php", "<?php")
	cached := factRows(decodeJSON(t, muxGet(mux, "/api/overview/facts?dir="+dir)))
	if cached["Migrations"] != "1" {
		t.Errorf("a cached read must not re-walk: Migrations = %q, want 1", cached["Migrations"])
	}
	fresh := factRows(decodeJSON(t, muxGet(mux, "/api/overview/facts?dir="+dir+"&fresh=1")))
	if fresh["Migrations"] != "2" {
		t.Errorf("?fresh=1 must rebuild: Migrations = %q, want 2", fresh["Migrations"])
	}
}

// --- the HEALTH / UPTIME / DB widgets --------------------------------------

// uptimeRank orders human uptime strings by their largest unit, so longestUptime
// can pick the oldest running service (the env-uptime headline) without parsing an
// exact duration. days > hours > minutes > seconds/unknown, all non-negative.
func TestUptimeRank(t *testing.T) {
	tests := []struct {
		s        string
		wantRank int
	}{
		{"3 days", 3}, {"2 weeks", 3}, {"1 month", 3}, {"1 year", 3},
		{"2 hours", 2},
		{"5 minutes", 1}, {"About a minute", 1},
		{"12 seconds", 0}, {"Less than a second", 0}, {"", 0}, {"garbage", 0},
	}
	for _, tc := range tests {
		if got := uptimeRank(tc.s); got != tc.wantRank {
			t.Errorf("uptimeRank(%q) = %d, want %d", tc.s, got, tc.wantRank)
		}
	}
}

// longestUptime returns the age of the longest-running service — the env's uptime
// headline. Only running services with an uptime count; nothing running is "".
func TestLongestUptime(t *testing.T) {
	svcs := []service{
		{Name: "app", Running: true, Uptime: "5 minutes"},
		{Name: "db", Running: true, Uptime: "2 hours"},
		{Name: "cache", Running: false, Uptime: ""},
		{Name: "worker", Running: true, Uptime: "3 seconds"},
	}
	if got := longestUptime(svcs); got != "2 hours" {
		t.Errorf("longestUptime = %q, want the oldest running service (2 hours)", got)
	}
	if got := longestUptime([]service{{Name: "app", Running: false}}); got != "" {
		t.Errorf("nothing running has no honest uptime, got %q", got)
	}
}

// buildDBWidget reports the reachable engine + a round-trip latency when the DB
// pings, and the calm down state (LatencyMS = -1 → "—" in the UI) when it does not.
func TestBuildDBWidget(t *testing.T) {
	t.Run("reachable reports engine + a real latency", func(t *testing.T) {
		db, _ := openFakeDB(t)
		defer stubProjectDB(t, db, enginePostgres)()
		w := buildDBWidget(context.Background(), t.TempDir())
		if !w.Up {
			t.Fatal("a pinging DB should be up")
		}
		if w.Engine != string(enginePostgres) {
			t.Errorf("engine = %q, want postgres", w.Engine)
		}
		if w.LatencyMS < 0 {
			t.Errorf("a reachable DB should report a non-negative latency, got %d", w.LatencyMS)
		}
	})
	t.Run("down is a calm state with unknown latency", func(t *testing.T) {
		stubDBUnreachable(t)
		w := buildDBWidget(context.Background(), t.TempDir())
		if w.Up {
			t.Error("a down DB must not report up")
		}
		if w.LatencyMS != -1 {
			t.Errorf("a down DB's latency is unknown (-1 → '—'), got %d", w.LatencyMS)
		}
		if w.Reason == "" {
			t.Error("a down DB must carry the calm reason")
		}
	})
}

// buildHealthWidget reduces the env's service listing to services-up / total + the
// env uptime, sharing the services path so the widget and the panel never disagree.
func TestBuildHealthWidget(t *testing.T) {
	isolateConfig(t)
	stubRuntime(t, true)
	// One running (with an uptime) + one exited service on a resolvable compose env.
	stubRunCapture(t, `[{"Service":"app","State":"running","Status":"Up 2 hours","Image":"app:dev"},`+
		`{"Service":"db","State":"exited","Status":"Exited (0) 1 minute ago","Image":"postgres:16"}]`, nil)
	dir := t.TempDir()
	writeManifest(t, dir, "django-docker", nil) // django-docker is a compose env

	h := buildHealthWidget(context.Background(), dir)
	if h.ServicesTotal != 2 || h.ServicesUp != 1 {
		t.Errorf("health = %d/%d up, want 1/2", h.ServicesUp, h.ServicesTotal)
	}
	if !h.Up {
		t.Error("one service running means the env is up")
	}
	if h.Uptime != "2 hours" {
		t.Errorf("env uptime = %q, want the longest-running service (2 hours)", h.Uptime)
	}
}

// The facts endpoint carries the HEALTH and DB widget objects (not just the legacy
// dbUp field), so the dashboard can draw the gauge tiles from one fetch.
func TestFactsPayloadHasWidgets(t *testing.T) {
	isolateConfig(t)
	stubDBUnreachable(t)
	stubRuntime(t, true)
	stubRunCapture(t, `[{"Service":"app","State":"running","Status":"Up 30 minutes","Image":"app:dev"}]`, nil)
	dir := t.TempDir()
	writeManifest(t, dir, "django-docker", nil)
	trackProject(t, dir)

	m := decodeJSON(t, muxGet(testMux(), "/api/overview/facts?dir="+dir))
	health, ok := m["health"].(map[string]any)
	if !ok {
		t.Fatalf("facts payload must carry a health widget: %v", m["health"])
	}
	if _, ok := health["servicesTotal"]; !ok {
		t.Error("the health widget must report servicesTotal")
	}
	db, ok := m["db"].(map[string]any)
	if !ok {
		t.Fatalf("facts payload must carry a db widget: %v", m["db"])
	}
	if _, ok := db["latencyMs"]; !ok {
		t.Error("the db widget must report a latencyMs (−1 when unknown)")
	}
}

// The perf fix (Dan: "overview widgets sit on '…' for ~5.2s"): the facts
// response must NOT ping the database. The DB ping is the one slow part (~5s when
// the DB is down), so it moved to its own async endpoint; /api/overview/facts is
// now all cheap file/`compose ps` reads. We prove it by making projectDB fatal:
// if the facts build touches it at all, the test fails.
func TestFactsDoesNotPingDatabase(t *testing.T) {
	isolateConfig(t)
	prev := projectDB
	projectDB = func(ctx context.Context, dir string) (*sql.DB, dbEngine, error) {
		t.Error("/api/overview/facts must not ping the database — the DB tile loads on its own async path")
		return nil, "", errors.New("stub: facts must not touch the DB")
	}
	t.Cleanup(func() { projectDB = prev })

	dir := t.TempDir()
	writeManifest(t, dir, "django-docker", nil)
	trackProject(t, dir)

	w := muxGet(testMux(), "/api/overview/facts?dir="+dir)
	if w.Code != http.StatusOK {
		t.Fatalf("facts should be 200, got %d: %s", w.Code, w.Body)
	}
	// The DB widget is still present, in its unknown state (latency -1 → "—"), so
	// an older consumer still reads a well-formed widget while the async tile loads.
	m := decodeJSON(t, w)
	db, ok := m["db"].(map[string]any)
	if !ok {
		t.Fatalf("facts must still carry a db widget (unknown state): %v", m["db"])
	}
	if lat, _ := db["latencyMs"].(float64); lat != -1 {
		t.Errorf("the un-pinged DB widget must report latencyMs -1 (unknown), got %v", db["latencyMs"])
	}
}

// The DB tile's OWN endpoint (/api/overview/db) is what actually pings the
// database — the load path split out of the facts response. A reachable DB
// reports engine + latency.
func TestHandleOverviewDBReachable(t *testing.T) {
	isolateConfig(t)
	db, _ := openFakeDB(t)
	defer stubProjectDB(t, db, enginePostgres)()
	dir := t.TempDir()
	writeManifest(t, dir, "django-docker", nil)
	trackProject(t, dir)

	w := muxGet(testMux(), "/api/overview/db?dir="+dir)
	if w.Code != http.StatusOK {
		t.Fatalf("db widget should be 200, got %d: %s", w.Code, w.Body)
	}
	m := decodeJSON(t, w)
	if up, _ := m["up"].(bool); !up {
		t.Errorf("a pinging DB should be up: %s", w.Body)
	}
	if m["engine"] != string(enginePostgres) {
		t.Errorf("engine = %v, want postgres: %s", m["engine"], w.Body)
	}
	if lat, _ := m["latencyMs"].(float64); lat < 0 {
		t.Errorf("a reachable DB should report a non-negative latency, got %v", m["latencyMs"])
	}
}

// A down database is the calm 200 down state on the DB endpoint, never an error:
// the tile reads the boolean + the reason, it does not catch an HTTP error.
func TestHandleOverviewDBDown(t *testing.T) {
	isolateConfig(t)
	stubDBUnreachable(t)
	dir := t.TempDir()
	writeManifest(t, dir, "django-docker", nil)
	trackProject(t, dir)

	w := muxGet(testMux(), "/api/overview/db?dir="+dir)
	if w.Code != http.StatusOK {
		t.Fatalf("a down DB is a normal 200, got %d", w.Code)
	}
	m := decodeJSON(t, w)
	if up, _ := m["up"].(bool); up {
		t.Error("a down DB must not report up")
	}
	if s, _ := m["reason"].(string); s == "" {
		t.Errorf("a down DB must carry the calm reason: %s", w.Body)
	}
}

// factMap turns a []fact into a label->value map for terse assertions.
func factMap(facts []fact) map[string]string {
	m := map[string]string{}
	for _, f := range facts {
		m[f.Label] = f.Value
	}
	return m
}

// factRows pulls the label->value map out of a decoded JSON facts payload.
func factRows(m map[string]any) map[string]string {
	out := map[string]string{}
	rows, _ := m["facts"].([]any)
	for _, r := range rows {
		if o, ok := r.(map[string]any); ok {
			label, _ := o["label"].(string)
			value, _ := o["value"].(string)
			out[label] = value
		}
	}
	return out
}
