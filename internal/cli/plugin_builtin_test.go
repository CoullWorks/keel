package cli

import (
	"testing"

	"github.com/coullworks/keel/internal/pluginstore"
	"github.com/coullworks/keel/internal/plugintest"
)

// `keel plugins disable <name>` and `enable <name>` must work for a discovered
// plugin and persist. keel ships zero built-ins, so a plugin is discovered on
// disk and toggled through the on-disk path; its enabled state lives in
// plugins.yaml, so we assert on the persisted record via pluginstore.Get.
func TestDisableEnableInstalledPersists(t *testing.T) {
	isolate(t)
	plugintest.Install(t, "demo") // discovered, enabled by default

	out, err := runRoot(t, "plugins", "disable", "demo")
	if err != nil {
		t.Fatalf("disable demo: %v", err)
	}
	mustContain(t, out, "disabled demo")
	if p, ok := pluginstore.Get("demo"); !ok || p.Enabled {
		t.Error("disabling an installed plugin did not persist to plugins.yaml")
	}

	out, err = runRoot(t, "plugins", "enable", "demo")
	if err != nil {
		t.Fatalf("enable demo: %v", err)
	}
	mustContain(t, out, "enabled demo")
	if p, ok := pluginstore.Get("demo"); !ok || !p.Enabled {
		t.Error("re-enabling an installed plugin did not persist to plugins.yaml")
	}
}

// Disabling an installed (on-disk) plugin still goes through the on-disk path,
// unchanged: an unknown name must error rather than silently succeed.
func TestDisableInstalledPluginStillUsesOnDiskPath(t *testing.T) {
	isolate(t)
	// A non-built-in name that is not installed must error, proving we did not
	// route it through the built-in (which would silently succeed).
	if _, err := runRoot(t, "plugins", "disable", "not-a-real-plugin"); err == nil {
		t.Error("disabling an unknown, non-built-in plugin should error")
	}
}
