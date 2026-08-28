package platform

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestDetectFields exercises Detect's field population. GOOS is always set;
// on linux the /proc/version read decides WSL. We can't force the file content,
// but we assert the WSL flag is only ever set on linux (never darwin/windows).
func TestDetectFields(t *testing.T) {
	o := Detect()
	if o.GOOS == "" {
		t.Fatal("GOOS empty")
	}
	if o.WSL && o.GOOS != "linux" {
		t.Fatalf("WSL set on non-linux GOOS %q", o.GOOS)
	}
	// HasBrew must agree with a direct PATH lookup of brew (no divergence).
	if o.HasBrew != Has("brew") {
		t.Fatal("HasBrew disagrees with Has(\"brew\")")
	}
}

// TestInstallerDefaultUnknownTool covers the default branch of Installer: an
// unknown tool gets generic, non-auto guidance and no command.
func TestInstallerDefaultUnknownTool(t *testing.T) {
	ins := Installer("kubectl", OS{GOOS: "linux"})
	if ins.Tool != "kubectl" {
		t.Fatalf("Tool=%q want kubectl", ins.Tool)
	}
	if ins.Auto {
		t.Fatal("unknown tool should not be auto-installable")
	}
	if !strings.Contains(ins.Guide, "kubectl") || !strings.Contains(ins.Guide, "PATH") {
		t.Fatalf("guide missing tool/PATH mention: %q", ins.Guide)
	}
	if ins.Command != "" || ins.URL != "" {
		t.Fatalf("unknown tool should have no command/url: cmd=%q url=%q", ins.Command, ins.URL)
	}
}

// TestInstallerDDEVBranches asserts each ddev branch resolves to the expected
// install method (Homebrew vs script vs manual Windows guidance).
func TestInstallerDDEVBranches(t *testing.T) {
	brew := Installer("ddev", OS{GOOS: "darwin", HasBrew: true})
	if !brew.Auto || !strings.Contains(brew.Command, "brew install") {
		t.Fatalf("darwin+brew ddev should use Homebrew: %+v", brew)
	}
	script := Installer("ddev", OS{GOOS: "linux"})
	if !script.Auto || !strings.Contains(script.Command, "install.sh") {
		t.Fatalf("linux ddev should use the install script: %+v", script)
	}
	// darwin without brew also falls to the script branch.
	if s := Installer("ddev", OS{GOOS: "darwin"}); !s.Auto || !strings.Contains(s.Command, "install.sh") {
		t.Fatalf("darwin-no-brew ddev should use the script: %+v", s)
	}
	win := Installer("ddev", OS{GOOS: "windows"})
	if win.Auto || !strings.Contains(win.Guide, "Chocolatey") {
		t.Fatalf("windows ddev should be manual w/ Chocolatey guidance: %+v", win)
	}
}

// TestInstallerDockerBranches asserts each docker branch: native linux auto,
// WSL manual (Docker Desktop), other manual.
func TestInstallerDockerBranches(t *testing.T) {
	nativeLinux := Installer("docker", OS{GOOS: "linux"})
	if !nativeLinux.Auto || !strings.Contains(nativeLinux.Command, "get.docker.com") {
		t.Fatalf("native linux docker should auto-install via convenience script: %+v", nativeLinux)
	}
	wsl := Installer("docker", OS{GOOS: "linux", WSL: true})
	if wsl.Auto || !strings.Contains(wsl.Guide, "Docker Desktop") {
		t.Fatalf("WSL docker should be manual Docker Desktop guidance: %+v", wsl)
	}
	mac := Installer("docker", OS{GOOS: "darwin"})
	if mac.Auto || !strings.Contains(mac.Guide, "Docker Desktop") {
		t.Fatalf("darwin docker should be manual Docker Desktop: %+v", mac)
	}
}

// TestRun executes the shell-out primitive with a harmless command and asserts
// stdout/stderr are streamed to the writer.
func TestRun(t *testing.T) {
	var buf bytes.Buffer
	if err := run(context.Background(), "printf 'hello-run'", &buf); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "hello-run") {
		t.Fatalf("run output = %q, want it to contain hello-run", got)
	}
}

// TestRunFailingCommand asserts a non-zero exit propagates as an error.
func TestRunFailingCommand(t *testing.T) {
	var buf bytes.Buffer
	if err := run(context.Background(), "exit 3", &buf); err == nil {
		t.Fatal("run should return an error for a non-zero exit")
	}
}

// TestEnsureToolAlreadyPresent covers the fast path: a tool already on PATH
// returns nil with no guidance printed.
func TestEnsureToolAlreadyPresent(t *testing.T) {
	var buf bytes.Buffer
	if err := EnsureTool(context.Background(), "sh", nil, &buf); err != nil {
		t.Fatalf("EnsureTool(sh) should succeed (sh is on PATH): %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("EnsureTool for a present tool should print nothing, got %q", buf.String())
	}
}

// TestEnsureToolMissingNoAuto covers the missing-tool guidance path for an
// unknown tool: it has no auto-install and no URL, so nothing is launched — it
// prints guidance and returns the "required" error. consent is nil (treated as
// no). This exercises EnsureTool without ever spawning a browser or installer.
func TestEnsureToolMissingNoAuto(t *testing.T) {
	var buf bytes.Buffer
	const bogus = "keel-not-a-real-tool-zzz"
	err := EnsureTool(context.Background(), bogus, nil, &buf)
	if err == nil {
		t.Fatal("EnsureTool for a missing, non-auto tool should return an error")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Fatalf("error should say the tool is required: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "not installed") || !strings.Contains(out, bogus) {
		t.Fatalf("guidance not printed: %q", out)
	}
}

// NOTE (untestable without side effects): EnsureTool's auto-install branch runs
// the real installer command (e.g. `curl https://ddev.com/install.sh | bash`)
// and its consent-declined / OpenURL branch launches a browser — every KNOWN
// auto-installable tool (ddev, docker) carries a non-empty URL, so reaching the
// consent path also reaches OpenURL. Both would hit the network / spawn a GUI,
// so they are left uncovered by design.
