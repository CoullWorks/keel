package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMonorepoManifestRoundTrip asserts a monorepo-root manifest (kind +
// members + shared services) survives a write/read cycle intact, and that a
// normal single-app manifest still round-trips without the new fields leaking.
func TestMonorepoManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := &Manifest{
		Kind: KindMonorepo,
		Members: []Member{
			{Path: "apps/web", Framework: "nextjs"},
			{Path: "apps/mobile", Framework: "expo"},
			{Path: "packages/ui", Framework: "lib"},
		},
		Services: &Services{
			EnvFile: ".env.local",
			DB: &DBService{
				Engine:   "postgres",
				Provider: "supabase",
				Source:   ".env.local:SUPABASE_DB_URL",
				Schema:   "myfamilyinfo",
			},
		},
	}
	if err := WriteManifestFile(dir, in); err != nil {
		t.Fatal(err)
	}
	got, err := ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindMonorepo {
		t.Errorf("kind = %q, want monorepo", got.Kind)
	}
	if len(got.Members) != 3 || got.Members[1].Framework != "expo" {
		t.Errorf("members round-trip wrong: %+v", got.Members)
	}
	if got.Services == nil || got.Services.DB == nil {
		t.Fatalf("services lost in round-trip: %+v", got.Services)
	}
	db := got.Services.DB
	if db.Engine != "postgres" || db.Provider != "supabase" || db.Schema != "myfamilyinfo" ||
		db.Source != ".env.local:SUPABASE_DB_URL" {
		t.Errorf("db round-trip wrong: %+v", db)
	}
}

// TestSingleAppManifestOmitsMonorepoFields asserts a normal manifest serialises
// without the monorepo-only keys, so existing single-app manifests are unchanged.
func TestSingleAppManifestOmitsMonorepoFields(t *testing.T) {
	dir := t.TempDir()
	if err := WriteManifestFile(dir, &Manifest{Framework: "laravel", Env: "sail", Recipes: []string{"laravel", "sail"}}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".keel", "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	b := string(raw)
	for _, k := range []string{"kind:", "members:", "services:"} {
		if strings.Contains(b, k) {
			t.Errorf("single-app manifest should omit %q, got:\n%s", k, b)
		}
	}
}
