package studio

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeEnvFile drops a named env file under dir.
func writeEnvFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// envByKey indexes an /api/env response's vars by key for assertions.
func envByKey(t *testing.T, m map[string]any) map[string]map[string]any {
	t.Helper()
	vars, ok := m["vars"].([]any)
	if !ok {
		t.Fatalf("expected a vars array, got %v", m["vars"])
	}
	byKey := map[string]map[string]any{}
	for _, v := range vars {
		row := v.(map[string]any)
		byKey[row["key"].(string)] = row
	}
	return byKey
}

// A Next.js app keeps its real config in .env.local. The reader must list those
// variables — NEXT_PUBLIC_ shown in full, secrets masked, ordinary config shown —
// and the raw secret value must NEVER cross the wire.
func TestEnvSurfacesNextLocalMasksSecrets(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "local", []string{"nextjs"})
	writeEnvFile(t, dir, ".env.local", strings.Join([]string{
		"NEXT_PUBLIC_SUPABASE_URL=https://abc.supabase.co",
		"NEXT_PUBLIC_SUPABASE_ANON_KEY=eyJpublicanonkey",
		"SUPABASE_SERVICE_ROLE_KEY=SERVICE_ROLE_MUST_NOT_LEAK",
		"DATABASE_URL=postgres://user:PGPASS_MUST_NOT_LEAK@db.supabase.co:5432/postgres",
		"APP_NAME=Fitness",
	}, "\n")+"\n")
	trackProject(t, dir)

	w := muxGet(testMux(), "/api/env?dir="+dir)
	body := w.Body.String()
	// The load-bearing assertion: NO secret value ever crosses the wire.
	for _, secret := range []string{"SERVICE_ROLE_MUST_NOT_LEAK", "PGPASS_MUST_NOT_LEAK"} {
		if strings.Contains(body, secret) {
			t.Fatalf("secret value leaked in response: %q\n%s", secret, body)
		}
	}
	m := decodeJSON(t, w)
	if m["found"] != true {
		t.Fatalf("found should be true, got %v", m["found"])
	}
	byKey := envByKey(t, m)

	// NEXT_PUBLIC_ is public by definition — value shown in full, even when the
	// name contains "KEY".
	if row := byKey["NEXT_PUBLIC_SUPABASE_URL"]; row == nil || row["public"] != true || row["value"] != "https://abc.supabase.co" {
		t.Fatalf("NEXT_PUBLIC_SUPABASE_URL should be public with its value, got %v", row)
	}
	if row := byKey["NEXT_PUBLIC_SUPABASE_ANON_KEY"]; row == nil || row["public"] != true || row["value"] != "eyJpublicanonkey" {
		t.Fatalf("NEXT_PUBLIC_ anon key should be public with its value (not masked despite 'KEY'), got %v", row)
	}
	// A *_KEY secret: masked, present, no value.
	if row := byKey["SUPABASE_SERVICE_ROLE_KEY"]; row == nil || row["secret"] != true || row["present"] != true {
		t.Fatalf("service role key should be masked secret+present, got %v", row)
	} else if _, hasVal := row["value"]; hasVal {
		t.Fatalf("service role key must not carry a value field, got %v", row)
	}
	// A DATABASE_URL with embedded credentials: masked as a secret.
	if row := byKey["DATABASE_URL"]; row == nil || row["secret"] != true {
		t.Fatalf("a credential-bearing DATABASE_URL should be masked, got %v", row)
	} else if _, hasVal := row["value"]; hasVal {
		t.Fatalf("credential URL must not carry a value field, got %v", row)
	}
	// Ordinary config: shown.
	if row := byKey["APP_NAME"]; row == nil || row["value"] != "Fitness" || row["secret"] == true || row["public"] == true {
		t.Fatalf("APP_NAME should be plain config with its value, got %v", row)
	}
	// Provenance names the file each var resolved from.
	if row := byKey["APP_NAME"]; row == nil || row["file"] != ".env.local" {
		t.Fatalf("APP_NAME provenance should be .env.local, got %v", row)
	}
}

