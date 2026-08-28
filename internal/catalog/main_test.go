package catalog

import (
	"os"
	"testing"
)

// TestMain isolates the config dir AND the home directory for the whole package.
// These tests validate the built-in catalogue, so a plugin or pack the developer
// has installed in their real ~/.config/keel — or a pack cloned anywhere under
// their real home, which the catalog now discovers the same way plugins are — must
// never leak in and fail them: a green build cannot depend on the machine's state.
// Isolating HOME to an empty dir is what makes the built-in catalogue snapshot
// deterministic now that home is a discovery input. KEEL_UID/KEEL_GID are pinned
// for the same reason: the effective plan bakes {{uid}}/{{gid}} from the invoking
// user, so without pinning, the snapshot would differ between a dev's machine and
// a CI runner with a different uid. A test that needs its own config or home still
// overrides these with t.Setenv.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "keel-catalog-test")
	if err != nil {
		panic(err)
	}
	home, err := os.MkdirTemp("", "keel-catalog-home")
	if err != nil {
		panic(err)
	}
	os.Setenv("KEEL_CONFIG_DIR", tmp)
	os.Setenv("HOME", home)
	os.Setenv("KEEL_UID", "1000")
	os.Setenv("KEEL_GID", "1000")
	code := m.Run()
	os.RemoveAll(tmp)
	os.RemoveAll(home)
	os.Exit(code)
}
