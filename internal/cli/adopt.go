package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/project"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/spf13/cobra"
)

func adoptCmd() *cobra.Command {
	c := &cobra.Command{
		Use: "adopt [path]",
		Example: "  keel adopt                                      # take over the project you are in\n" +
			"  keel adopt ~/code/myshop\n",
		Short: "Adopt an existing project so keel can manage it (writes .keel/manifest.yaml)",
		Long: "Detects the stack + local dev environment of an existing project and writes\n" +
			"a .keel/manifest.yaml, so `keel db`, `keel secrets`, `keel deploy` and the\n" +
			"studio can manage it. Only that one file is added; nothing else is touched.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			dir := "."
			if len(args) == 1 {
				dir = project.Expand(args[0])
			}
			m, err := adoptDir(dir)
			if err != nil {
				return err
			}
			// Track it, which is what "keel-managed" has to mean. The manifest
			// alone is not enough: the studio only offers a project it can find
			// in the registry, so an adopted one was rejected as "not a tracked
			// keel project" by the very surface this command says can manage it.
			//
			// Best effort. A registry that cannot be written is worth a warning,
			// not a failure - the manifest is on disk and the adoption stands.
			trackProject(out, mustAbs(dir))
			name := filepath.Base(mustAbs(dir))
			if m.Kind == engine.KindMonorepo {
				fmt.Fprintf(out, "✓ adopted %s (monorepo, %d members). Now keel-managed\n", name, len(m.Members))
				if m.Services != nil && m.Services.DB != nil {
					db := m.Services.DB
					provider := db.Provider
					if provider == "" {
						provider = "self-hosted"
					}
					fmt.Fprintf(out, "  shared backend: %s (%s), inherited by every member\n", db.Engine, provider)
				}
				// A root-launch workspace runs every member from one root command,
				// so keel does not give the members a Docker/env of their own —
				// `keel run dev` at the root (or a member) drives the launcher.
				if m.Services != nil && m.Services.Launch != nil {
					l := m.Services.Launch
					fmt.Fprintf(out, "  root-launch: members run together via `%s`, no per-member env\n", engine.LaunchCommandHint(l))
					fmt.Fprintln(out, "  next: keel secrets sync · keel db migrate · keel run dev")
					return nil
				}
				fmt.Fprintln(out, "  next: keel secrets sync · keel db migrate")
				return nil
			}
			fmt.Fprintf(out, "✓ adopted %s (%s / %s). Now keel-managed\n", name, m.Framework, m.Env)
			fmt.Fprintln(out, "  next: keel secrets sync · keel db migrate · keel deploy")
			return nil
		},
	}
	return c
}

func mustAbs(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// adoptDir detects a directory's stack + env and writes a keel manifest for it.
// Idempotent: if a manifest already exists it is returned unchanged.
func adoptDir(dir string) (*engine.Manifest, error) {
	if m, err := engine.ReadManifest(dir); err == nil {
		return m, nil // already managed
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("no such directory: %s", dir)
	}
	// A monorepo root is adopted as a whole: keel records its members and the
	// one backend they share (see project.BuildMonorepoManifest), so a member
	// inherits the root's DB/env instead of each app re-deriving one. This is the
	// MyFamilyInfo case — seven apps on a single hosted Supabase.
	if project.IsMonorepo(dir) {
		m := project.BuildMonorepoManifest(dir)
		if err := engine.WriteManifestFile(dir, m); err != nil {
			return nil, err
		}
		return m, nil
	}
	fw := project.Detect(dir)
	if fw == "" {
		return nil, fmt.Errorf("could not detect a stack in %s (no composer.json/manage.py/pyproject/package.json)", dir)
	}
	// A member of a root-launch workspace has no runtime of its own: the whole
	// workspace launches from ONE root command. Adopting it must NOT pick a
	// per-member env/webserver (a Docker/DDEV/Sail the member never runs) — it
	// inherits the root's DB/env and runs via the root launcher. Record it with
	// no env, so `keel run` resolves the root command instead of a per-member
	// spin-up. A member NOT under a root-launch workspace keeps the old path.
	if _, launch := project.EffectiveLaunch(dir); launch != nil {
		m := &engine.Manifest{Framework: fw, Env: "", Recipes: []string{fw}}
		if err := engine.WriteManifestFile(dir, m); err != nil {
			return nil, err
		}
		return m, nil
	}
	reg, err := catalog.Registry()
	if err != nil {
		return nil, err
	}
	envID := resolveEnvID(reg, fw, project.DetectEnv(dir))
	m := &engine.Manifest{Framework: fw, Env: envID, Recipes: []string{fw, envID}}
	if err := engine.WriteManifestFile(dir, m); err != nil {
		return nil, err
	}
	return m, nil
}

// resolveEnvID picks the framework's env recipe id best matching a detected kind
// (ddev/sail/docker/local), falling back to the framework's default env.
func resolveEnvID(reg *recipe.Registry, fw, kind string) string {
	envs := reg.ForFramework(fw, recipe.Env)
	// exact-ish match: recipe id contains the kind, or (for docker) it provides docker
	for _, e := range envs {
		if strings.Contains(strings.ToLower(e.ID), kind) {
			return e.ID
		}
	}
	if kind == "docker" {
		for _, e := range envs {
			for _, p := range e.Provides {
				if p == "docker" {
					return e.ID
				}
			}
		}
	}
	for _, e := range envs {
		if e.Default {
			return e.ID
		}
	}
	if len(envs) > 0 {
		return envs[0].ID
	}
	return kind
}

// trackProject records a directory in the projects registry, so every surface
// can find it.
//
// Shared by `keel new` and `keel adopt` because both produce a keel-managed
// project and neither used to say so anywhere but on disk. Only the studio's own
// build endpoint registered anything, which meant the studio's Projects area was
// empty for everyone who used the CLI, and a plugin screen opened against a
// project built minutes earlier answered "not a tracked keel project".
//
// Best effort by design: the project exists either way, and a registry write
// failing is not a reason to fail a build that has already succeeded.
func trackProject(out io.Writer, dir string) {
	reg, err := project.Load()
	if err != nil {
		fmt.Fprintf(out, "  note: could not read the projects list (%v)\n", err)
		return
	}
	if _, err := reg.Add(dir); err != nil {
		fmt.Fprintf(out, "  note: could not track %s (%v)\n", dir, err)
		return
	}
	if err := reg.Save(); err != nil {
		fmt.Fprintf(out, "  note: could not save the projects list (%v)\n", err)
	}
}
