package brand

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config directory resolution mirrors internal/profile exactly (KEEL_CONFIG_DIR
// then XDG_CONFIG_HOME then ~/.config/keel), so the global brand default lives
// next to profile.yaml as the Fitness Round-2 brand item requires. It is
// duplicated rather than imported to keep the brand package free of a profile
// dependency (a little copying over a little dependency) — the two are read at
// different layers and should not couple.

// dir is the keel config directory (honours KEEL_CONFIG_DIR then XDG_CONFIG_HOME).
func dir() string {
	if x := os.Getenv("KEEL_CONFIG_DIR"); x != "" {
		return x
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "keel")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "keel")
}

// GlobalPath is the global brand default file location, next to profile.yaml.
func GlobalPath() string { return filepath.Join(dir(), "brand.yaml") }

// GlobalExists reports whether a global brand default has been saved.
func GlobalExists() bool {
	_, err := os.Stat(GlobalPath())
	return err == nil
}

// LoadGlobal reads the global brand default from ~/.config/keel/brand.yaml,
// returning (tokens, true) when one exists and (zero, false) when none has been
// set. A missing file is not an error — it means "no global default yet, fall
// back to the kit's own colours"; only a genuinely unreadable/corrupt file
// errors, since that is a real problem the user should see.
func LoadGlobal() (BrandTokens, bool, error) {
	b, err := os.ReadFile(GlobalPath())
	if err != nil {
		if os.IsNotExist(err) {
			return BrandTokens{}, false, nil
		}
		return BrandTokens{}, false, err
	}
	var t BrandTokens
	if err := yaml.Unmarshal(b, &t); err != nil {
		return BrandTokens{}, false, err
	}
	return t, true, nil
}

// SaveGlobal persists tokens as the global brand default, creating the config
// dir if needed. `keel brand set` calls this.
func SaveGlobal(t BrandTokens) error {
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		return err
	}
	b, err := yaml.Marshal(t)
	if err != nil {
		return err
	}
	return os.WriteFile(GlobalPath(), b, 0o644)
}
