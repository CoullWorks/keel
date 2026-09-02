package studio

// overview.go backs the Overview's data-led WIDGET dashboard — the "this is a
// real project dashboard, not quick-links" build. Alongside the running-services
// list (services.go), it surfaces REAL, cheap, per-framework STATISTICS the
// studio can introspect for a project and packages them for widget tiles: three
// top widgets (Health = services up/total + env state + env uptime; DB =
// reachable + engine + a cheap ping latency; and the framework identity + git),
// then a rich per-framework stat-grid (Laravel routes/migrations/models/
// controllers/jobs/DB-tables/versions; Next routes/api/components/versions;
// Django apps/models/migrations/routes/versions; FastAPI endpoints/schemas/
// versions; Magento modules/themes/mode; and generic git/files/LOC/last-modified
// for all). Everything is bounded and degrades cleanly: a stat keel cannot
// cheaply learn is returned empty ("—" in the UI), never fabricated.
//
// The discipline here mirrors magento_env.go and services.go: read files where a
// count is a cheap filesystem walk; shell out ONLY for facts a file cannot give
// and only through the runCapture seam (argv, no shell, context-bounded); and
// return a state, never an error the caller must catch. Results are cached per
// dir for a few seconds so a repaint (tab switch, refresh) is instant and does
// not re-walk the tree or re-ping the database.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coullworks/keel/internal/engine"
)

// factsTimeout bounds a single facts build. The facts build is now all cheap
// file/`compose ps` reads (the DB ping moved to its own async endpoint), so this
// only needs to leash a filesystem walk and the service listing, not a database.
const factsTimeout = 6 * time.Second

// dbWidgetTimeout is the leash on the DB tile's own async ping (/api/overview/db).
// It matches the Data tab's pre-flight leash: a ping that hangs is worse than one
// that says "not reachable", because the tile is showing "checking…" until it
// resolves. A wedged database surfaces as the calm down state within this bound.
const dbWidgetTimeout = 4 * time.Second

// factsCacheTTL is how long a built facts snapshot is served before it is
// rebuilt. Overview repaints (tab switch, slug route, refresh) are frequent; the
// facts move slowly, so a short cache keeps the dashboard instant without going
// stale in a way the user would notice. Refresh forces a rebuild.
const factsCacheTTL = 5 * time.Second

// fact is one labelled row the dashboard draws: a short label, its value, and an
// optional hint the UI shows as a tooltip. An empty Value renders as "—" — the
// honest "keel could not cheaply learn this" state, never a guess.
type fact struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Hint  string `json:"hint,omitempty"`
}

// healthWidget is the top-left dashboard tile: how many of the env's services
// are up out of the total, whether the env is up overall, and the env's uptime
// (the longest-running service's age, i.e. how long the stack has been online).
// A native/no-service project reports zero services and no uptime — the UI shows
// it as "native", not "down".
type healthWidget struct {
	ServicesUp    int    `json:"servicesUp"`
	ServicesTotal int    `json:"servicesTotal"`
	Up            bool   `json:"up"`
	Uptime        string `json:"uptime,omitempty"` // longest running service age, "2 hours"
	Family        string `json:"family,omitempty"`
}

// dbWidget is the top DB tile: whether the project database is reachable (the
// same ping the Data tab gates on), the engine family (postgres/mysql), and a
// cheap round-trip latency in milliseconds. LatencyMS is -1 when unknown (DB
// down or the ping did not time), so the UI can show "—" rather than a fake 0.
type dbWidget struct {
	Up        bool   `json:"up"`
	Engine    string `json:"engine,omitempty"`
	LatencyMS int64  `json:"latencyMs"`
	Reason    string `json:"reason,omitempty"`
}

// overviewFacts is the whole widget payload: the framework + env identity, the
// health + DB widgets, the git state, and the rich per-framework stat rows.
// Message carries a calm note when the project is not introspectable (unmanaged,
// unknown framework), so the UI never renders blank. The legacy DBUp/DBReason
// fields are kept so an older consumer/test still reads the DB state; the DB
// widget supersedes them for the dashboard.
type overviewFacts struct {
	Framework string       `json:"framework"`
	Env       string       `json:"env"`
	DBUp      bool         `json:"dbUp"`
	DBReason  string       `json:"dbReason,omitempty"`
	Health    healthWidget `json:"health"`
	DB        dbWidget     `json:"db"`
	Git       []fact       `json:"git"`
	Facts     []fact       `json:"facts"`
	Message   string       `json:"message,omitempty"`
}

// factsCacheEntry is one cached snapshot plus the moment it was built, so the
// cache can expire it after factsCacheTTL.
type factsCacheEntry struct {
	at   time.Time
	data overviewFacts
}

