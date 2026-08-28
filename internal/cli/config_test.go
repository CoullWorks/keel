package cli

import (
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/profile"
)

// TestConfigListPrintsProfile: bare `keel config` prints every known key and the
// profile path, reading the same store keel init writes.
func TestConfigListPrintsProfile(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "config")
	if err != nil {
		t.Fatalf("keel config: %v", err)
	}
	// Every settable key appears in the listing.
	for _, k := range configKeyNames() {
		if !strings.Contains(out, k) {
			t.Errorf("config list missing key %q\n%s", k, out)
		}
	}
	if !strings.Contains(out, "profile:") {
		t.Errorf("config list should print the profile path:\n%s", out)
	}
	// The empty projects_dir default reads as the human phrase, not a blank.
	if !strings.Contains(out, "(current directory)") {
		t.Errorf("empty projects_dir should render as (current directory):\n%s", out)
	}
}

// TestConfigSetThenGetRoundTrips: set writes through the profile store and get
// reads it back, and the value is actually persisted (a fresh Load sees it).
func TestConfigSetThenGetRoundTrips(t *testing.T) {
	isolate(t)
	if _, err := runRoot(t, "config", "set", "hosting", "fly"); err != nil {
		t.Fatalf("config set hosting fly: %v", err)
	}
	out, err := runRoot(t, "config", "get", "hosting")
	if err != nil {
		t.Fatalf("config get hosting: %v", err)
	}
	if strings.TrimSpace(out) != "fly" {
		t.Errorf("config get hosting = %q, want fly", strings.TrimSpace(out))
	}
	// Persisted to the same store the studio + init use.
	p, _ := profile.Load()
	if p.Defaults["hosting"] != "fly" {
		t.Errorf("profile hosting = %q, want fly", p.Defaults["hosting"])
	}
}

// TestConfigSetGitIdentity: name/email live in the Git block, not Defaults, and
// set routes to the right field.
func TestConfigSetGitIdentity(t *testing.T) {
	isolate(t)
	if _, err := runRoot(t, "config", "set", "name", "Ada Lovelace"); err != nil {
		t.Fatalf("config set name: %v", err)
	}
	p, _ := profile.Load()
	if p.Git.Name != "Ada Lovelace" {
		t.Errorf("git name = %q, want Ada Lovelace", p.Git.Name)
	}
}

// TestConfigRejectsUnknownKey: an unknown key is refused (get and set), so a
// typo never silently creates a setting nothing reads.
func TestConfigRejectsUnknownKey(t *testing.T) {
	isolate(t)
	for _, argv := range [][]string{
		{"config", "get", "nonsense"},
		{"config", "set", "nonsense", "x"},
	} {
		_, err := runRoot(t, argv...)
		if err == nil {
			t.Errorf("`keel %s` should reject an unknown key", strings.Join(argv, " "))
		} else if !strings.Contains(err.Error(), "unknown config key") {
			t.Errorf("`keel %s` error should name the problem: %v", strings.Join(argv, " "), err)
		}
	}
}

// TestConfigValidatesHosting: hosting is a closed set, so a value nothing maps to
// is refused with the valid options named.
func TestConfigValidatesHosting(t *testing.T) {
	isolate(t)
	_, err := runRoot(t, "config", "set", "hosting", "heroku")
	if err == nil {
		t.Fatal("config set hosting heroku should be refused (not a known target)")
	}
	if !strings.Contains(err.Error(), "hosting must be one of") {
		t.Errorf("error should list the valid hosting targets: %v", err)
	}
}

// TestConfigValidatesFramework: framework is validated against the recipe
// registry (the same source `keel new` uses), so `config set framework` cannot
// accept a framework `keel new` would reject. Setting a real framework and
// clearing it both succeed.
func TestConfigValidatesFramework(t *testing.T) {
	isolate(t)
	if _, err := runRoot(t, "config", "set", "framework", "notaframework"); err == nil {
		t.Fatal("config set framework notaframework should be refused (unknown framework)")
	}
	if _, err := runRoot(t, "config", "set", "framework", "laravel"); err != nil {
		t.Fatalf("config set framework laravel should succeed: %v", err)
	}
	// Clearing the default (empty value) is allowed — it opts out.
	if _, err := runRoot(t, "config", "set", "framework", ""); err != nil {
		t.Fatalf("config set framework '' (clear) should succeed: %v", err)
	}
}

// TestConfigGetUnset: get on a valid-but-unset key prints a blank line, not an
// error (an unset value is a legitimate state).
func TestConfigGetUnset(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "config", "get", "email")
	if err != nil {
		t.Fatalf("config get email: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("unset email should print blank, got %q", out)
	}
}

// TestConfigListSubcommand: `keel config list` is the explicit alias of the bare
// form.
func TestConfigListSubcommand(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "config", "list")
	if err != nil {
		t.Fatalf("config list: %v", err)
	}
	mustContain(t, out, "framework", "hosting")
}
