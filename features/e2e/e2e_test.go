// Package e2e is keel's end-to-end BDD suite: Gherkin scenarios that drive the
// real `keel` binary. The default (offline) run resolves every stack through the
// binary; the @visible scenarios scaffold for real, boot the app, load its
// homepage in a browser and save a screenshot as visible proof (run them with
// `make e2e-visible`). Every scenario cleans up after itself.
package e2e

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/coullworks/keel/internal/proxy"

	"github.com/coullworks/keel/internal/project"
	"github.com/cucumber/godog"
)

var (
	buildOnce  sync.Once
	keelBin    string
	keelBinDir string
	buildErr   error
)

// TestMain brackets the whole suite with the cleanup guarantees: sweep anything
// a previous crashed run left behind, and on the way out remove the temp binary
// and fail if this run leaked containers, volumes or networks of its own.
func TestMain(m *testing.M) {
	// Isolate the config dir so a plugin/pack the developer installed in their real
	// ~/.config/keel never leaks into the e2e binary's catalogue and fails the
	// matrix. Child keel invocations inherit this through os.Environ() in run().
	if tmp, err := os.MkdirTemp("", "keel-e2e-cfg"); err == nil {
		os.Setenv("KEEL_CONFIG_DIR", tmp)
		defer os.RemoveAll(tmp)
	}
	sweepOrphans()
	clearArtifacts()
	before := danglingVolumes()

	code := m.Run()

	sweepOrphans()
	if keelBinDir != "" {
		_ = os.RemoveAll(keelBinDir) // the built binary lived here
	}
	if left := orphansRemain(); len(left) > 0 {
		fmt.Fprintf(os.Stderr, "e2e leaked resources that the sweep could not remove:\n  %s\n",
			strings.Join(left, "\n  "))
		if code == 0 {
			code = 1
		}
	}
	// A volume that became dangling across this run and is not one of ours by
	// name. Named rather than removed: this machine has other work on it, and
	// deleting a volume we cannot attribute is not a risk worth taking to keep
	// a test suite tidy.
	if stranded := newlyDangling(before); len(stranded) > 0 {
		fmt.Fprintf(os.Stderr,
			"e2e left %d dangling volume(s). Check them, then remove with:\n  docker volume rm %s\n",
			len(stranded), strings.Join(stranded, " "))
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func buildKeel() (string, error) {
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "keel-e2e-bin")
		if err != nil {
			buildErr = err
			return
		}
		keelBinDir = dir
		bin := filepath.Join(dir, "keel")
		cmd := exec.Command("go", "build", "-o", bin, "./cmd/keel")
		cmd.Dir = repoRoot()
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("build keel: %v\n%s", err, out)
			return
		}
		keelBin = bin
	})
	return keelBin, buildErr
}

