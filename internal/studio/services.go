package studio

// services.go backs the Overview's running-services dashboard: the primary
// answer to "is this project running?". It enumerates a managed project's env
// services with each one's up/down state, and starts/stops/restarts a single
// named service.
//
// The runtime is decided by the env recipe's family (compose/sail/ddev/local),
// never guessed: a compose or sail stack is read from `docker compose ps
// --format json`, a ddev stack from `ddev describe -j`, and a local (native)
// env has no services to manage. Every shell-out goes through the runCapture
// seam (argv, no shell) so a test can stand in for docker/ddev, and each call is
// bounded by a context timeout so a wedged runtime surfaces as a clear state
// rather than a hung request.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/platform"
	"github.com/coullworks/keel/internal/recipe"
)

// hasRuntime reports whether a container runtime binary (docker/ddev) is on
// PATH. It is a package var wrapping platform.Has so a test can drive both the
// installed and not-installed states without depending on the host — the same
// seam idea as runCapture. The real body is the trivial one-liner the seam
// convention allows to go uncovered.
var hasRuntime = platform.Has

// servicesTimeout bounds a single enumeration or control call. Listing services
// (`docker compose ps` / `ddev describe`) is a fast metadata read, so a call
// that outlives this is a wedged runtime, not slow work — and letting it hang
// would leave the Overview on a spinner that never resolves. A start/stop that
// pulls an image can legitimately take longer, so control runs on its own
// (streamed) path, not this leash.
const servicesTimeout = 12 * time.Second

// service is one row the dashboard draws: its env service name, whether it is
// running, a short kind label (the image or type, best-effort), and — for a
// running compose container — its human uptime ("2 hours", "3 minutes") parsed
// from the runtime's own Status string, so the dashboard shows how long each
// service has been up without a second probe.
type service struct {
	Name    string `json:"name"`
	Running bool   `json:"running"`
	Kind    string `json:"kind,omitempty"`
	Uptime  string `json:"uptime,omitempty"` // "2 hours" — running compose containers only
}

// servicesResult is the whole Overview state: the runtime family, whether the
// env is up overall, the per-service rows, whether per-service control is
// supported for this runtime, and — when there is nothing to draw — a calm
// inline message the UI shows instead of a blank.
type servicesResult struct {
	Family   string    `json:"family"`
	Up       bool      `json:"up"`
	Services []service `json:"services"`
	Controls bool      `json:"controls"`
	Message  string    `json:"message,omitempty"`
}

