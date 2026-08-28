package cli

// services_runtime.go is the terminal counterpart to the studio's services
// panel: it enumerates a keel project's env services with each one's up/down
// state, and starts/stops/restarts a single named service. `keel service` and
// `keel status` both read through it, so the two agree on what is running.
//
// The runtime is decided by the project env recipe's family (compose/sail/ddev/
// local), never guessed: a compose or sail stack is read from
// `docker compose ps --format json`, a ddev stack from `ddev describe -j`, and a
// local (native) env has no services to manage. Every shell-out goes through
// captureCmd (argv, no shell) so nothing user-supplied ever reaches a shell, and
// each call is bounded by a context so a wedged runtime surfaces as a clear
// state rather than a hang.
//
// This deliberately re-derives the same behaviour internal/studio/services.go
// implements over HTTP, because the CLI must not import the studio (wrong
// direction). The two are worth consolidating into a shared low-level package
// later - see the report.

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/platform"
	"github.com/coullworks/keel/internal/recipe"
)

// runtimeTimeout bounds a single service enumeration. Listing services
// (`docker compose ps` / `ddev describe`) is a fast metadata read, so a call
// that outlives this is a wedged runtime, not slow work. A start/stop that pulls
// an image can legitimately take longer, so control runs off this leash.
const runtimeTimeout = 12 * time.Second

// svcState is one service row: its name, whether it is running, and a short kind
// label (the image or ddev type, best-effort).
type svcState struct {
	Name    string
	Running bool
	Kind    string
}

// svcListing is the whole env view: the runtime family, whether the env is up
// overall, the per-service rows, whether per-service control is supported for
// this runtime, and - when there is nothing to draw - a calm message.
type svcListing struct {
	Family   string
	Up       bool
	Services []svcState
	Controls bool
	Message  string
}

// hasRuntime reports whether a container runtime binary (docker/ddev) is on
// PATH. A package var so a test can drive both installed and not-installed
// states; the real body is platform.Has.
var hasRuntime = platform.Has

// captureCmd runs argv in dir and returns combined output. It is a seam: a test
// stands in for docker/ddev without depending on the host. No element of argv
// ever reaches a shell.
var captureCmd = func(ctx context.Context, dir string, argv ...string) (string, error) {
	var b strings.Builder
	err := (engine.ExecRunner{Out: &b}).RunArgv(ctx, dir, argv)
	return b.String(), err
}

// projectEnvRecipe resolves a keel project's env recipe (the source of truth for
// its runtime family). A missing manifest is the "not a keel project" error the
// caller renders; an unknown env recipe is its own clear message.
func projectEnvRecipe(dir string) (recipe.Recipe, error) {
	m, err := engine.ReadManifest(dir)
	if err != nil {
		return recipe.Recipe{}, manifestErr(err)
	}
	reg, err := catalog.Registry()
	if err != nil {
		return recipe.Recipe{}, err
	}
	env, ok := reg.Get(m.Env)
	if !ok {
		return recipe.Recipe{}, errUnknownEnv{m.Env}
	}
	return env, nil
}

// errUnknownEnv is the manifest-names-an-unknown-env case, kept as a type so the
// wording lives in one place.
type errUnknownEnv struct{ env string }

func (e errUnknownEnv) Error() string {
	return "this project's environment (" + e.env + ") is not a known recipe"
}

// runtimeFamily reports the env recipe's runtime family, defaulting to local so
// an env naming no family is treated as the safe no-service case.
func runtimeFamily(env recipe.Recipe) string {
	if env.EnvFamily != "" {
		return env.EnvFamily
	}
	return recipe.FamilyLocal
}

// listServices enumerates the services for one env, dispatching on its family.
// It never returns an error: every failure mode (runtime not installed, env
// down, parse trouble) is a listing with a clear Message, because the caller
// prints a state, it does not catch an exception.
func listServices(ctx context.Context, dir string, env recipe.Recipe) svcListing {
	switch fam := runtimeFamily(env); fam {
	case recipe.FamilyCompose, recipe.FamilySail:
		return composeServices(ctx, dir, fam)
	case recipe.FamilyDDEV:
		return ddevServices(ctx, dir)
	default:
		return svcListing{
			Family:   recipe.FamilyLocal,
			Services: []svcState{},
			Message:  "This project runs natively - there are no containers to manage. Use keel run to start the dev server.",
		}
	}
}

// --- compose / sail ----------------------------------------------------------

// composePS is the subset of `docker compose ps --format json` we read. The
// docker CLI emits either one JSON object per line or a single array depending
// on version, so the parser accepts both.
type composePS struct {
	Service string `json:"Service"`
	Name    string `json:"Name"`
	State   string `json:"State"`
	Image   string `json:"Image"`
}

