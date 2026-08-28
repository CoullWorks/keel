package project

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetect(t *testing.T) {
	cases := []struct {
		name  string
		setup func(dir string)
		want  string
	}{
		{"laravel", func(d string) { write(t, d, "composer.json", `{"require":{"laravel/framework":"^11"}}`) }, "laravel"},
		{"magento", func(d string) {
			write(t, d, "composer.json", `{"require":{"magento/product-community-edition":"2.4"}}`)
		}, "magento"},
		{"django", func(d string) { write(t, d, "manage.py", "# django") }, "django"},
		{"fastapi", func(d string) { write(t, d, "pyproject.toml", "dependencies = [\"fastapi\"]") }, "fastapi"},
		{"nextjs-config", func(d string) { write(t, d, "next.config.mjs", "export default {}") }, "nextjs"},
		{"nextjs-pkg", func(d string) { write(t, d, "package.json", `{"dependencies":{"next":"15"}}`) }, "nextjs"},
		{"manifest-wins", func(d string) {
			write(t, d, "package.json", `{"dependencies":{"next":"15"}}`)
			write(t, d, ".keel/manifest.yaml", "framework: laravel\n")
		}, "laravel"},
		{"unknown", func(d string) { write(t, d, "README.md", "hi") }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(dir)
			if got := Detect(dir); got != tc.want {
				t.Errorf("Detect = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExpandTilde(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := Expand("~/www/x"); got != filepath.Join(home, "www/x") {
		t.Errorf("Expand(~/www/x) = %q", got)
	}
	if got := Expand("~"); got != home {
		t.Errorf("Expand(~) = %q", got)
	}
	if got := Expand("/abs/path"); got != "/abs/path" {
		t.Errorf("Expand kept absolute wrong: %q", got)
	}
}

func TestMonorepoDetection(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("KEEL_CONFIG_DIR", cfg)
	mono := t.TempDir()
	// a pnpm/turbo monorepo with two Next apps + a config-only package
	write(t, mono, "pnpm-workspace.yaml", "packages:\n  - 'apps/*'\n  - 'packages/*'\n")
	write(t, mono, "turbo.json", "{}")
	write(t, mono, "package.json", `{"name":"root","private":true}`)
	write(t, mono, "apps/web/next.config.ts", "export default {}")
	write(t, mono, "apps/admin/next.config.mjs", "export default {}")
	write(t, mono, "packages/config/index.ts", "export const x=1")

	r := &Registry{}
	p, err := r.Add(mono)
	if err != nil {
		t.Fatal(err)
	}
	if p.Framework != "monorepo" {
		t.Fatalf("framework = %q, want monorepo", p.Framework)
	}
	names := map[string]string{}
	for _, m := range p.Members {
		names[m.Name] = m.Framework
	}
	if names["web"] != "nextjs" || names["admin"] != "nextjs" {
		t.Errorf("members = %+v, want web+admin nextjs", p.Members)
	}
	if _, ok := names["config"]; ok {
		t.Errorf("config (no stack) should be skipped, got %+v", p.Members)
	}
}

func TestAddMissingDir(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	r := &Registry{}
	if _, err := r.Add("/definitely/not/here/xyzzy"); err == nil {
		t.Error("expected error adding a non-existent dir")
	}
}

func TestRegistryAddRemove(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("KEEL_CONFIG_DIR", cfg)

	proj := t.TempDir()
	write(t, proj, "manage.py", "# django")

	r := &Registry{}
	p, err := r.Add(proj)
	if err != nil {
		t.Fatal(err)
	}
	if p.Framework != "django" {
		t.Errorf("framework = %q", p.Framework)
	}
	if p.Managed {
		t.Error("should not be managed (no .keel manifest)")
	}
	// Idempotent on re-add.
	if _, err := r.Add(proj); err != nil {
		t.Fatal(err)
	}
	if len(r.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(r.Projects))
	}
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil || len(got.Projects) != 1 {
		t.Fatalf("reload: %v, %d projects", err, len(got.Projects))
	}
	r.Remove(proj)
	if len(r.Projects) != 0 {
		t.Errorf("remove failed: %d left", len(r.Projects))
	}
}
