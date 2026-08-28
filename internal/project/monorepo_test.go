package project

import (
	"path/filepath"
	"testing"

	"github.com/coullworks/keel/internal/engine"
)

// TestDetectExpo covers the Expo/React Native detector added so a mobile app is
// a recognised monorepo member.
func TestDetectExpo(t *testing.T) {
	cases := []struct {
		name string
		pkg  string
		want string
	}{
		{"expo", `{"dependencies":{"expo":"~51.0.0","react-native":"0.74"}}`, "expo"},
		{"react-native-only", `{"dependencies":{"react-native":"0.74"}}`, "expo"},
		{"expo-wins-over-next", `{"dependencies":{"expo":"~51.0.0","next":"15"}}`, "expo"},
		{"related-name-not-a-match", `{"dependencies":{"@react-native-async-storage/async-storage":"1.0","next":"15"}}`, "nextjs"},
		{"plain-next", `{"dependencies":{"next":"15"}}`, "nextjs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "package.json", tc.pkg)
			if got := Detect(dir); got != tc.want {
				t.Errorf("Detect = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestMembersKeepsLibs asserts a workspace package with no framework is kept as
// a FrameworkLib member (not dropped), and a non-package dir is still skipped.
func TestMembersKeepsLibs(t *testing.T) {
	mono := t.TempDir()
	write(t, mono, "pnpm-workspace.yaml", "packages:\n  - 'apps/*'\n  - 'packages/*'\n")
	write(t, mono, "package.json", `{"name":"root","private":true}`)
	// a real app, a mobile app, two shared libs (packages), and a stray dir
	write(t, mono, "apps/web/next.config.ts", "export default {}")
	write(t, mono, "apps/mobile/package.json", `{"dependencies":{"expo":"~51.0.0"}}`)
	write(t, mono, "packages/ui/package.json", `{"name":"@x/ui"}`)
	write(t, mono, "packages/config/package.json", `{"name":"@x/config"}`)
	write(t, mono, "packages/notapkg/readme.md", "no package.json here")

	byName := map[string]string{}
	for _, m := range Members(mono) {
		byName[m.Name] = m.Framework
	}
	if byName["web"] != "nextjs" {
		t.Errorf("web = %q, want nextjs", byName["web"])
	}
	if byName["mobile"] != "expo" {
		t.Errorf("mobile = %q, want expo", byName["mobile"])
	}
	if byName["ui"] != FrameworkLib || byName["config"] != FrameworkLib {
		t.Errorf("shared libs should be %q members, got ui=%q config=%q", FrameworkLib, byName["ui"], byName["config"])
	}
	if _, ok := byName["notapkg"]; ok {
		t.Error("a dir with no package.json should be skipped")
	}
}

// TestBuildMonorepoManifest asserts the root manifest records members + the
// inferred shared Supabase backend from the root's .env.local.
func TestBuildMonorepoManifest(t *testing.T) {
	mono := t.TempDir()
	write(t, mono, "turbo.json", "{}")
	write(t, mono, "package.json", `{"name":"mfi","private":true,"workspaces":["apps/*"]}`)
	write(t, mono, "apps/web/next.config.ts", "export default {}")
	write(t, mono, "apps/mobile/package.json", `{"dependencies":{"expo":"~51.0.0"}}`)
	write(t, mono, "apps/ui/package.json", `{"name":"@mfi/ui"}`) // a shared lib
	// one hosted Supabase for the whole repo, secrets in .env.local
	write(t, mono, ".env.local", "SUPABASE_DB_URL=postgresql://postgres:pw@db.example.supabase.co:5432/postgres\n")

	m := BuildMonorepoManifest(mono)
	if m.Kind != engine.KindMonorepo {
		t.Fatalf("kind = %q, want monorepo", m.Kind)
	}
	byPath := map[string]string{}
	for _, mem := range m.Members {
		byPath[mem.Path] = mem.Framework
	}
	if byPath["apps/web"] != "nextjs" || byPath["apps/mobile"] != "expo" || byPath["apps/ui"] != FrameworkLib {
		t.Errorf("members wrong: %+v", m.Members)
	}
	if m.Services == nil || m.Services.DB == nil {
		t.Fatalf("expected a shared DB, got services=%+v", m.Services)
	}
	db := m.Services.DB
	if db.Engine != "postgres" || db.Provider != "supabase" {
		t.Errorf("db = %+v, want postgres/supabase", db)
	}
	if m.Services.EnvFile != ".env.local" {
		t.Errorf("env_file = %q, want .env.local", m.Services.EnvFile)
	}
	if db.Source != ".env.local:SUPABASE_DB_URL" {
		t.Errorf("source = %q, want .env.local:SUPABASE_DB_URL", db.Source)
	}
}

// TestBuildMonorepoManifestNoDB asserts a repo with no shared DB still produces
// a valid manifest (services present, DB nil).
func TestBuildMonorepoManifestNoDB(t *testing.T) {
	mono := t.TempDir()
	write(t, mono, "turbo.json", "{}")
	write(t, mono, "package.json", `{"name":"x","workspaces":["apps/*"]}`)
	write(t, mono, "apps/web/next.config.ts", "export default {}")

	m := BuildMonorepoManifest(mono)
	if m.Services == nil {
		t.Fatal("services should be present even with no DB")
	}
	if m.Services.DB != nil {
		t.Errorf("expected no DB, got %+v", m.Services.DB)
	}
}

// TestEffectiveBackendInherits asserts a member resolves its DB/env from the
// monorepo root manifest (env read points at the root, DB is inherited).
func TestEffectiveBackendInherits(t *testing.T) {
	mono := t.TempDir()
	write(t, mono, "turbo.json", "{}")
	write(t, mono, "package.json", `{"name":"mfi","workspaces":["apps/*"]}`)
	write(t, mono, "apps/web/next.config.ts", "export default {}")
	write(t, mono, ".env.local", "SUPABASE_DB_URL=postgresql://postgres:pw@db.example.supabase.co:5432/postgres\n")

	m := BuildMonorepoManifest(mono)
	if err := engine.WriteManifestFile(mono, m); err != nil {
		t.Fatal(err)
	}

	memberDir := filepath.Join(mono, "apps", "web")
	b := EffectiveBackend(memberDir)
	if !b.Inherited {
		t.Error("member should inherit the root backend")
	}
	if b.EnvDir != mono {
		t.Errorf("env dir = %q, want the root %q", b.EnvDir, mono)
	}
	if b.EnvFile != ".env.local" {
		t.Errorf("env file = %q, want .env.local", b.EnvFile)
	}
	if b.DB == nil || b.DB.Engine != "postgres" || b.DB.Provider != "supabase" {
		t.Errorf("db = %+v, want inherited postgres/supabase", b.DB)
	}
}

// TestEffectiveBackendStandalone asserts a project with no monorepo root above
// it degrades to reading its own directory (not inherited).
func TestEffectiveBackendStandalone(t *testing.T) {
	proj := t.TempDir()
	write(t, proj, "package.json", `{"name":"solo","dependencies":{"next":"15"}}`)

	b := EffectiveBackend(proj)
	if b.Inherited {
		t.Error("a standalone project should not inherit")
	}
	if b.EnvDir != proj {
		t.Errorf("env dir = %q, want the project itself %q", b.EnvDir, proj)
	}
	if b.DB != nil {
		t.Errorf("standalone with no shared root has no inherited DB, got %+v", b.DB)
	}
}

// TestDetectSharedDBVariants covers each recognised (and rejected) DB signal.
func TestDetectSharedDBVariants(t *testing.T) {
	cases := []struct {
		name         string
		env          map[string]string
		wantEngine   string
		wantProvider string
		wantSource   string
		wantNil      bool
	}{
		{"supabase-db-url", map[string]string{"SUPABASE_DB_URL": "postgresql://h/db"}, "postgres", "supabase", ".env:SUPABASE_DB_URL", false},
		{"postgres-database-url", map[string]string{"DATABASE_URL": "postgres://h/db"}, "postgres", "", ".env:DATABASE_URL", false},
		{"supabase-database-url", map[string]string{"DATABASE_URL": "postgres://x.supabase.co/db"}, "postgres", "supabase", ".env:DATABASE_URL", false},
		{"mysql-database-url-rejected", map[string]string{"DATABASE_URL": "mysql://h/db"}, "", "", "", true},
		{"supabase-project-url", map[string]string{"NEXT_PUBLIC_SUPABASE_URL": "https://x.supabase.co"}, "postgres", "supabase", ".env:NEXT_PUBLIC_SUPABASE_URL", false},
		{"none", map[string]string{"FOO": "bar"}, "", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			get := func(k string) string { return tc.env[k] }
			db := detectSharedDB(".env", get)
			if tc.wantNil {
				if db != nil {
					t.Fatalf("expected nil DB, got %+v", db)
				}
				return
			}
			if db == nil {
				t.Fatal("expected a DB, got nil")
			}
			if db.Engine != tc.wantEngine || db.Provider != tc.wantProvider || db.Source != tc.wantSource {
				t.Errorf("db = %+v, want engine=%q provider=%q source=%q", db, tc.wantEngine, tc.wantProvider, tc.wantSource)
			}
		})
	}
}
