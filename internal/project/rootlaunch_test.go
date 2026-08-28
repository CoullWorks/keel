package project

import (
	"path/filepath"
	"testing"

	"github.com/coullworks/keel/internal/engine"
)

// TestRootLaunchDetection covers the "launched from the root" signal across the
// package managers and workspace shapes, plus the negatives (a workspace with no
// root script, and a standalone project) that must NOT be root-launch.
func TestRootLaunchDetection(t *testing.T) {
	cases := []struct {
		name    string
		files   map[string]string
		wantMgr string
		wantDev string
	}{
		{
			name: "pnpm-workspace-with-root-dev",
			files: map[string]string{
				"pnpm-workspace.yaml":   "packages:\n  - 'apps/*'\n",
				"package.json":          `{"name":"root","scripts":{"dev":"turbo run dev"}}`,
				"apps/web/package.json": `{"name":"web","dependencies":{"next":"15"}}`,
			},
			wantMgr: "pnpm", wantDev: "dev",
		},
		{
			name: "packageManager-field-wins",
			files: map[string]string{
				"package.json":          `{"name":"root","private":true,"packageManager":"yarn@4.1.0","workspaces":["apps/*"],"scripts":{"dev":"yarn workspaces foreach run dev"}}`,
				"apps/web/package.json": `{"name":"web"}`,
			},
			wantMgr: "yarn", wantDev: "dev",
		},
		{
			name: "npm-workspace-lockfile",
			files: map[string]string{
				"package.json":              `{"name":"root","workspaces":["packages/*"],"scripts":{"dev":"npm run dev --workspaces"}}`,
				"package-lock.json":         `{}`,
				"packages/api/package.json": `{"name":"api"}`,
			},
			wantMgr: "npm", wantDev: "dev",
		},
		{
			name: "turbo-launches-without-root-dev-script",
			files: map[string]string{
				"turbo.json":            `{"pipeline":{"dev":{}}}`,
				"package.json":          `{"name":"root","workspaces":["apps/*"]}`,
				"apps/web/package.json": `{"name":"web","scripts":{"dev":"next dev"}}`,
			},
			wantMgr: "turbo", wantDev: "dev",
		},
		{
			name: "start-script-when-no-dev",
			files: map[string]string{
				"pnpm-workspace.yaml":   "packages:\n  - 'apps/*'\n",
				"package.json":          `{"name":"root","scripts":{"start":"pnpm -r start"}}`,
				"apps/web/package.json": `{"name":"web"}`,
			},
			wantMgr: "pnpm", wantDev: "start",
		},
		{
			name: "workspace-with-no-root-script-is-not-root-launch",
			files: map[string]string{
				"pnpm-workspace.yaml":      "packages:\n  - 'packages/*'\n",
				"package.json":             `{"name":"root","private":true}`,
				"packages/ui/package.json": `{"name":"ui"}`,
			},
			wantMgr: "", wantDev: "",
		},
		{
			name: "standalone-project-is-not-root-launch",
			files: map[string]string{
				"package.json": `{"name":"solo","scripts":{"dev":"next dev"},"dependencies":{"next":"15"}}`,
			},
			wantMgr: "", wantDev: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for path, body := range tc.files {
				write(t, dir, path, body)
			}
			l := RootLaunch(dir)
			if l.Manager != tc.wantMgr {
				t.Errorf("manager = %q, want %q", l.Manager, tc.wantMgr)
			}
			if l.DevScript != tc.wantDev {
				t.Errorf("dev script = %q, want %q", l.DevScript, tc.wantDev)
			}
		})
	}
}