// this file lives at <root>/features/e2e — the repo root is two up.
func repoRoot() string {
	wd, _ := os.Getwd()
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

type world struct {
	bin    string
	work   string // temp workspace (removed on cleanup)
	target string // the scaffolded project dir
	out    string // last command output
	dev    *exec.Cmd
	devLog *syncBuf
	booted bool
	port   int
	name   string // this scenario's labelled identity
}

// labelPrefix marks every container, volume and project this suite creates, so
// teardown can find them by name. A crashed run (or a CI job killed mid-scenario)
// leaves nothing that a later sweep cannot identify and remove.
const labelPrefix = "keel-e2e-"

// label is the unique name for this scenario's resources.
func (w *world) label() string {
	if w.name == "" {
		w.name = fmt.Sprintf("%s%d", labelPrefix, time.Now().UnixNano())
	}
	return w.name
}

func (w *world) keelIsBuilt() error {
	bin, err := buildKeel()
	if err != nil {
		return err
	}
	w.bin = bin
	w.work, err = os.MkdirTemp("", "keel-e2e-work")
	return err
}

func (w *world) newDryRun(fw, kit, env string) error {
	w.target = filepath.Join(w.work, fw)
	args := []string{"new", fw, "-o", w.target, "--env", env, "--yes", "--dry-run"}
	if kit != "" {
		args = append(args, "--with", kit)
	}
	out, err := w.run(60*time.Second, w.bin, args...)
	w.out = out
	if err != nil {
		return fmt.Errorf("keel new: %v\n%s", err, out)
	}
	return nil
}

func (w *world) planIncludes(sub string) error {
	if !strings.Contains(w.out, sub) {
		return fmt.Errorf("plan missing %q:\n%s", sub, w.out)
	}
	return nil
}

// probe is for asking a port what it is, never for waiting on it. The default
// http client has no timeout, so a peer that accepts the connection and then
// says nothing hangs the caller until the whole test binary times out. That is
// not hypothetical: it is how this function hung a run for ten minutes.
var probe = &http.Client{Timeout: 3 * time.Second}

// siteProbe is for the container tier, where probe's assumptions do not hold.
//
// Thirty seconds because a first request can be a first render: Magento builds
// caches and generates code on the way to its first page, and three seconds
// calls that dead. Redirects are followed because several of these stacks
// legitimately redirect the root, and a 301 is not proof that anything is behind
// it.
//
// TLS verification is off because DDEV issues each project its own certificate
// from a locally generated authority. The question here is whether the
// application serves a page, not whether a certificate minted on this machine
// chains to a public root - and refusing it would fail every DDEV row for a
// reason that has nothing to do with keel. Nothing secret crosses this
// connection: it is a scaffolded example project on loopback.
var siteProbe = &http.Client{
	Timeout:   30 * time.Second,
	Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // local dev certificates, see above
}

// portFree reports whether the scenario's port can actually be bound.
//
// Under WSL2 a Windows-side server on the same port answers on 127.0.0.1 and
// does not appear in `ss`, so "is anything listening" is not the question. The
// question is whether we can bind it, which is what the dev server has to do.
func portFree(port int) error {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		hint := ""
		if resp, gerr := probe.Get(fmt.Sprintf("http://127.0.0.1:%d/", port)); gerr == nil {
			resp.Body.Close()
			hint = fmt.Sprintf(" Something is already serving there (HTTP %d, %s).",
				resp.StatusCode, resp.Header.Get("Server")+resp.Header.Get("X-Powered-By"))
		}
		return fmt.Errorf("port %d is not available.%s Stop it and re-run, "+
			"and note that under WSL2 a Windows-side process holds the port too "+
			"while staying invisible to ss: %v", port, hint, err)
	}
	return l.Close()
}

// scaffoldAndBoot allocates the port rather than being handed one.
//
// The scenarios used to name 3000, 4321, 5173 and 8000. Three rows then failed
// on a machine where 3000 was held by an unrelated process, which said nothing
// about keel. A port the kernel just offered cannot collide, and keel proxy
// makes the number irrelevant to a human anyway.
func (w *world) scaffoldAndBoot(fw, kit, env string) error {
	if !selected(fw, kit, env) {
		return godog.ErrSkip
	}
	port, err := proxy.FreePort()
	if err != nil {
		return fmt.Errorf("no free port available: %w", err)
	}
	if err := portFree(port); err != nil {
		return err // raced with something else between allocating and binding
	}
	w.target = filepath.Join(w.work, fw)
	args := []string{"new", fw, "-o", w.target, "--env", env, "--yes"}
	if kit != "" {
		args = append(args, "--with", kit)
	}
	if out, err := w.run(15*time.Minute, w.bin, args...); err != nil {
		return fmt.Errorf("scaffold: %v\n%s", err, out)
	}
	w.port = port
	w.dev = exec.Command(w.bin, "run", "dev")
	w.dev.Dir = w.target
	w.dev.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", port))
	w.dev.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // own process group so we can kill children
	// Capture the dev server's output. Without it a failure to boot reports
	// only "never became ready", which says nothing about whether the install
	// failed, the port was taken or the process died on start.
	w.devLog = &syncBuf{}
	w.dev.Stdout = w.devLog
	w.dev.Stderr = w.devLog
	if err := w.dev.Start(); err != nil {
		return err
	}
	return w.waitReady(3 * time.Minute)
}

// syncBuf collects a child process's output while the poller reads it from
// another goroutine.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