// factsCache memoises built facts per project dir for factsCacheTTL. It is a
// package var (not a field) because handlers are free functions here; a mutex
// guards it because the studio serves requests concurrently.
var (
	factsCacheMu sync.Mutex
	factsCache   = map[string]factsCacheEntry{}
)

// handleOverviewFacts is GET /api/overview/facts?dir= — the data-driven overview
// dashboard's fact feed. It is a read that names a project by ?dir=, so it is a
// guarded GET. An unmanaged/unknown project is a NORMAL answer (200 + message),
// never an error. ?fresh=1 bypasses the cache (the Refresh button).
func handleOverviewFacts(w http.ResponseWriter, r *http.Request) {
	dir, err := projectDir(r.URL.Query().Get("dir"))
	if err != nil {
		writeJSON(w, overviewFacts{Facts: []fact{}, Message: err.Error()})
		return
	}
	fresh := r.URL.Query().Get("fresh") == "1"
	if !fresh {
		if cached, ok := cachedFacts(dir); ok {
			writeJSON(w, cached)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), factsTimeout)
	defer cancel()
	res := buildFacts(ctx, dir)
	storeFacts(dir, res)
	writeJSON(w, res)
}

// handleOverviewDB is GET /api/overview/db?dir= — the Overview DB tile's OWN
// async feed, split out of the facts response so the dashboard never blocks on
// the database. It pings the project DB (up to dbWidgetTimeout) and returns the
// dbWidget (up / engine / latency / calm reason). A down database is a NORMAL
// answer (200 + Up=false + reason), never an error — the caller reads the state.
func handleOverviewDB(w http.ResponseWriter, r *http.Request) {
	dir, err := projectDir(r.URL.Query().Get("dir"))
	if err != nil {
		writeJSON(w, dbWidget{LatencyMS: -1, Reason: err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), dbWidgetTimeout)
	defer cancel()
	writeJSON(w, buildDBWidget(ctx, dir))
}

// cachedFacts returns a fresh-enough cached snapshot for dir, if one exists.
func cachedFacts(dir string) (overviewFacts, bool) {
	factsCacheMu.Lock()
	defer factsCacheMu.Unlock()
	e, ok := factsCache[dir]
	if !ok || time.Since(e.at) > factsCacheTTL {
		return overviewFacts{}, false
	}
	return e.data, true
}

// storeFacts records a built snapshot under dir with the current time.
func storeFacts(dir string, data overviewFacts) {
	factsCacheMu.Lock()
	defer factsCacheMu.Unlock()
	factsCache[dir] = factsCacheEntry{at: time.Now(), data: data}
}

// buildFacts assembles the facts for one project. It reads the manifest for the
// framework/env identity, pings the database (the same pre-flight the Data tab
// uses), then dispatches to the per-framework collector. It never returns an
// error: an unmanaged or unknown project comes back with a clear Message.
func buildFacts(ctx context.Context, dir string) overviewFacts {
	res := overviewFacts{Facts: []fact{}, Git: []fact{}, DB: dbWidget{LatencyMS: -1}}
	m, err := engine.ReadManifest(dir)
	if err != nil {
		res.Message = "This project has no keel manifest yet, so there are no framework facts to show. Adopt it to unlock the dashboard."
		return res
	}
	res.Framework = m.Framework
	res.Env = m.Env

	// The DB widget is DELIBERATELY not built here. Pinging the database can take
	// ~5s when it is down, and blocking the whole facts response on it left the
	// Health/Uptime widgets and the framework-stats grid sitting on the "…"
	// skeleton for that whole time. The database round-trip is the one part of
	// the dashboard that is not a cheap file/`compose ps` read, so it is loaded
	// on its own async path (/api/overview/db → handleOverviewDB) and the DB tile
	// shows "checking…" until it resolves. res.DB stays the unknown state
	// (LatencyMS = -1) so an older consumer still sees a well-formed widget.

	// The Health widget: enumerate the env's services (the same listing the
	// services panel draws) and reduce it to up/total + env uptime. This shares
	// the services.go path, so the widget and the panel below it can never
	// disagree about what is running.
	res.Health = buildHealthWidget(ctx, dir)

	// Generic git facts (branch + uncommitted count) apply to every framework —
	// a cheap `git` read through the runCapture seam, degrading to no rows when
	// git is absent or the dir is not a repo.
	res.Git = gitFacts(ctx, dir)

	res.Facts = frameworkFacts(ctx, dir, m.Framework)
	if len(res.Facts) == 0 {
		res.Message = "keel has no cheap framework signals for " + firstNonEmpty(m.Framework, "this project") + " yet. The services and database state above are the live view."
	}
	return res
}

// buildDBWidget pings the project database and times the round-trip. The ping is
// the Data tab's own pre-flight (projectDB), so reachability agrees with the DB
// tab; the latency is the wall time of the connect+close, a cheap "how snappy is
// the DB right now" signal. A down database is a normal state: Up=false, the
// calm reason, and LatencyMS=-1 (unknown, renders "—").
func buildDBWidget(ctx context.Context, dir string) dbWidget {
	w := dbWidget{LatencyMS: -1}
	start := time.Now()
	db, eng, derr := projectDB(ctx, dir)
	if derr != nil {
		w.Reason = pingReason(derr)
		return w
	}
	// A successful open already round-tripped; measure it and close.
	w.LatencyMS = time.Since(start).Milliseconds()
	_ = db.Close()
	w.Up = true
	w.Engine = string(eng)
	return w
}

// buildHealthWidget reduces the env's service listing to the Health tile:
// services up / total, whether the env is up, its family, and the env uptime
// (the longest-running service's age — the best cheap proxy for "how long has the
// stack been online"). It reuses listServices so the widget and the services
// panel are one source of truth. A project with no resolvable env recipe (or a
// native env) reports zero services, which the UI renders as "native".
func buildHealthWidget(ctx context.Context, dir string) healthWidget {
	env, err := projectEnvRecipe(dir)
	if err != nil {
		return healthWidget{}
	}
	res := listServices(ctx, dir, env)
	h := healthWidget{ServicesTotal: len(res.Services), Up: res.Up, Family: res.Family}
	for _, s := range res.Services {
		if s.Running {
			h.ServicesUp++
		}
	}
	h.Uptime = longestUptime(res.Services)
	return h
}

// longestUptime returns the age of the longest-running service — the env's
// uptime headline. Compose reports each running container's age as human text
// ("2 hours", "3 minutes"); we rank those coarsely by unit so the widget shows
// the oldest, which is the best cheap proxy for the stack's uptime. Empty when
// nothing is running (no honest uptime to show).
func longestUptime(svcs []service) string {
	best, bestRank := "", -1
	for _, s := range svcs {
		if !s.Running || s.Uptime == "" {
			continue
		}
		if r := uptimeRank(s.Uptime); r > bestRank {
			bestRank, best = r, s.Uptime
		}
	}
	return best
}

// uptimeRank scores a human uptime string by its largest unit so longestUptime
// can pick the oldest without parsing an exact duration. It is deliberately
// coarse — days > hours > minutes > seconds — because the widget only needs the
// order, not arithmetic. An unrecognised string ranks lowest but non-negative,
// so it still beats "no uptime at all".
func uptimeRank(s string) int {
	l := strings.ToLower(s)
	switch {
	case strings.Contains(l, "day"), strings.Contains(l, "week"), strings.Contains(l, "month"), strings.Contains(l, "year"):
		return 3
	case strings.Contains(l, "hour"):
		return 2
	case strings.Contains(l, "minute"):
		return 1
	default: // seconds / "About a minute" / unknown
		return 0
	}
}

// frameworkFacts dispatches to the per-framework collector. A framework keel has
// no cheap signals for returns no rows (the UI still shows the identity + DB +
// services); it never fabricates a number.
func frameworkFacts(ctx context.Context, dir, framework string) []fact {
	switch framework {
	case "laravel":
		return laravelFacts(dir)
	case "next", "nextjs":
		return nextFacts(dir)
	case "django":
		return djangoFacts(dir)
	case "fastapi":
		return fastapiFacts(dir)
	case "magento":
		return magentoFacts(ctx, dir)
	default:
		return nil
	}
}

// --- cheap filesystem helpers ------------------------------------------------

// maxWalkEntries caps every directory walk a fact collector does, so a
// pathological tree (a vendored node_modules slipped into a routes dir) cannot
// turn a cheap count into an unbounded scan.
const maxWalkEntries = 5000

// countFiles counts files under dir whose name satisfies match, bounded by
// maxWalkEntries and skipping the usual heavy vendor dirs. It is the primitive
// behind every "how many migrations / routes / modules" fact — a bounded walk,
// never a shell-out. A missing dir is a zero count, not an error.
func countFiles(dir string, match func(name string) bool) int {
	n, seen := 0, 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entry: skip, don't abort the whole count
		}
		if d.IsDir() {
			if skipDir(d.Name()) && path != dir {
				return filepath.SkipDir
			}
			return nil
		}
		seen++
		if seen > maxWalkEntries {
			return filepath.SkipAll
		}
		if match(d.Name()) {
			n++
		}
		return nil
	})
	return n
}

