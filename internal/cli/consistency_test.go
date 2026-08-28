package cli

import (
	"strings"
	"testing"
)

// Every namespace command (a parent that only groups subcommands) must reject an
// unknown subcommand with an error and a non-zero exit, the same as config /
// plugins / service already did. Before the fix, secrets / commerce / proxy /
// recipes printed help and exited 0, so a typo (or a script) silently "worked".
func TestUnknownSubcommandErrorsEverywhere(t *testing.T) {
	parents := []string{"secrets", "commerce", "proxy", "recipes", "config", "service", "plugins"}
	for _, p := range parents {
		t.Run(p, func(t *testing.T) {
			isolate(t)
			_, err := runRoot(t, p, "frobnicate-xyz")
			if err == nil {
				t.Fatalf("`keel %s frobnicate-xyz` should error on the unknown subcommand", p)
			}
		})
	}
}

// The bare namespace command (no subcommand) still prints help and exits 0 — the
// error is only for an unknown subcommand, not for asking "what can I do here?".
func TestBareNamespacePrintsHelp(t *testing.T) {
	for _, p := range []string{"secrets", "commerce", "proxy", "recipes"} {
		t.Run(p, func(t *testing.T) {
			isolate(t)
			out, err := runRoot(t, p)
			if err != nil {
				t.Fatalf("`keel %s` (bare) should print help and succeed, got: %v", p, err)
			}
			if !strings.Contains(out, "Usage:") {
				t.Errorf("`keel %s` (bare) should print usage:\n%s", p, out)
			}
		})
	}
}

// deploy --help must not advertise a --out flag that does not exist: the Example
// used to show `keel deploy compose --out deploy/`, which errors when run.
func TestDeployHelpHasNoOutFlag(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "deploy", "--help")
	if err != nil {
		t.Fatalf("deploy --help: %v", err)
	}
	if strings.Contains(out, "--out") {
		t.Errorf("deploy --help advertises a nonexistent --out flag:\n%s", out)
	}
}
