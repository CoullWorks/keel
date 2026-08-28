// This file surfaces a monorepo member's effective (inherited) backend to the
// studio, so a member's detail view resolves its shared database once from the
// monorepo root instead of each member re-deriving a local one.
//
// The area-first studio only ever gave a monorepo member a "Data" button, and
// that button re-derived a local DSN — so a Next.js member on a hosted Supabase
// project resolved to a local MySQL (the per-member DB bug the fitness review
// records). The project-first studio opens a member's own detail view with the
// full action set; this endpoint tells that view which shared backend the member
// actually uses (engine + provider + which env file the credential lives in),
// via project.EffectiveBackend, without copying any secret across the boundary.
package studio

import (
	"net/http"

	"github.com/coullworks/keel/internal/project"
)

// backendDTO is a member's effective backend in the studio's stable JSON shape.
// It names where the shared credential lives, never the credential itself: this
// crosses an HTTP boundary into a browser, and the DB block only records an env
// reference (project.EffectiveBackend never reads a secret).
type backendDTO struct {
	// Inherited is true when the backend came from a monorepo root the member
	// inherits, so the UI can label it "shared" rather than the member's own.
	Inherited bool `json:"inherited"`
	// EnvDir is the directory whose env file(s) carry the member's real
	// credentials — the monorepo root for an inheriting member, else itself.
	EnvDir string `json:"envDir"`
	// EnvFile is the specific env file the shared credentials live in
	// (".env.local" for a Node/Next monorepo); empty means the usual precedence.
	EnvFile string `json:"envFile,omitempty"`
	// Engine/Provider/Schema describe the shared database when there is one.
	// Engine is empty when the root records no shared database.
	Engine   string `json:"engine,omitempty"`
	Provider string `json:"provider,omitempty"`
	Schema   string `json:"schema,omitempty"`
	// Source is the env reference the credential is read from (e.g.
	// ".env.local:SUPABASE_DB_URL"), so the UI can show the developer exactly
	// where the connection comes from without ever showing its value.
	Source string `json:"source,omitempty"`
	// RootLaunch is true when this member belongs to a workspace launched from
	// the root (pnpm/yarn/npm-workspace/turbo with a root dev script): its Run
	// action is the one root command, and it has no per-member env/webserver/
	// deploy of its own. False for an ordinary monorepo member or a standalone
	// project, so the UI keeps the existing per-member actions.
	RootLaunch bool `json:"rootLaunch,omitempty"`
	// LaunchManager is the workspace's package manager ("pnpm" | "yarn" | "npm"
	// | "turbo"), and LaunchCommand the exact command the member's Run resolves
	// to (e.g. "pnpm dev" or "pnpm --filter web dev"), so the member view can
	// label "launched from the workspace root (pnpm dev)".
	LaunchManager string `json:"launchManager,omitempty"`
	LaunchCommand string `json:"launchCommand,omitempty"`
}

// handleMemberBackend returns the effective, inherited backend a monorepo member
// uses, so the member's detail view shows the one shared backend (resolved from
// the monorepo root) rather than re-deriving a wrong local DSN. It is a read
// keyed by ?dir=, guarded like /api/db/ping and /api/generators: a GET behind
// the loopback Host, same-origin, session-token and method allowlist.
//
// A directory that is not under a shared-backend root is not an error the caller
// must catch: EffectiveBackend degrades to the member's own directory
// (Inherited=false, no shared DB), so a standalone project still gets a valid,
// non-inherited answer and the UI simply shows no "shared backend" banner.
func handleMemberBackend(w http.ResponseWriter, r *http.Request) {
	dir, err := projectDir(r.URL.Query().Get("dir"))
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	b := project.EffectiveBackend(dir)
	out := backendDTO{
		Inherited: b.Inherited,
		EnvDir:    b.EnvDir,
		EnvFile:   b.EnvFile,
	}
	if b.DB != nil {
		out.Engine = b.DB.Engine
		out.Provider = b.DB.Provider
		out.Schema = b.DB.Schema
		out.Source = b.DB.Source
	}
	// Root-launch: when this member belongs to a workspace run from one root
	// command, tell the UI so it shows "launched from the workspace root" and its
	// Run resolves to that command instead of a per-member env/webserver/deploy.
	if rootDir, launch := project.EffectiveLaunch(dir); launch != nil {
		out.RootLaunch = true
		out.LaunchManager = launch.Manager
		out.LaunchCommand = project.RootRunCommand(rootDir, launch, dir)
	}
	writeJSON(w, out)
}

// isRootLaunchMember reports whether dir belongs to a root-launch workspace (it
// inherits a recorded root launch). The run handlers use it to admit a member
// that has no keel manifest of its own but runs from the workspace root — `keel
// run` resolves the root command for it. A standalone project or an ordinary
// monorepo member is not one, so the manifest requirement stands for them.
func isRootLaunchMember(dir string) bool {
	_, launch := project.EffectiveLaunch(dir)
	return launch != nil
}
