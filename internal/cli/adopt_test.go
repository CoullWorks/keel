package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/engine"
)

// scaffoldDjango writes the minimal files that make project.Detect return
// "django" and makes .keel-project detection see a local env.
func scaffoldDjango(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "manage.py"), []byte("# django manage.py\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAdoptDetectsAndWritesManifest(t *testing.T) {
	wd := isolate(t)
	scaffoldDjango(t, wd)

	out, err := runRoot(t, "adopt")
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	mustContain(t, out, "adopted", "django")
	if _, err := os.Stat(filepath.Join(wd, ".keel", "manifest.yaml")); err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
}

// adopt on an undetectable directory reports it clearly.
func TestAdoptUndetectable(t *testing.T) {
	isolate(t)
	_, err := runRoot(t, "adopt")
	if err == nil {
		t.Fatal("expected an error adopting an empty dir")
	}
	mustContain(t, err.Error(), "could not detect")
}

// adopt is idempotent: a directory that already has a manifest is returned
// unchanged.
func TestAdoptIdempotent(t *testing.T) {
	wd := isolate(t)
	scaffoldDjango(t, wd)
	if _, err := adoptDir(wd); err != nil {
		t.Fatalf("first adopt: %v", err)
	}
	m, err := adoptDir(wd)
	if err != nil {
		t.Fatalf("second adopt: %v", err)
	}
	if m.Framework != "django" {
		t.Errorf("framework = %q, want django", m.Framework)
	}
}

// adopt <path> adopts a project at an explicit path (not the CWD).
func TestAdoptExplicitPath(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	scaffoldDjango(t, proj)
	out, err := runRoot(t, "adopt", proj)
	if err != nil {
		t.Fatalf("adopt <path>: %v", err)
	}
	mustContain(t, out, "adopted", "django")
	if _, err := os.Stat(filepath.Join(proj, ".keel", "manifest.yaml")); err != nil {
		t.Errorf("manifest not written at path: %v", err)
	}
}

// adopt on a missing path errors.
func TestAdoptMissingPath(t *testing.T) {
	isolate(t)
	_, err := runRoot(t, "adopt", filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("expected error for missing directory")
	}
	mustContain(t, err.Error(), "no such directory")
}

// adopt on a monorepo root writes a kind: monorepo manifest recording its
// members + the inferred shared backend, instead of the old hard refusal.
func TestAdoptMonorepoRoot(t *testing.T) {
	isolate(t)
	mono := t.TempDir()
	writeFile(t, mono, "turbo.json", "{}")
	writeFile(t, mono, "package.json", `{"name":"mfi","private":true,"workspaces":["apps/*","packages/*"]}`)
	writeFile(t, mono, "apps/web/next.config.ts", "export default {}")
	writeFile(t, mono, "apps/mobile/package.json", `{"dependencies":{"expo":"~51.0.0"}}`)
	writeFile(t, mono, "packages/ui/package.json", `{"name":"@mfi/ui"}`)
	writeFile(t, mono, ".env.local", "SUPABASE_DB_URL=postgresql://postgres:pw@db.example.supabase.co:5432/postgres\n")

	out, err := runRoot(t, "adopt", mono)
	if err != nil {
		t.Fatalf("adopt monorepo: %v", err)
	}
	mustContain(t, out, "monorepo", "shared backend", "supabase")

	m, err := engine.ReadManifest(mono)
	if err != nil {
		t.Fatalf("read monorepo manifest: %v", err)
	}
	if m.Kind != engine.KindMonorepo {
		t.Fatalf("kind = %q, want monorepo", m.Kind)
	}
	byPath := map[string]string{}
	for _, mem := range m.Members {
		byPath[mem.Path] = mem.Framework
	}
	if byPath["apps/web"] != "nextjs" || byPath["apps/mobile"] != "expo" || byPath["packages/ui"] != "lib" {
		t.Errorf("members wrong: %+v", m.Members)
	}
	if m.Services == nil || m.Services.DB == nil || m.Services.DB.Provider != "supabase" {
		t.Errorf("shared backend not recorded: %+v", m.Services)
	}
}