// composeServices lists a compose/sail project's DEFINED services, each with its
// running state. The defined set comes from `docker compose config --services`
// (reads the FILE, works with the daemon down); the running set from
// `docker compose ps` (needs the daemon). Defined is the source of truth, ps is
// overlaid, and a down daemon still shows every service off rather than a blank.
func composeServices(ctx context.Context, dir, fam string) svcListing {
	res := svcListing{Family: fam, Services: []svcState{}, Controls: true}
	if !hasRuntime("docker") {
		res.Controls = false
		res.Message = "Docker is not installed or not on PATH, so keel cannot read this project's services."
		return res
	}

	defined, dErr := composeDefinedServices(ctx, dir)
	psRows, rErr := composePSServices(ctx, dir)
	running := map[string]bool{}
	kinds := map[string]string{}
	for _, s := range psRows {
		if s.Running {
			running[s.Name] = true
		}
		if s.Kind != "" {
			kinds[s.Name] = s.Kind
		}
	}

	if dErr != nil && rErr != nil {
		res.Controls = false
		res.Message = "Could not read this project's services - is a docker-compose file present and Docker reachable?"
		return res
	}
	if dErr != nil || len(defined) == 0 {
		res.Services = append(res.Services, psRows...)
	} else {
		for _, name := range defined {
			res.Services = append(res.Services, svcState{Name: name, Running: running[name], Kind: kinds[name]})
		}
	}
	sortSvcs(res.Services)
	res.Up = anyRunning(res.Services)

	if rErr != nil && len(res.Services) > 0 {
		res.Message = "Docker is not reachable, so these services show as stopped - start Docker (or the environment) to bring them online."
	} else if len(res.Services) == 0 {
		res.Message = "This environment is down - no containers are up yet. Start it to bring the stack online."
	}
	return res
}

// composeDefinedServices returns the service names the compose file declares, via
// `docker compose config --services` (one name per line). Reads the FILE, so it
// answers with the daemon down.
func composeDefinedServices(ctx context.Context, dir string) ([]string, error) {
	out, err := captureCmd(ctx, dir, "docker", "compose", "config", "--services")
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

// composePSServices returns every container `docker compose ps --all` reports
// (name, running, image). Needs the daemon, so its error is soft: the caller
// treats every defined service as off on error. `--all` includes stopped
// containers so a created-but-exited service is still seen.
func composePSServices(ctx context.Context, dir string) ([]svcState, error) {
	out, err := captureCmd(ctx, dir, "docker", "compose", "ps", "--all", "--format", "json")
	if err != nil {
		return nil, err
	}
	rows, perr := parseComposePS(out)
	if perr != nil {
		return nil, perr
	}
	var svcs []svcState
	for _, row := range rows {
		name := firstNonEmptyStr(row.Service, row.Name)
		if name == "" {
			continue
		}
		svcs = append(svcs, svcState{
			Name:    name,
			Running: strings.EqualFold(strings.TrimSpace(row.State), "running"),
			Kind:    row.Image,
		})
	}
	return svcs, nil
}

// parseComposePS accepts both shapes `docker compose ps --format json` emits: a
// JSON array or newline-delimited JSON objects. Blank output (never started) is
// a valid empty result, not an error.
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

// ddevDescribe is the slice of `ddev describe -j` we read: raw.services maps a
// service name to its status/type, and raw.status is the project's overall state.
type ddevDescribe struct {
	Raw struct {
		Status   string `json:"status"`
		Services map[string]struct {
			Status string `json:"status"`
			Type   string `json:"type"`
		} `json:"services"`
	} `json:"raw"`
}

// ddevServices reads `ddev describe -j`. ddev brings its services up and down as
// one unit (no first-class per-service start/stop), so Controls is false and the
// caller directs per-service intent to the whole-env start/stop.
func ddevServices(ctx context.Context, dir string) svcListing {
	res := svcListing{Family: recipe.FamilyDDEV, Services: []svcState{}, Controls: false}
	if !hasRuntime("ddev") {
		res.Message = "DDEV is not installed or not on PATH, so keel cannot read this project's services."
		return res
	}
	out, err := captureCmd(ctx, dir, "ddev", "describe", "-j")
	if err != nil {
		res.Message = "Could not read services - is DDEV running? Start the environment, then try again."
		return res
	}
	var parsed ddevDescribe
	if jerr := json.Unmarshal([]byte(out), &parsed); jerr != nil {
		res.Message = "Could not read this project's service list from DDEV."
		return res
	}
	for name, svc := range parsed.Raw.Services {
		res.Services = append(res.Services, svcState{
			Name:    name,
			Running: strings.EqualFold(strings.TrimSpace(svc.Status), "running"),
			Kind:    svc.Type,
		})
	}
	sortSvcs(res.Services)
	res.Up = strings.EqualFold(strings.TrimSpace(parsed.Raw.Status), "running") || anyRunning(res.Services)
	if len(res.Services) == 0 {
		res.Message = "This environment is down - DDEV reports no running services. Start it to bring the stack online."
	}
	return res
}

// --- shared helpers ----------------------------------------------------------

// safeServiceName accepts only the shape a compose/ddev service name takes: a
// short identifier of letters, digits, hyphens, underscores and dots. It is the
// argv guard before a name reaches the command line - a name that could never be
// a service is refused, and it can never carry a flag or a path.
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

// anyRunning reports whether at least one service is up (the "is it running?"
// headline).
func anyRunning(svcs []svcState) bool {
	for _, s := range svcs {
		if s.Running {
			return true
		}
	}
	return false
}

// sortSvcs orders rows deterministically by name so output does not reshuffle
// between calls.
func sortSvcs(svcs []svcState) {
	sort.Slice(svcs, func(i, j int) bool { return svcs[i].Name < svcs[j].Name })
}

// firstNonEmptyStr returns the first non-blank string.
func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