// tail returns the last n lines, which is the part that says why.
func (s *syncBuf) tail(n int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	lines := strings.Split(strings.TrimRight(s.b.String(), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func (w *world) waitReady(timeout time.Duration) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/", w.port)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// 4xx counts as ready, 5xx does not. Ready is a statement about the
		// process, not about the route: an API-only stack has nothing mounted at
		// / and correctly answers "Cannot GET /" there, so the old < 400 rule
		// declared a perfectly healthy express server dead after three minutes
		// while its own log said "listening on :NNNN".
		//
		// 5xx still waits, because that is what a dev server returns while it is
		// mid-compile or wired to a database that has not come up yet - and
		// calling that ready would hand the next step a page that is about to
		// change. What each scenario actually wants is asserted by the scenario:
		// the landing-page rows check the page content, the API rows check
		// /health.
		if resp, err := probe.Get(url); err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		// A dev server that has already exited will never become ready, so stop
		// waiting out the full timeout and report what it said on the way down.
		if w.dev != nil && w.dev.ProcessState != nil {
			return fmt.Errorf("dev server exited before :%d answered (%s):\n%s",
				w.port, w.dev.ProcessState, w.devLogTail())
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("app on :%d never became ready within %s. Last output:\n%s",
		w.port, timeout, w.devLogTail())
}

func (w *world) devLogTail() string {
	if w.devLog == nil {
		return "(no output captured)"
	}
	if s := w.devLog.tail(25); s != "" {
		return s
	}
	return "(the dev server printed nothing)"
}

func (w *world) get(path string) (string, error) {
	resp, err := probe.Get(fmt.Sprintf("http://127.0.0.1:%d%s", w.port, path))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b), nil
}

func (w *world) homepageContains(sub string) error {
	body, err := w.get("/")
	if err != nil {
		return err
	}
	if !strings.Contains(strings.ToLower(body), strings.ToLower(sub)) {
		return fmt.Errorf("homepage missing %q", sub)
	}
	return nil
}

func (w *world) getReturns(path, sub string) error {
	body, err := w.get(path)
	if err != nil {
		return err
	}
	if !strings.Contains(body, sub) {
		return fmt.Errorf("GET %s missing %q: %s", path, sub, body)
	}
	return nil
}

// screenshotSaved drives Playwright to load the running homepage, assert the app
// actually rendered, and capture features/e2e/artifacts/<name>.png.
//
// This replaced a raw chromium --screenshot invocation, which proved only that a
// PNG was written: a stack trace screenshots perfectly well. Playwright waits for
// a real selector, fails on an uncaught page error, and pins one browser build
// instead of using whichever chromium happened to be on PATH.
func (w *world) sessionRecorded(name string) error {
	dir := filepath.Join(repoRoot(), "features", "e2e", "browser")
	if _, err := os.Stat(filepath.Join(dir, "node_modules", ".bin", "playwright")); err != nil {
		return fmt.Errorf("the browser harness is not installed. Run: make e2e-browser")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join("node_modules", ".bin", "playwright"), "test")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("KEEL_URL=http://127.0.0.1:%d", w.port),
		"KEEL_NAME="+name,
		"KEEL_EXPECT=app is running",
	)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("browser session for %s failed: %v\n%s\n\n"+
			"If the browser is missing, run: make e2e-browser", name, err, b)
	}

	shot := filepath.Join(repoRoot(), "features", "e2e", "artifacts", name+".png")
	info, err := os.Stat(shot)
	if err != nil {
		return fmt.Errorf("the run reported success but wrote no screenshot: %w", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("screenshot %s is empty", shot)
	}
	return nil
}

func (w *world) cleanup() {
	if w.dev != nil && w.dev.Process != nil {
		_ = syscall.Kill(-w.dev.Process.Pid, syscall.SIGKILL) // kill the whole process group
		_, _ = w.dev.Process.Wait()
	}
	if w.target != "" {
		// Best effort here: the scenario has already reported its own verdict,
		// and a teardown failure at this point must not mask it.
		_ = teardownEnv(w.target, w.label())
	}
	if w.work != "" {
		_ = os.RemoveAll(w.work)
	}
}

// teardownEnv drops any container env + its volumes so nothing is orphaned.
// Teardown is part of the contract, not best effort: an orphaned DDEV project or
// docker volume breaks the *next* run, on a different scenario, which is a
// miserable thing to debug.
func teardownEnv(target, label string) error {
	var failed error
	run := func(name string, args ...string) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		c := exec.CommandContext(ctx, name, args...)
		c.Dir = target
		c.Env = append(os.Environ(), "COMPOSE_PROJECT_NAME="+label)
		if out, err := c.CombinedOutput(); err != nil {
			failed = fmt.Errorf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
		}
	}
	switch project.DetectEnv(target) {
	case "ddev":
		run("ddev", "delete", "-Oy")
	case "sail":
		run(filepath.Join(target, "vendor", "bin", "sail"), "down", "-v", "--remove-orphans")
	case "docker":
		run("docker", "compose", "down", "-v", "--remove-orphans")
	}
	return failed
}