// handleServices lists the project env's services + each one's state. It names a
// project by ?dir=, so it is a guarded GET. A stopped env, an uninstalled
// runtime and a local (no-service) env are all NORMAL answers (HTTP 200 with a
// clear message), never an error the caller must catch or a spinner that hangs.
func handleServices(w http.ResponseWriter, r *http.Request) {
	dir, err := projectDir(r.URL.Query().Get("dir"))
	if err != nil {
		writeJSON(w, servicesResult{Services: []service{}, Message: err.Error()})
		return
	}
	env, ferr := projectEnvRecipe(dir)
	if ferr != nil {
		writeJSON(w, servicesResult{Services: []service{}, Message: ferr.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), servicesTimeout)
	defer cancel()
	writeJSON(w, listServices(ctx, dir, env))
}

// projectEnvRecipe resolves a tracked project's env recipe (the source of truth
// for its runtime family). A missing manifest or unknown env recipe is returned
// as an error the caller renders inline.
func projectEnvRecipe(dir string) (recipe.Recipe, error) {
	m, err := engine.ReadManifest(dir)
	if err != nil {
		return recipe.Recipe{}, fmt.Errorf("not a keel project: %s", dir)
	}
	reg, err := catalog.Registry()
	if err != nil {
		return recipe.Recipe{}, err
	}
	env, ok := reg.Get(m.Env)
	if !ok {
		return recipe.Recipe{}, fmt.Errorf("this project's environment (%s) is not a known recipe", m.Env)
	}
	return env, nil
}

// familyOf reports the env recipe's runtime family, defaulting to local so an
// env that names no family is treated as the safe no-service case rather than
// shelling to a runtime that may not apply.
func familyOf(env recipe.Recipe) string {
	if env.EnvFamily != "" {
		return env.EnvFamily
	}
	return recipe.FamilyLocal
}

// listServices enumerates the services for one env, dispatching on its family.
// It never returns an error: every failure mode (runtime not installed, env
// down, parse trouble) is a servicesResult with a clear Message, because the
// dashboard renders a state, it does not catch an exception.
func listServices(ctx context.Context, dir string, env recipe.Recipe) servicesResult {
	fam := familyOf(env)
	switch fam {
	case recipe.FamilyCompose, recipe.FamilySail:
		return composeServices(ctx, dir, fam)
	case recipe.FamilyDDEV:
		return ddevServices(ctx, dir)
	default: // local / unknown
		return servicesResult{
			Family:   recipe.FamilyLocal,
			Services: []service{},
			Message:  "This project runs natively. There are no containers to manage. Use Run to start the dev server.",
		}
	}
}

// --- compose / sail ----------------------------------------------------------

// composePS is the one line of `docker compose ps --format json` the dashboard
// reads. The docker CLI emits either one JSON object per line or a single JSON
// array depending on version, so the parser accepts both.
type composePS struct {
	Service string `json:"Service"`
	Name    string `json:"Name"`
	State   string `json:"State"`  // "running" | "exited" | "created" | …
	Image   string `json:"Image"`  // used as the kind label
	Health  string `json:"Health"` // "healthy" | "" — informational
	Status  string `json:"Status"` // "Up 2 hours" | "Exited (0) 5 minutes ago" — uptime source
}

// composeServices lists a compose/sail project's DEFINED services, each with its
// running state. This is the "how do I start a service if I can't see it's off?"
// fix: the list must show every service the compose FILE declares — not only the
// containers `docker compose ps` reports — so a stopped (or never-`up`'d) env
// still renders a full on/off dashboard with a Start on each row.
//
// The defined set comes from `docker compose config --services`, which reads the
// compose file and works with the daemon down or the services never created. The
// running set comes from `docker compose ps`, which needs the daemon. We take
// defined as the source of truth, overlay the running rows onto it, and — when
// the daemon is unreachable — still show the defined services (all off) with a
// note rather than a blank dashboard. sail is compose under the hood (its
// vendor/bin/sail wrapper only sets compose env vars), so it takes this path too.
func composeServices(ctx context.Context, dir, fam string) servicesResult {
	res := servicesResult{Family: fam, Services: []service{}, Controls: true}
	if !hasRuntime("docker") {
		res.Controls = false
		res.Message = "Docker is not installed or not on PATH, so keel cannot read this project's services."
		return res
	}

	// The DEFINED service list from the compose file. This works with the daemon
	// down and the env never started — it is the spine of the dashboard.
	defined, dErr := composeDefinedServices(ctx, dir)

	// The full ps view (needs the daemon). A daemon that is down/wedged is not a
	// hard failure: we fall back to "every defined service is off, daemon
	// unreachable" so the user can still see and start a service.
	psRows, rErr := composePSServices(ctx, dir)
	running := map[string]bool{}
	kinds := map[string]string{}
	uptimes := map[string]string{}
	for _, s := range psRows {
		if s.Running {
			running[s.Name] = true
		}
		if s.Kind != "" {
			kinds[s.Name] = s.Kind
		}
		if s.Uptime != "" {
			uptimes[s.Name] = s.Uptime
		}
	}

	if dErr != nil && rErr != nil {
		// Neither the file nor the daemon could be read — the only genuinely blank
		// case. Say why, and keep controls off so no row offers an action that
		// cannot run.
		res.Controls = false
		res.Message = "Could not read this project's services. Is a docker-compose file present and Docker reachable?"
		return res
	}

	if dErr != nil || len(defined) == 0 {
		// The compose file could not be read (or declared no usable service names),
		// but the daemon answered: fall back to the ps view alone (the pre-fix
		// behaviour), so a compose flow that puts services elsewhere still shows
		// what is up (or exited) rather than a blank list.
		res.Services = append(res.Services, psRows...)
	} else {
		// Defined is the source of truth: one row per declared service, its state
		// overlaid from the ps view. A defined service with no container is shown
		// OFF — the whole point of the fix.
		for _, name := range defined {
			res.Services = append(res.Services, service{Name: name, Running: running[name], Kind: kinds[name], Uptime: uptimes[name]})
		}
	}
	sortServices(res.Services)
	res.Up = anyRunning(res.Services)

	if rErr != nil && len(res.Services) > 0 {
		// The defined services rendered, but the daemon is down — everything reads
		// off. Tell the user why the state is all-off, don't imply it's truly down.
		res.Message = "Docker isn't reachable, so these services show as stopped. Start Docker (or the environment) to bring them online."
	} else if len(res.Services) == 0 {
		res.Message = "This environment is down. No containers are up yet. Start it to bring the stack online."
	}
	return res
}

// composeDefinedServices returns the service names the compose file declares, via
// `docker compose config --services`. This reads the FILE, so it answers with the
// daemon down and the env never brought up — the list every service row is drawn
// from. Its output is one service name per line.
func composeDefinedServices(ctx context.Context, dir string) ([]string, error) {
	out, err := runCapture(ctx, dir, "docker", "compose", "config", "--services")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		if name != "" && safeServiceName(name) {
			names = append(names, name)
		}
	}
	return names, nil
}

