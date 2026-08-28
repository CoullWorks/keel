package profile

import (
	"os"
	"path/filepath"
	"testing"
)

// clearConfigEnv unsets every env var Dir() consults so each test controls it.
func clearConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv("KEEL_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	if err := os.Unsetenv("KEEL_CONFIG_DIR"); err != nil {
		t.Fatalf("unset KEEL_CONFIG_DIR: %v", err)
	}
	if err := os.Unsetenv("XDG_CONFIG_HOME"); err != nil {
		t.Fatalf("unset XDG_CONFIG_HOME: %v", err)
	}
}

func TestDirKeelConfigDirWins(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	t.Setenv("KEEL_CONFIG_DIR", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg")) // should be ignored
	if got := Dir(); got != dir {
		t.Fatalf("Dir() = %q, want %q (KEEL_CONFIG_DIR wins)", got, dir)
	}
}

func TestDirXDGConfigHome(t *testing.T) {
	clearConfigEnv(t)
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	want := filepath.Join(xdg, "keel")
	if got := Dir(); got != want {
		t.Fatalf("Dir() = %q, want %q", got, want)
	}
}

func TestDirHomeFallback(t *testing.T) {
	clearConfigEnv(t)
	home := t.TempDir()
	// os.UserHomeDir reads $HOME on unix.
	t.Setenv("HOME", home)
	want := filepath.Join(home, ".config", "keel")
	if got := Dir(); got != want {
		t.Fatalf("Dir() = %q, want %q", got, want)
	}
}

func TestPath(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	t.Setenv("KEEL_CONFIG_DIR", dir)
	if got := Path(); got != filepath.Join(dir, "profile.yaml") {
		t.Fatalf("Path() = %q", got)
	}
}

func TestExists(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	t.Setenv("KEEL_CONFIG_DIR", dir)

	if Exists() {
		t.Fatal("Exists() = true before any profile is saved")
	}
	if err := Default().Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	if !Exists() {
		t.Fatal("Exists() = false after save")
	}
}

func TestLoadMissingReturnsDefault(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())

	p, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// A fresh Default profile.
	if p.Get("", "framework") != "laravel" {
		t.Fatalf("default framework = %q, want laravel", p.Get("", "framework"))
	}
	if p.Get("", "env") != "ddev" {
		t.Fatalf("default env = %q, want ddev", p.Get("", "env"))
	}
}

func TestLoadNilMapsInitialised(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	t.Setenv("KEEL_CONFIG_DIR", dir)
	// A YAML file with no defaults/overrides keys -> both maps end up nil after
	// unmarshal and Load must initialise them so Get/Set don't panic.
	writeProfile(t, dir, "git:\n  name: DC\n  email: dc@example.com\n")

	p, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Defaults == nil {
		t.Fatal("Defaults map is nil after Load")
	}
	if p.Overrides == nil {
		t.Fatal("Overrides map is nil after Load")
	}
	if p.Git.Name != "DC" {
		t.Fatalf("git name = %q, want DC", p.Git.Name)
	}
	// Writing into the initialised maps must not panic.
	p.Defaults["framework"] = "symfony"
	p.Overrides["php"] = map[string]string{"database": "mysql"}
}

func TestLoadMalformedYAML(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	t.Setenv("KEEL_CONFIG_DIR", dir)
	// defaults declared as a scalar where a map is expected -> unmarshal error.
	writeProfile(t, dir, "defaults: 42\n")

	if _, err := Load(); err == nil {
		t.Fatal("want error loading malformed profile YAML")
	}
}

func TestSaveLoadOverridesRoundTrip(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())

	p := Default()
	p.Overrides["php"] = map[string]string{"database": "mariadb", "env": "sail"}
	p.Defaults["editor"] = "nvim"
	if err := p.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Get("php", "database") != "mariadb" {
		t.Fatalf("php database override lost: %q", got.Get("php", "database"))
	}
	if got.Get("php", "env") != "sail" {
		t.Fatalf("php env override lost: %q", got.Get("php", "env"))
	}
	// A key not overridden for php falls back to the global default.
	if got.Get("php", "editor") != "nvim" {
		t.Fatalf("php editor should fall back to default: %q", got.Get("php", "editor"))
	}
	// A language with no overrides always sees the global default.
	if got.Get("python", "database") != "postgres" {
		t.Fatalf("python database = %q, want postgres", got.Get("python", "database"))
	}
}

func TestSaveCreatesDir(t *testing.T) {
	clearConfigEnv(t)
	// Point at a nested dir that does not exist yet — Save must MkdirAll it.
	base := t.TempDir()
	nested := filepath.Join(base, "deep", "nested", "cfg")
	t.Setenv("KEEL_CONFIG_DIR", nested)

	if err := Default().Save(); err != nil {
		t.Fatalf("save into non-existent dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(nested, "profile.yaml")); err != nil {
		t.Fatalf("profile.yaml not written: %v", err)
	}
}

func writeProfile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "profile.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}
}