// TestRootRunCommand covers the run-resolution: the whole-workspace command for
// the root (or a member with no script of its own), and the member-scoped
// --filter form for a member that defines the launch script.
func TestRootRunCommand(t *testing.T) {
	root := t.TempDir()
	memberWithScript := filepath.Join(root, "apps", "web")
	memberNoScript := filepath.Join(root, "apps", "docs")
	write(t, root, "apps/web/package.json", `{"name":"@acme/web","scripts":{"dev":"next dev"}}`)
	write(t, root, "apps/docs/package.json", `{"name":"@acme/docs"}`) // no dev script

	cases := []struct {
		name   string
		launch engine.Launch
		member string
		want   string
	}{
		{"pnpm-root", engine.Launch{Manager: "pnpm", DevScript: "dev"}, root, "pnpm dev"},
		{"pnpm-member-filtered", engine.Launch{Manager: "pnpm", DevScript: "dev"}, memberWithScript, "pnpm --filter @acme/web dev"},
		{"pnpm-member-no-script-falls-back-to-root", engine.Launch{Manager: "pnpm", DevScript: "dev"}, memberNoScript, "pnpm dev"},
		{"yarn-member-filtered", engine.Launch{Manager: "yarn", DevScript: "dev"}, memberWithScript, "yarn workspace @acme/web dev"},
		{"npm-member-filtered", engine.Launch{Manager: "npm", DevScript: "dev"}, memberWithScript, "npm run dev --workspace @acme/web"},
		{"npm-root-run-verb", engine.Launch{Manager: "npm", DevScript: "dev"}, root, "npm run dev"},
		{"npm-root-start-verb", engine.Launch{Manager: "npm", DevScript: "start"}, root, "npm start"},
		{"turbo-member-filtered", engine.Launch{Manager: "turbo", DevScript: "dev"}, memberWithScript, "turbo dev --filter @acme/web"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RootRunCommand(root, &tc.launch, tc.member)
			if got != tc.want {
				t.Errorf("RootRunCommand = %q, want %q", got, tc.want)
			}
		})
	}

	// A nil launch resolves to "" so a standalone project falls back to its own run.
	if got := RootRunCommand(root, nil, memberWithScript); got != "" {
		t.Errorf("nil launch should give empty command, got %q", got)
	}
}