// skipDir names the heavy, never-interesting directories a fact walk prunes, so a
// count stays cheap even in a project with a big dependency tree.
func skipDir(name string) bool {
	switch name {
	case "node_modules", "vendor", ".git", ".keel", "dist", "build", ".next", "storage", "var":
		return true
	}
	return false
}

// fileExists reports whether a plain file exists at dir/parts... — the primitive
// behind ".env present?" style facts.
func fileExists(dir string, parts ...string) bool {
	safeBase, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	p, err := filepath.Abs(filepath.Join(append([]string{dir}, parts...)...))
	if err != nil || !strings.HasPrefix(p, safeBase) {
		return false // refuse a path that would escape the project dir
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// countLines returns the number of non-blank lines in a bounded read of a file,
// used for cheap "routes declared" style counts where the file is small. A
// missing/unreadable file is zero. It reads at most maxFileBytes so a huge file
// cannot blow up the dashboard.
const maxFileBytes = 512 * 1024

func readBounded(path string) (string, bool) {
	abs, err := filepath.Abs(path)
	if err != nil || strings.Contains(abs, "..") {
		return "", false
	}
	f, err := os.Open(abs)
	if err != nil {
		return "", false
	}
	defer f.Close()
	buf := make([]byte, maxFileBytes)
	n, _ := f.Read(buf)
	if n <= 0 {
		return "", false
	}
	return string(buf[:n]), true
}

// countMatches counts non-overlapping occurrences of sub across a bounded read of
// path. It is deliberately coarse — a routes count from `Route::` / `path(` /
// `@app.get` is a signal, not a compiler — and the UI labels it as a signal.
func countMatches(path, sub string) int {
	body, ok := readBounded(path)
	if !ok {
		return 0
	}
	return strings.Count(body, sub)
}

// --- git + version + generic signals -----------------------------------------

// gitFacts surfaces the two cheap git signals every framework shares: the current
// branch and the number of uncommitted changes. Both come from `git` through the
// runCapture seam (argv, no shell, ctx-bounded), so a test stands in for git and
// a project that is not a repo (or a host without git) degrades to no rows rather
// than an error. `git status --porcelain` is one line per changed path, so its
// non-blank line count is the uncommitted total.
func gitFacts(ctx context.Context, dir string) []fact {
	branch, berr := runCapture(ctx, dir, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if berr != nil {
		return nil // not a repo, or git absent: no git facts, not an error
	}
	branch = strings.TrimSpace(branch)
	rows := []fact{{Label: "Branch", Value: firstNonEmpty(branch, "—"), Hint: "current git branch"}}
	if status, serr := runCapture(ctx, dir, "git", "status", "--porcelain"); serr == nil {
		rows = append(rows, fact{
			Label: "Uncommitted",
			Value: countOrDash(nonBlankLines(status)),
			Hint:  "changed paths in git status --porcelain",
		})
	}
	return rows
}

// nonBlankLines counts the non-empty lines in s — the primitive behind the
// uncommitted-count and any other "one line per item" command output.
func nonBlankLines(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// jsonVersion reads a dependency's version string from a package.json's
// dependencies/devDependencies, cleaning the leading ^/~/>=. It is the cheap
// "what Next / node dep version" read — a bounded file read + a JSON decode, no
// npm/node shell-out. A missing file or dep yields "".
func jsonVersion(path, dep string) string {
	body, ok := readBounded(path)
	if !ok {
		return ""
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal([]byte(body), &pkg); err != nil {
		return ""
	}
	for _, m := range []map[string]string{pkg.Dependencies, pkg.DevDependencies} {
		if v, ok := m[dep]; ok {
			return cleanVersion(v)
		}
	}
	return ""
}

// composerVersion reads a package's version from a composer.json's require /
// require-dev — the Laravel/PHP analogue of jsonVersion. Cleaned of the ^/~
// constraint prefix. Missing file/package yields "".
func composerVersion(path, pkg string) string {
	body, ok := readBounded(path)
	if !ok {
		return ""
	}
	var c struct {
		Require    map[string]string `json:"require"`
		RequireDev map[string]string `json:"require-dev"`
	}
	if err := json.Unmarshal([]byte(body), &c); err != nil {
		return ""
	}
	for _, m := range []map[string]string{c.Require, c.RequireDev} {
		if v, ok := m[pkg]; ok {
			return cleanVersion(v)
		}
	}
	return ""
}

// pyVersion reads a python dependency's pinned version from a requirements.txt
// (a "pkg==1.2.3" / "pkg>=1.2" line) or a pyproject.toml ('pkg = "^1.2"' /
// 'pkg>=1.2'). It is a coarse text scan — a signal labelled as such — not a
// resolver; the point is a cheap "which Django/FastAPI" without importing python.
func pyVersion(dir, pkg string) string {
	for _, name := range []string{"requirements.txt", "pyproject.toml"} {
		body, ok := readBounded(filepath.Join(dir, name))
		if !ok {
			continue
		}
		if v := scanPyDep(body, pkg); v != "" {
			return v
		}
	}
	return ""
}

// scanPyDep finds pkg in a requirements/pyproject body and returns its version
// text. It matches the common forms line by line: "pkg==1.2.3", "pkg>=1.2",
// 'pkg = "^1.2"'. Case-insensitive on the package name (PyPI is). "" when absent.
func scanPyDep(body, pkg string) string {
	lp := strings.ToLower(pkg)
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		low := strings.ToLower(line)
		if !strings.HasPrefix(low, lp) {
			continue
		}
		// The char right after the name must be a delimiter, not more name
		// (so "fastapi" does not match "fastapi-users").
		rest := strings.TrimSpace(line[len(pkg):])
		if rest == "" || !strings.ContainsAny(rest[:1], "=<>~^ \t\"'") {
			continue
		}
		return cleanVersion(rest)
	}
	return ""
}

// cleanVersion strips the constraint punctuation (^ ~ >= == = < > " ' spaces and
// a leading =) off a version string so the widget shows "10.3" not "^10.3". It is
// deliberately lenient: the value is a display signal, not a parsed semver.
func cleanVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.Trim(v, `"'`)
	v = strings.TrimLeft(v, "^~>=< ")
	v = strings.TrimSpace(v)
	// Keep only the first token (drop a trailing ", <2" style upper bound).
	if i := strings.IndexAny(v, " ,;"); i > 0 {
		v = v[:i]
	}
	return v
}

// lastModified reports the most recent modification time under dir (skipping the
// heavy vendor dirs), rendered as a short relative age ("3 minutes", "2 days").
// It is the generic "when was this project last touched" signal — a bounded walk
// stat, no shell-out. "" when the tree is empty/unreadable.
func lastModified(dir string) string {
	var newest time.Time
	seen := 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) && path != dir {
				return filepath.SkipDir
			}
			return nil
		}
		seen++
		if seen > maxWalkEntries {
			return filepath.SkipAll
		}
		if info, ierr := d.Info(); ierr == nil {
			if info.ModTime().After(newest) {
				newest = info.ModTime()
			}
		}
		return nil
	})
	if newest.IsZero() {
		return ""
	}
	return humanAge(time.Since(newest))
}