// Precedence: a higher-priority file wins a shared key, and its provenance is the
// file that won. .env.local overrides .env.
func TestEnvPrecedenceHighestWins(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "local", []string{"nextjs"})
	// .env (lowest) sets both; .env.local (higher) overrides SHARED and adds ONLY_LOCAL.
	writeEnvFile(t, dir, ".env", "SHARED=from_base\nONLY_BASE=base\n")
	writeEnvFile(t, dir, ".env.local", "SHARED=from_local\nONLY_LOCAL=local\n")
	trackProject(t, dir)

	m := decodeJSON(t, muxGet(testMux(), "/api/env?dir="+dir))
	byKey := envByKey(t, m)
	if row := byKey["SHARED"]; row == nil || row["value"] != "from_local" || row["file"] != ".env.local" {
		t.Fatalf("SHARED should resolve from .env.local (highest present), got %v", row)
	}
	if row := byKey["ONLY_BASE"]; row == nil || row["value"] != "base" || row["file"] != ".env" {
		t.Fatalf("ONLY_BASE should resolve from .env, got %v", row)
	}
	if row := byKey["ONLY_LOCAL"]; row == nil || row["value"] != "local" || row["file"] != ".env.local" {
		t.Fatalf("ONLY_LOCAL should resolve from .env.local, got %v", row)
	}
}

// .env.$(NODE_ENV).local sits above .env.local. With NODE_ENV=development the
// development-local file wins a shared key.
func TestEnvPrecedenceNodeEnvLocalWins(t *testing.T) {
	isolateConfig(t)
	t.Setenv("NODE_ENV", "development")
	dir := t.TempDir()
	writeManifest(t, dir, "local", []string{"nextjs"})
	writeEnvFile(t, dir, ".env.local", "TARGET=from_local\n")
	writeEnvFile(t, dir, ".env.development.local", "TARGET=from_dev_local\n")
	trackProject(t, dir)

	m := decodeJSON(t, muxGet(testMux(), "/api/env?dir="+dir))
	byKey := envByKey(t, m)
	if row := byKey["TARGET"]; row == nil || row["value"] != "from_dev_local" || row["file"] != ".env.development.local" {
		t.Fatalf("TARGET should resolve from .env.development.local (highest), got %v", row)
	}
}

// A monorepo member with no local env falls back to the workspace ROOT's env,
// and each inherited var is labelled fromRoot with the response reporting the
// root as the env dir. This is the fix for a Next.js member whose env lives at
// the root — the tab must show something, not nothing.
func TestEnvMonorepoRootFallback(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	writeMonorepoRoot(t, root, "apps/web") // writes root .env.local with SUPABASE_DB_URL (a secret)
	// Add a NEXT_PUBLIC_ var and plain config to the root so we can assert display.
	writeEnvFile(t, root, ".env.local",
		"SUPABASE_DB_URL=postgres://user:ROOT_PW_MUST_NOT_LEAK@db.supabase.co:5432/postgres\n"+
			"NEXT_PUBLIC_SITE=https://app.example.com\n"+
			"TURBO_TEAM=acme\n")
	trackProject(t, root)

	member := filepath.Join(root, "apps", "web") // no local env of its own
	w := muxGet(testMux(), "/api/env?dir="+member)
	body := w.Body.String()
	if strings.Contains(body, "ROOT_PW_MUST_NOT_LEAK") {
		t.Fatalf("root secret leaked to a member's env response: %s", body)
	}
	m := decodeJSON(t, w)
	if m["found"] != true {
		t.Fatalf("a member with no local env must inherit the root's env, got found=%v: %s", m["found"], body)
	}
	if m["fromRoot"] != true {
		t.Fatalf("the response should report fromRoot=true, got %v", m["fromRoot"])
	}
	byKey := envByKey(t, m)
	// The DB URL is a credential URL → masked, and labelled as inherited.
	if row := byKey["SUPABASE_DB_URL"]; row == nil || row["secret"] != true || row["fromRoot"] != true {
		t.Fatalf("SUPABASE_DB_URL should be an inherited masked secret, got %v", row)
	} else if _, hasVal := row["value"]; hasVal {
		t.Fatalf("inherited DB URL must not carry a value, got %v", row)
	}
	if row := byKey["NEXT_PUBLIC_SITE"]; row == nil || row["public"] != true || row["value"] != "https://app.example.com" || row["fromRoot"] != true {
		t.Fatalf("NEXT_PUBLIC_SITE should be inherited public with its value, got %v", row)
	}
}

