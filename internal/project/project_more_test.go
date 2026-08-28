package project

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDetectMoreMarkers covers the framework branches the existing table misses:
// magento-cloud, django-via-pyproject, next.config.js/ts, a plain composer
// project, and a package.json with no "next" dependency.
func TestDetectMoreMarkers(t *testing.T) {
	cases := []struct {
		name  string
		setup func(dir string)
		want  string
	}{
		{"magento-cloud", func(d string) {
			write(t, d, "composer.json", `{"require":{"magento/magento-cloud-metapackage":"1.0"}}`)
		}, "magento"},
		{"composer-plain", func(d string) { write(t, d, "composer.json", `{"require":{"symfony/console":"^7"}}`) }, "laravel"},
		{"django-pyproject", func(d string) { write(t, d, "pyproject.toml", `dependencies = ["Django>=5"]`) }, "django"},
		{"pyproject-neither", func(d string) { write(t, d, "pyproject.toml", `dependencies = ["requests"]`) }, ""},
		{"next-config-js", func(d string) { write(t, d, "next.config.js", "module.exports = {}") }, "nextjs"},
		{"next-config-ts", func(d string) { write(t, d, "next.config.ts", "export default {}") }, "nextjs"},
		{"pkg-no-next", func(d string) { write(t, d, "package.json", `{"dependencies":{"react":"18"}}`) }, ""},
		{"empty", func(d string) {}, ""},
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

// TestManifestFrameworkBadYaml covers manifestFramework's malformed-yaml branch.
func TestManifestFrameworkBadYaml(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".keel/manifest.yaml", ":::not: valid: yaml: [")
	if got := manifestFramework(dir); got != "" {
		t.Errorf("bad yaml should yield empty framework, got %q", got)
	}
}

// TestDetectEnv covers every DetectEnv branch.
func TestDetectEnv(t *testing.T) {
	cases := []struct {
		name  string
		setup func(dir string)
		want  string
	}{
		{"ddev", func(d string) { os.MkdirAll(filepath.Join(d, ".ddev"), 0o755) }, "ddev"},
		{"sail", func(d string) { write(t, d, "vendor/bin/sail", "#!/bin/sh") }, "sail"},
		{"docker-compose", func(d string) { write(t, d, "docker-compose.yml", "services: {}") }, "docker"},
		{"compose-yaml", func(d string) { write(t, d, "compose.yaml", "services: {}") }, "docker"},
		{"dockerfile", func(d string) { write(t, d, "Dockerfile", "FROM alpine") }, "docker"},
		{"local", func(d string) { write(t, d, "README.md", "hi") }, "local"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(dir)
			if got := DetectEnv(dir); got != tc.want {
				t.Errorf("DetectEnv = %q, want %q", got, tc.want)
			}
		})
	}
	// ddev must be a directory, not a file, to count.
	fileDdev := t.TempDir()
	write(t, fileDdev, ".ddev", "not a dir")
	if got := DetectEnv(fileDdev); got == "ddev" {
		t.Error("a .ddev file (not dir) must not be detected as ddev")
	}
}