// Adopting a root-launch workspace (a pnpm root with a dev script) records the
// launch on the manifest and tells the user members run from the root — no
// per-member env.
func TestAdoptRootLaunchWorkspace(t *testing.T) {
	isolate(t)
	mono := t.TempDir()
	writeFile(t, mono, "pnpm-workspace.yaml", "packages:\n  - 'apps/*'\n")
	writeFile(t, mono, "package.json", `{"name":"root","private":true,"scripts":{"dev":"turbo run dev"}}`)
	writeFile(t, mono, "apps/web/package.json", `{"name":"@acme/web","scripts":{"dev":"next dev"},"dependencies":{"next":"15"}}`)

	out, err := runRoot(t, "adopt", mono)
	if err != nil {
		t.Fatalf("adopt root-launch workspace: %v", err)
	}
	mustContain(t, out, "root-launch", "pnpm dev", "no per-member env")

	m, err := engine.ReadManifest(mono)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if m.Services == nil || m.Services.Launch == nil {
		t.Fatalf("launch not recorded: %+v", m.Services)
	}
	if m.Services.Launch.Manager != "pnpm" {
		t.Errorf("launch manager = %q, want pnpm", m.Services.Launch.Manager)
	}
}

// Adopting a MEMBER of a root-launch workspace must NOT give it a per-member env:
// it inherits the root launch, so its manifest records no env and `keel run`
// resolves the root command for it.
func TestAdoptRootLaunchMemberSkipsPerMemberEnv(t *testing.T) {
	isolate(t)
	mono := t.TempDir()
	writeFile(t, mono, "pnpm-workspace.yaml", "packages:\n  - 'apps/*'\n")
	writeFile(t, mono, "package.json", `{"name":"root","private":true,"scripts":{"dev":"turbo run dev"}}`)
	writeFile(t, mono, "apps/web/package.json", `{"name":"@acme/web","scripts":{"dev":"next dev"},"dependencies":{"next":"15"}}`)
	// Adopt the root first so the member can inherit the recorded launch.
	if _, err := runRoot(t, "adopt", mono); err != nil {
		t.Fatalf("adopt root: %v", err)
	}

	member := filepath.Join(mono, "apps", "web")
	m, err := adoptDir(member)
	if err != nil {
		t.Fatalf("adopt member: %v", err)
	}
	if m.Env != "" {
		t.Errorf("a root-launch member must not get a per-member env, got %q", m.Env)
	}
	if m.Framework != "nextjs" {
		t.Errorf("member framework = %q, want nextjs", m.Framework)
	}
	// The recorded recipes must not include an env recipe (docker/sail/ddev).
	for _, id := range m.Recipes {
		if id == "docker" || id == "sail" || id == "ddev" || id == "nextjs-docker" {
			t.Errorf("root-launch member recorded a per-member env recipe %q", id)
		}
	}
}

// writeFile writes a file (creating parent dirs) inside a monorepo fixture.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// resolveEnvID prefers an id containing the detected kind, then docker-provider,
// then the framework default.
func TestResolveEnvID(t *testing.T) {
	reg, err := catalog.Registry()
	if err != nil {
		t.Fatal(err)
	}
	// Laravel ships a ddev env; the "ddev" kind should map to it.
	if id := resolveEnvID(reg, "laravel", "ddev"); id != "ddev" {
		t.Errorf("laravel/ddev env = %q, want ddev", id)
	}
	// An unknown kind falls back to the framework's default env (non-empty).
	if id := resolveEnvID(reg, "laravel", "totally-unknown"); id == "" {
		t.Error("expected a fallback env id for laravel")
	}
	// A framework with no env recipes returns the kind unchanged.
	if id := resolveEnvID(reg, "no-such-fw", "local"); id != "local" {
		t.Errorf("unknown framework env = %q, want local", id)
	}
}

// mustAbs returns an absolute path (and never panics on a relative input).
func TestMustAbs(t *testing.T) {
	if got := mustAbs("relative/path"); !filepath.IsAbs(got) {
		t.Errorf("mustAbs(relative) = %q, not absolute", got)
	}
}

// A manifest written by adopt can be read straight back.
func TestAdoptManifestRoundTrip(t *testing.T) {
	wd := isolate(t)
	scaffoldDjango(t, wd)
	if _, err := adoptDir(wd); err != nil {
		t.Fatal(err)
	}
	m, err := engine.ReadManifest(wd)
	if err != nil {
		t.Fatalf("read back manifest: %v", err)
	}
	if m.Framework != "django" {
		t.Errorf("framework = %q, want django", m.Framework)
	}
}
