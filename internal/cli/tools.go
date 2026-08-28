package cli

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/coullworks/keel/internal/platform"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

// toolReq is one external tool a plan needs on the host.
type toolReq struct {
	name, reason, hint string
	auto               bool // ensureTools can auto-install it (docker/ddev)
}

// requiredTools returns the host tools a plan needs. Containerised envs
// (ddev/docker/sail) run composer/php/uv/node inside the container, so the host
// only needs docker (+ the env binary). A Local env needs the language toolchain
// on the host.
func requiredTools(plan *resolver.Plan) []toolReq {
	var reqs []toolReq
	containerized := false
	envBin, lang := "", ""
	for _, r := range plan.Recipes {
		switch r.Kind {
		case recipe.Env:
			for _, p := range r.Provides {
				if p == "docker" {
					containerized = true
				}
			}
			envBin = r.Bin
			if envBin == "" {
				envBin = r.ID
			}
		case recipe.Framework:
			lang = r.Lang
		}
	}
	if containerized {
		reqs = append(reqs, toolReq{"docker", "the container dev environment", "https://docs.docker.com/get-docker/", true})
		if envBin == "ddev" {
			reqs = append(reqs, toolReq{"ddev", "the DDEV environment", "https://ddev.readthedocs.io/en/stable/users/install/", true})
		}
	} else { // Local env → the language toolchain must be on the host
		switch lang {
		case "php":
			reqs = append(reqs, toolReq{"php", "running PHP locally", "install PHP 8.3+ (herd.laravel.com / your package manager)", false},
				toolReq{"composer", "PHP dependencies", "https://getcomposer.org/download/", false})
		case "python":
			reqs = append(reqs, toolReq{"uv", "Python deps + venv", "https://docs.astral.sh/uv/getting-started/installation/", false})
		case "node":
			reqs = append(reqs, toolReq{"node", "running Node locally", "https://nodejs.org (or fnm/volta)", false})
		}
	}
	reqs = append(reqs, toolReq{"git", "version control + the git extra", "https://git-scm.com/downloads", false})
	return reqs
}

// preflight prints a required-tools checklist for the plan and returns an error
// if a tool keel can't auto-install is missing (so the real installers never run
// into a cryptic failure). docker/ddev aren't hard-blocked here — ensureTools
// offers to install them next.
func preflight(plan *resolver.Plan, out io.Writer) error {
	reqs := requiredTools(plan)
	var blocking []toolReq
	fmt.Fprintln(out, "pre-flight, required tools:")
	for _, r := range reqs {
		if _, err := exec.LookPath(r.name); err == nil {
			fmt.Fprintf(out, "  ✓ %s\n", r.name)
			continue
		}
		if r.auto {
			fmt.Fprintf(out, "  … %s (not found, keel can install it)\n", r.name)
			continue
		}
		fmt.Fprintf(out, "  ✗ %s: needed for %s\n      → %s\n", r.name, r.reason, r.hint)
		blocking = append(blocking, r)
	}
	if len(blocking) > 0 {
		names := make([]string, len(blocking))
		for i, r := range blocking {
			names[i] = r.name
		}
		return fmt.Errorf("missing required tool(s): %s - install them (see hints above) and re-run", strings.Join(names, ", "))
	}
	return nil
}

// ensureTools makes sure the external tools the plan's env needs (docker, ddev)
// are installed, auto-installing them per-platform with the user's consent.
func ensureTools(ctx context.Context, out io.Writer, plan *resolver.Plan) error {
	for _, t := range neededTools(plan) {
		consent := func(prompt string) bool {
			ok := false
			_ = huh.NewConfirm().Title(prompt).Value(&ok).Run()
			return ok
		}
		if err := platform.EnsureTool(ctx, t, consent, out); err != nil {
			return err
		}
	}
	return nil
}

// neededTools returns the external tools required by the plan's env, docker first
// (ddev needs it running).
func neededTools(plan *resolver.Plan) []string {
	var docker, ddev bool
	for _, r := range plan.Recipes {
		for _, p := range r.Provides {
			if p == "docker" {
				docker = true
			}
		}
		if r.Kind == recipe.Env {
			bin := r.Bin
			if bin == "" {
				bin = r.ID
			}
			if bin == "ddev" {
				ddev = true
			}
		}
	}
	var out []string
	if docker {
		out = append(out, "docker")
	}
	if ddev {
		out = append(out, "ddev")
	}
	return out
}
