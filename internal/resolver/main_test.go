package resolver

import (
	"os"
	"testing"
)

// TestMain isolates the config dir AND the home dir so a plugin or pack the
// developer has installed in their real ~/.config/keel — or a pack cloned
// anywhere under their real home, which the catalogue now discovers the same way
// plugins are — does not leak into the catalogue these tests resolve against:
// resolution must be tested against the built-in set, not the machine's state. A
// test that needs its own config or home overrides these with t.Setenv.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "keel-resolver-test")
	if err != nil {
		panic(err)
	}
	home, err := os.MkdirTemp("", "keel-resolver-home")
	if err != nil {
		panic(err)
	}
	os.Setenv("KEEL_CONFIG_DIR", tmp)
	os.Setenv("HOME", home)
	code := m.Run()
	os.RemoveAll(tmp)
	os.RemoveAll(home)
	os.Exit(code)
}