// humanAge renders a duration as a coarse relative age ("just now", "5 minutes",
// "3 hours", "2 days") for the last-modified signal. It is intentionally coarse —
// the dashboard wants recency, not precision.
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return pluralUnit(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return pluralUnit(int(d.Hours()), "hour")
	default:
		return pluralUnit(int(d.Hours()/24), "day")
	}
}

// pluralUnit renders "1 minute" / "3 minutes" for the human-age strings.
func pluralUnit(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}

// codeExts is the set of source extensions the generic files/LOC count includes —
// the languages keel's supported frameworks are written in. It stays small and
// bounded so the count is a cheap signal, not a linguist.
var codeExts = map[string]bool{
	".php": true, ".go": true, ".py": true, ".ts": true, ".tsx": true,
	".js": true, ".jsx": true, ".vue": true, ".rb": true,
}

// codeStats returns (files, lines) for the project's source files — a cheap,
// bounded walk that counts files with a known code extension and their line
// totals (a bounded read each). It is the generic "size of the codebase" signal
// every framework shows; skipDir prunes vendor trees so it stays cheap.
func codeStats(dir string) (files, lines int) {
	seen := 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) && path != dir {
				return filepath.SkipDir
			}
			return nil
		}
		seen++
		if seen > maxWalkEntries {
			return filepath.SkipAll
		}
		if !codeExts[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}
		files++
		if body, ok := readBounded(path); ok {
			lines += nonBlankLines(body)
		}
		return nil
	})
	return files, lines
}