// TestRootRunCommandMemberOwnScript is the regression for the reported bug: `keel
// run` in a member must scope to THAT member (its own dev/start script), not fan
// out to the whole workspace. When a member's runnable script differs from the
// root task name, keel still runs it singly with the member's own script — and
// only a member with no runnable script at all falls back to the whole-workspace
// launch. Covers all four managers so the single-member selector is right for each.
func TestRootRunCommandMemberOwnScript(t *testing.T) {
	root := t.TempDir()
	// Member whose only runnable script is "start" (root task is "dev").
	write(t, root, "apps/api/package.json", `{"name":"@acme/api","scripts":{"start":"node server.js"}}`)
	// Member with NO scripts at all — nothing single to run.
	write(t, root, "apps/blank/package.json", `{"name":"@acme/blank"}`)
	// Member whose package.json has no "name" — cannot be targeted by a selector.
	write(t, root, "apps/anon/package.json", `{"scripts":{"dev":"next dev"}}`)

	startMember := filepath.Join(root, "apps", "api")
	blankMember := filepath.Join(root, "apps", "blank")
	anonMember := filepath.Join(root, "apps", "anon")

	cases := []struct {
		name   string
		launch engine.Launch
		member string
		want   string
	}{
		// Root task "dev", member only has "start": run the member singly with its
		// OWN script, per manager — not the whole-workspace "dev".
		{"pnpm-member-start", engine.Launch{Manager: "pnpm", DevScript: "dev"}, startMember, "pnpm --filter @acme/api start"},
		{"turbo-member-start", engine.Launch{Manager: "turbo", DevScript: "dev"}, startMember, "turbo start --filter @acme/api"},
		{"yarn-member-start", engine.Launch{Manager: "yarn", DevScript: "dev"}, startMember, "yarn workspace @acme/api start"},
		{"npm-member-start", engine.Launch{Manager: "npm", DevScript: "dev"}, startMember, "npm run start --workspace @acme/api"},
		// A member with no runnable script falls back to the whole-workspace launch.
		{"member-no-script-falls-back", engine.Launch{Manager: "pnpm", DevScript: "dev"}, blankMember, "pnpm dev"},
		// A member with no package name cannot be selected: whole-workspace launch.
		{"member-no-name-falls-back", engine.Launch{Manager: "pnpm", DevScript: "dev"}, anonMember, "pnpm dev"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RootRunCommand(root, &tc.launch, tc.member); got != tc.want {
				t.Errorf("RootRunCommand = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRootLaunchEdges covers the negative/edge branches: an unrecognised
// packageManager field, malformed JSON, and a member with no package.json.
func TestRootLaunchEdges(t *testing.T) {
	t.Run("unknown packageManager falls through to lockfile", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "pnpm-workspace.yaml", "packages:\n  - 'apps/*'\n")
		// bun@1 is not a manager keel launches from, so managerName returns "" and
		// detection falls through to the pnpm-workspace marker.
		write(t, dir, "package.json", `{"name":"root","packageManager":"bun@1.0.0","scripts":{"dev":"x"}}`)
		if l := RootLaunch(dir); l.Manager != "pnpm" {
			t.Errorf("manager = %q, want pnpm (fell through the unknown field)", l.Manager)
		}
	})
	t.Run("malformed root package.json = no launch", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "pnpm-workspace.yaml", "packages:\n  - 'apps/*'\n")
		write(t, dir, "package.json", `{not json`)
		if l := RootLaunch(dir); l.Manager != "" {
			t.Errorf("a broken package.json should yield no launch, got %+v", l)
		}
	})
	t.Run("member run with malformed member package.json falls back to root", func(t *testing.T) {
		root := t.TempDir()
		member := filepath.Join(root, "apps", "web")
		write(t, root, "apps/web/package.json", `{oops`)
		l := engine.Launch{Manager: "pnpm", DevScript: "dev"}
		if got := RootRunCommand(root, &l, member); got != "pnpm dev" {
			t.Errorf("a member keel cannot read must fall back to the root launch, got %q", got)
		}
	})
}

// TestBuildMonorepoManifestRecordsRootLaunch asserts a root-launch workspace's
// manifest carries the launch signal (so setup + run read it), and that an
// ordinary workspace with no root script records no launch (unchanged).
func TestBuildMonorepoManifestRecordsRootLaunch(t *testing.T) {
	t.Run("records launch", func(t *testing.T) {
		mono := t.TempDir()
		write(t, mono, "pnpm-workspace.yaml", "packages:\n  - 'apps/*'\n")
		write(t, mono, "package.json", `{"name":"root","private":true,"scripts":{"dev":"turbo run dev"}}`)
		write(t, mono, "apps/web/package.json", `{"name":"web","dependencies":{"next":"15"}}`)

		m := BuildMonorepoManifest(mono)
		if m.Services == nil || m.Services.Launch == nil {
			t.Fatalf("expected a recorded launch, got services=%+v", m.Services)
		}
		if m.Services.Launch.Manager != "pnpm" || m.Services.Launch.DevScript != "dev" {
			t.Errorf("launch = %+v, want pnpm/dev", m.Services.Launch)
		}
	})
	t.Run("no launch for a library-only workspace", func(t *testing.T) {
		mono := t.TempDir()
		write(t, mono, "pnpm-workspace.yaml", "packages:\n  - 'packages/*'\n")
		write(t, mono, "package.json", `{"name":"root","private":true}`) // no dev script
		write(t, mono, "packages/ui/package.json", `{"name":"ui"}`)

		m := BuildMonorepoManifest(mono)
		if m.Services != nil && m.Services.Launch != nil {
			t.Errorf("a workspace with no root script must not record a launch: %+v", m.Services.Launch)
		}
	})
}

// TestEffectiveLaunchInheritsAndDegrades asserts a member resolves its launch
// from the adopted root manifest, and a member NOT under a root-launch root gets
// a nil launch (so it degrades to its own run path).
func TestEffectiveLaunchInheritsAndDegrades(t *testing.T) {
	mono := t.TempDir()
	write(t, mono, "pnpm-workspace.yaml", "packages:\n  - 'apps/*'\n")
	write(t, mono, "package.json", `{"name":"root","private":true,"scripts":{"dev":"turbo run dev"}}`)
	write(t, mono, "apps/web/package.json", `{"name":"@acme/web","scripts":{"dev":"next dev"}}`)

	m := BuildMonorepoManifest(mono)
	if err := engine.WriteManifestFile(mono, m); err != nil {
		t.Fatal(err)
	}

	member := filepath.Join(mono, "apps", "web")
	rootDir, launch := EffectiveLaunch(member)
	if launch == nil {
		t.Fatal("member should inherit the root launch")
	}
	if rootDir != mono {
		t.Errorf("root dir = %q, want %q", rootDir, mono)
	}
	if got := RootRunCommand(rootDir, launch, member); got != "pnpm --filter @acme/web dev" {
		t.Errorf("member run command = %q, want the filtered root launcher", got)
	}

	// A standalone project (no root-launch root above it) has no inherited launch.
	solo := t.TempDir()
	write(t, solo, "package.json", `{"name":"solo","scripts":{"dev":"next dev"},"dependencies":{"next":"15"}}`)
	if _, l := EffectiveLaunch(solo); l != nil {
		t.Errorf("a standalone project must not inherit a launch, got %+v", l)
	}
}

// TestEffectiveLaunchLiveFallback covers the unadopted case: a tracked-but-not-
// adopted pnpm/turbo workspace (no .keel/manifest.yaml) still resolves its root
// launch live, from BOTH the root itself and a member under it. A standalone
// project, and a library-only workspace with no root script, still degrade to nil.
func TestEffectiveLaunchLiveFallback(t *testing.T) {
	t.Run("unadopted workspace root resolves its own launch live", func(t *testing.T) {
		mono := t.TempDir()
		write(t, mono, "pnpm-workspace.yaml", "packages:\n  - 'apps/*'\n")
		write(t, mono, "turbo.json", `{"pipeline":{"dev":{}}}`)
		write(t, mono, "package.json", `{"name":"root","private":true,"scripts":{"dev":"turbo run dev"}}`)
		write(t, mono, "apps/web/package.json", `{"name":"@acme/web","dependencies":{"next":"15"}}`)
		// No manifest written — this is the tracked-but-unadopted case.

		rootDir, launch := EffectiveLaunch(mono)
		if launch == nil {
			t.Fatal("an unadopted workspace root should resolve its launch live")
		}
		if rootDir != mono {
			t.Errorf("root dir = %q, want %q", rootDir, mono)
		}
		// turbo wins over pnpm when a turbo.json is present (RootLaunch preference).
		if launch.Manager != "turbo" || launch.DevScript != "dev" {
			t.Errorf("launch = %+v, want turbo/dev", launch)
		}
	})
	t.Run("unadopted workspace member resolves the root launch live", func(t *testing.T) {
		mono := t.TempDir()
		write(t, mono, "pnpm-workspace.yaml", "packages:\n  - 'apps/*'\n")
		write(t, mono, "package.json", `{"name":"root","private":true,"scripts":{"dev":"pnpm -r dev"}}`)
		write(t, mono, "apps/web/package.json", `{"name":"@acme/web","dependencies":{"next":"15"}}`)

		member := filepath.Join(mono, "apps", "web")
		rootDir, launch := EffectiveLaunch(member)
		if launch == nil {
			t.Fatal("a member of an unadopted workspace should resolve the root launch live")
		}
		if rootDir != mono {
			t.Errorf("root dir = %q, want %q", rootDir, mono)
		}
		if launch.Manager != "pnpm" || launch.DevScript != "dev" {
			t.Errorf("launch = %+v, want pnpm/dev", launch)
		}
	})
	t.Run("unadopted library-only workspace stays nil", func(t *testing.T) {
		mono := t.TempDir()
		write(t, mono, "pnpm-workspace.yaml", "packages:\n  - 'packages/*'\n")
		write(t, mono, "package.json", `{"name":"root","private":true}`) // no root dev script
		write(t, mono, "packages/ui/package.json", `{"name":"ui"}`)
		if _, l := EffectiveLaunch(mono); l != nil {
			t.Errorf("a library-only workspace has no root launch, got %+v", l)
		}
	})
	t.Run("standalone project stays nil", func(t *testing.T) {
		solo := t.TempDir()
		write(t, solo, "package.json", `{"name":"solo","scripts":{"dev":"next dev"},"dependencies":{"next":"15"}}`)
		if _, l := EffectiveLaunch(solo); l != nil {
			t.Errorf("a standalone project has no root launch, got %+v", l)
		}
	})
}

// TestInspectSetsLaunchCommand asserts inspect() surfaces the whole-workspace root
// launch command on a monorepo ROOT (live, no manifest), and leaves it empty for a
// library-only workspace and a standalone project — the signal the studio's root
// view reads to offer a Run.
func TestInspectSetsLaunchCommand(t *testing.T) {
	t.Run("monorepo root carries the live launch command", func(t *testing.T) {
		mono := t.TempDir()
		write(t, mono, "pnpm-workspace.yaml", "packages:\n  - 'apps/*'\n")
		write(t, mono, "turbo.json", `{"pipeline":{"dev":{}}}`)
		write(t, mono, "package.json", `{"name":"root","private":true,"scripts":{"dev":"turbo run dev"}}`)
		write(t, mono, "apps/web/package.json", `{"name":"web","dependencies":{"next":"15"}}`)

		p := inspect(mono)
		if p.Framework != "monorepo" {
			t.Fatalf("framework = %q, want monorepo", p.Framework)
		}
		if p.LaunchCommand != "turbo dev" {
			t.Errorf("LaunchCommand = %q, want %q", p.LaunchCommand, "turbo dev")
		}
	})
	t.Run("library-only workspace has no launch command", func(t *testing.T) {
		mono := t.TempDir()
		write(t, mono, "pnpm-workspace.yaml", "packages:\n  - 'packages/*'\n")
		write(t, mono, "package.json", `{"name":"root","private":true}`)
		write(t, mono, "packages/ui/package.json", `{"name":"ui"}`)
		if p := inspect(mono); p.LaunchCommand != "" {
			t.Errorf("LaunchCommand = %q, want empty for a library-only workspace", p.LaunchCommand)
		}
	})
	t.Run("standalone project has no launch command", func(t *testing.T) {
		solo := t.TempDir()
		write(t, solo, "package.json", `{"name":"solo","scripts":{"dev":"next dev"},"dependencies":{"next":"15"}}`)
		if p := inspect(solo); p.LaunchCommand != "" {
			t.Errorf("LaunchCommand = %q, want empty for a standalone project", p.LaunchCommand)
		}
	})
}
