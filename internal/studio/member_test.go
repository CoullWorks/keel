package studio

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// writeMonorepoRoot drops a monorepo-root manifest + a matching .env.local into
// dir, so ReadManifest sees a kind:monorepo root carrying a shared Supabase
// backend and EffectiveBackend can resolve a member's inherited DB from it.
func writeMonorepoRoot(t *testing.T, dir, member string) {
	t.Helper()
	kdir := filepath.Join(dir, ".keel")
	if err := os.MkdirAll(kdir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A workspace so project.Members (and thus projectDir's member allowlist)
	// recognises the member directory as part of this tracked root.
	if err := os.WriteFile(filepath.Join(dir, "pnpm-workspace.yaml"), []byte("packages:\n  - 'apps/*'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"root","private":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	mdir := filepath.Join(dir, member)
	if err := os.MkdirAll(mdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mdir, "package.json"), []byte(`{"dependencies":{"next":"15"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	man := "kind: monorepo\n" +
		"members:\n  - path: " + member + "\n    framework: nextjs\n" +
		"services:\n  db:\n    engine: postgres\n    provider: supabase\n    source: \".env.local:SUPABASE_DB_URL\"\n    schema: app\n  env_file: .env.local\n"
	if err := os.WriteFile(filepath.Join(kdir, "manifest.yaml"), []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("SUPABASE_DB_URL=postgres://user:pw@db.supabase.co:5432/postgres\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A monorepo member resolves its EFFECTIVE (inherited) backend from the root,
// not a re-derived local DSN. This is the fix for the per-member bug: a Next.js
// member on hosted Supabase must report postgres/supabase, inherited, with the
// env reference the shared credential lives in — never a local MySQL.
func TestHandleMemberBackendInheritsSharedSupabase(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	writeMonorepoRoot(t, root, "apps/web")
	trackProject(t, root) // tracks the root; projectDir accepts its members

	member := filepath.Join(root, "apps", "web")
	w := muxGet(testMux(), "/api/member/backend?dir="+member)
	if w.Code != http.StatusOK {
		t.Fatalf("member backend should be 200, got %d: %s", w.Code, w.Body)
	}
	m := decodeJSON(t, w)
	if inh, _ := m["inherited"].(bool); !inh {
		t.Errorf("a monorepo member must inherit its backend: %s", w.Body)
	}
	if eng, _ := m["engine"].(string); eng != "postgres" {
		t.Errorf("engine = %q, want postgres (the shared Supabase, not a local MySQL): %s", eng, w.Body)
	}
	if prov, _ := m["provider"].(string); prov != "supabase" {
		t.Errorf("provider = %q, want supabase: %s", prov, w.Body)
	}
	if src, _ := m["source"].(string); src == "" {
		t.Errorf("source should name where the credential lives: %s", w.Body)
	}
	// The endpoint records an env reference, never the secret value itself.
	if src, _ := m["source"].(string); len(src) > 0 && (src == "postgres://user:pw@db.supabase.co:5432/postgres") {
		t.Errorf("the backend must not leak the credential value: %q", src)
	}
}

// A standalone project (no shared-backend root above it) is not an error: the
// endpoint degrades to a non-inherited answer with no shared DB, so the UI shows
// no shared-backend banner and the project's own DSN resolution still applies.
func TestHandleMemberBackendStandaloneDegrades(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "laravel-docker", []string{"laravel"})
	trackProject(t, dir)

	w := muxGet(testMux(), "/api/member/backend?dir="+dir)
	if w.Code != http.StatusOK {
		t.Fatalf("standalone backend should be 200, got %d: %s", w.Code, w.Body)
	}
	m := decodeJSON(t, w)
	if inh, _ := m["inherited"].(bool); inh {
		t.Errorf("a standalone project must not report an inherited backend: %s", w.Body)
	}
	if _, ok := m["error"]; ok {
		t.Errorf("a standalone project is a normal answer, not an error: %s", w.Body)
	}
}

// writeRootLaunchRoot drops a root-launch pnpm workspace (root dev script + a
// member with its own dev script) with an adopted monorepo manifest recording
// the launch, so EffectiveLaunch resolves it for the member.
func writeRootLaunchRoot(t *testing.T, dir, member string) {
	t.Helper()
	kdir := filepath.Join(dir, ".keel")
	if err := os.MkdirAll(kdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pnpm-workspace.yaml"), []byte("packages:\n  - 'apps/*'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"root","private":true,"scripts":{"dev":"turbo run dev"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	mdir := filepath.Join(dir, member)
	if err := os.MkdirAll(mdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mdir, "package.json"), []byte(`{"name":"@acme/web","scripts":{"dev":"next dev"},"dependencies":{"next":"15"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	man := "kind: monorepo\n" +
		"members:\n  - path: " + member + "\n    framework: nextjs\n" +
		"services:\n  launch:\n    manager: pnpm\n    dev_script: dev\n"
	if err := os.WriteFile(filepath.Join(kdir, "manifest.yaml"), []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A member of a root-launch workspace reports rootLaunch=true with the exact root
// command its Run resolves to (scoped to the member via --filter), so the member
// view can say "launched from the workspace root" and drive the right command.
func TestHandleMemberBackendRootLaunch(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	writeRootLaunchRoot(t, root, "apps/web")
	trackProject(t, root)

	member := filepath.Join(root, "apps", "web")
	w := muxGet(testMux(), "/api/member/backend?dir="+member)
	if w.Code != http.StatusOK {
		t.Fatalf("member backend should be 200, got %d: %s", w.Code, w.Body)
	}
	m := decodeJSON(t, w)
	if rl, _ := m["rootLaunch"].(bool); !rl {
		t.Errorf("a root-launch member must report rootLaunch=true: %s", w.Body)
	}
	if mgr, _ := m["launchManager"].(string); mgr != "pnpm" {
		t.Errorf("launchManager = %q, want pnpm: %s", mgr, w.Body)
	}
	if cmd, _ := m["launchCommand"].(string); cmd != "pnpm --filter @acme/web dev" {
		t.Errorf("launchCommand = %q, want the filtered root launcher: %s", cmd, w.Body)
	}
}

// A standalone project does NOT report root-launch, so the UI keeps its
// per-member actions — the non-workspace path is unaffected.
func TestHandleMemberBackendStandaloneNotRootLaunch(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "laravel-docker", []string{"laravel"})
	trackProject(t, dir)

	w := muxGet(testMux(), "/api/member/backend?dir="+dir)
	m := decodeJSON(t, w)
	if rl, _ := m["rootLaunch"].(bool); rl {
		t.Errorf("a standalone project must not report root-launch: %s", w.Body)
	}
}

// A tracked-but-UNADOPTED pnpm/turbo workspace (no .keel/manifest.yaml) still
// surfaces its root launch live: the root exposes launchCommand through
// /api/projects, and a member reports rootLaunch=true from /api/member/backend —
// the studio's root view can offer a Run without the workspace being adopted.
func TestUnadoptedWorkspaceSurfacesLaunchLive(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	// A bare workspace: workspace + turbo markers + a root dev script + a member,
	// but deliberately NO .keel/manifest.yaml (the tracked-but-unadopted case).
	if err := os.WriteFile(filepath.Join(root, "pnpm-workspace.yaml"), []byte("packages:\n  - 'apps/*'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "turbo.json"), []byte(`{"pipeline":{"dev":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"root","private":true,"scripts":{"dev":"turbo run dev"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	mdir := filepath.Join(root, "apps", "web")
	if err := os.MkdirAll(mdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mdir, "package.json"), []byte(`{"name":"@acme/web","dependencies":{"next":"15"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	trackProject(t, root)

	// The ROOT view: /api/projects re-inspects live and carries the launch command.
	w := muxGet(testMux(), "/api/projects")
	if w.Code != http.StatusOK {
		t.Fatalf("/api/projects should be 200, got %d: %s", w.Code, w.Body)
	}
	m := decodeJSON(t, w)
	projs, _ := m["projects"].([]any)
	if len(projs) != 1 {
		t.Fatalf("want one tracked project, got %d: %s", len(projs), w.Body)
	}
	p, _ := projs[0].(map[string]any)
	if fw, _ := p["framework"].(string); fw != "monorepo" {
		t.Errorf("framework = %q, want monorepo (live-detected): %s", fw, w.Body)
	}
	if cmd, _ := p["launchCommand"].(string); cmd != "turbo dev" {
		t.Errorf("launchCommand = %q, want %q (live, no manifest): %s", cmd, "turbo dev", w.Body)
	}

	// The MEMBER view: rootLaunch is live-detected too, so per-member env/deploy is
	// suppressed and its Run resolves to the root command.
	mw := muxGet(testMux(), "/api/member/backend?dir="+mdir)
	mm := decodeJSON(t, mw)
	if rl, _ := mm["rootLaunch"].(bool); !rl {
		t.Errorf("an unadopted workspace member must report rootLaunch=true live: %s", mw.Body)
	}
}

// The endpoint refuses a directory that is not a tracked keel project, the same
// guard projectDir enforces for exec and SQL — a browser cannot name an
// arbitrary path.
func TestHandleMemberBackendRefusesUntrackedDir(t *testing.T) {
	isolateConfig(t)
	other := t.TempDir()
	w := muxGet(testMux(), "/api/member/backend?dir="+other)
	if w.Code != http.StatusOK { // a handled error is still a 200 JSON body
		t.Fatalf("untracked dir should be a handled 200, got %d: %s", w.Code, w.Body)
	}
	m := decodeJSON(t, w)
	if _, ok := m["error"]; !ok {
		t.Errorf("an untracked dir must be refused with an error: %s", w.Body)
	}
}
