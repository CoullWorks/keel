package plugins

import (
	"testing"

	"github.com/coullworks/keel/internal/pluginstore"
)

// These tests exercise the compiled-in (built-in) machinery: the default-off
// seed, the disabled set, and the bundled-provenance labelling. keel ships zero
// built-ins, so each test registers an in-process fixture into `All` (and, where
// relevant, marks it bundled) and asserts the machinery treats it correctly. The
// machinery remains as the seam for a future first-party plugin; these tests keep
// it from rotting without keel actually bundling anything.

// A built-in the user disabled must not register — none of its commands, screens,
// steps or listeners appear — but it is still remembered so a listing can show it
// as present-but-off. Before this, a built-in bypassed the enabled check entirely
// and could not be switched off at all.
func TestDisabledBuiltinDoesNotRegisterButIsRemembered(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	regBuiltin(t, "demo", false, demoPlugin("demo"))

	if err := pluginstore.SetBuiltinEnabled("demo", false); err != nil {
		t.Fatal(err)
	}

	reg := Load()
	for _, p := range reg.Plugins() {
		if p.Meta().Name == "demo" {
			t.Fatal("a disabled built-in registered anyway")
		}
	}
	// demo contributes the `keel demo` command, which must be gone.
	if _, ok := reg.Command("demo"); ok {
		t.Error("a disabled built-in still mounted its command")
	}
	// It is remembered, so a listing can show it as off.
	var remembered bool
	for _, p := range reg.DisabledBuiltins() {
		if p.Meta().Name == "demo" {
			remembered = true
		}
	}
	if !remembered {
		t.Error("a disabled built-in was forgotten, so re-enabling it would be undiscoverable")
	}
}

// The disabled built-in shows up in the listing as present-but-disabled, not
// missing: dropping it would make it look uninstallable rather than off.
func TestRowsShowsADisabledBuiltinAsOff(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	regBuiltin(t, "demo", false, demoPlugin("demo"))
	if err := pluginstore.SetBuiltinEnabled("demo", false); err != nil {
		t.Fatal(err)
	}

	var found bool
	for _, r := range Rows(Load()) {
		if r.Name == "demo" {
			found = true
			if r.State != "disabled" {
				t.Errorf("state = %q, want disabled", r.State)
			}
			if !r.BuiltIn {
				t.Error("demo is a built-in but the row did not say so")
			}
		}
	}
	if !found {
		t.Error("the disabled built-in is not listed at all")
	}
}

// A default-off built-in does not register on a fresh keel: it does not fire its
// wizard step or event listener in every real project. It is still listed (as
// off) so enabling it is discoverable — while a default-on built-in registers.
func TestDefaultOffBuiltinIsOffButListed(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	regBuiltin(t, "demo-off", true, demoPlugin("demo-off"))
	regBuiltin(t, "demo-on", false, demoPlugin("demo-on"))

	reg := Load()
	for _, p := range reg.Plugins() {
		if p.Meta().Name == "demo-off" {
			t.Fatal("demo-off registered, but it is default-off")
		}
	}
	if _, ok := reg.Command("demo-off"); ok {
		t.Error("default-off built-in still mounted its command")
	}
	var listed bool
	for _, p := range reg.DisabledBuiltins() {
		if p.Meta().Name == "demo-off" {
			listed = true
		}
	}
	if !listed {
		t.Error("a default-off built-in is not listed as off, so enabling it would be undiscoverable")
	}
	// The default-off plugin is the only one off: a default-on built-in registers.
	if _, ok := reg.Command("demo-on"); !ok {
		t.Error("a default-on built-in should register; only default-off ones start off")
	}
}

// Enabling a default-off built-in sticks across reloads: the seed writes the
// initial off default only when there is no record, so an explicit enable is not
// reverted on the next Load. This is the bug the explicit on-record fixes.
func TestEnablingDefaultOffBuiltinPersists(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	regBuiltin(t, "demo-off", true, demoPlugin("demo-off"))

	// First load seeds demo-off off.
	if _, ok := Load().Command("demo-off"); ok {
		t.Fatal("demo-off should start off")
	}
	// The user turns it on.
	if err := pluginstore.SetBuiltinEnabled("demo-off", true); err != nil {
		t.Fatal(err)
	}
	// A later load must keep it on, not re-seed it off.
	if _, ok := Load().Command("demo-off"); !ok {
		t.Error("an enabled default-off built-in reverted to off on reload")
	}
}

// A re-enabled built-in registers again, proving the toggle is not one-way.
func TestReEnabledBuiltinRegisters(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	regBuiltin(t, "demo", false, demoPlugin("demo"))
	if err := pluginstore.SetBuiltinEnabled("demo", false); err != nil {
		t.Fatal(err)
	}
	if err := pluginstore.SetBuiltinEnabled("demo", true); err != nil {
		t.Fatal(err)
	}
	if _, ok := Load().Command("demo"); !ok {
		t.Error("a re-enabled built-in did not register its command")
	}
}

// A bundled built-in (a separate COULLWORKS tool) is labelled as bundled and
// carries its own author and homepage, so the listing does not imply keel wrote
// it. A first-party built-in is not bundled and carries no separate-tool
// provenance.
func TestRowsLabelsBundledToolsWithProvenance(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	// A separate tool shipped in the binary, and a first-party feature.
	regBuiltin(t, "tool", false, demoPlugin("tool"))
	markBundled(t, "tool")
	regBuiltin(t, "feature", false, demoPlugin("feature"))

	rows := map[string]struct {
		bundled  bool
		author   string
		homepage string
		builtIn  bool
	}{}
	for _, r := range Rows(Load()) {
		rows[r.Name] = struct {
			bundled  bool
			author   string
			homepage string
			builtIn  bool
		}{r.Bundled, r.Author, r.Homepage, r.BuiltIn}
	}

	got := rows["tool"]
	if !got.builtIn {
		t.Error("tool should be a built-in")
	}
	if !got.bundled {
		t.Error("tool is a separate COULLWORKS tool and should be marked bundled")
	}
	if got.author == "" {
		t.Error("tool should carry its author so the listing says whose tool it is")
	}
	if got.homepage == "" {
		t.Error("tool should carry its homepage")
	}

	// A first-party built-in is not a separate tool, so it must not be labelled
	// bundled or carry separate-tool provenance.
	ft := rows["feature"]
	if !ft.builtIn {
		t.Error("feature should be a built-in")
	}
	if ft.bundled {
		t.Error("feature is first-party, not a separate bundled tool")
	}
	if ft.author != "" || ft.homepage != "" {
		t.Errorf("a first-party built-in should carry no separate-tool provenance, got author=%q homepage=%q", ft.author, ft.homepage)
	}
}
