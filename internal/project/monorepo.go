package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/envfile"
)

// This file models the shared-backend relationship in a monorepo: a root
// manifest of kind "monorepo" records the one backend its members share, and a
// member reads its effective (inherited) DB/env from that root rather than each
// re-deriving a local one. The studio's DB panel and `keel db` are the intended
// consumers — they call EffectiveBackend for a member to learn which directory's
// env to read and which shared database it talks to.

// Backend is a member's effective, inherited backend: the answer to "which DB
// and env does this workspace actually use?". For a member of a shared-backend
// monorepo the env lives at the root, not in the member, so EnvDir points at the
// root; for a standalone project it points at the project itself. It is a plain
// value the studio can act on without knowing the monorepo rules.
type Backend struct {
	// EnvDir is the directory whose env file(s) carry this member's real
	// credentials — the monorepo root for an inheriting member, else the
	// member's own directory.
	EnvDir string
	// EnvFile is the specific env file within EnvDir the shared credentials
	// live in (".env.local" for a Node/Next monorepo). Empty means "the
	// caller's usual precedence" (.env then .env.local).
	EnvFile string
	// DB is the shared database's shape, copied from the root manifest's
	// services block. Nil when there is no recorded shared database.
	DB *engine.DBService
	// Inherited is true when this backend came from a monorepo root (the member
	// inherits it) rather than from the member's own manifest.
	Inherited bool
}

// EffectiveBackend resolves the DB/env a member effectively uses, walking up to
// the nearest monorepo-root manifest that lists it. When memberDir sits under a
// monorepo whose root manifest carries a shared services block, the returned
// Backend points env resolution at the root and carries the shared DB
// (Inherited = true). When there is no such root, it degrades to the member's
// own directory (Inherited = false), so a standalone project keeps working.
//
// It never reads a secret: the DB block only names where the credential lives
// (an env reference), so a consumer still opens the env file itself.
func EffectiveBackend(memberDir string) Backend {
	abs, err := filepath.Abs(Expand(memberDir))
	if err != nil {
		abs = memberDir
	}
	rootDir, root := findMonorepoRoot(abs)
	if root == nil || root.Services == nil {
		// No shared-backend root above this member: it stands alone.
		return Backend{EnvDir: abs}
	}
	return Backend{
		EnvDir:    rootDir,
		EnvFile:   root.Services.EnvFile,
		DB:        root.Services.DB,
		Inherited: true,
	}
}

// RootLaunch reports how a workspace launches from its root. A pnpm/yarn/
// npm-workspace or Turborepo whose root package.json carries a dev (or start)
// script is run from ONE root command (`pnpm dev`), which brings every member up
// together — so its members must not each get a Docker/env/webserver of their
// own. This is the signal setup and run read: IsRootLaunch is true only when dir
// is a workspace monorepo AND a package manager AND a root launch script are all
// present. A standalone project, or a workspace with no root script (a pure
// library monorepo), returns IsRootLaunch=false and everything behaves as before.
func RootLaunch(dir string) engine.Launch {
	abs, err := filepath.Abs(Expand(dir))
	if err != nil {
		abs = dir
	}
	if !IsMonorepo(abs) {
		return engine.Launch{}
	}
	mgr := packageManager(abs)
	if mgr == "" {
		return engine.Launch{}
	}
	script := rootLaunchScript(abs)
	// Turborepo's own `turbo dev` fans out to every member's dev task, so a turbo
	// workspace is root-launched even when the ROOT package.json has no dev
	// script of its own — the pipeline is the launch. For pnpm/yarn/npm the root
	// script IS the launch, so one must be present.
	if script == "" && mgr != "turbo" {
		return engine.Launch{}
	}
	if script == "" {
		script = "dev"
	}
	return engine.Launch{Manager: mgr, DevScript: script}
}

// packageManager infers the workspace's package manager. Turborepo (turbo.json)
// is recognised as its own launcher; otherwise the manager is read from the
// packageManager field, then a lockfile, then the pnpm-workspace marker, and
// defaults to npm for a bare package.json workspace. It never guesses for a
// non-workspace directory — callers gate on IsMonorepo first.
func packageManager(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, "turbo.json")); err == nil {
		return "turbo"
	}
	// The packageManager field ("pnpm@9.1.0") is the authoritative, committed
	// declaration when present — Corepack reads the same field.
	if b, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		var pkg struct {
			PackageManager string `json:"packageManager"`
		}
		if json.Unmarshal(b, &pkg) == nil {
			if name := managerName(pkg.PackageManager); name != "" {
				return name
			}
		}
	}
	for lock, mgr := range map[string]string{
		"pnpm-lock.yaml":    "pnpm",
		"yarn.lock":         "yarn",
		"package-lock.json": "npm",
	} {
		if _, err := os.Stat(filepath.Join(dir, lock)); err == nil {
			return mgr
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "pnpm-workspace.yaml")); err == nil {
		return "pnpm"
	}
	// A package.json "workspaces" field with no other signal is an npm/yarn
	// workspace; npm is the safe default (the command shape is the same).
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
		return "npm"
	}
	return ""
}

