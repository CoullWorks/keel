package studio

import (
	"os"
	"testing"
)

// TestMain isolates the config dir AND the home dir for the whole package. The
// studio builds the catalogue and lists plugins and packs, and both are now
// discovered from anywhere under the user's home — so a plugin or pack the
// developer keeps in their real home must never leak into these tests and make
// their assertions depend on the machine's state. Isolating HOME to an empty dir
// is what keeps endpoints like the plugin-state and pack listings deterministic.
// A test that needs its own config or home still overrides these with t.Setenv.
func TestMain(m *testing.M) {
	cfg, err := os.MkdirTemp("", "keel-studio-cfg")
	if err != nil {
		panic(err)
	}
	home, err := os.MkdirTemp("", "keel-studio-home")
	if err != nil {
		panic(err)
	}
	os.Setenv("KEEL_CONFIG_DIR", cfg)
	os.Setenv("HOME", home)
	code := m.Run()
	os.RemoveAll(cfg)
	os.RemoveAll(home)
	os.Exit(code)
}
