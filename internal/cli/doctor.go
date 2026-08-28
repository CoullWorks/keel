package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/coullworks/keel/internal/tui"
	"github.com/spf13/cobra"
)

func doctorCmd() *cobra.Command {
	var fix, asJSON bool
	c := &cobra.Command{
		Use: "doctor",
		Long: "Reports on the host tools keel shells out to, and what is wrong with them:\n" +
			"Docker installed but not running, Node lines available through nvm, and the\n" +
			"WSL2 gotchas that break DDEV (filesystem location, mkcert, hosts elevation).\n" +
			"It also flags a committed/un-gitignored .env in the current project.\n" +
			"\n" +
			"--fix installs what it safely can. --json emits structured results (for agents/CI).\n",
		Example: "  keel doctor\n" +
			"  keel doctor --json              # machine-readable results\n" +
			"  keel doctor --fix               # install what is missing\n",
		Args:  cobra.NoArgs,
		Short: "Check the host tools Keel shells out to (Docker running, Node versions via nvm, WSL+DDEV)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if asJSON {
				b, _ := json.MarshalIndent(doctorReport(), "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
				return nil
			}
			runDoctor(cmd.OutOrStdout(), fix)
			return nil
		},
	}
	c.Flags().BoolVar(&fix, "fix", false, "install what's missing: nvm + common Node versions, and mkcert on WSL")
	c.Flags().BoolVar(&asJSON, "json", false, "emit structured JSON results")
	return c
}

func runDoctor(w io.Writer, fix bool) {
	fmt.Fprint(w, tui.RenderDoctor(hostTools()))
	if s := secretsIssue(); s != "" {
		fmt.Fprintf(w, "\nSecrets\n  ✗ %s\n", s)
	}
	reportNode(w, fix)
	reportWSL(w, fix)
}

// hostTools probes each tool keel shells out to and returns its check result.
func hostTools() []tui.Tool {
	order := []string{"git", "docker", "ddev", "composer", "php", "node", "uv", "shopify", "wp"}
	tools := make([]tui.Tool, 0, len(order))
	for _, t := range order {
		if t == "docker" {
			tools = append(tools, checkDocker())
			continue
		}
		st := tui.ToolMissing
		if _, err := exec.LookPath(t); err == nil {
			st = tui.ToolOK
		}
		tools = append(tools, tui.Tool{Name: t, State: st})
	}
	return tools
}

// secretsIssue reports the single worst .env problem in the current dir, or "" if
// there is none. It reuses the git/gitignore helpers from secrets.go so the wording
// and detection stay in one place.
func secretsIssue() string {
	if _, err := os.Stat(".env"); os.IsNotExist(err) {
		return ""
	}
	if tracked(".env") {
		return ".env is tracked by git. Secrets may be committed. Add it to .gitignore and run: git rm --cached .env"
	}
	if !gitignored(".env") {
		return ".env is not in .gitignore"
	}
	return ""
}

// doctorReport is the structured shape emitted by `keel doctor --json`.
func doctorReport() map[string]any {
	stateName := map[tui.ToolState]string{tui.ToolOK: "ok", tui.ToolWarn: "warn", tui.ToolMissing: "missing"}
	tools := make([]map[string]any, 0)
	for _, t := range hostTools() {
		tools = append(tools, map[string]any{"name": t.Name, "state": stateName[t.State], "note": t.Note})
	}
	rep := map[string]any{
		"tools": tools,
		"wsl":   isWSL(),
	}
	secretsOK := true
	if s := secretsIssue(); s != "" {
		secretsOK = false
		rep["secrets_issue"] = s
	}
	rep["secrets_ok"] = secretsOK
	return rep
}

// checkDocker verifies the binary is present AND the daemon is reachable - the
// difference between "docker is installed" and "ddev will actually start".
func checkDocker() tui.Tool {
	if _, err := exec.LookPath("docker"); err != nil {
		return tui.Tool{Name: "docker", State: tui.ToolMissing}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").Output()
	if err != nil {
		return tui.Tool{Name: "docker", State: tui.ToolWarn, Note: "installed, daemon not running"}
	}
	return tui.Tool{Name: "docker", State: tui.ToolOK, Note: "v" + strings.TrimSpace(string(b))}
}

// DockerRunning is the gate the v0.2 executor calls before any env command.
func DockerRunning() bool {
	return checkDocker().State == tui.ToolOK
}

// nvmDir is where nvm installs (respects $NVM_DIR).
func nvmDir() string {
	if d := os.Getenv("NVM_DIR"); d != "" {
		return d
	}
	return os.Getenv("HOME") + "/.nvm"
}

func nvmInstalled() bool {
	_, err := os.Stat(nvmDir() + "/nvm.sh")
	return err == nil
}

// bashLogin runs a script with nvm's shell function loaded (nvm is not a binary).
// NVM_DIR is passed through the environment rather than pasted into the script:
// a directory name containing a quote would otherwise close the string and the
// rest of the path would be read as commands.
func bashLogin(script string) (string, error) {
	cmd := exec.Command("bash", "-lc", `[ -s "$NVM_DIR/nvm.sh" ] && . "$NVM_DIR/nvm.sh"; `+script)
	cmd.Env = append(os.Environ(), "NVM_DIR="+nvmDir())
	b, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(b)), err
}