// managerName extracts the manager name from a packageManager field value
// ("pnpm@9.1.0" -> "pnpm"), accepting only the managers keel launches from.
func managerName(field string) string {
	field = strings.TrimSpace(field)
	if field == "" {
		return ""
	}
	name := field
	if i := strings.IndexByte(field, '@'); i >= 0 {
		name = field[:i]
	}
	switch name {
	case "pnpm", "yarn", "npm":
		return name
	}
	return ""
}

// rootLaunchScript returns the root package.json's launch script name, preferring
// "dev" over "start" (the dev-server convention), or "" if the root defines
// neither — in which case there is nothing to launch from the root.
func rootLaunchScript(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(b, &pkg) != nil {
		return ""
	}
	for _, name := range []string{"dev", "start"} {
		if strings.TrimSpace(pkg.Scripts[name]) != "" {
			return name
		}
	}
	return ""
}

// RootRunCommand resolves the command that runs a root-launch workspace member's
// task, scoped to ONE member — the fix for "keel run in a member started the
// whole workspace". For a root-launch workspace the answer is the ROOT package
// manager driving that one member, never a per-member env spin-up: the workspace's
// node_modules, workspace:* siblings and lockfile are products of the root install,
// so the launcher runs from the root and targets the member via the manager's
// single-member selector.
//
//   - Running the ROOT itself (memberDir == rootDir, or empty) = the whole-workspace
//     launch (`pnpm dev` / `turbo dev` / `npm run dev` / `yarn dev`), which the root
//     script fans out to every member.
//   - Running a MEMBER = the manager's single-member command scoped by the member's
//     package.json "name" (the correct --filter/--workspace selector, not its dir
//     base): `pnpm --filter <name> <script>`, `turbo <script> --filter <name>`,
//     `yarn workspace <name> <script>`, `npm run <script> --workspace <name>`. The
//     script is the member's OWN launch script — the root task name (usually "dev")
//     when the member defines it, else the member's own "dev" then "start" — so a
//     member whose runnable script differs from the root's is still run singly.
//   - A member with no package.json name, or no runnable script at all, falls back
//     to the whole-workspace launch (there is nothing single to scope to).
//
// Returns "" when the workspace is not root-launch, so a standalone project falls
// back to its own `keel run` unchanged. rootDir/launch describe the workspace root;
// memberDir is the member being run (may equal rootDir to run the whole workspace).
func RootRunCommand(rootDir string, launch *engine.Launch, memberDir string) string {
	if launch == nil || launch.Manager == "" {
		return ""
	}
	root := managerVerb(launch.Manager, launch.DevScript)
	// Running the root itself is the whole-workspace launch.
	absRoot, _ := filepath.Abs(Expand(rootDir))
	absMember, _ := filepath.Abs(Expand(memberDir))
	if absMember == "" || absMember == absRoot {
		return root
	}
	name := memberPackageName(absMember)
	script := memberScript(absMember, launch.DevScript)
	// A member keel cannot single out (no package name, or no runnable script of
	// its own) runs via the whole-workspace launch.
	if name == "" || script == "" {
		return root
	}
	return filterCommand(launch.Manager, name, script)
}

// managerVerb renders the root launch command for a manager + script. pnpm and
// yarn run a script by bare name (`pnpm dev`); npm needs the `run` verb for a
// non-lifecycle script but keeps `npm start` for start; turbo runs its pipeline
// task directly (`turbo dev`).
func managerVerb(mgr, script string) string {
	switch mgr {
	case "pnpm", "yarn":
		return mgr + " " + script
	case "turbo":
		return "turbo " + script
	case "npm":
		if script == "start" {
			return "npm start"
		}
		return "npm run " + script
	}
	return mgr + " " + script
}

// filterCommand scopes the launcher to one member package. pnpm/turbo have a
// native --filter; yarn v1 uses `workspace <name>`; npm uses `--workspace`.
func filterCommand(mgr, pkg, script string) string {
	switch mgr {
	case "pnpm":
		return "pnpm --filter " + pkg + " " + script
	case "turbo":
		return "turbo " + script + " --filter " + pkg
	case "yarn":
		return "yarn workspace " + pkg + " " + script
	case "npm":
		return "npm run " + script + " --workspace " + pkg
	}
	return managerVerb(mgr, script)
}