// TestLoadMissingAndBad covers Load: missing (empty), corrupt yaml.
func TestLoadMissingAndBad(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	// Nothing saved yet -> empty registry, no error.
	r, err := Load()
	if err != nil || len(r.Projects) != 0 {
		t.Fatalf("empty load: %v, %d", err, len(r.Projects))
	}
	// Corrupt file -> error.
	if err := os.WriteFile(path(), []byte("projects: [:::bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Error("expected an error loading corrupt yaml")
	}
}

// TestSaveSortsByName covers Save writing sorted output and a round-trip.
func TestSaveSortsByName(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	r := &Registry{Projects: []Project{
		{Path: "/z", Name: "zeta"}, {Path: "/a", Name: "alpha"},
	}}
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	if r.Projects[0].Name != "alpha" {
		t.Errorf("Save should sort by name, first = %q", r.Projects[0].Name)
	}
	got, err := Load()
	if err != nil || len(got.Projects) != 2 {
		t.Fatalf("reload: %v, %d", err, len(got.Projects))
	}
}

// TestRefreshRedetectsAndDropsGone covers Refresh re-detecting a live project
// (managed flag flips when a manifest appears) and dropping a vanished one.
func TestRefreshRedetects(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	live := t.TempDir()
	write(t, live, "manage.py", "# django")
	gone := filepath.Join(t.TempDir(), "vanished")

	r := &Registry{Projects: []Project{
		{Path: live, Name: filepath.Base(live), Framework: "unknown"},
		{Path: gone, Name: "vanished", Framework: "django"},
	}}
	// Add a manifest so Refresh detects "managed" now.
	write(t, live, ".keel/manifest.yaml", "framework: django\n")

	r.Refresh()
	if len(r.Projects) != 1 {
		t.Fatalf("gone project should be dropped, got %d", len(r.Projects))
	}
	if !r.Projects[0].Managed {
		t.Error("Refresh should mark the project managed once a manifest exists")
	}
	if r.Projects[0].Framework != "django" {
		t.Errorf("Refresh should re-detect framework, got %q", r.Projects[0].Framework)
	}
}

// TestPrune drops entries whose directory is gone but keeps live ones.
func TestPrune(t *testing.T) {
	live := t.TempDir()
	gone := filepath.Join(t.TempDir(), "nope")
	r := &Registry{Projects: []Project{
		{Path: live, Name: "live"},
		{Path: gone, Name: "gone"},
	}}
	r.Prune()
	if len(r.Projects) != 1 || r.Projects[0].Path != live {
		t.Fatalf("Prune kept wrong set: %+v", r.Projects)
	}
}

// TestRemoveAbsent covers Remove being a no-op for an unknown path and removing
// a known one.
func TestRemoveAbsent(t *testing.T) {
	live := t.TempDir()
	r := &Registry{Projects: []Project{{Path: live, Name: "live"}}}
	r.Remove("/not/tracked/at/all")
	if len(r.Projects) != 1 {
		t.Error("removing an absent path should be a no-op")
	}
	r.Remove(live)
	if len(r.Projects) != 0 {
		t.Error("removing a tracked path should drop it")
	}
}

// TestAddManagedProject covers inspect marking a scaffolded project managed.
func TestAddManagedProject(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	proj := t.TempDir()
	write(t, proj, "composer.json", `{"require":{"laravel/framework":"^11"}}`)
	write(t, proj, ".keel/manifest.yaml", "framework: laravel\n")
	r := &Registry{}
	p, err := r.Add(proj)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Managed {
		t.Error("a project with a .keel manifest should be Managed")
	}
	if p.Framework != "laravel" {
		t.Errorf("framework = %q", p.Framework)
	}
}

// TestAddExpandsTilde covers Add going through Expand (a bare ~ resolves home).
func TestAddTildeToHome(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	r := &Registry{}
	p, err := r.Add("~")
	if err != nil {
		t.Fatalf("adding ~ (home) should succeed: %v", err)
	}
	home, _ := os.UserHomeDir()
	if p.Path != home {
		t.Errorf("~ should resolve to home %q, got %q", home, p.Path)
	}
}

// TestExpandNonTilde covers Expand's passthrough (no home lookup) branch.
func TestExpandNonTilde(t *testing.T) {
	if got := Expand("relative/path"); got != "relative/path" {
		t.Errorf("Expand(relative) = %q", got)
	}
	if got := Expand("  spaced  "); got != "spaced" {
		t.Errorf("Expand should trim, got %q", got)
	}
}

// TestIsMonorepoVariants covers each monorepo signal and the negative case.
func TestIsMonorepoVariants(t *testing.T) {
	// turbo.json
	d1 := t.TempDir()
	write(t, d1, "turbo.json", "{}")
	if !IsMonorepo(d1) {
		t.Error("turbo.json should mark a monorepo")
	}
	// package.json workspaces array
	d2 := t.TempDir()
	write(t, d2, "package.json", `{"workspaces":["apps/*"]}`)
	if !IsMonorepo(d2) {
		t.Error("package.json workspaces should mark a monorepo")
	}
	// plain single-package repo
	d3 := t.TempDir()
	write(t, d3, "package.json", `{"name":"solo"}`)
	if IsMonorepo(d3) {
		t.Error("a plain package.json is not a monorepo")
	}
	// nothing
	if IsMonorepo(t.TempDir()) {
		t.Error("empty dir is not a monorepo")
	}
}

// TestWorkspaceGlobsFromPackageJsonArray covers the package.json "workspaces"
// array form (no pnpm-workspace.yaml).
func TestWorkspaceGlobsFromPackageJsonArray(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"workspaces":["apps/*","libs/*"]}`)
	globs := workspaceGlobs(dir)
	if len(globs) != 2 || globs[0] != "apps/*" || globs[1] != "libs/*" {
		t.Errorf("array workspaces globs = %v", globs)
	}
}

// TestWorkspaceGlobsFromPackageJsonObject covers the {"packages":[...]} object
// form of "workspaces".
func TestWorkspaceGlobsFromPackageJsonObject(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"workspaces":{"packages":["services/*"]}}`)
	globs := workspaceGlobs(dir)
	if len(globs) != 1 || globs[0] != "services/*" {
		t.Errorf("object workspaces globs = %v", globs)
	}
}

