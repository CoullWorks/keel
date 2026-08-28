package studio

// console.go holds the handlers that back the reorganised 9-area console: the
// Projects dashboard (per-project actions) and Run & Logs (a live task tail).
// They are deliberately thin: each validates its project directory through
// projectDir (the same front door the grid and exec use, so an action only ever
// touches a directory the user already tracked), then reuses the existing
// engine.ExecRunner + sseWriter streaming path rather than inventing a second
// one. Everything here is registered behind guardAPI (loopback Host, same-origin,
// session token, POST-only) in newMux — see security.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/platform"
	"github.com/coullworks/keel/internal/profile"
	"github.com/coullworks/keel/plugin"
)

// projectActions is the whitelist of per-project actions the dashboard may
// trigger. Anything else is refused, the same way execAllowed gates keel
// subcommands: a closed set is the only safe shape for a request that ends in a
// real process on the developer's machine.
var projectActions = map[string]bool{"open": true, "start": true, "stop": true, "restart": true}

// openInEditor launches editor at dir as a detached process. It is a package var
// so a test can swap it and no real editor opens — the same seam pattern as
// platform.startProcess. It runs argv directly (no shell) so a directory name is
// one argument whatever characters it contains.
var openInEditor = func(editor, dir string) error {
	return exec.Command(editor, dir).Start()
}

// editorCommand returns the editor binary to open a project with: the profile's
// chosen editor, else $VISUAL/$EDITOR, else VS Code's `code`. It is a bare
// binary name (the first field of whatever was configured) so the caller can run
// it as argv[0] with the directory as the only argument — never a shell string.
func editorCommand() string {
	pick := func(v string) string {
		v = strings.TrimSpace(v)
		if v == "" {
			return ""
		}
		return strings.Fields(v)[0] // "code --wait" -> "code"
	}
	// A saved profile's editor is an explicit choice the user made in onboarding,
	// so it wins — but only when a profile actually exists. A fresh install seeds
	// Defaults["editor"]="code" whether or not anyone chose it, so reading that
	// unconditionally would silently ignore a developer's $EDITOR.
	if profile.Exists() {
		if p, err := profile.Load(); err == nil {
			if e := pick(p.Defaults["editor"]); e != "" {
				return e
			}
		}
	}
	for _, env := range []string{"VISUAL", "EDITOR"} {
		if e := pick(os.Getenv(env)); e != "" {
			return e
		}
	}
	return "code"
}

// envCommandFor resolves the start/down/restart command for a project's env
// recipe. The env recipe is the single source of truth for how a stack comes up
// and down (docker compose up -d / down -v, ddev start / stop), so the dashboard
// renders those rather than hardcoding a guess per environment. Returns an error
// when the manifest or env recipe does not define the requested command, so the
// caller reports a clear reason instead of running nothing.
func envCommandFor(dir, action string) (string, error) {
	m, err := engine.ReadManifest(dir)
	if err != nil {
		return "", fmt.Errorf("not a keel project: %s", dir)
	}
	reg, err := catalog.Registry()
	if err != nil {
		return "", err
	}
	env, ok := reg.Get(m.Env)
	if !ok {
		return "", fmt.Errorf("this project's environment (%s) is not a known recipe", m.Env)
	}
	// The dashboard's verbs map onto the env recipe's own command vocabulary:
	// start brings the stack up, stop tears it down, restart bounces it.
	key := map[string]string{"start": "start", "stop": "down", "restart": "restart"}[action]
	cmd := strings.TrimSpace(env.Commands[key])
	if cmd == "" {
		return "", fmt.Errorf("the %s environment has no %q command", m.Env, action)
	}
	return cmd, nil
}

// handleProjectAction runs one whitelisted action against a tracked project and
// streams its output live over SSE (the same envelope as handleExec/handleBuild).
// "open" launches the editor and returns immediately; "start"/"stop"/"restart"
// run the env recipe's own up/down command. Loopback-only, POST-only,
// token-guarded.
func handleProjectAction(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	var body struct {
		Dir    string `json:"dir"`
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Action) == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	sw := &sseWriter{w: w, f: flusher}
	fail := func(msg string) { sw.emit([]byte("✗ " + msg)); emitDone(w, flusher, false, 1, msg) }

	if !projectActions[body.Action] {
		fail("action not allowed: " + body.Action)
		return
	}
	dir, err := projectDir(body.Dir)
	if err != nil {
		fail(err.Error())
		return
	}

	if body.Action == "open" {
		editor := editorCommand()
		if !platform.Has(editor) {
			fail("editor not found on PATH: " + editor + " (set one in Settings, or $EDITOR)")
			return
		}
		if err := openInEditor(editor, dir); err != nil {
			fail(err.Error())
			return
		}
		sw.emit([]byte("✓ opened " + dir + " in " + editor))
		// Opening a project in the editor is the studio focusing it, so fire
		// project.opened: sonar's listener reports the project's visibility score
		// here without being asked, matching what the CLI does when it is pointed
		// at an existing project. A listener's output joins the stream; its trouble
		// never fails the open.
		emitProjectEvent(r.Context(), sw, pluginsFor(), plugin.EventProjectOpened, projectFor(dir))
		emitDone(w, flusher, true, 0, "")
		return
	}

	// start / stop / restart: run the env recipe's own command. It is a
	// recipe-authored string, not request input, so it is safe to run through the
	// shell the same way engine.Build runs env steps.
	cmd, err := envCommandFor(dir, body.Action)
	if err != nil {
		fail(err.Error())
		return
	}
	verb := map[string]string{"start": "starting", "stop": "stopping", "restart": "restarting"}[body.Action]
	sw.emit([]byte("→ " + verb + " " + filepath.Base(dir)))
	runErr := (engine.ExecRunner{Out: sw}).Run(r.Context(), dir, cmd)
	sw.flushRemainder()
	if runErr != nil {
		sw.emit([]byte("✗ " + runErr.Error()))
		emitDone(w, flusher, false, exitCode(runErr), runErr.Error())
		return
	}
	sw.emit([]byte("✓ done"))
	emitDone(w, flusher, true, 0, "")
}