// genericFacts appends the framework-agnostic size + recency signals every
// collector shares: source files, lines of code, and last-modified. They are the
// generic "how big / how fresh is this project" tail on each framework's stat
// grid, kept in one place (DRY) so every framework reports them identically.
func genericFacts(dir string) []fact {
	files, lines := codeStats(dir)
	return []fact{
		{Label: ".env", Value: presentOrMissing(fileExists(dir, ".env") || fileExists(dir, ".env.local")), Hint: ".env / .env.local present"},
		{Label: "Files", Value: countOrDash(files), Hint: "source files (php/go/py/ts/js/…)"},
		{Label: "Lines", Value: countOrDash(lines), Hint: "non-blank lines across source files"},
		{Label: "Modified", Value: firstNonEmpty(lastModified(dir), "—"), Hint: "most recent source change"},
	}
}

// --- per-framework collectors -------------------------------------------------

// laravelFacts surfaces the rich Laravel signal set — routes, migrations, models,
// controllers, jobs, and the PHP + Laravel versions — all cheap filesystem reads.
// Migrations are files in database/migrations; routes are the `Route::`
// declarations in routes/*.php (a signal, not a resolved route table); models/
// controllers/jobs are file counts under their conventional app/ dirs; versions
// come from composer.json (php + laravel/framework). DB tables, when the database
// is reachable, are appended by buildFacts — not here — so this stays a pure,
// testable file read.
func laravelFacts(dir string) []fact {
	migrations := countFiles(filepath.Join(dir, "database", "migrations"), func(n string) bool {
		return strings.HasSuffix(n, ".php")
	})
	routes := 0
	for _, f := range []string{"web.php", "api.php"} {
		routes += countMatches(filepath.Join(dir, "routes", f), "Route::")
	}
	phpFile := func(n string) bool { return strings.HasSuffix(n, ".php") }
	models := countFiles(filepath.Join(dir, "app", "Models"), phpFile)
	controllers := countFiles(filepath.Join(dir, "app", "Http", "Controllers"), phpFile)
	jobs := countFiles(filepath.Join(dir, "app", "Jobs"), phpFile)
	composer := filepath.Join(dir, "composer.json")
	rows := []fact{
		{Label: "Routes", Value: countOrDash(routes), Hint: "Route:: declarations in routes/web.php + api.php"},
		{Label: "Migrations", Value: countOrDash(migrations), Hint: "files in database/migrations"},
		{Label: "Models", Value: countOrDash(models), Hint: "classes in app/Models"},
		{Label: "Controllers", Value: countOrDash(controllers), Hint: "classes in app/Http/Controllers"},
		{Label: "Jobs", Value: countOrDash(jobs), Hint: "classes in app/Jobs"},
		{Label: "Laravel", Value: firstNonEmpty(composerVersion(composer, "laravel/framework"), "—"), Hint: "laravel/framework in composer.json"},
		{Label: "PHP", Value: firstNonEmpty(composerVersion(composer, "php"), "—"), Hint: "php constraint in composer.json"},
	}
	return append(rows, genericFacts(dir)...)
}

