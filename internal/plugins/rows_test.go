package plugins

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/pluginstore"
)

// install writes a plugin manifest and installs it into an isolated config dir.
func install(t *testing.T, name, manifest string) {
	t.Helper()
	src := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(src, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "config", "register.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := pluginstore.Install(context.Background(), src, ""); err != nil {
		t.Fatal(err)
	}
}

// Rows lists the plugins compiled into the build, not only what is installed.
// Both front ends read this, so a listing that skipped built-ins made a stock
// keel look like it had no plugins at all.
func TestRowsIncludesBuiltIns(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", t.TempDir())

	rows := Rows(Load())
	var built int
	for _, r := range rows {
		if r.BuiltIn {
			built++
		}
	}
	if built != len(All) {
		t.Errorf("listed %d built-in plugins, this build has %d", built, len(All))
	}
	// Built-ins sort first: they are always there, so they are the stable part
	// of the list.
	if len(rows) > 0 && !rows[0].BuiltIn && len(All) > 0 {
		t.Errorf("first row is not a built-in: %+v", rows[0])
	}
}

// A plugin that is enabled on disk but did not register is reported as not
// loaded, with the registry's reason.
//
// It used to be listed as "disabled" with no reason at all: two lies at once,
// because the user had not disabled it and keel knew exactly what was wrong
// with it. The registry's problems were printed in a separate list where
// nothing connected them to the row.
func TestRowsExplainsAPluginThatDidNotLoad(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	// No license, which validate rejects.
	install(t, "broken", "schema: 1\nname: broken\nversion: 1.0.0\nauthor: a\ndescription: d\n")

	var got string
	var state string
	for _, r := range Rows(Load()) {
		if r.Name == "broken" {
			state, got = r.State, r.Problem
		}
	}
	if state != "not loaded" {
		t.Errorf("state = %q, want %q: the user did not switch this off", state, "not loaded")
	}
	if !strings.Contains(got, "license") {
		t.Errorf("problem = %q, want the registry's reason (no license)", got)
	}
}

// A plugin the user switched off is reported as disabled, not as broken.
func TestRowsReportsADisabledPlugin(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	install(t, "off", "schema: 1\nname: off\nversion: 1.0.0\nauthor: a\nlicense: MIT\ndescription: d\n")
	if err := pluginstore.SetEnabled("off", false); err != nil {
		t.Fatal(err)
	}

	for _, r := range Rows(Load()) {
		if r.Name != "off" {
			continue
		}
		if r.State != "disabled" {
			t.Errorf("state = %q, want disabled", r.State)
		}
		if r.Problem != "" {
			t.Errorf("a plugin switched off on purpose has no problem, got %q", r.Problem)
		}
		return
	}
	t.Error("the disabled plugin is not listed at all")
}

// loadFailures attributes a problem to the plugin it names, and leaves alone
// anything that is not in "<name>: <reason>" form.
func TestLoadFailures(t *testing.T) {
	r := &Registry{problems: []error{
		errors.New("demo: no license"),
		errors.New("something went wrong with no plugin named"),
	}}
	got := loadFailures(r)
	if got["demo"] != "no license" {
		t.Errorf("loadFailures[demo] = %q", got["demo"])
	}
	if len(got) != 1 {
		t.Errorf("unattributed problems should be skipped, got %v", got)
	}
	if len(loadFailures(nil)) != 0 {
		t.Error("a nil registry should yield no failures")
	}
}

// Discover finds keel-<name> executables on PATH, de-dupes across entries and
// ignores directories.
func TestDiscover(t *testing.T) {
	d1, d2 := t.TempDir(), t.TempDir()
	for _, f := range []string{filepath.Join(d1, "keel-alpha"), filepath.Join(d2, "keel-alpha"), filepath.Join(d2, "keel-beta")} {
		if err := os.WriteFile(f, []byte("#\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(d2, "keel-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", d1+string(os.PathListSeparator)+d2)

	got := Discover()
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Errorf("Discover() = %v, want [alpha beta]", got)
	}
}
