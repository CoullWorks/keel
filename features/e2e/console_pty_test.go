package e2e

import (
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// The console is a full-screen terminal program, and nothing had ever run it as
// one. Every console test until now called Update and View directly, which
// proves the state machine and proves nothing about the program.
//
// These drive the real binary on a real pseudo-terminal. What they are for is
// the class of failure that only exists there: a build or a picker started from
// inside the console runs a second bubbletea program while the first is
// suspended, and if that goes wrong the session is gone and no unit test can see
// it.

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b[()][AB0]|\x1b[=>]`)

// screen is what a person would see: escapes stripped, carriage returns gone.
func screen(raw string) string {
	return strings.ReplaceAll(ansiRe.ReplaceAllString(raw, ""), "\r", "")
}

// Timings here are generous on purpose. bubbletea asks the terminal for its
// capabilities at start-up (an OSC background-colour query among them) and
// waits for the reply; a bare pseudo-terminal has no emulator to answer, so it
// sits out that timeout before it reads a key. Measured at four to six seconds
// here, against 81ms for the console's own start-up, so this is the harness
// paying the terminal's bill and not keel being slow.
const settle = 6 * time.Second

// key is one step of a script: wait, then type.
type key struct {
	wait time.Duration
	send string
}

// drive runs bin on a pseudo-terminal, plays the script, and reports everything
// the program drew plus whether it was still running at the end.
func drive(t *testing.T, bin string, dir string, script []key, args ...string) (out string, exited bool) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "KEEL_CONFIG_DIR="+t.TempDir())

	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 120})
	if err != nil {
		t.Skipf("no pseudo-terminal available here: %v", err)
	}
	defer func() { _ = f.Close() }()

	var sb strings.Builder
	read := make(chan struct{})
	go func() { _, _ = io.Copy(&sb, f); close(read) }()

	for _, k := range script {
		time.Sleep(k.wait)
		if k.send != "" {
			if _, err := f.WriteString(k.send); err != nil {
				break
			}
		}
	}

	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	select {
	case <-wait:
		exited = true
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-wait
	}
	<-read
	return screen(sb.String()), exited
}

// The console opens, draws itself, and quits on q.
func TestConsoleRunsOnARealTerminal(t *testing.T) {
	bin, err := buildKeel()
	if err != nil {
		t.Fatalf("build keel: %v", err)
	}
	out, exited := drive(t, bin, t.TempDir(), []key{
		{wait: settle},
		{wait: 700 * time.Millisecond, send: "j"},
		{wait: 700 * time.Millisecond, send: "j"},
		{wait: 700 * time.Millisecond, send: "q"},
		{wait: 1500 * time.Millisecond, send: "q"},
		{wait: 1500 * time.Millisecond},
	}, "console")

	for _, want := range []string{"keel", "Projects", "Plugins", "Settings"} {
		if !strings.Contains(out, want) {
			t.Errorf("the console never drew %q:\n%s", want, tail(out))
		}
	}
	if !exited {
		t.Errorf("q did not quit the console:\n%s", tail(out))
	}
}

// A screen opened from the console can be left again, and the console is still
// there afterwards.
func TestConsoleScreensAreNotDeadEnds(t *testing.T) {
	bin, err := buildKeel()
	if err != nil {
		t.Fatalf("build keel: %v", err)
	}
	out, exited := drive(t, bin, t.TempDir(), []key{
		{wait: settle},
		// Into Plugins, look at it, back out, and on to another area.
		{wait: 600 * time.Millisecond, send: "7"},
		{wait: 600 * time.Millisecond, send: "\r"},
		{wait: 900 * time.Millisecond, send: "\x1b"}, // esc
		{wait: 600 * time.Millisecond, send: "1"},
		{wait: 600 * time.Millisecond, send: "\r"},
		{wait: 900 * time.Millisecond, send: "\x1b"},
		{wait: 600 * time.Millisecond, send: "q"},
		{wait: 1500 * time.Millisecond, send: "q"},
		{wait: 1500 * time.Millisecond},
	}, "console")

	if !strings.Contains(out, "built in") && !strings.Contains(out, "Plugins") {
		t.Errorf("the Plugins screen never opened:\n%s", tail(out))
	}
	if !exited {
		t.Error("the console did not quit after opening and leaving screens")
	}
}

// Running something from inside the console does not end the session.
//
// This is the one that cannot be tested any other way. The console suspends
// itself, the action takes the real terminal, and the console has to come back:
// it used to quit so `keel console` could run the action on the way out, so
// picking anything at all dropped you at a shell prompt.
func TestConsoleSurvivesRunningAnAction(t *testing.T) {
	bin, err := buildKeel()
	if err != nil {
		t.Fatalf("build keel: %v", err)
	}
	dir := t.TempDir()
	// Run & Logs offers doctor, which needs no project and finishes quickly.
	out, exited := drive(t, bin, dir, []key{
		{wait: settle},
		{wait: 600 * time.Millisecond, send: "5"},  // Run & Logs
		{wait: 600 * time.Millisecond, send: "\r"}, // open it
		{wait: 900 * time.Millisecond, send: "\r"}, // choose the project, or the first task
		{wait: 900 * time.Millisecond, send: "\r"},
		// The action holds the terminal and waits for Enter before handing it
		// back, so this is the "press enter to return to keel" prompt.
		{wait: 6 * time.Second, send: "\r"},
		{wait: 2 * time.Second, send: "q"},
		{wait: 2 * time.Second, send: "q"},
		{wait: 2 * time.Second},
	}, "console")

	if !exited {
		t.Error("the console did not quit on q after running an action")
	}
	// Whatever it ran, it must have come back into the frame rather than
	// leaving the user at a shell.
	if !strings.Contains(out, "Support keel") && !strings.Contains(out, "quit") {
		t.Errorf("the console frame never came back after the action:\n%s", tail(out))
	}
}

// tail is the last part of a screen dump, for a readable failure.
func tail(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > 45 {
		lines = lines[len(lines)-45:]
	}
	return strings.Join(lines, "\n")
}