// nextFacts surfaces the rich Next signal set — page routes, API routes,
// components, and the node + Next versions — all cheap file reads. Page routes
// are page.* files under app/ (or src/app) + files under pages/; API routes are
// route.* files (App Router) plus files under pages/api; components are .tsx/.jsx
// under a components/ dir; versions come from package.json (next) and .nvmrc /
// engines (node). Routes are file counts — the cheap, honest signal, not a
// resolved Next route manifest.
func nextFacts(dir string) []fact {
	appDir := filepath.Join(dir, "app")
	if !dirExists(appDir) {
		appDir = filepath.Join(dir, "src", "app")
	}
	pageRoutes := countFiles(appDir, isNextPageFile) +
		countFiles(filepath.Join(dir, "pages"), func(n string) bool {
			return isTSXorJSX(n) && !strings.HasPrefix(n, "_")
		})
	apiRoutes := countFiles(appDir, isNextRouteHandler) +
		countFiles(filepath.Join(dir, "pages", "api"), isTSXorJSX)
	components := nextComponentCount(dir)
	hasConfig := fileExists(dir, "next.config.js") || fileExists(dir, "next.config.mjs") || fileExists(dir, "next.config.ts")
	pkg := filepath.Join(dir, "package.json")
	router := "—"
	if dirExists(appDir) {
		router = "App Router"
	} else if dirExists(filepath.Join(dir, "pages")) {
		router = "Pages Router"
	}
	rows := []fact{
		{Label: "Routes", Value: countOrDash(pageRoutes + apiRoutes), Hint: "page + route.* files under app/ (or src/app) + pages/"},
		{Label: "Router", Value: router, Hint: "App Router (app/) vs Pages Router (pages/)"},
		{Label: "API routes", Value: countOrDash(apiRoutes), Hint: "route.* handlers + pages/api files"},
		{Label: "Components", Value: countOrDash(components), Hint: ".tsx/.jsx files under a components/ dir"},
		{Label: "Config", Value: presentOrMissing(hasConfig), Hint: "next.config.{js,mjs,ts}"},
		{Label: "Next", Value: firstNonEmpty(jsonVersion(pkg, "next"), "—"), Hint: "next in package.json"},
		{Label: "Node", Value: firstNonEmpty(nodeVersion(dir), "—"), Hint: ".nvmrc or engines.node in package.json"},
	}
	return append(rows, genericFacts(dir)...)
}

// dirExists reports whether path is a directory — used to prefer app/ over
// src/app for the route walk without counting twice.
func dirExists(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil || strings.Contains(abs, "..") {
		return false
	}
	st, err := os.Stat(abs)
	return err == nil && st.IsDir()
}

// isNextPageFile matches an App Router page file (page.tsx/.jsx/…).
func isNextPageFile(n string) bool {
	return baseName(n) == "page" && isTSXorJSX(n)
}

