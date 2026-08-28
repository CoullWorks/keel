package pluginstore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coullworks/keel/plugin"
)

// withConfigDir points the store at a temp dir so a test never touches the
// developer's real ~/.config/keel. It also isolates HOME to a separate empty
// temp dir and clears KEEL_PLUGIN_PATH, so the home-directory discovery walk
// (defaultRoots is now the user's home) sees a controlled, empty tree instead of
// the developer's real home full of plugin repos. os.UserHomeDir honours $HOME
// on Linux/macOS, so setting HOME redirects the walk. Config and home are kept
// separate dirs so the walk over home does not also traverse the managed
// plugins dir under the config dir and double-count.
func withConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("KEEL_CONFIG_DIR", dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KEEL_PLUGIN_PATH", "")
	// The List cache is package-level and outlives a single test; clear it so a
	// result cached under a previous test's HOME cannot leak into this one.
	Invalidate()
	return dir
}

// writePlugin creates a minimal valid plugin source directory.
func writePlugin(t *testing.T, root, name, version string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema: 1\nname: " + name + "\nversion: " + version +
		"\ndescription: a test plugin\nauthor: t\nlicense: MIT\n"
	if err := os.WriteFile(filepath.Join(dir, "config", "register.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeNamedPlugin creates a plugin at an exact directory whose basename may
// differ from the manifest name (e.g. keel-plugin-foo holding manifest "foo"),
// the shape of an externalised plugin repo cloned under its own name.
func writeNamedPlugin(t *testing.T, dir, name, version string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema: 1\nname: " + name + "\nversion: " + version +
		"\ndescription: a test plugin\nauthor: t\nlicense: MIT\n"
	if err := os.WriteFile(filepath.Join(dir, "config", "register.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestInstallFromFolderThenList is the headline gap this package closes: before
// it, a plugin could only be a Go package compiled into keel, so "add a plugin
// by folder" had no answer at all.
func TestInstallFromFolderThenList(t *testing.T) {
	withConfigDir(t)
	src := writePlugin(t, t.TempDir(), "demo", "1.2.3")

	got, err := Install(context.Background(), src, "")
	if err != nil {
		t.Fatalf("install from folder: %v", err)
	}
	if got.Name != "demo" || got.Meta.Version != "1.2.3" {
		t.Fatalf("wrong metadata: %+v", got)
	}

	all, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 installed plugin, got %d", len(all))
	}
	if !all[0].Enabled {
		t.Error("a freshly installed plugin should be enabled")
	}
	if all[0].Problem != "" {
		t.Errorf("valid plugin reported a problem: %s", all[0].Problem)
	}
}

// TestListFindsPluginsDroppedInByHand: discovery is a directory scan, not a
// registry, so copying a plugin into the plugins dir is enough. This is what
// "auto-find local plugins" has to mean.
func TestListFindsPluginsDroppedInByHand(t *testing.T) {
	withConfigDir(t)
	writePlugin(t, Dir(), "manual", "0.1.0")

	all, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Name != "manual" {
		t.Fatalf("a plugin copied into %s was not discovered: %+v", Dir(), all)
	}
}

// TestDiscoversPluginsUnderHome: a plugin cloned anywhere under the user's home
// directory is found with no KEEL_PLUGIN_PATH and no configuration — the "clone
// anywhere, keel finds it" default. keel ships zero built-ins, so this is how a
// freshly cloned plugin repo shows up without an install step or an env var.
func TestDiscoversPluginsUnderHome(t *testing.T) {
	withConfigDir(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	// home/www/keel/extensions/plugins/keel-plugin-foo/config/register.yaml —
	// several levels deep, to prove the bounded walk finds it, not just a shallow
	// root+1 scan.
	deep := filepath.Join(home, "www", "keel", "extensions", "plugins")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	writeNamedPlugin(t, filepath.Join(deep, "keel-plugin-foo"), "foo", "1.0.0")

	all, err := List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range all {
		if p.Name == "foo" {
			found = true
		}
	}
	if !found {
		t.Errorf("a plugin cloned deep under home should be discovered with no config, got %+v", all)
	}
}

// TestEnableDisableSurvivesReload: turning a plugin off must persist, and must
// not delete it.
func TestEnableDisableSurvivesReload(t *testing.T) {
	withConfigDir(t)
	src := writePlugin(t, t.TempDir(), "toggle", "1.0.0")
	if _, err := Install(context.Background(), src, ""); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("toggle", false); err != nil {
		t.Fatal(err)
	}
	p, ok := Get("toggle")
	if !ok {
		t.Fatal("disabling removed the plugin")
	}
	if p.Enabled {
		t.Error("disable did not persist")
	}
	if _, err := os.Stat(p.Dir); err != nil {
		t.Errorf("disable deleted the files: %v", err)
	}
	if err := SetEnabled("toggle", true); err != nil {
		t.Fatal(err)
	}
	if p, _ := Get("toggle"); !p.Enabled {
		t.Error("re-enable did not persist")
	}
}

// TestStateSticksWhenDirNameDiffersFromManifestName is the real-world case the
// managed-dir tests miss: a plugin repo cloned under its own name — keel-plugin-
// widget holding the manifest "widget", discovered on KEEL_PLUGIN_PATH. Its
// enabled / trusted / granted state is stored under the manifest name, so
// discovery must match the index by the manifest name, not the directory
// basename. Keying it by the directory name meant none of it stuck, and no such
// plugin — every externalised keel-plugin-* repo — could ever be trusted,
// disabled or granted a capability.
func TestStateSticksWhenDirNameDiffersFromManifestName(t *testing.T) {
	withConfigDir(t)
	root := t.TempDir()
	dir := filepath.Join(root, "keel-plugin-widget") // dir name != manifest name
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema: 1\nname: widget\nversion: 1.0.0\n" +
		"description: a test plugin\nauthor: t\nlicense: MIT\ncapabilities:\n  - exec\n"
	if err := os.WriteFile(filepath.Join(dir, "config", "register.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KEEL_PLUGIN_PATH", root)

	// Discovered under the manifest name, from the differently-named directory.
	p, ok := Get("widget")
	if !ok {
		t.Fatal(`a plugin at keel-plugin-widget/ with manifest name "widget" was not discovered as "widget"`)
	}
	if filepath.Base(p.Dir) != "keel-plugin-widget" {
		t.Fatalf("discovered dir = %q, want keel-plugin-widget", p.Dir)
	}

	// Trust must persist to the manifest-named record and read back.
	if err := SetTrusted("widget", true); err != nil {
		t.Fatal(err)
	}
	if got, _ := Get("widget"); !got.Trusted {
		t.Error("trust did not stick: a discovered plugin whose dir name differs from its manifest name could never be trusted")
	}

	// So must a granted capability.
	if err := SetCapabilityGranted("widget", plugin.CapExec, true); err != nil {
		t.Fatal(err)
	}
	if !CapabilityGranted("widget", plugin.CapExec) {
		t.Error("granted capability did not stick across the name mismatch")
	}

	// And so must disable.
	if err := SetEnabled("widget", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := Get("widget"); got.Enabled {
		t.Error("disable did not stick across the name mismatch")
	}
}

func TestRemoveDeletesEverything(t *testing.T) {
	withConfigDir(t)
	src := writePlugin(t, t.TempDir(), "gone", "1.0.0")
	rec, err := Install(context.Background(), src, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := Remove("gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rec.Dir); !os.IsNotExist(err) {
		t.Error("remove left the directory behind")
	}
	if _, ok := Get("gone"); ok {
		t.Error("remove left the plugin in the index")
	}
}

// TestInstallRejectsNonPlugin: a source without a manifest must fail loudly and
// leave nothing behind, rather than installing a directory that later turns out
// to contribute nothing.
func TestInstallRejectsNonPlugin(t *testing.T) {
	withConfigDir(t)
	empty := t.TempDir()
	if _, err := Install(context.Background(), empty, ""); err == nil {
		t.Fatal("installing a directory with no register.yaml should fail")
	}
	all, _ := List()
	if len(all) != 0 {
		t.Errorf("a failed install left %d plugin(s) behind", len(all))
	}
}

// TestValidateRefusesEscapingName: the name becomes a directory under the keel
// config dir. A manifest that names "../../evil" must be refused rather than
// given a path outside the store.
func TestValidateRefusesEscapingName(t *testing.T) {
	for _, name := range []string{"../evil", "a/b", `a\b`, ".", ".."} {
		src := t.TempDir()
		if err := os.MkdirAll(filepath.Join(src, "config"), 0o755); err != nil {
			t.Fatal(err)
		}
		// Single-quoted YAML: in double quotes YAML would read the backslash in
		// `a\b` as an escape (\b is a backspace), so the validator would be
		// handed a different string than the one under test.
		manifest := "schema: 1\nname: '" + name + "'\nversion: 1.0.0\ndescription: d\n"
		if err := os.WriteFile(filepath.Join(src, "config", "register.yaml"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadMeta(src); err == nil {
			t.Errorf("name %q was accepted; it must be refused", name)
		}
	}
}

// TestUnsupportedSchemaIsRefused: a plugin written for a future keel must not
// half-load.
func TestUnsupportedSchemaIsRefused(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema: 99\nname: future\nversion: 1.0.0\ndescription: d\n"
	if err := os.WriteFile(filepath.Join(src, "config", "register.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadMeta(src)
	if err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("expected a schema error, got %v", err)
	}
}

// TestBrokenPluginIsReportedNotHidden: a directory that is not a valid plugin
// still appears in the listing, carrying the reason. Dropping it silently is how
// someone concludes their plugin "does nothing".
func TestBrokenPluginIsReportedNotHidden(t *testing.T) {
	withConfigDir(t)
	broken := filepath.Join(Dir(), "broken")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	all, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("expected the broken plugin to be listed, got %d entries", len(all))
	}
	if all[0].Problem == "" {
		t.Error("a directory with no manifest was listed as healthy")
	}
}

// TestWalkSkipsHeavyAndCacheTrees: the home walk must not descend into
// dependency trees (node_modules), dotdirs (.git), or the Go module cache — both
// because they hold no keel plugins and because the module cache is multi-GB and
// walking it makes discovery unusably slow. A register.yaml planted inside each
// must NOT be found.
func TestWalkSkipsHeavyAndCacheTrees(t *testing.T) {
	withConfigDir(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	// A plugin hidden inside each tree that discovery must skip.
	writeNamedPlugin(t, filepath.Join(home, "proj", "node_modules", "keel-plugin-nm"), "nm", "1.0.0")
	writeNamedPlugin(t, filepath.Join(home, "proj", ".git", "keel-plugin-git"), "git", "1.0.0")
	// A fake Go module cache in the ~/go/pkg/mod layout, matched by the
	// name/parent heuristic (pkg whose parent is go) with no reliance on the real
	// GOMODCACHE.
	writeNamedPlugin(t, filepath.Join(home, "go", "pkg", "mod", "keel-plugin-mod"), "modcache", "1.0.0")

	// A real plugin outside those trees, to prove the walk still works.
	writeNamedPlugin(t, filepath.Join(home, "proj", "keel-plugin-real"), "real", "1.0.0")

	all, err := List()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, p := range all {
		names[p.Name] = true
	}
	if !names["real"] {
		t.Error("a plugin outside the skipped trees should be discovered")
	}
	for _, bad := range []string{"nm", "git", "modcache"} {
		if names[bad] {
			t.Errorf("discovery descended into a skipped tree and found %q", bad)
		}
	}
}

// TestTrustSticksAfterList is the cache-invalidation regression guard: List is
// cached for a short TTL, so a mutation that did not invalidate the cache would
// appear not to take effect until the TTL elapsed. After SetTrusted, a fresh
// List (via Get) must reflect the new trust immediately.
func TestTrustSticksAfterList(t *testing.T) {
	withConfigDir(t)
	writeNamedPlugin(t, filepath.Join(Dir(), "keel-plugin-trusty"), "trusty", "1.0.0")

	// Prime the cache: the plugin is discovered and, at first, untrusted.
	p, ok := Get("trusty")
	if !ok {
		t.Fatal("plugin was not discovered")
	}
	if p.Trusted {
		t.Fatal("a freshly discovered plugin should not be trusted")
	}

	if err := SetTrusted("trusty", true); err != nil {
		t.Fatal(err)
	}

	// A subsequent List must reflect it now, not after the TTL — proving the
	// mutation invalidated the cache.
	got, ok := Get("trusty")
	if !ok {
		t.Fatal("plugin disappeared after SetTrusted")
	}
	if !got.Trusted {
		t.Error("trust did not stick on the next List: the cache was not invalidated")
	}
}

// TestTrustDoesNotTransferAcrossPaths is the security property behind trust-by-
// path: trusting a plugin binds the trust to the directory that was trusted, so a
// same-named plugin discovered at a DIFFERENT path (a moved copy, or a malicious
// keel-plugin-foo planted in the user's tree) is NOT trusted until the user
// trusts THAT copy. Without this, discovery-by-name would let a planted plugin
// inherit the trust granted to a different one.
func TestTrustDoesNotTransferAcrossPaths(t *testing.T) {
	withConfigDir(t)

	// The original foo, discovered on KEEL_PLUGIN_PATH, trusted by the user.
	rootA := t.TempDir()
	writeNamedPlugin(t, filepath.Join(rootA, "keel-plugin-foo"), "foo", "1.0.0")
	t.Setenv("KEEL_PLUGIN_PATH", rootA)
	Invalidate()
	if err := SetTrusted("foo", true); err != nil {
		t.Fatalf("trust foo at rootA: %v", err)
	}
	if p, ok := Get("foo"); !ok || !p.Trusted {
		t.Fatalf("foo at its trusted path should be trusted: %+v", p)
	}

	// A DIFFERENT foo now shadows it from another path (the trusted copy is gone).
	rootB := t.TempDir()
	writeNamedPlugin(t, filepath.Join(rootB, "keel-plugin-foo"), "foo", "1.0.0")
	t.Setenv("KEEL_PLUGIN_PATH", rootB)
	Invalidate()

	p, ok := Get("foo")
	if !ok {
		t.Fatal("foo should still be discovered at rootB")
	}
	if p.Trusted {
		t.Fatal("trust MUST NOT transfer to a same-named plugin at a different path")
	}
	if p.TrustNote == "" {
		t.Error("a shadowed plugin should carry a TrustNote explaining why it is untrusted")
	}

	// Trusting THIS copy makes it trusted, and re-checking confirms it sticks.
	if err := SetTrusted("foo", true); err != nil {
		t.Fatalf("trust foo at rootB: %v", err)
	}
	if p, _ := Get("foo"); !p.Trusted {
		t.Fatal("after trusting the rootB copy, it should be trusted")
	}
}