// composePSServices returns every container `docker compose ps --all` reports —
// its name, whether it is running, and its image (the kind label). It needs the
// daemon, so its error is soft: the caller overlays this onto the defined list
// and, on error, treats every defined service as off. `--all` includes stopped
// containers, so a created-but-exited service is still seen (not just running
// ones). Rows without a resolvable name are skipped.
func composePSServices(ctx context.Context, dir string) ([]service, error) {
	out, err := runCapture(ctx, dir, "docker", "compose", "ps", "--all", "--format", "json")
	if err != nil {
		return nil, err
	}
	rows, perr := parseComposePS(out)
	if perr != nil {
		return nil, perr
	}
	var out2 []service
	for _, row := range rows {
		name := firstNonEmpty(row.Service, row.Name)
		if name == "" {
			continue
		}
		running := strings.EqualFold(strings.TrimSpace(row.State), "running")
		s := service{
			Name:    name,
			Running: running,
			Kind:    row.Image,
		}
		if running {
			s.Uptime = parseComposeUptime(row.Status)
		}
		out2 = append(out2, s)
	}
	return out2, nil
}

// upStatusRe pulls the age out of a compose "Up <age>" Status string. Docker
// renders a running container's Status as "Up 2 hours", "Up About a minute",
// "Up 3 days (healthy)", etc.; the age is the text after "Up " and before any
// parenthetical health note. A Status that is not an "Up …" line (exited,
// created, restarting) yields no uptime — the row is not running.
var upStatusRe = regexp.MustCompile(`(?i)^Up\s+(.+?)(?:\s*\(.*\))?$`)

