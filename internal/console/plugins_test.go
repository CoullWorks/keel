package console

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/pluginstore"
	"github.com/coullworks/keel/internal/plugintest"
)

// installFixture puts a plugin in an isolated config dir. The manifest carries
// every field the registry validates, so the plugin actually registers: an
// incomplete one lands in the listing as "not loaded" with the missing field as
// its reason, which is a different test.
func installFixture(t *testing.T, name string) {
	t.Helper()
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	src := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(src, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema: 1\nname: " + name + "\nversion: 1.0.0\n" +
		"author: fixtures\nlicense: MIT\ndescription: fixture\n"
	if err := os.WriteFile(filepath.Join(src, "config", "register.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := pluginstore.Install(context.Background(), src, ""); err != nil {
		t.Fatal(err)
	}
}

// openPlugins opens the Plugins screen and returns the model and its flow.
func openPlugins(t *testing.T) (model, *pluginFlow) {
	t.Helper()
	m := New()
	m.w, m.h = 120, 40
	// The fixture config dir has no profile, so New() opens in first-run setup.
	// These tests are about the Plugins area, not onboarding.
	m.setup, m.focus = false, 0
	m.nav = m.indexOf("plugins")
	if m.nav < 0 {
		t.Fatal("there is no Plugins area")
	}
	got, _ := m.Update(fkey("enter"))
	m = got.(model)
	pf, ok := m.flow.(*pluginFlow)
	if !ok {
		t.Fatalf("Plugins opened a %T", m.flow)
	}
	return m, pf
}

// row finds a listed plugin by name.
func (f *pluginFlow) row(name string) (int, bool) {
	for i, r := range f.rows {
		if r.Name == name {
			return i, true
		}
	}
	return 0, false
}

// The Plugins screen lists every plugin keel knows about — one discovered on
// KEEL_PLUGIN_PATH, one installed under the config directory, any compiled in —
// not only what lives in the managed directory.
//
// keel ships zero built-ins now: every plugin is its own repository, discovered
// wherever it is cloned. So a plugin found on the search path must appear here,
// listed as installed and enabled, and installing another has to be reachable
// from the screen itself. It used to read the managed directory alone, so a
// plugin sitting on the search path was invisible with no action offered.
func TestPluginsAreaListsInstalledPlugins(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	plugintest.Install(t, "demo") // discovered on KEEL_PLUGIN_PATH, enabled by default
	_, pf := openPlugins(t)

	i, ok := pf.row("demo")
	if !ok {
		t.Fatalf("the discovered plugin is not listed, got %d rows: %+v", len(pf.rows), pf.rows)
	}
	if r := pf.rows[i]; r.State != "enabled" || r.Where != "installed" || r.BuiltIn {
		t.Errorf("discovered plugin listed wrong: state=%q where=%q builtIn=%v", r.State, r.Where, r.BuiltIn)
	}
	v := pf.view(120)
	if !strings.Contains(v, "built in") {
		t.Errorf("the screen does not say how many are built in:\n%s", v)
	}
	// Installing has to be reachable from the screen itself.
	if !strings.Contains(v, "a install") {
		t.Errorf("no way to install a plugin from the Plugins screen:\n%s", v)
	}
}

// `a` opens the install prompt and Enter turns it into a real install action,
// rather than printing a command to go and type somewhere else.
func TestPluginsAreaInstalls(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	_, pf := openPlugins(t)

	if act, _ := pf.update(fkey("a")); act.Kind != "" {
		t.Fatalf("`a` should open the prompt, not act: %+v", act)
	}
	if !pf.adding {
		t.Fatal("`a` did not open the install prompt")
	}
	// An empty source is refused rather than run.
	if act, _ := pf.update(fkey("enter")); act.Kind != "" {
		t.Fatalf("an empty source should not install: %+v", act)
	}
	for _, r := range "./x" {
		pf.update(fkey(string(r)))
	}
	act, _ := pf.update(fkey("enter"))
	if act.Kind != "argv" || strings.Join(act.Argv, " ") != "plugins add ./x" {
		t.Fatalf("install action = %+v, want `plugins add ./x`", act)
	}
}

// An installed plugin can be turned off in place, and that persists to disk.
func TestPluginsAreaListsAndToggles(t *testing.T) {
	installFixture(t, "demo")
	m, pf := openPlugins(t)

	i, ok := pf.row("demo")
	if !ok {
		t.Fatalf("installed plugin not listed: %+v", pf.rows)
	}
	if !strings.Contains(pf.view(120), "demo") {
		t.Error("the plugin is not rendered")
	}
	pf.cur = i

	got, _ := m.Update(fkey(" "))
	m = got.(model)
	pf = m.flow.(*pluginFlow)
	if i, _ := pf.row("demo"); pf.rows[i].State != "disabled" {
		t.Errorf("space did not disable the plugin, state = %q", pf.rows[i].State)
	}
	if p, _ := pluginstore.Get("demo"); p.Enabled {
		t.Error("disabling in the console did not persist")
	}
}

// x deletes an installed plugin from disk.
func TestPluginsAreaRemoves(t *testing.T) {
	installFixture(t, "goaway")
	m, pf := openPlugins(t)

	i, ok := pf.row("goaway")
	if !ok {
		t.Fatal("the installed plugin is not listed")
	}
	pf.cur = i

	got, _ := m.Update(fkey("x"))
	m = got.(model)
	if pf := m.flow.(*pluginFlow); func() bool { _, ok := pf.row("goaway"); return ok }() {
		t.Error("x left the plugin listed")
	}
	if _, ok := pluginstore.Get("goaway"); ok {
		t.Error("x did not remove the plugin from disk")
	}
}
