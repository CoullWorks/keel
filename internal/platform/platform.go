// Package platform detects the host OS and installs/opens the tools keel relies
// on (ddev, docker) the right way per platform — so a chosen env "just works"
// on a fresh machine. It also opens URLs in the browser (WSL-aware), used by the
// Magento auth-key flow.
package platform

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// OS is a best-effort description of the host.
type OS struct {
	GOOS    string // linux, darwin, windows
	WSL     bool   // running under WSL
	HasBrew bool   // Homebrew present (macOS/Linuxbrew)
}

// Detect inspects the running environment.
func Detect() OS {
	o := OS{GOOS: runtime.GOOS}
	if o.GOOS == "linux" {
		if b, err := os.ReadFile("/proc/version"); err == nil {
			s := strings.ToLower(string(b))
			o.WSL = strings.Contains(s, "microsoft") || strings.Contains(s, "wsl")
		}
	}
	o.HasBrew = Has("brew")
	return o
}

// Seams for tests: swapped out so no real browser opens and no installer runs.
var (
	lookPath     = exec.LookPath
	startProcess = func(name string, args ...string) error { return exec.Command(name, args...).Start() }
)

// Has reports whether a binary is on PATH.
func Has(tool string) bool {
	_, err := lookPath(tool)
	return err == nil
}

// OpenURL opens url in the default browser, handling WSL (where xdg-open is
// absent) by shelling out to Windows. Best-effort — a non-nil error is advisory.
// opener returns the browser-open command + args for the host (pure — testable).
func opener(o OS, hasWslview bool, url string) (string, []string) {
	switch {
	case o.GOOS == "darwin":
		return "open", []string{url}
	case o.GOOS == "windows":
		return "cmd", []string{"/c", "start", url}
	case o.WSL && hasWslview:
		return "wslview", []string{url}
	case o.WSL:
		return "explorer.exe", []string{url}
	default: // native linux
		return "xdg-open", []string{url}
	}
}

func OpenURL(url string) error {
	name, args := opener(Detect(), Has("wslview"), url)
	return startProcess(name, args...)
}

// Install describes how to obtain a tool on this platform.
type Install struct {
	Tool    string
	Auto    bool   // keel can run Command to install it
	Command string // shell command (only meaningful when Auto)
	Guide   string // human-readable steps (always shown)
	URL     string // docs/download page to open when we can't auto-install
}

// Installer returns the install plan for a tool on the given OS. Known tools:
// "ddev", "docker".
func Installer(tool string, o OS) Install {
	switch tool {
	case "ddev":
		switch {
		case o.GOOS == "darwin" && o.HasBrew:
			return Install{Tool: tool, Auto: true, Command: "brew install ddev/ddev/ddev",
				Guide: "Install DDEV via Homebrew.", URL: "https://ddev.readthedocs.io/en/stable/users/install/ddev-installation/"}
		case o.GOOS == "linux" || o.GOOS == "darwin":
			return Install{Tool: tool, Auto: true, Command: "curl -fsSL https://ddev.com/install.sh | bash",
				Guide: "Install DDEV via the official install script.", URL: "https://ddev.readthedocs.io/en/stable/users/install/ddev-installation/"}
		default: // windows
			return Install{Tool: tool, Auto: false,
				Guide: "On Windows, install DDEV with Chocolatey: `choco install ddev`, or use WSL2.",
				URL:   "https://ddev.readthedocs.io/en/stable/users/install/ddev-installation/"}
		}
	case "docker":
		switch {
		case o.GOOS == "linux" && !o.WSL:
			return Install{Tool: tool, Auto: true, Command: "curl -fsSL https://get.docker.com | sh",
				Guide: "Install Docker Engine via the official convenience script, then add yourself to the docker group: `sudo usermod -aG docker $USER` (log out/in after).",
				URL:   "https://docs.docker.com/engine/install/"}
		case o.WSL:
			return Install{Tool: tool, Auto: false,
				Guide: "In WSL2, install Docker Desktop for Windows and enable WSL integration (most reliable). Alternatively run the Linux convenience script inside the distro: `curl -fsSL https://get.docker.com | sh`.",
				URL:   "https://docs.docker.com/desktop/wsl/"}
		default: // darwin, windows
			return Install{Tool: tool, Auto: false,
				Guide: "Install Docker Desktop for your OS, then start it.",
				URL:   "https://docs.docker.com/desktop/"}
		}
	default:
		return Install{Tool: tool, Auto: false, Guide: fmt.Sprintf("Install %q and ensure it is on your PATH.", tool)}
	}
}

// EnsureTool makes sure tool is available. If missing, it prints guidance and —
// when an auto-install exists and consent returns true — runs it, verifying the
// tool appears afterwards. Otherwise it opens the docs page and returns an error
// telling the user to install and re-run. consent may be nil (treated as no).
func EnsureTool(ctx context.Context, tool string, consent func(prompt string) bool, out io.Writer) error {
	if Has(tool) {
		return nil
	}
	if out == nil {
		out = os.Stdout
	}
	ins := Installer(tool, Detect())
	fmt.Fprintf(out, "\n%s is not installed.\n%s\n", tool, ins.Guide)
	if ins.Auto && consent != nil && consent(fmt.Sprintf("Install %s now? keel will run: %s", tool, ins.Command)) {
		if err := run(ctx, ins.Command, out); err != nil {
			return fmt.Errorf("installing %s: %w", tool, err)
		}
		if !Has(tool) {
			return fmt.Errorf("%s still not found after install - you may need to restart your shell", tool)
		}
		fmt.Fprintf(out, "%s installed.\n", tool)
		return nil
	}
	if ins.URL != "" {
		_ = OpenURL(ins.URL)
	}
	return fmt.Errorf("%s is required - install it and re-run (see the page opened in your browser)", tool)
}

var run = func(ctx context.Context, command string, out io.Writer) error {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}
