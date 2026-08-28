package profile

import (
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())

	// no file yet -> Default
	p, err := Load()
	if err != nil {
		t.Fatalf("load default: %v", err)
	}
	if p.Get("", "database") != "postgres" {
		t.Fatalf("default database = %q, want postgres", p.Get("", "database"))
	}

	p.Defaults["framework"] = "magento"
	p.Git = Git{Name: "DC", Email: "dc@example.com"}
	if err := p.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Defaults["framework"] != "magento" {
		t.Fatalf("framework = %q, want magento", got.Defaults["framework"])
	}
	if got.Git.Name != "DC" {
		t.Fatalf("git name = %q", got.Git.Name)
	}
}

func TestPerLanguageOverride(t *testing.T) {
	p := Default()
	p.Overrides["php"] = map[string]string{"database": "mariadb"}
	if got := p.Get("php", "database"); got != "mariadb" {
		t.Fatalf("php db override = %q, want mariadb", got)
	}
	if got := p.Get("python", "database"); got != "postgres" {
		t.Fatalf("python db (no override) = %q, want postgres default", got)
	}
}