// A member that HAS its own local env keeps it — the root fallback only triggers
// when the member has no env of its own.
func TestEnvMemberLocalOverridesRoot(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	writeMonorepoRoot(t, root, "apps/web")
	member := filepath.Join(root, "apps", "web")
	writeEnvFile(t, member, ".env.local", "APP_NAME=MemberOwn\n")
	trackProject(t, root)

	m := decodeJSON(t, muxGet(testMux(), "/api/env?dir="+member))
	if m["fromRoot"] != false {
		t.Fatalf("a member with its own env must not report fromRoot, got %v", m["fromRoot"])
	}
	byKey := envByKey(t, m)
	if row := byKey["APP_NAME"]; row == nil || row["value"] != "MemberOwn" {
		t.Fatalf("member's own env should win, got %v", row)
	}
	// The inherited root-only var is not merged in (the member's local env stands).
	if _, has := byKey["SUPABASE_DB_URL"]; has {
		t.Fatalf("the member has its own env, the root's vars should not be merged: %v", byKey)
	}
}

// No env anywhere: a calm found=false with a note, not an error. A monorepo
// member on Vercel-injected env legitimately has no file.
func TestEnvNoEnvGracefulState(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "local", []string{"nextjs"})
	trackProject(t, dir)

	m := decodeJSON(t, muxGet(testMux(), "/api/env?dir="+dir))
	if m["found"] != false {
		t.Fatalf("no env should report found=false, got %v", m["found"])
	}
	if note, _ := m["note"].(string); note == "" {
		t.Fatalf("no-env should carry a calm note, got %q", note)
	}
	if _, hasErr := m["error"]; hasErr {
		t.Fatalf("no-env is not an error, got %v", m["error"])
	}
	if vars, _ := m["vars"].([]any); len(vars) != 0 {
		t.Fatalf("no-env should list no vars, got %v", vars)
	}
}

// An untracked directory is refused, exactly like every other /api route.
func TestEnvRejectsUntrackedDir(t *testing.T) {
	isolateConfig(t)
	other := t.TempDir() // never tracked
	w := httptest.NewRecorder()
	handleEnv(w, httptest.NewRequest("GET", "http://127.0.0.1/api/env?dir="+other, nil))
	m := decodeJSON(t, w)
	if _, ok := m["error"]; !ok {
		t.Fatalf("an untracked dir must be refused, got %v", m)
	}
}

// Unit-level guarantees for the classification rules the surface depends on.
func TestClassifyEnvRules(t *testing.T) {
	tests := []struct {
		name       string
		key, value string
		wantPublic bool
		wantSecret bool
		wantValue  bool // whether a value field is present
	}{
		{"public prefix shows value", "NEXT_PUBLIC_URL", "https://x", true, false, true},
		{"public prefix wins over KEY word", "NEXT_PUBLIC_API_KEY", "pk_live_abc", true, false, true},
		{"secret by name masks value", "STRIPE_SECRET_KEY", "sk_live_abc", false, true, false},
		{"password masked", "DB_PASSWORD", "hunter2", false, true, false},
		{"token masked", "GITHUB_TOKEN", "ghp_abc", false, true, false},
		{"credential url masked", "DATABASE_URL", "postgres://u:p@h:5432/db", false, true, false},
		{"plain url shown", "API_URL", "https://api.example.com", false, false, true},
		{"plain config shown", "APP_NAME", "keel", false, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyEnv(tc.key, tc.value, ".env", false)
			if got.Public != tc.wantPublic {
				t.Errorf("Public = %v, want %v", got.Public, tc.wantPublic)
			}
			if got.Secret != tc.wantSecret {
				t.Errorf("Secret = %v, want %v", got.Secret, tc.wantSecret)
			}
			hasVal := got.Value != ""
			if hasVal != tc.wantValue {
				t.Errorf("value present = %v, want %v (value=%q)", hasVal, tc.wantValue, got.Value)
			}
			// A secret must NEVER carry the raw value.
			if got.Secret && got.Value == tc.value && tc.value != "" {
				t.Errorf("a secret leaked its value: %q", got.Value)
			}
		})
	}
}

// urlHasCredentials only fires on scheme://user:pass@host, not on a bare host or
// a URL without embedded creds.
func TestURLHasCredentials(t *testing.T) {
	withCreds := []string{
		"postgres://user:pass@host:5432/db",
		"mysql://root:secret@127.0.0.1/app",
		"redis://:authpass@cache:6379",
	}
	without := []string{
		"https://api.example.com",
		"postgres://host:5432/db", // host:port, no '@'
		"not a url",
		"",
	}
	for _, u := range withCreds {
		if !urlHasCredentials(u) {
			t.Errorf("urlHasCredentials(%q) = false, want true", u)
		}
	}
	for _, u := range without {
		if urlHasCredentials(u) {
			t.Errorf("urlHasCredentials(%q) = true, want false", u)
		}
	}
}
