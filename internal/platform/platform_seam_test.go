package platform

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// TestOpenerBranches covers the pure opener() for every host shape without
// launching anything.
func TestOpenerBranches(t *testing.T) {
	cases := []struct {
		name       string
		os         OS
		hasWslview bool
		wantCmd    string
	}{
		{"darwin", OS{GOOS: "darwin"}, false, "open"},
		{"windows", OS{GOOS: "windows"}, false, "cmd"},
		{"wsl-wslview", OS{GOOS: "linux", WSL: true}, true, "wslview"},
		{"wsl-explorer", OS{GOOS: "linux", WSL: true}, false, "explorer.exe"},
		{"linux", OS{GOOS: "linux"}, false, "xdg-open"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name, args := opener(c.os, c.hasWslview, "http://x")
			if name != c.wantCmd {
				t.Fatalf("opener cmd = %q, want %q", name, c.wantCmd)
			}
			if len(args) == 0 || args[len(args)-1] != "http://x" {
				t.Fatalf("opener args should end with the url: %v", args)
			}
		})
	}
}

// TestOpenURLUsesStarter covers OpenURL via a stubbed process starter (no browser).
func TestOpenURLUsesStarter(t *testing.T) {
	orig := startProcess
	defer func() { startProcess = orig }()
	var gotName string
	var gotArgs []string
	startProcess = func(name string, args ...string) error {
		gotName, gotArgs = name, args
		return nil
	}
	if err := OpenURL("http://keel.test"); err != nil {
		t.Fatalf("OpenURL: %v", err)
	}
	if gotName == "" || len(gotArgs) == 0 || gotArgs[len(gotArgs)-1] != "http://keel.test" {
		t.Fatalf("OpenURL should call the starter with the url: %q %v", gotName, gotArgs)
	}
}

func TestEnsureToolAutoInstallSucceeds(t *testing.T) {
	origLook, origRun := lookPath, run
	defer func() { lookPath, run = origLook, origRun }()
	installed := false
	lookPath = func(name string) (string, error) {
		if name == "ddev" && installed {
			return "/usr/local/bin/ddev", nil
		}
		return "", errors.New("not found")
	}
	run = func(context.Context, string, io.Writer) error { installed = true; return nil }
	var buf strings.Builder
	if err := EnsureTool(context.Background(), "ddev", func(string) bool { return true }, &buf); err != nil {
		t.Fatalf("auto-install should succeed: %v", err)
	}
	if !strings.Contains(buf.String(), "installed") {
		t.Fatalf("expected an installed confirmation, got %q", buf.String())
	}
}

func TestEnsureToolInstallFails(t *testing.T) {
	origLook, origRun := lookPath, run
	defer func() { lookPath, run = origLook, origRun }()
	lookPath = func(string) (string, error) { return "", errors.New("nf") }
	run = func(context.Context, string, io.Writer) error { return errors.New("boom") }
	err := EnsureTool(context.Background(), "ddev", func(string) bool { return true }, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "installing ddev") {
		t.Fatalf("install failure should propagate, got %v", err)
	}
}

func TestEnsureToolStillMissingAfterInstall(t *testing.T) {
	origLook, origRun := lookPath, run
	defer func() { lookPath, run = origLook, origRun }()
	lookPath = func(string) (string, error) { return "", errors.New("nf") } // never appears
	run = func(context.Context, string, io.Writer) error { return nil }
	err := EnsureTool(context.Background(), "ddev", func(string) bool { return true }, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "still not found") {
		t.Fatalf("expected still-not-found error, got %v", err)
	}
}

func TestEnsureToolConsentDeclinedOpensDocs(t *testing.T) {
	origLook, origStart := lookPath, startProcess
	defer func() { lookPath, startProcess = origLook, origStart }()
	lookPath = func(string) (string, error) { return "", errors.New("nf") }
	opened := ""
	startProcess = func(name string, args ...string) error {
		if len(args) > 0 {
			opened = args[len(args)-1]
		}
		return nil
	}
	err := EnsureTool(context.Background(), "ddev", func(string) bool { return false }, io.Discard)
	if err == nil {
		t.Fatal("declined install should return an error")
	}
	if opened == "" {
		t.Fatal("declining should open the docs URL via the starter")
	}
}