// isNextRouteHandler matches an App Router API route handler (route.ts/.js/…).
func isNextRouteHandler(n string) bool {
	return baseName(n) == "route" && isTSXorJSX(n)
}

// baseName strips the ts/tsx/js/jsx extension off a file name for the page/route
// discrimination.
func baseName(n string) string {
	return strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(n, ".tsx"), ".ts"), ".jsx"), ".js")
}

// nextComponentCount counts .tsx/.jsx files that live under a directory named
// "components" anywhere in the tree — the conventional home of React components.
// A bounded walk; unrecognised layouts simply count zero rather than guessing.
func nextComponentCount(dir string) int {
	n, seen := 0, 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) && path != dir {
				return filepath.SkipDir
			}
			return nil
		}
		seen++
		if seen > maxWalkEntries {
			return filepath.SkipAll
		}
		if isTSXorJSX(d.Name()) && strings.HasSuffix(d.Name(), "x") &&
			pathHasSegment(path, "components") {
			n++
		}
		return nil
	})
	return n
}

// pathHasSegment reports whether any directory segment of path equals seg — used
// to attribute a file to a "components" dir wherever it sits in the tree.
func pathHasSegment(path, seg string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == seg {
			return true
		}
	}
	return false
}

// nodeVersion reads the node version a Next project pins — .nvmrc first (a bare
// version line), then engines.node in package.json. "" when neither pins one.
func nodeVersion(dir string) string {
	if body, ok := readBounded(filepath.Join(dir, ".nvmrc")); ok {
		if v := cleanVersion(strings.TrimSpace(body)); v != "" {
			return v
		}
	}
	body, ok := readBounded(filepath.Join(dir, "package.json"))
	if !ok {
		return ""
	}
	var pkg struct {
		Engines struct {
			Node string `json:"node"`
		} `json:"engines"`
	}
	if err := json.Unmarshal([]byte(body), &pkg); err != nil {
		return ""
	}
	return cleanVersion(pkg.Engines.Node)
}

func isTSXorJSX(n string) bool {
	return strings.HasSuffix(n, ".tsx") || strings.HasSuffix(n, ".ts") ||
		strings.HasSuffix(n, ".jsx") || strings.HasSuffix(n, ".js")
}

// djangoFacts surfaces the rich Django signal set — apps, models, migrations,
// routes, and the python + Django versions — all cheap filesystem reads, never a
// manage.py shell-out (which would boot Django). Apps are packages with an
// apps.py; models are `class …(models.Model)` declarations across models.py;
// migrations are numbered files under */migrations; routes are path()/re_path()
// across urls.py; versions come from requirements/pyproject (Django) and a
// runtime.txt / .python-version (python).
func djangoFacts(dir string) []fact {
	apps := countFiles(dir, func(n string) bool { return n == "apps.py" })
	models := countModelsDjango(dir)
	migrations := countMigrationsDjango(dir)
	routes := countRoutesDjango(dir)
	rows := []fact{
		{Label: "Apps", Value: countOrDash(apps), Hint: "packages with an apps.py"},
		{Label: "Models", Value: countOrDash(models), Hint: "models.Model subclasses across models.py"},
		{Label: "Migrations", Value: countOrDash(migrations), Hint: "migration files under */migrations"},
		{Label: "Routes", Value: countOrDash(routes), Hint: "path()/re_path() declarations across urls.py"},
		{Label: "Django", Value: firstNonEmpty(pyVersion(dir, "django"), "—"), Hint: "Django in requirements.txt / pyproject.toml"},
		{Label: "Python", Value: firstNonEmpty(pythonVersion(dir), "—"), Hint: ".python-version / runtime.txt / pyproject requires-python"},
	}
	return append(rows, genericFacts(dir)...)
}

// countModelsDjango sums Django model classes across every models.py — a coarse
// `models.Model` subclass count (a signal, not an AST). Bounded by the shared
// walk cap and vendor-pruned.
func countModelsDjango(dir string) int {
	total, seen := 0, 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) && path != dir {
				return filepath.SkipDir
			}
			return nil
		}
		seen++
		if seen > maxWalkEntries {
			return filepath.SkipAll
		}
		if d.Name() == "models.py" {
			total += countMatches(path, "models.Model")
		}
		return nil
	})
	return total
}

// pythonVersion reads the python version a project pins — .python-version /
// runtime.txt (a bare version, "python-3.12"), then a pyproject requires-python.
// "" when none is pinned.
func pythonVersion(dir string) string {
	for _, name := range []string{".python-version", "runtime.txt"} {
		if body, ok := readBounded(filepath.Join(dir, name)); ok {
			v := strings.TrimSpace(body)
			v = strings.TrimPrefix(strings.ToLower(v), "python-")
			if cv := cleanVersion(v); cv != "" {
				return cv
			}
		}
	}
	if body, ok := readBounded(filepath.Join(dir, "pyproject.toml")); ok {
		for _, raw := range strings.Split(body, "\n") {
			line := strings.TrimSpace(raw)
			if strings.HasPrefix(strings.ToLower(line), "requires-python") {
				if i := strings.Index(line, "="); i >= 0 {
					return cleanVersion(line[i+1:])
				}
			}
		}
	}
	return ""
}