// sweepOrphans removes anything this suite labelled that is still around. It
// runs before the first scenario and after the last, so a previous crashed run
// cannot poison this one. It only ever touches names carrying labelPrefix: a
// global prune would take the developer's own containers with it.
func sweepOrphans() {
	docker := func(args ...string) []string {
		out, err := exec.Command("docker", args...).Output()
		if err != nil {
			return nil
		}
		var ids []string
		for _, l := range strings.Split(string(out), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				ids = append(ids, l)
			}
		}
		return ids
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return
	}
	// -v takes the anonymous volumes attached to these containers with them.
	// Without it a force-removed container leaves untagged volumes behind that
	// no name filter can find, which is how a suite quietly fills a disk.
	if ids := docker("ps", "-aq", "--filter", "name="+labelPrefix); len(ids) > 0 {
		_ = exec.Command("docker", append([]string{"rm", "-f", "-v"}, ids...)...).Run()
	}
	if vols := docker("volume", "ls", "-q", "--filter", "name="+labelPrefix); len(vols) > 0 {
		_ = exec.Command("docker", append([]string{"volume", "rm", "-f"}, vols...)...).Run()
	}
	// Networks last: docker refuses to remove one that still has an endpoint
	// attached, so the containers above have to go first.
	//
	// compose creates <project>_default for every stack it brings up. `compose
	// down` removes it, but a run that crashes or is killed before teardown
	// never gets there, and nothing else was looking for them: they accumulated
	// one per scenario, invisibly, until docker ran out of address space for new
	// bridge networks and unrelated stacks stopped being able to start.
	if nets := docker("network", "ls", "-q", "--filter", "name="+labelPrefix); len(nets) > 0 {
		_ = exec.Command("docker", append([]string{"network", "rm"}, nets...)...).Run()
	}
}

// danglingVolumes is the set of unused volumes, by id. Compared before and
// after the suite so a volume this run stranded is named rather than left to
// accumulate. They are reported and never deleted: a dangling volume can
// belong to anything on the machine, and this suite is not entitled to guess.
func danglingVolumes() map[string]bool {
	out := map[string]bool{}
	if _, err := exec.LookPath("docker"); err != nil {
		return out
	}
	b, err := exec.Command("docker", "volume", "ls", "-q", "--filter", "dangling=true").Output()
	if err != nil {
		return out
	}
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out[l] = true
		}
	}
	return out
}