// TestWorkspaceGlobsFallback covers the default apps/packages/services layout
// when no workspace config is present.
func TestWorkspaceGlobsFallback(t *testing.T) {
	globs := workspaceGlobs(t.TempDir())
	want := []string{"apps/*", "packages/*", "services/*"}
	if len(globs) != len(want) {
		t.Fatalf("fallback globs = %v", globs)
	}
	for i := range want {
		if globs[i] != want[i] {
			t.Errorf("fallback globs = %v, want %v", globs, want)
		}
	}
}

// TestExpandGlobLiteralAndDoubleStar covers expandGlob's /** suffix, a literal
// directory path, a quoted pattern, and a non-matching literal.
func TestExpandGlobVariants(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "apps", "web"), 0o755)
	os.MkdirAll(filepath.Join(root, "apps", ".hidden"), 0o755) // hidden -> skipped
	os.MkdirAll(filepath.Join(root, "single"), 0o755)

	// /** suffix
	got := expandGlob(root, "apps/**")
	if len(got) != 1 || filepath.Base(got[0]) != "web" {
		t.Errorf("apps/** = %v (hidden dir must be skipped)", got)
	}
	// quoted /* pattern
	got = expandGlob(root, "'apps/*'")
	if len(got) != 1 || filepath.Base(got[0]) != "web" {
		t.Errorf("quoted apps/* = %v", got)
	}
	// literal existing dir
	got = expandGlob(root, "single")
	if len(got) != 1 || filepath.Base(got[0]) != "single" {
		t.Errorf("literal single = %v", got)
	}
	// literal missing dir -> nil
	if got := expandGlob(root, "missing"); got != nil {
		t.Errorf("missing literal should be nil, got %v", got)
	}
	// base dir of a glob that doesn't exist -> nil
	if got := expandGlob(root, "nope/*"); got != nil {
		t.Errorf("nonexistent glob base should be nil, got %v", got)
	}
}

// TestMembersDedupsAndSkips covers Members deduping overlapping globs and
// skipping stackless packages.
func TestMembersDedupsAndSkips(t *testing.T) {
	root := t.TempDir()
	write(t, root, "pnpm-workspace.yaml", "packages:\n  - 'apps/*'\n  - 'apps/*'\n") // duplicate glob
	write(t, root, "apps/api/manage.py", "# django")
	os.MkdirAll(filepath.Join(root, "apps", "empty"), 0o755) // no stack -> skipped

	members := Members(root)
	if len(members) != 1 {
		t.Fatalf("expected 1 detected member, got %d (%+v)", len(members), members)
	}
	if members[0].Name != "api" || members[0].Framework != "django" {
		t.Errorf("member = %+v", members[0])
	}
}