// runCapture runs argv in dir and returns its combined output. It is a package
// var so a test can stand in for the real keel binary without building it — the
// same seam idea as projectDB. The real body runs argv directly (no shell).
var runCapture = func(ctx context.Context, dir string, argv ...string) (string, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// runTask is the shape the Run area's picker renders: a task name and the
// command it resolves to under the project's environment.
type runTask struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

// handleRunTasks lists the runnable tasks (dev/test/lint/…) for a tracked
// project so the Run & Logs area can offer a picker. It shells the keel binary's
// own `run --json` rather than re-deriving tasks here, so the two front ends can
// never disagree about what a stack can run. Loopback-only, POST-only,
// token-guarded — returns JSON, not a stream.
func handleRunTasks(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Dir string `json:"dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		badRequest(w, "bad request")
		return
	}
	dir, err := projectDir(body.Dir)
	if err != nil {
		writeJSON(w, map[string]any{"tasks": []runTask{}, "error": err.Error()})
		return
	}
	// A member of a root-launch workspace has no manifest of its own — it runs
	// from the root command — so it must not be rejected here. `keel run --json`
	// resolves the root launcher for it; every other member still needs its own
	// keel manifest to expose a per-framework task set.
	if !isRootLaunchMember(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".keel", "manifest.yaml")); err != nil {
			writeJSON(w, map[string]any{"tasks": []runTask{}, "error": "not a keel project: " + dir})
			return
		}
	}
	exe, err := os.Executable()
	if err != nil {
		exe = "keel"
	}
	out, err := runCapture(r.Context(), dir, exe, "run", "--json")
	if err != nil {
		writeJSON(w, map[string]any{"tasks": []runTask{}, "error": strings.TrimSpace(out) + err.Error()})
		return
	}
	var parsed struct {
		Framework string    `json:"framework"`
		Env       string    `json:"env"`
		Tasks     []runTask `json:"tasks"`
	}
	if jsonErr := json.Unmarshal([]byte(out), &parsed); jsonErr != nil {
		writeJSON(w, map[string]any{"tasks": []runTask{}, "error": "could not read this project's tasks"})
		return
	}
	if parsed.Tasks == nil {
		parsed.Tasks = []runTask{}
	}
	writeJSON(w, map[string]any{"framework": parsed.Framework, "env": parsed.Env, "tasks": parsed.Tasks})
}

// handleRun streams a `keel run <task>` live over SSE: the user picks a tracked
// project and a task, and its stdout/stderr tail into the console. The task name
// is validated to a safe shape and then passed to the binary as one argv element
// (never concatenated into a shell line); `keel run` itself validates it against
// the project's real task list. Cancelling the request (the browser's Stop)
// cancels the context, which terminates the child process. Loopback-only,
// POST-only, token-guarded.
func handleRun(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	var body struct {
		Dir  string `json:"dir"`
		Task string `json:"task"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Task) == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	sw := &sseWriter{w: w, f: flusher}
	fail := func(msg string) { sw.emit([]byte("✗ " + msg)); emitDone(w, flusher, false, 1, msg) }

	if !safeTaskName(body.Task) {
		fail("invalid task name: " + body.Task)
		return
	}
	dir, err := projectDir(body.Dir)
	if err != nil {
		fail(err.Error())
		return
	}
	// A root-launch member runs via the workspace root command (`keel run dev`
	// resolves it), so it is allowed through without a manifest of its own; any
	// other member still needs its keel manifest.
	if !isRootLaunchMember(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".keel", "manifest.yaml")); err != nil {
			fail("not a keel project: " + dir)
			return
		}
	}
	exe, err := os.Executable()
	if err != nil {
		exe = "keel"
	}
	sw.emit([]byte("→ keel run " + body.Task))
	// RunArgv, not Run: the task name reaches us from the browser, so it is passed
	// as a single argument with no shell in between. `keel run <task>` then renders
	// and runs the real command through the project's environment.
	runErr := (engine.ExecRunner{Out: sw}).RunArgv(r.Context(), dir, []string{exe, "run", body.Task})
	sw.flushRemainder()
	if runErr != nil {
		sw.emit([]byte("✗ " + runErr.Error()))
		emitDone(w, flusher, false, exitCode(runErr), runErr.Error())
		return
	}
	sw.emit([]byte("✓ done"))
	emitDone(w, flusher, true, 0, "")
}

// safeTaskName accepts only the shape a keel task name takes: a short lowercase
// identifier (dev, test, lint, typecheck, build) with optional hyphens. It is
// belt-and-braces on top of `keel run` validating the task against the stack — a
// name that could never be a task is refused before the binary is even launched,
// and it can never carry a flag or path.
func safeTaskName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 40 || strings.HasPrefix(name, "-") {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}
