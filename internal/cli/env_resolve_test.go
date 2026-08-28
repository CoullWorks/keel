package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEnvNodeEnv: an explicit NODE_ENV overrides the development default, and
// the precedence list honours it (.env.<node>.local first).
func TestEnvNodeEnv(t *testing.T) {
	t.Setenv("NODE_ENV", "production")
	if envNodeEnv() != "production" {
		t.Errorf("envNodeEnv = %q, want production", envNodeEnv())
	}
	files := envFilesInPrecedence("production")
	if files[0] != ".env.production.local" {
		t.Errorf("highest precedence = %q, want .env.production.local", files[0])
	}
	// NODE_ENV=test skips .env.local (Next.js rule).
	for _, f := range envFilesInPrecedence("test") {
		if f == ".env.local" {
			t.Errorf(".env.local must be skipped under NODE_ENV=test")
		}
	}
}

// TestClassifyVar exercises the public/secret/config rule directly.
func TestClassifyVar(t *testing.T) {
	tests := []struct {
		name       string
		key, value string
		wantPublic bool
		wantSecret bool
		wantValue  string // "" when secret (value withheld)
	}{
		{"public prefix shows value", "NEXT_PUBLIC_URL", "https://x", true, false, "https://x"},
		{"public wins over KEY word", "NEXT_PUBLIC_API_KEY", "pk_live_abc", true, false, "pk_live_abc"},
		{"password masked", "DB_PASSWORD", "s3cret", false, true, ""},
		{"token masked", "API_TOKEN", "abc", false, true, ""},
		{"credential url masked", "DATABASE_URL", "postgres://u:p@h/db", false, true, ""},
		{"plain config shown", "APP_NAME", "Widgets", false, false, "Widgets"},
		{"plain url shown", "APP_URL", "https://app.example.com", false, false, "https://app.example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := classifyVar(tc.key, tc.value, ".env", false)
			if v.Public != tc.wantPublic || v.Secret != tc.wantSecret {
				t.Errorf("public/secret = %v/%v, want %v/%v", v.Public, v.Secret, tc.wantPublic, tc.wantSecret)
			}
			if v.Value != tc.wantValue {
				t.Errorf("value = %q, want %q (a secret must carry no value)", v.Value, tc.wantValue)
			}
		})
	}
}

// TestResolveProjectEnvMonorepoFallback: a member with no local env inherits the
// workspace root's env (via project.EffectiveBackend), and each inherited var is
// marked FromRoot with the root's file as provenance.
func TestResolveProjectEnvMonorepoFallback(t *testing.T) {
	root := t.TempDir()
	// A monorepo root manifest with a shared-services block is what points a
	// member's EffectiveBackend at the root env (see project.EffectiveBackend).
	kdir := filepath.Join(root, ".keel")
	if err := os.MkdirAll(kdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pnpm-workspace.yaml"), []byte("packages:\n  - 'apps/*'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"root","private":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	man := "kind: monorepo\n" +
		"members:\n  - path: apps/web\n    framework: nextjs\n" +
		"services:\n  db:\n    engine: postgres\n    provider: supabase\n    source: \".env:SHARED_SECRET\"\n  env_file: .env\n"
	if err := os.WriteFile(filepath.Join(kdir, "manifest.yaml"), []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	member := filepath.Join(root, "apps", "web")
	if err := os.MkdirAll(member, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(member, "package.json"), []byte(`{"dependencies":{"next":"15"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("NEXT_PUBLIC_SITE=https://app.example.com\nSHARED_SECRET=top\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := resolveProjectEnv(member)
	if !res.Found || !res.FromRoot {
		t.Fatalf("member should inherit the root env (found=%v fromRoot=%v)", res.Found, res.FromRoot)
	}
	byKey := map[string]resolvedVar{}
	for _, v := range res.Vars {
		byKey[v.Key] = v
	}
	pub := byKey["NEXT_PUBLIC_SITE"]
	if !pub.Public || !pub.FromRoot || pub.Value != "https://app.example.com" {
		t.Errorf("inherited public var wrong: %+v", pub)
	}
	sec := byKey["SHARED_SECRET"]
	if !sec.Secret || sec.Value != "" {
		t.Errorf("inherited secret must be masked with no value: %+v", sec)
	}
}