// countMigrationsDjango counts numbered migration files under any migrations/
// directory (skipping __init__.py), bounded by the shared walk cap.
func countMigrationsDjango(dir string) int {
	n, seen := 0, 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) && path != dir {
				return filepath.SkipDir
			}
			return nil
		}
		seen++
		if seen > maxWalkEntries {
			return filepath.SkipAll
		}
		if filepath.Base(filepath.Dir(path)) == "migrations" &&
			strings.HasSuffix(d.Name(), ".py") && d.Name() != "__init__.py" {
			n++
		}
		return nil
	})
	return n
}

// countRoutesDjango sums path()/re_path() declarations across every urls.py.
func countRoutesDjango(dir string) int {
	total, seen := 0, 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) && path != dir {
				return filepath.SkipDir
			}
			return nil
		}
		seen++
		if seen > maxWalkEntries {
			return filepath.SkipAll
		}
		if d.Name() == "urls.py" {
			// `re_path(` contains `path(`, so counting `path(` alone already covers
			// both path() and re_path() — counting them separately double-counts.
			total += countMatches(path, "path(")
		}
		return nil
	})
	return total
}

// fastapiFacts surfaces the route-decorator signal (@app./@router. .get/.post/…)
// across the tree and whether a requirements/pyproject is present. Route counting
// is a coarse decorator match — a signal, labelled as such, never a resolved
// OpenAPI path table (which would import the app).
func fastapiFacts(dir string) []fact {
	routes := countRoutesFastAPI(dir)
	deps := fileExists(dir, "pyproject.toml") || fileExists(dir, "requirements.txt")
	return []fact{
		{Label: "Routes", Value: countOrDash(routes), Hint: "@app./@router. HTTP decorators across the tree"},
		{Label: "Deps", Value: presentOrMissing(deps), Hint: "pyproject.toml or requirements.txt"},
	}
}

// countRoutesFastAPI sums FastAPI route decorators across .py files. It matches
// the common @app. / @router. verb decorators; nested routers named otherwise are
// undercounted, which is why the row is labelled a signal.
func countRoutesFastAPI(dir string) int {
	verbs := []string{".get(", ".post(", ".put(", ".patch(", ".delete("}
	total, seen := 0, 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) && path != dir {
				return filepath.SkipDir
			}
			return nil
		}
		seen++
		if seen > maxWalkEntries {
			return filepath.SkipAll
		}
		if !strings.HasSuffix(d.Name(), ".py") {
			return nil
		}
		body, ok := readBounded(path)
		if !ok {
			return nil
		}
		for _, prefix := range []string{"@app", "@router"} {
			for _, v := range verbs {
				total += strings.Count(body, prefix+v)
			}
		}
		return nil
	})
	return total
}

// magentoFacts surfaces the enabled-module count (files under app/etc/config.php,
// counted cheaply) and whether env.xml exists (installed?). It avoids `bin/magento`
// (slow: boots the app); the deploy mode/indexer status would need that CLI, so we
// show only what a file read can honestly give and leave the rest to "—".
func magentoFacts(ctx context.Context, dir string) []fact {
	_ = ctx // reserved: a future cheap `bin/magento` probe would use the leash
	modules := countMatches(filepath.Join(dir, "app", "etc", "config.php"), "=> 1")
	if modules == 0 {
		// Fall back to composer-installed module dirs under vendor is too heavy;
		// count declared modules under app/code instead (custom modules).
		modules = countFiles(filepath.Join(dir, "app", "code"), func(n string) bool {
			return n == "module.xml"
		})
	}
	installed := fileExists(dir, "app", "etc", "env.xml")
	return []fact{
		{Label: "Modules", Value: countOrDash(modules), Hint: "enabled modules in app/etc/config.php (or app/code module.xml)"},
		{Label: "Installed", Value: presentOrMissing(installed), Hint: "app/etc/env.xml present"},
	}
}

// --- value formatting ---------------------------------------------------------

// countOrDash renders a count, using "—" for zero so the dashboard reads "not
// found / none" honestly rather than implying a real zero the walk proved.
func countOrDash(n int) string {
	if n <= 0 {
		return "—"
	}
	return strconv.Itoa(n)
}

// presentOrMissing renders a boolean file/config fact as a short word.
func presentOrMissing(present bool) string {
	if present {
		return "yes"
	}
	return "no"
}
