package cli

import (
	"os"
	"testing"
)

// TestMain isolates the config dir AND the home dir so a plugin or pack the
// developer has installed in their real ~/.config/keel — or a pack cloned
// anywhere under their real home, which the catalogue now discovers the same way
// plugins are — does not leak into commands these tests drive. A green build
// cannot depend on the machine's state. The git identity is supplied via GIT_*
// env so the few cli tests that shell out to `git commit` still work without the
// real ~/.gitconfig the isolated home no longer carries. A test that needs its
// own config or home still overrides these with t.Setenv.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "keel-cli-test")
	if err != nil {
		panic(err)
	}
	home, err := os.MkdirTemp("", "keel-cli-home")
	if err != nil {
		panic(err)
	}
	os.Setenv("KEEL_CONFIG_DIR", tmp)
	os.Setenv("HOME", home)
	os.Setenv("GIT_AUTHOR_NAME", "keel-test")
	os.Setenv("GIT_AUTHOR_EMAIL", "test@keel.test")
	os.Setenv("GIT_COMMITTER_NAME", "keel-test")
	os.Setenv("GIT_COMMITTER_EMAIL", "test@keel.test")
	code := m.Run()
	os.RemoveAll(tmp)
	os.RemoveAll(home)
	os.Exit(code)
}