// clearArtifacts empties the screenshot directory before the suite.
//
// A screenshot from a previous run is worse than a missing one: the scenario
// that should have written it can fail, and the stale PNG sits there reading as
// proof the stack booted. That happened, twice, with the Next.js rows.
func clearArtifacts() {
	dir := filepath.Join("artifacts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			if e.Name() == "sessions" {
				_ = os.RemoveAll(filepath.Join(dir, e.Name()))
			}
			continue
		}
		if strings.HasSuffix(e.Name(), ".png") || strings.HasSuffix(e.Name(), ".webm") {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// newlyDangling returns volumes that were in use or absent before the suite and
// are unused after it, sorted so the failure message is stable.
func newlyDangling(before map[string]bool) []string {
	var out []string
	for id := range danglingVolumes() {
		if !before[id] {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// orphansRemain reports any labelled container or volume still present, so a
// leak fails the run instead of quietly accumulating.
func orphansRemain() []string {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil
	}
	var left []string
	for _, q := range [][]string{
		{"ps", "-a", "--filter", "name=" + labelPrefix, "--format", "container {{.Names}}"},
		{"volume", "ls", "--filter", "name=" + labelPrefix, "--format", "volume {{.Name}}"},
		// Networks count as a leak too. Leaving them out is why a clean-looking
		// run could still strand one bridge network per scenario.
		{"network", "ls", "--filter", "name=" + labelPrefix, "--format", "network {{.Name}}"},
	} {
		out, err := exec.Command("docker", q...).Output()
		if err != nil {
			continue
		}
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if l != "" {
				left = append(left, l)
			}
		}
	}
	return left
}

func (w *world) run(timeout time.Duration, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = w.work
	// Own process group: a scaffold shells out to installers, and killing only
	// the direct child left a hung `npm install` running after the timeout.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(), "COMPOSE_PROJECT_NAME="+w.label())
	done := make(chan struct{})
	var out []byte
	var err error
	go func() { out, err = cmd.CombinedOutput(); close(done) }()
	select {
	case <-done:
		return string(out), err
	case <-time.After(timeout):
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return string(out), fmt.Errorf("timed out after %s", timeout)
	}
}

// tagsForTier maps KEEL_E2E to the scenarios that actually run. The advertised
// tiers used to be identical: "native" and "full" both cleared the tag filter,
// so the container matrix the Makefile promised never ran at all.
//
//	(unset)  dry     resolve every stack offline; no Docker, no browser
//	native   @visible  also scaffold and boot the no-container stacks
//	full     @full     also bring up DDEV/Sail/compose stacks and tear them down
func tagsForTier(tier string) string {
	switch tier {
	case "native":
		return "~@full"
	case "full":
		return ""
	default:
		return "~@visible && ~@full"
	}
}

// --- the container tier -------------------------------------------------
//
// These three steps were declared in the feature file and never implemented, so
// all sixteen container rows reported as "undefined" rather than as failures and
// the suite read as though it covered them. It did not: no container was ever
// started by this suite until these existed.

// scaffoldContainer builds a project whose environment brings itself up, so
// unlike scaffoldAndBoot there is no dev server to start or port to poll.
//
// COMPOSE_PROJECT_NAME is forced so every container and volume compose creates
// carries the sweep's prefix. Without it compose names them after the directory
// and the teardown check cannot find them.
func (w *world) scaffoldContainer(fw, kit, env string) error {
	if !selected(fw, kit, env) {
		return godog.ErrSkip
	}
	if err := requireDocker(); err != nil {
		return err
	}
	w.target = filepath.Join(w.work, fw)

	args := []string{"new", fw, "-o", w.target, "--env", env, "--yes"}
	if kit != "" {
		args = append(args, "--with", kit)
	}
	// The compose environments publish one port and read it from HTTP_PORT,
	// defaulting to 8080. Taking a port the kernel just offered instead means a
	// row cannot fail because something unrelated holds 8080, and gives the HTTP
	// assertion an address to use. DDEV ignores this and is asked for its own
	// URL instead.
	port, err := proxy.FreePort()
	if err != nil {
		return fmt.Errorf("no free port available: %w", err)
	}
	if err := portFree(port); err != nil {
		return err // raced with something else between allocating and binding
	}
	w.port = port

	cmd := exec.Command(w.bin, args...)
	cmd.Env = append(os.Environ(),
		"COMPOSE_PROJECT_NAME="+w.label(),
		fmt.Sprintf("HTTP_PORT=%d", port))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out := &syncBuf{}
	cmd.Stdout, cmd.Stderr = out, out

	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("scaffold %s on %s: %v\n%s", fw, env, err, out.tail(40))
		}
	case <-time.After(25 * time.Minute):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return fmt.Errorf("scaffold %s on %s did not finish in 25m:\n%s", fw, env, out.tail(40))
	}
	w.booted = true
	return nil
}

// environmentRunning asks the environment itself, rather than inferring from the
// scaffold exiting 0. A scaffold can succeed and leave nothing running.
func (w *world) environmentRunning() error {
	if w.target == "" {
		return fmt.Errorf("no project was scaffolded")
	}
	if _, err := os.Stat(filepath.Join(w.target, ".ddev", "config.yaml")); err == nil {
		out, err := w.runIn(w.target, 3*time.Minute, "ddev", "describe", "-j")
		if err != nil {
			return fmt.Errorf("ddev describe: %v\n%s", err, out)
		}
		if !strings.Contains(out, `"status":"running"`) && !strings.Contains(out, `"status": "running"`) {
			return fmt.Errorf("ddev reports the project is not running:\n%s", out)
		}
		return nil
	}
	out, err := w.runIn(w.target, 3*time.Minute, "docker", "compose", "ps", "--format", "{{.Name}} {{.State}}")
	if err != nil {
		return fmt.Errorf("docker compose ps: %v\n%s", err, out)
	}
	if !strings.Contains(out, "running") {
		return fmt.Errorf("no compose service is running:\n%s", out)
	}
	return nil
}