// reportNode shows the active Node + nvm-managed versions, and with --fix installs
// nvm and common Node lines (20/22/24). Frameworks that pin a Node line (e.g.
// AdonisJS needs 24) rely on this being set up so keel can switch.
func reportNode(w io.Writer, fix bool) {
	fmt.Fprintln(w, "\nNode / nvm")
	nodeV := "-"
	if b, err := exec.Command("node", "-v").Output(); err == nil {
		nodeV = strings.TrimSpace(string(b))
	}
	fmt.Fprintf(w, "  active node   %s\n", nodeV)

	if fix && !nvmInstalled() {
		fmt.Fprintln(w, "  installing nvm …")
		if out, err := bashLogin(`curl -fsSL https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.3/install.sh | bash`); err != nil {
			fmt.Fprintf(w, "  ! nvm install failed: %s\n", firstLine(out))
		}
	}

	if !nvmInstalled() {
		fmt.Fprintln(w, "  nvm           not installed  →  keel doctor --fix   (or https://github.com/nvm-sh/nvm)")
		fmt.Fprintln(w, "  why           some frameworks pin a Node line (e.g. AdonisJS needs 24); nvm lets keel switch")
		return
	}

	if fix {
		fmt.Fprintln(w, "  ensuring common Node lines (20, 22, 24) …")
		if out, err := bashLogin(`nvm install 20 >/dev/null 2>&1; nvm install 22 >/dev/null 2>&1; nvm install 24 >/dev/null 2>&1; nvm alias default 22 >/dev/null 2>&1; echo ok`); err != nil {
			fmt.Fprintf(w, "  ! %s\n", firstLine(out))
		}
	}

	if ls, err := bashLogin(`nvm ls --no-colors 2>/dev/null | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | sort -uV | tr '\n' ' '`); err == nil && ls != "" {
		fmt.Fprintf(w, "  nvm versions  %s\n", ls)
	} else {
		fmt.Fprintln(w, "  nvm           installed (no versions yet, `keel doctor --fix` installs 20/22/24)")
	}
	fmt.Fprintln(w, "  switch        nvm use 24        (keel switches automatically for frameworks that pin a Node line)")
}

// reportWSL flags WSL and the DDEV-on-WSL gotchas (hosts-file elevation, /mnt/c
// slowness), and with --fix runs mkcert -install so DDEV HTTPS + hostnames work.
func reportWSL(w io.Writer, fix bool) {
	if !isWSL() {
		return
	}
	fmt.Fprintln(w, "\nWSL2 + DDEV")
	fmt.Fprintln(w, "  environment   WSL2 detected")

	if cwd, _ := os.Getwd(); strings.HasPrefix(cwd, "/mnt/") {
		fmt.Fprintln(w, "  ! location    you're on the Windows filesystem (/mnt/…). DDEV is slow and hosts-file")
		fmt.Fprintln(w, "                edits are painful here. Work in the WSL2 home (~/…) instead.")
	} else {
		fmt.Fprintln(w, "  location      WSL2 filesystem (good, fast + DDEV-friendly)")
	}

	if _, err := exec.LookPath("mkcert"); err != nil {
		fmt.Fprintln(w, "  ! mkcert      missing (DDEV needs it for trusted HTTPS)  →  keel doctor --fix")
	} else {
		fmt.Fprintln(w, "  mkcert        present")
		if fix {
			fmt.Fprintln(w, "  running mkcert -install …")
			if out, err := exec.Command("mkcert", "-install").CombinedOutput(); err != nil {
				fmt.Fprintf(w, "  ! %s\n", firstLine(string(out)))
			}
		}
	}
	fmt.Fprintln(w, "  hosts         first `ddev start` for a new project may prompt for Windows elevation to add")
	fmt.Fprintln(w, "                <project>.ddev.site to the hosts file. That's normal, approve it once.")
	fmt.Fprintln(w, "  headless/CI   can't approve the prompt? ddev config global --wsl2-no-windows-hosts-mgt")
}

// isWSL reports whether we're running under WSL (the kernel string carries it).
func isWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	b, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	v := strings.ToLower(string(b))
	return strings.Contains(v, "microsoft") || strings.Contains(v, "wsl")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