// parseComposeUptime turns a compose Status ("Up 2 hours") into the human age
// ("2 hours") the dashboard shows on a running service. Anything that is not an
// "Up …" status (exited/created/empty) returns "" — the caller only asks for a
// running container's uptime, and an unparseable status degrades to no badge
// rather than a wrong one.
func parseComposeUptime(status string) string {
	m := upStatusRe.FindStringSubmatch(strings.TrimSpace(status))
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// sortServices orders the rows deterministically (by name) so the dashboard does
// not reshuffle between refreshes — `config --services` and a map overlay have no
// stable order of their own.
func sortServices(svcs []service) {
	sort.Slice(svcs, func(i, j int) bool { return svcs[i].Name < svcs[j].Name })
}

// parseComposePS accepts both shapes `docker compose ps --format json` emits: a
// JSON array (older/newer CLIs differ) or newline-delimited JSON objects. Blank
// output (the env was never started) is a valid, empty result, not an error.
func parseComposePS(out string) ([]composePS, error) {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	if strings.HasPrefix(out, "[") {
		var rows []composePS
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			return nil, err
		}
		return rows, nil
	}
	var rows []composePS
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row composePS
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// --- ddev --------------------------------------------------------------------

// ddevServicesJSON is the slice of `ddev describe -j` the dashboard reads:
// raw.services is a map of service name -> {status, type}. ddev reports each
// service's status here, so the studio (on the host) renders it without a probe.
type ddevServicesJSON struct {
	Raw struct {
		Status   string `json:"status"` // the whole project's status
		Services map[string]struct {
			Status string `json:"status"`
			Type   string `json:"type"`
			Full   string `json:"full_name"`
		} `json:"services"`
	} `json:"raw"`
}

// ddevServices reads `ddev describe -j` for the project. ddev brings its
// services up and down as one unit (there is no first-class per-service
// start/stop), so Controls is false and the dashboard directs per-service
// intent to the header's Start/Stop. A missing ddev binary or a stopped project
// resolve to a clear, non-blank state.
func ddevServices(ctx context.Context, dir string) servicesResult {
	res := servicesResult{Family: recipe.FamilyDDEV, Services: []service{}, Controls: false}
	if !hasRuntime("ddev") {
		res.Message = "DDEV is not installed or not on PATH, so keel cannot read this project's services."
		return res
	}
	out, err := runCapture(ctx, dir, "ddev", "describe", "-j")
	if err != nil {
		res.Message = "Could not read services. Is DDEV running? Start the environment, then reopen Overview."
		return res
	}
	var parsed ddevServicesJSON
	if jerr := json.Unmarshal([]byte(out), &parsed); jerr != nil {
		res.Message = "Could not read this project's service list from DDEV."
		return res
	}
	for name, svc := range parsed.Raw.Services {
		res.Services = append(res.Services, service{
			Name:    name,
			Running: strings.EqualFold(strings.TrimSpace(svc.Status), "running"),
			Kind:    svc.Type,
		})
	}
	res.Up = strings.EqualFold(strings.TrimSpace(parsed.Raw.Status), "running") || anyRunning(res.Services)
	if len(res.Services) == 0 {
		res.Message = "This environment is down. DDEV reports no running services. Start it to bring the stack online."
	}
	return res
}

// anyRunning reports whether at least one service is up, which is the Overview's
// "is it running?" headline.
func anyRunning(svcs []service) bool {
	for _, s := range svcs {
		if s.Running {
			return true
		}
	}
	return false
}

// --- per-service control -----------------------------------------------------

// safeServiceName accepts only the shape a compose/ddev service name takes: a
// short identifier of letters, digits, hyphens, underscores and dots. It is
// belt-and-braces before the name reaches the command line as one argv element —
// a name that could never be a service is refused, and it can never carry a flag
// or a path.
func safeServiceName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 60 || strings.HasPrefix(name, "-") {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

// serviceActions is the closed set of verbs a service control may run, the same
// shape projectActions uses: a whitelist is the only safe form for a request
// that ends in a real process on the developer's machine.
var serviceActions = map[string]bool{"start": true, "stop": true, "restart": true}

// handleServiceAction starts/stops/restarts one named service and streams the
// result over SSE (the same envelope as handleProjectAction). It runs
// `docker compose start|stop|restart <svc>` for a compose/sail env; ddev has no
// first-class per-service control, so it is refused with a clear reason that
// points at the whole-env Start/Stop. The service name is validated to a safe
// shape and passed as one argv element (never concatenated into a shell line).
// Loopback-only, POST-only, token-guarded.
func handleServiceAction(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	var body struct {
		Dir     string `json:"dir"`
		Service string `json:"service"`
		Action  string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	sw := &sseWriter{w: w, f: flusher}
	fail := func(msg string) { sw.emit([]byte("✗ " + msg)); emitDone(w, flusher, false, 1, msg) }

	if !serviceActions[body.Action] {
		fail("action not allowed: " + body.Action)
		return
	}
	if !safeServiceName(body.Service) {
		fail("invalid service name: " + body.Service)
		return
	}
	dir, err := projectDir(body.Dir)
	if err != nil {
		fail(err.Error())
		return
	}
	env, ferr := projectEnvRecipe(dir)
	if ferr != nil {
		fail(ferr.Error())
		return
	}
	fam := familyOf(env)
	switch fam {
	case recipe.FamilyCompose, recipe.FamilySail:
		if !hasRuntime("docker") {
			fail("Docker is not installed or not on PATH.")
			return
		}
	case recipe.FamilyDDEV:
		fail("DDEV manages its services together. Use the project's Start / Stop to bring the whole environment up or down.")
		return
	default:
		fail("This project runs natively. There are no services to " + body.Action + ".")
		return
	}

	// docker compose <argv> <svc>: recipe-family-selected verb + a validated
	// service name, each a separate argv element. No request text ever reaches a
	// shell — the whole line is built here from a closed set, mirroring handleRun.
	//
	// Start uses `up -d --no-deps` rather than `start`: `compose start` only
	// resumes a container that already exists, so a DEFINED-but-never-created
	// service (the whole reason the dashboard now lists off services) would fail.
	// `up -d --no-deps <svc>` creates and starts just that one service without
	// dragging its dependencies up. Stop/restart act on the existing container.
	verb := map[string]string{"start": "starting", "stop": "stopping", "restart": "restarting"}[body.Action]
	argv := map[string][]string{
		"start":   {"docker", "compose", "up", "-d", "--no-deps", body.Service},
		"stop":    {"docker", "compose", "stop", body.Service},
		"restart": {"docker", "compose", "restart", body.Service},
	}[body.Action]
	sw.emit([]byte("→ " + verb + " service " + body.Service))
	runErr := (engine.ExecRunner{Out: sw}).RunArgv(r.Context(), dir, argv)
	sw.flushRemainder()
	if runErr != nil {
		sw.emit([]byte("✗ " + runErr.Error()))
		emitDone(w, flusher, false, exitCode(runErr), runErr.Error())
		return
	}
	sw.emit([]byte("✓ done"))
	emitDone(w, flusher, true, 0, "")
}
