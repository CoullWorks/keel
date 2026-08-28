package plugins

import (
	"os"
	"testing"
)

// TestMain isolates the config dir AND the home dir for the whole package. These
// tests exercise the exact set of plugins this build registers, so a plugin the
// developer has cloned under their real home — which plugin discovery now walks
// by default — must never leak in and fail them. A green build cannot depend on
// the machine's state. A test that needs its own config or home still overrides
// this with t.Setenv.
func TestMain(m *testing.M) {
	cfg, err := os.MkdirTemp("", "keel-plugins-cfg")
	if err != nil {
		panic(err)
	}
	home, err := os.MkdirTemp("", "keel-plugins-home")
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
