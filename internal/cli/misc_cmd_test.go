package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/plugins"
	"github.com/coullworks/keel/internal/plugintest"
	"github.com/coullworks/keel/internal/tui"
)

// doctor renders the host-tool checklist and the Node/nvm section. It only reads
// the host (LookPath / `docker info`) — never installs anything without --fix.
func TestDoctor(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	mustContain(t, out, "keel doctor", "git", "Node / nvm")
}

// doctor --json emits structured results (tools + secrets), never the boxed UI.
func TestDoctorJSON(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("doctor --json: %v", err)
	}
	mustContain(t, out, `"tools"`, `"secrets_ok"`, `"state"`, `"git"`)
	// JSON mode replaces the human report entirely.
	mustNotContain(t, out, "Node / nvm")
}

// doctor flags a .env that is present but not gitignored, in both surfaces.
func TestDoctorSecretsIssue(t *testing.T) {
	wd := isolate(t)
	if err := os.WriteFile(filepath.Join(wd, ".env"), []byte("SECRET=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	human, err := runRoot(t, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	mustContain(t, human, "Secrets", "not in .gitignore")

	js, err := runRoot(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("doctor --json: %v", err)
	}
	mustContain(t, js, `"secrets_ok": false`, `"secrets_issue"`)
}

// A gitignored .env is healthy: no secrets warning surfaces.
func TestDoctorSecretsHealthy(t *testing.T) {
	wd := isolate(t)
	if err := os.WriteFile(filepath.Join(wd, ".env"), []byte("SECRET=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, ".gitignore"), []byte(".env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	mustNotContain(t, out, "not in .gitignore")
}

// checkDocker classifies docker as missing / warn / ok; here we only assert it
// returns a well-formed tool with the right name (host-dependent state).
func TestCheckDocker(t *testing.T) {
	tool := checkDocker()
	if tool.Name != "docker" {
		t.Errorf("checkDocker name = %q", tool.Name)
	}
}

// DockerRunning must agree with checkDocker: true iff the tool state is OK.
func TestDockerRunning(t *testing.T) {
	want := checkDocker().State == tui.ToolOK
	if DockerRunning() != want {
		t.Errorf("DockerRunning() = %v, want %v", DockerRunning(), want)
	}
}

// nvmDir respects $NVM_DIR, else falls back to $HOME/.nvm.
func TestNvmDir(t *testing.T) {
	t.Setenv("NVM_DIR", "/custom/nvm")
	if nvmDir() != "/custom/nvm" {
		t.Errorf("nvmDir with env = %q", nvmDir())
	}
	t.Setenv("NVM_DIR", "")
	t.Setenv("HOME", "/home/someone")
	if nvmDir() != "/home/someone/.nvm" {
		t.Errorf("nvmDir fallback = %q", nvmDir())
	}
}

// firstLine returns the first line of a multi-line string.
func TestFirstLine(t *testing.T) {
	if got := firstLine("hello\nworld"); got != "hello" {
		t.Errorf("firstLine = %q", got)
	}
	if got := firstLine("nolf"); got != "nolf" {
		t.Errorf("firstLine single = %q", got)
	}
}

// isWSL keys off the env var (deterministic under test).
func TestIsWSL(t *testing.T) {
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	if !isWSL() {
		t.Error("expected WSL when WSL_DISTRO_NAME is set")
	}
}

// plugins lists nothing when PATH holds no keel-* executables.
// With no keel-* executable on PATH, `keel plugins` lists the plugins keel has
// discovered — keel ships zero built-ins, so a discovered plugin is an installed
// row — and says what each one contributes.
func TestPluginsListsRegistered(t *testing.T) {
	// The plugin registry is cached process-wide. Rebuild it for this test's
	// isolated plugin dir, and reset it afterwards (registered first, so it runs
	// last — once the isolated env is restored) so the discovered fixture does not
	// leak into another test's command surface.
	t.Cleanup(reloadRegistry)
	isolate(t)
	plugintest.Install(t, "demo") // a discovered plugin, enabled by default
	reloadRegistry()              // rebuild against this test's isolated plugin dir
	t.Setenv("PATH", t.TempDir()) // no keel-* executables on PATH
	out, err := runRoot(t, "plugins")
	if err != nil {
		t.Fatalf("plugins: %v", err)
	}
	// One table, and the columns that make it readable: which plugin, where it
	// came from, whether it is on, and what it contributes. The previous format
	// printed things in different shapes, so there was no way to see at a glance
	// what was available versus installed, or which were switched off.
	mustContain(t, out, "demo")
	mustContain(t, out, "enabled")
	// What the plugin contributes, shown in the adds column. The column wraps a
	// long list across lines (wrap, not truncate), so assert the extension points
	// are present rather than as one contiguous run.
	for _, adds := range []string{"command", "screen", "step"} {
		mustContain(t, out, adds)
	}
	// Counts, so an empty install directory is stated rather than implied. keel
	// ships no built-ins, so the discovered plugin is the single installed row.
	mustContain(t, out, "0 built-in, 1 installed")
	// Nothing external is on PATH, so nothing should claim to be.
	if strings.Contains(out, "on PATH`") {
		t.Errorf("no keel-* is on PATH, so no external row should appear:\n%s", out)
	}
}

// plugins lists an installed keel-<name> executable on PATH.
func TestPluginsInstalled(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "keel-hello")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	out, err := runRoot(t, "plugins")
	if err != nil {
		t.Fatalf("plugins: %v", err)
	}
	// An external keel-<name> is a row in the same table now, not a separate
	// trailing section: it is a third way a command can exist, and hiding it
	// under its own heading made it easy to miss one you set up months ago.
	mustContain(t, out, "hello", "external executable on PATH")
}

// plugins.Discover de-dups keel-* names across PATH entries and skips dirs.
func TestDiscoverPlugins(t *testing.T) {
	d1, d2 := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(d1, "keel-alpha"), []byte("#\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Same name in a later PATH dir → de-duped.
	if err := os.WriteFile(filepath.Join(d2, "keel-alpha"), []byte("#\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d2, "keel-beta"), []byte("#\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A directory named keel-dir must be ignored.
	if err := os.MkdirAll(filepath.Join(d2, "keel-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", d1+string(os.PathListSeparator)+d2)
	got := plugins.Discover()
	want := map[string]bool{"alpha": true, "beta": true}
	if len(got) != 2 {
		t.Fatalf("plugins.Discover = %v, want 2 unique", got)
	}
	for _, n := range got {
		if !want[n] {
			t.Errorf("unexpected plugin %q", n)
		}
	}
}

// isBuiltin recognises real commands and the help/completion pseudo-commands, and
// rejects unknown names (which would dispatch to a plugin).
func TestIsBuiltin(t *testing.T) {
	root := rootCmd()
	for _, name := range []string{"new", "doctor", "recipes", "help", "completion"} {
		if !isBuiltin(root, name) {
			t.Errorf("%q should be built-in", name)
		}
	}
	if isBuiltin(root, "totally-made-up") {
		t.Error("unknown name should not be built-in")
	}
}

// dispatchPlugin returns false when no matching keel-<name> is on PATH.
func TestDispatchPluginMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if handled, _ := dispatchPlugin(io.Discard, "nope", nil); handled {
		t.Error("expected false for a missing plugin")
	}
}

// dispatchPlugin runs a matching keel-<name> executable and reports the exit
// code back rather than exiting the process, so a failing plugin is testable.
func TestDispatchPluginSuccess(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "keel-echo")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	handled, code := dispatchPlugin(io.Discard, "echo", []string{"hi"})
	if !handled {
		t.Error("expected dispatchPlugin to handle the call")
	}
	if code != 0 {
		t.Errorf("a plugin exiting 0 should give code 0, got %d", code)
	}
}

// A plugin's non-zero exit becomes keel's exit code instead of killing the
// process from inside a library function.
func TestDispatchPluginPropagatesExitCode(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "keel-fails")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	handled, code := dispatchPlugin(io.Discard, "fails", nil)
	if !handled || code != 3 {
		t.Errorf("dispatchPlugin = (%v, %d), want (true, 3)", handled, code)
	}
}

// doctor with a HOME that has no nvm hits the "nvm not installed" guidance path
// (no --fix, so nothing is ever installed).
func TestDoctorNoNvm(t *testing.T) {
	isolate(t)
	empty := t.TempDir()
	t.Setenv("HOME", empty)
	t.Setenv("NVM_DIR", filepath.Join(empty, ".nvm"))
	out, err := runRoot(t, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	mustContain(t, out, "Node / nvm")
}

// nvmInstalled reflects whether nvm.sh exists under NVM_DIR.
func TestNvmInstalled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NVM_DIR", dir)
	if nvmInstalled() {
		t.Error("empty NVM_DIR should report not installed")
	}
	if err := os.WriteFile(filepath.Join(dir, "nvm.sh"), []byte("# nvm\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !nvmInstalled() {
		t.Error("nvm.sh present should report installed")
	}
}

// sponsor prints the GitHub Sponsors URL.
func TestSponsor(t *testing.T) {
	isolate(t)
	// OpenURL may try to launch a browser; neutralise it by pointing BROWSER at
	// a no-op and clearing DISPLAY so it fails silently (return value ignored).
	t.Setenv("BROWSER", "true")
	t.Setenv("DISPLAY", "")
	out, err := runRoot(t, "sponsor")
	if err != nil {
		t.Fatalf("sponsor: %v", err)
	}
	mustContain(t, out, "github.com/sponsors/coullworks")
}

// self-update --help documents the command and its alias.
func TestSelfUpdateHelp(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "self-update", "--help")
	if err != nil {
		t.Fatalf("self-update --help: %v", err)
	}
	// Cobra shows Long in place of Short once a command has one, so assert on
	// what the help actually has to tell the user.
	mustContain(t, out, "latest GitHub release", "checksum", "upgrade")
}

// mcp (read-only) serves over stdio. We hand it an already-closed stdin so Serve
// sees EOF immediately and returns cleanly — covering the mcp RunE/Serve wiring
// without a live agent session (and without depending on the harness's stdin).
func TestMCPServeEOF(t *testing.T) {
	isolate(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Close() // reader now returns EOF immediately
	oldIn := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = oldIn; _ = r.Close() })

	if _, err := runRoot(t, "mcp"); err != nil {
		t.Fatalf("mcp (EOF stdin): %v", err)
	}
}

// mcp --help documents the stdio server and its --write flag.
func TestMCPHelp(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "mcp", "--help")
	if err != nil {
		t.Fatalf("mcp --help: %v", err)
	}
	mustContain(t, out, "Model Context Protocol", "--write")
}