// servesOverHTTP asks the stack for a page, which is the only question a person
// building a project actually cares about.
//
// The container tier had no such step. It proved containers were up and stopped
// there, and "up" is not "working": Django and four of the Node skeletons had no
// route at / and answered 404 at the very URL keel prints, and Reflex served an
// empty directory and answered 403 to everything. All of it passed, because
// nothing here ever made a request.
//
// A 404 fails. keel prints a URL at the end of a build, so nothing at that URL
// is a defect, not a style. 5xx fails for the obvious reason. Redirects do not:
// Magento's admin and WordPress both legitimately redirect the root.
func (w *world) servesOverHTTP() error {
	url, err := w.siteURL()
	if err != nil {
		return err
	}
	// A container can report healthy a moment before the application behind it
	// finishes booting, so this retries rather than asking once.
	deadline := time.Now().Add(3 * time.Minute)
	var last string
	for {
		resp, err := siteProbe.Get(url)
		switch {
		case err != nil:
			last = err.Error()
		default:
			code := resp.StatusCode
			resp.Body.Close()
			if code < 400 {
				return nil
			}
			last = fmt.Sprintf("HTTP %d", code)
			if code == http.StatusNotFound {
				last += " - the stack is up but nothing is mounted at /, " +
					"which is the address keel prints when the build finishes"
			}
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("%s did not serve a page: %s", url, last)
		}
		time.Sleep(2 * time.Second)
	}
}

// siteURL is where this environment publishes the app. DDEV assigns its own
// hostname and knows it; compose publishes the port the scaffold step chose.
func (w *world) siteURL() (string, error) {
	if _, err := os.Stat(filepath.Join(w.target, ".ddev", "config.yaml")); err != nil {
		return fmt.Sprintf("http://127.0.0.1:%d/", w.port), nil
	}
	out, err := w.runIn(w.target, time.Minute, "ddev", "describe", "-j")
	if err != nil {
		return "", fmt.Errorf("ddev describe: %v\n%s", err, out)
	}
	var described struct {
		Raw struct {
			PrimaryURL string `json:"primary_url"`
		} `json:"raw"`
	}
	if err := json.Unmarshal([]byte(out), &described); err != nil {
		return "", fmt.Errorf("ddev describe returned no JSON: %v\n%s", err, out)
	}
	if described.Raw.PrimaryURL == "" {
		return "", fmt.Errorf("ddev describe named no primary_url:\n%s", out)
	}
	return described.Raw.PrimaryURL, nil
}

// teardownIsClean tears the environment down and proves it left nothing. The
// assertion is the point: a teardown that half works is how a machine fills up.
func (w *world) teardownIsClean() error {
	if err := teardownEnv(w.target, w.label()); err != nil {
		return err
	}
	w.booted = false
	if left := orphansRemain(); len(left) > 0 {
		return fmt.Errorf("teardown left %d resource(s) behind:\n  %s",
			len(left), strings.Join(left, "\n  "))
	}
	return nil
}

// runIn runs a command in dir with COMPOSE_PROJECT_NAME set, so compose targets
// the same project the scaffold created.
func (w *world) runIn(dir string, timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "COMPOSE_PROJECT_NAME="+w.label())
	b, err := cmd.CombinedOutput()
	return string(b), err
}

// requireDocker fails the scenario rather than skipping it. A container tier
// that quietly passes without Docker is the problem this file already had.
func requireDocker() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker is not on PATH, and the @full tier needs it: %v", err)
	}
	if out, err := exec.Command("docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput(); err != nil {
		return fmt.Errorf("docker is installed but not running: %v\n%s", err, out)
	}
	return nil
}

// A note on the DDEV rows, so nobody re-discovers this the slow way.
//
// `ddev start` has to make <project>.ddev.site resolve. It prefers DNS - the
// domain resolves to 127.0.0.1 publicly - and falls back to writing a hosts
// file entry, which needs root. Both halves of that fallback are closed on a
// WSL2 workstation without an interactive session:
//
//   - With DDEV managing the Windows hosts file (the default), it shells out to
//     ddev-hostname.exe, which raises a UAC prompt. Run by hand you approve it;
//     run unattended nobody does, and the row hangs until the suite times out.
//   - With `ddev config global --wsl2-no-windows-hosts-mgt` set, it writes
//     /etc/hosts instead and needs sudo, which fails with "a terminal is
//     required to read the password".
//
// So the fallback only works where the suite can be root, or where DNS answers.
// It was measured here: an existing project's hostname resolved (its entry was
// already in the hosts file from an interactive run) while a new one did not.
//
// None of that is keel's doing - `keel doctor` reports it and names DDEV's own
// setting - and it does not apply to CI, which runs Linux as root and writes
// /etc/hosts without asking. Locally, use KEEL_E2E_ONLY=docker,sail to run every
// compose-based row, and either accept the UAC prompts by sitting with the run,
// or pre-add the project hostnames to the hosts file once.
//
// Reporting a DDEV row as passing without it having started would be worse than
// not running it, so these fail loudly rather than skipping.