// memberPackageName reads a member's package.json "name" (what --filter targets),
// or "" if it has none.
func memberPackageName(memberDir string) string {
	b, err := os.ReadFile(filepath.Join(memberDir, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(b, &pkg) != nil {
		return ""
	}
	return strings.TrimSpace(pkg.Name)
}

// memberScript resolves the script keel scopes to when running one member: the
// root task name (rootScript, usually "dev") when the member defines it, else the
// member's OWN runnable script preferring "dev" then "start". It returns "" when
// the member defines none of those (nothing single to run), so the caller falls
// back to the whole-workspace launch. Reading the member's own script — rather
// than assuming the root's — is what lets a member whose runnable script differs
// from the root's still be run singly (spec A: "the member's own dev/start script
// when it has one").
func memberScript(memberDir, rootScript string) string {
	b, err := os.ReadFile(filepath.Join(memberDir, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(b, &pkg) != nil {
		return ""
	}
	has := func(name string) bool { return strings.TrimSpace(pkg.Scripts[name]) != "" }
	if rootScript != "" && has(rootScript) {
		return rootScript
	}
	for _, name := range []string{"dev", "start"} {
		if name != rootScript && has(name) {
			return name
		}
	}
	return ""
}

// EffectiveLaunch resolves the root launch a member inherits. It is the run-time
// twin of EffectiveBackend: env/DB/webserver live once at the root, and so does
// the launch. It resolves two ways so it works before AND after adoption:
//
//  1. an adopted root's recorded manifest launch (Services.Launch) — the
//     authoritative answer once `keel adopt` has written it; then
//  2. a LIVE fallback — walk up to the nearest workspace root (IsMonorepo) and
//     compute RootLaunch(rootDir) directly, so a tracked-but-unadopted pnpm/turbo
//     workspace still surfaces its root launch.
//
// Returns (rootDir, launch) with a nil launch when memberDir is not under a
// root-launch workspace, so a standalone project degrades cleanly.
func EffectiveLaunch(memberDir string) (rootDir string, launch *engine.Launch) {
	abs, err := filepath.Abs(Expand(memberDir))
	if err != nil {
		abs = memberDir
	}
	// Prefer a recorded manifest launch (the adopted case): it is the committed
	// declaration and may differ from a fresh detection.
	if dir, root := findMonorepoRoot(abs); root != nil && root.Services != nil && root.Services.Launch != nil {
		return dir, root.Services.Launch
	}
	// Fall back to live detection: the nearest workspace root's RootLaunch. This
	// makes a tracked-but-unadopted workspace resolve its launch without a manifest.
	if dir := findWorkspaceRoot(abs); dir != "" {
		if l := RootLaunch(dir); l.Manager != "" && l.DevScript != "" {
			return dir, &l
		}
	}
	return "", nil
}

// findWorkspaceRoot walks from dir upward, returning the nearest ancestor (or dir
// itself) that IsMonorepo reports as a workspace root. It mirrors findMonorepoRoot
// but keys on live detection rather than a manifest, and honours the same bounds:
// it stops at the first of a match, a VCS boundary (a directory containing .git —
// a workspace root is at or under its repo, never above it), the filesystem root,
// or maxMonorepoWalk levels. Returns "" if none is found.
func findWorkspaceRoot(dir string) string {
	cur := dir
	for depth := 0; depth < maxMonorepoWalk; depth++ {
		if IsMonorepo(cur) {
			return cur
		}
		// A .git here marks the repo boundary: a workspace root is at or below its
		// repository, so there is nothing to find above it.
		if fi, err := os.Stat(filepath.Join(cur, ".git")); err == nil && (fi.IsDir() || fi.Mode().IsRegular()) {
			return ""
		}
		parent := filepath.Dir(cur)
		if parent == cur { // reached the filesystem root
			return ""
		}
		cur = parent
	}
	return ""
}

// maxMonorepoWalk caps how many parent directories findMonorepoRoot inspects. A
// monorepo root is a handful of levels above any member; this bounds the stats on
// a pathologically deep path so the walk can never turn into an unbounded scan to
// the filesystem root.
const maxMonorepoWalk = 40

// findMonorepoRoot walks from dir upward, returning the nearest ancestor (or dir
// itself) whose .keel/manifest.yaml is a monorepo root. It stops at the first of:
// a match, the filesystem root, a VCS boundary (a directory containing .git — a
// monorepo root lives at or under its repo, never above it), or maxMonorepoWalk
// levels. Returns ("", nil) if none is found.
func findMonorepoRoot(dir string) (string, *engine.Manifest) {
	cur := dir
	for depth := 0; depth < maxMonorepoWalk; depth++ {
		if m, err := engine.ReadManifest(cur); err == nil && m.Kind == engine.KindMonorepo {
			return cur, m
		}
		// A .git here marks the repo boundary: a shared-backend monorepo root is at
		// or below its repository, so there is nothing to find above it.
		if fi, err := os.Stat(filepath.Join(cur, ".git")); err == nil && (fi.IsDir() || fi.Mode().IsRegular()) {
			return "", nil
		}
		parent := filepath.Dir(cur)
		if parent == cur { // reached the filesystem root
			return "", nil
		}
		cur = parent
	}
	return "", nil
}

// BuildMonorepoManifest assembles a monorepo-root manifest for dir: it records
// every workspace member (apps and shared libs alike) and infers the shared
// backend from the root's env. It is what `keel adopt` writes for a monorepo
// root. Detection is best-effort — a repo with no recognisable shared database
// still gets a valid manifest (Services with a nil DB), because the member list
// alone is useful.
func BuildMonorepoManifest(dir string) *engine.Manifest {
	members := Members(dir)
	mm := make([]engine.Member, 0, len(members))
	for _, m := range members {
		rel, err := filepath.Rel(dir, m.Path)
		if err != nil {
			rel = m.Path
		}
		mm = append(mm, engine.Member{Path: filepath.ToSlash(rel), Framework: m.Framework})
	}
	return &engine.Manifest{
		Kind:     engine.KindMonorepo,
		Members:  mm,
		Services: detectSharedServices(dir),
	}
}

// detectSharedServices infers the backend a monorepo's members share from the
// root's env files. It reads the root's .env/.env.local (never a member's) and
// recognises a hosted Supabase / Postgres connection, since that is the real
// case: one hosted database for the whole repo. It returns a Services block with
// the env file it read and, when found, the shared DB — recording only where the
// credential lives, never the credential.
func detectSharedServices(dir string) *engine.Services {
	envFile, get := rootEnv(dir)
	svc := &engine.Services{EnvFile: envFile}
	svc.DB = detectSharedDB(envFile, get)
	// Record root-launch when the workspace is run from one root command, so a
	// member's setup skips a per-member env/webserver and its Run resolves to the
	// root launcher. Nil for a workspace with no root script (unchanged behaviour).
	if l := RootLaunch(dir); l.Manager != "" {
		svc.Launch = &l
	}
	return svc
}

// detectSharedDB recognises the shared database from the root env. It prefers an
// explicit Supabase DB URL, then a Postgres DATABASE_URL, then a Supabase
// project URL (hosted Postgres whose direct DB URL is not in this file), and
// returns nil when the env names no database keel understands as shared.
func detectSharedDB(envFile string, get func(string) string) *engine.DBService {
	if get == nil {
		return nil
	}
	ref := func(key string) string {
		if envFile == "" {
			return key
		}
		return envFile + ":" + key
	}
	// A full Postgres DB URL is the strongest signal, and its own key wins.
	for _, key := range []string{"SUPABASE_DB_URL", "DATABASE_URL"} {
		v := strings.TrimSpace(get(key))
		if v == "" {
			continue
		}
		if key == "DATABASE_URL" && !isPostgresURL(v) {
			continue // a MySQL/other DATABASE_URL: not the Supabase/Postgres case
		}
		provider := ""
		if key == "SUPABASE_DB_URL" || strings.Contains(v, "supabase") {
			provider = "supabase"
		}
		return &engine.DBService{Engine: "postgres", Provider: provider, Source: ref(key)}
	}
	// A Supabase project URL (no direct DB URL here) still fixes engine+provider;
	// the source names the project URL the consumer resolves the DB from.
	for _, key := range []string{"NEXT_PUBLIC_SUPABASE_URL", "SUPABASE_URL"} {
		if strings.TrimSpace(get(key)) != "" {
			return &engine.DBService{Engine: "postgres", Provider: "supabase", Source: ref(key)}
		}
	}
	return nil
}

// isPostgresURL reports whether a DATABASE_URL is a Postgres connection string.
func isPostgresURL(u string) bool {
	return strings.HasPrefix(u, "postgres://") || strings.HasPrefix(u, "postgresql://")
}

// rootEnv reads a monorepo root's env with Node/Next precedence (.env.local
// overrides .env) and reports which file it should be attributed to: .env.local
// when that file exists (the Node/Next convention, where a hosted project keeps
// its real secrets), else .env. The returned getter never fails — a missing file
// yields no keys — so a repo with no env still produces a usable (nil-DB) result.
func rootEnv(dir string) (envFile string, get func(string) string) {
	merged := map[string]string{}
	envFile = ".env"
	for _, name := range []string{".env", ".env.local"} { // later file wins
		f, err := envfile.Load(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		keys := f.Keys()
		if len(keys) == 0 {
			continue
		}
		if _, statErr := os.Stat(filepath.Join(dir, name)); statErr == nil {
			envFile = name // the last present, non-empty file names the source
		}
		for _, k := range keys {
			merged[k] = f.Get(k)
		}
	}
	return envFile, func(k string) string { return merged[k] }
}