// selected reports whether a row should run, given KEEL_E2E_ONLY.
//
// The container tier is sixteen rows and several of them install a whole
// ecommerce platform, so one run is hours. Without a way to ask for a single
// row the practical choice was between all of it and none of it, and none of it
// is what kept happening: the tier had never been run once.
//
// The value is a comma-separated list of substrings matched against the
// framework and env, so `KEEL_E2E_ONLY=magento,laravel` runs those rows and
// skips the rest, and `KEEL_E2E_ONLY=ddev` runs every DDEV row. Unset runs
// everything. CI shards on the same variable rather than repeating the list of
// frameworks in the workflow.
//
// Matched on the arguments rather than the scenario name because godog reports
// a Scenario Outline row under the outline's unsubstituted title - every row of
// a table shares the literal name "<name> resolves through the keel binary", so
// filtering there matched all rows or none.
func selected(parts ...string) bool {
	only := strings.TrimSpace(os.Getenv("KEEL_E2E_ONLY"))
	if only == "" {
		return true
	}
	hay := strings.ToLower(strings.Join(parts, " "))
	for _, want := range strings.Split(only, ",") {
		if want = strings.ToLower(strings.TrimSpace(want)); want != "" &&
			strings.Contains(hay, want) {
			return true
		}
	}
	return false
}

func TestScaffoldE2E(t *testing.T) {
	tags := tagsForTier(os.Getenv("KEEL_E2E"))
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			w := &world{}
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				w.cleanup()
				return ctx, nil
			})
			sc.Step(`^keel is built$`, w.keelIsBuilt)
			sc.Step(`^I run keel new for "([^"]*)" with kit "([^"]*)" on env "([^"]*)"$`, w.newDryRun)
			sc.Step(`^the build plan includes "([^"]*)"$`, w.planIncludes)
			sc.Step(`^I scaffold and boot "([^"]*)" with kit "([^"]*)" on env "([^"]*)"$`, w.scaffoldAndBoot)
			sc.Step(`^the homepage contains "([^"]*)"$`, w.homepageContains)
			sc.Step(`^the session is recorded as "([^"]*)"$`, w.sessionRecorded)
			sc.Step(`^GET "([^"]*)" returns "([^"]*)"$`, w.getReturns)
			sc.Step(`^I scaffold "([^"]*)" with kit "([^"]*)" on env "([^"]*)"$`, w.scaffoldContainer)
			sc.Step(`^the environment reports itself running$`, w.environmentRunning)
			sc.Step(`^it serves a page over HTTP$`, w.servesOverHTTP)
			sc.Step(`^tearing it down leaves no containers or volumes behind$`, w.teardownIsClean)
		},
		Options: &godog.Options{
			Format: "pretty",
			Paths:  []string{"."},
			Tags:   tags,
			// Without this an undefined step is reported and the run still
			// passes. Sixteen container scenarios sat undefined for weeks and
			// the suite read as though it covered them.
			Strict:   true,
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("e2e scenarios failed")
	}
}

// A busy port used to cost a full scaffold and the whole readiness timeout
// before reporting something knowable in a millisecond, so the preflight is
// worth a test of its own.
func TestPortFree(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot bind a loopback port here")
	}
	busy := l.Addr().(*net.TCPAddr).Port

	if err := portFree(busy); err == nil {
		t.Errorf("port %d is held open, portFree should have refused it", busy)
	} else if !strings.Contains(err.Error(), "not available") {
		t.Errorf("the message should say the port is unavailable, got: %v", err)
	}

	l.Close()
	if err := portFree(busy); err != nil {
		t.Errorf("port %d is free now, portFree should accept it: %v", busy, err)
	}
}
