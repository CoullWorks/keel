// Package project tracks the projects keel knows about and detects a directory's
// stack, so the console dashboard and studio can list, add and open projects.
package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/profile"
	"gopkg.in/yaml.v3"
)

// Project is one tracked project on disk.
type Project struct {
	Path      string    `yaml:"path" json:"path"`
	Name      string    `yaml:"name" json:"name"`
	Framework string    `yaml:"framework" json:"framework"`
	Members   []Project `yaml:"members,omitempty" json:"members,omitempty"` // for a monorepo: each workspace's detected stack
	Env       string    `yaml:"env,omitempty" json:"env,omitempty"`
	Managed   bool      `yaml:"managed" json:"managed"` // true if keel scaffolded it (has .keel/manifest.yaml)
	// LaunchCommand is the whole-workspace root launch command ("turbo dev",
	// "pnpm dev") for a monorepo ROOT that is run from one root command. It is
	// live-detected (no manifest required), so the studio's root view can offer a
	// Run without the workspace having been adopted. Empty for a standalone
	// project or a workspace with no root launch. Not persisted — derived per
	// inspect from the current on-disk state.
	LaunchCommand string `yaml:"-" json:"launchCommand,omitempty"`
}

// Detect infers a project's framework from marker files, so "add existing
// project" works without a keel manifest. Order matters: the most specific
// marker wins (Magento's composer name beats a generic composer.json).
func Detect(dir string) string {
	// A keel manifest is authoritative.
	if fw := manifestFramework(dir); fw != "" {
		return fw
	}
	has := func(name string) bool {
		safeBase, err := filepath.Abs(dir)
		if err != nil {
			return false
		}
		p, err := filepath.Abs(filepath.Join(dir, name))
		if err != nil || !strings.HasPrefix(p, safeBase) {
			return false
		}
		_, err = os.Stat(p)
		return err == nil
	}
	readUnder := func(name string) ([]byte, bool) {
		safeBase, err := filepath.Abs(dir)
		if err != nil {
			return nil, false
		}
		p, err := filepath.Abs(filepath.Join(dir, name))
		if err != nil || !strings.HasPrefix(p, safeBase) {
			return nil, false
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, false
		}
		return b, true
	}
	if has("composer.json") {
		if b, ok := readUnder("composer.json"); ok {
			s := string(b)
			switch {
			case strings.Contains(s, "magento/product-community-edition"),
				strings.Contains(s, "magento/magento-cloud-metapackage"):
				return "magento"
			case strings.Contains(s, "laravel/framework"):
				return "laravel"
			}
		}
		return "laravel" // a PHP project with composer, best guess
	}
	if has("manage.py") {
		return "django"
	}
	if has("pyproject.toml") {
		if b, ok := readUnder("pyproject.toml"); ok {
			l := strings.ToLower(string(b))
			if strings.Contains(l, "fastapi") {
				return "fastapi"
			}
			if strings.Contains(l, "django") {
				return "django"
			}
		}
	}
	if has("next.config.js") || has("next.config.mjs") || has("next.config.ts") {
		return "nextjs"
	}
	if has("package.json") {
		if b, ok := readUnder("package.json"); ok {
			s := string(b)
			// Expo/React Native before plain Next: an Expo app can pull Next in as
			// a web target, but its identity is the mobile framework. Match the
			// dependency keys, not a bare substring, so "@react-native-async-storage"
			// in a web app does not masquerade as a native app.
			if hasDep(s, "expo") || hasDep(s, "react-native") {
				return "expo"
			}
			if strings.Contains(s, "\"next\"") {
				return "nextjs"
			}
		}
	}
	return ""
}

// hasDep reports whether a package.json's text declares dep as a dependency,
// matching the quoted key ("expo": ...) rather than any substring, so a
// scoped/related package name does not trigger a false positive.
func hasDep(pkgJSON, dep string) bool {
	return strings.Contains(pkgJSON, `"`+dep+`":`)
}

func manifestFramework(dir string) string {
	safeBase, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	p, err := filepath.Abs(filepath.Join(dir, ".keel", "manifest.yaml"))
	if err != nil || !strings.HasPrefix(p, safeBase) {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	var m struct {
		Framework string `yaml:"framework"`
	}
	if yaml.Unmarshal(b, &m) == nil {
		return m.Framework
	}
	return ""
}

func isManaged(dir string) bool {
	safeBase, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	p, err := filepath.Abs(filepath.Join(dir, ".keel", "manifest.yaml"))
	if err != nil || !strings.HasPrefix(p, safeBase) {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// DetectEnv infers a project's local dev environment kind from its files, so
// `keel adopt` can pick the matching env recipe. Returns "ddev", "sail",
// "docker" or "local".
func DetectEnv(dir string) string {
	safeBase, err := filepath.Abs(dir)
	if err != nil {
		return "local"
	}
	under := func(rest ...string) (string, bool) {
		p, err := filepath.Abs(filepath.Join(append([]string{dir}, rest...)...))
		if err != nil || !strings.HasPrefix(p, safeBase) {
			return "", false
		}
		return p, true
	}
	if p, ok := under(".ddev"); ok {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return "ddev"
		}
	}
	if p, ok := under("vendor", "bin", "sail"); ok {
		if _, err := os.Stat(p); err == nil {
			return "sail"
		}
	}
	for _, f := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml", "Dockerfile"} {
		p, ok := under(f)
		if !ok {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return "docker"
		}
	}
	return "local"
}

// Registry is the tracked-projects list persisted next to the profile.
//
// A Registry value is not shared-safe by default: the studio, mcp and console
// each Load their own copy and mutate it in isolation, which is the intended use.
// The mutating methods (Add/Refresh/Prune/Remove/Save) nonetheless take an
// internal lock so that if one instance IS shared across goroutines, its
// Projects slice cannot be mutated concurrently and corrupt itself. The lock is
// per-Registry and cheap; it does not make two Registry values consistent with
// each other — that is still the caller's Load/Save discipline.
type Registry struct {
	Projects []Project  `yaml:"projects"`
	mu       sync.Mutex `yaml:"-"`
}

func path() string { return filepath.Join(profile.Dir(), "projects.yaml") }

// safePath resolves the registry path confined under the (trusted) profile dir,
// so the file sinks consume an Abs+HasPrefix-guarded path.
func safePath() (string, error) {
	safeBase, err := filepath.Abs(profile.Dir())
	if err != nil {
		return "", err
	}
	p, err := filepath.Abs(filepath.Join(profile.Dir(), "projects.yaml"))
	if err != nil || !strings.HasPrefix(p, safeBase) {
		return "", fmt.Errorf("refusing path outside %q", profile.Dir())
	}
	return p, nil
}

// Load reads the registry (empty if none yet).
func Load() (*Registry, error) {
	p, err := safePath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return &Registry{}, nil
	}
	if err != nil {
		return nil, err
	}
	var r Registry
	if err := yaml.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Save persists the registry, sorted by name for stable output.
func (r *Registry) Save() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	sort.SliceStable(r.Projects, func(i, j int) bool { return r.Projects[i].Name < r.Projects[j].Name })
	p, err := safePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(profile.Dir(), 0o755); err != nil {
		return err
	}
	b, err := yaml.Marshal(r)
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}

// Add registers a directory as a project, detecting its stack (or, for a
// monorepo, each workspace's stack). Handles a leading ~ and validates the dir
// exists. Idempotent on absolute path. Returns the added/updated project.
func (r *Registry) Add(dir string) (Project, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	abs, err := filepath.Abs(Expand(dir))
	if err != nil {
		return Project{}, err
	}
	d, err := filepath.Abs(abs)
	if err != nil {
		return Project{}, err
	}
	if strings.Contains(d, "..") {
		return Project{}, fmt.Errorf("refusing path outside %q", abs)
	}
	if info, err := os.Stat(d); err != nil || !info.IsDir() {
		return Project{}, fmt.Errorf("no such directory: %s", abs)
	}
	p := inspect(abs)
	for i := range r.Projects {
		if r.Projects[i].Path == abs {
			r.Projects[i] = p
			return p, nil
		}
	}
	r.Projects = append(r.Projects, p)
	return p, nil
}

// inspect detects a directory's stack (or monorepo members) + managed status.
// Fresh each call, so managed flags reflect the current on-disk state.
func inspect(abs string) Project {
	p := Project{Path: abs, Name: filepath.Base(abs), Managed: isManaged(abs)}
	if IsMonorepo(abs) {
		p.Framework = "monorepo"
		p.Members = Members(abs)
		// A monorepo root that launches from one root command surfaces that command
		// live (no manifest required), so the root view can offer a Run for a
		// tracked-but-unadopted workspace. Empty for a pure-library workspace.
		if rl := RootLaunch(abs); rl.Manager != "" {
			p.LaunchCommand = engine.LaunchCommandHint(&rl)
		}
	} else {
		p.Framework = Detect(abs)
	}
	return p
}

// Refresh re-detects every tracked project in place (drops ones whose directory
// is gone), so managed/stack/member state is current — e.g. after `keel adopt`.
func (r *Registry) Refresh() {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.Projects[:0]
	for _, p := range r.Projects {
		d, err := filepath.Abs(p.Path)
		if err != nil || strings.Contains(d, "..") {
			continue // treat an unresolvable/escaping path as gone
		}
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			out = append(out, inspect(p.Path))
		}
	}
	r.Projects = out
}

// Expand resolves a leading ~ to the user's home directory (the shell would
// normally do this, but a value typed into the studio reaches us verbatim).
func Expand(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// IsMonorepo reports whether dir is a JS/TS workspace monorepo (pnpm/turbo/
// lerna/nx, or a package.json with a "workspaces" field). `keel adopt` uses it
// to decide between a single-app adoption and a shared-backend monorepo one.
func IsMonorepo(dir string) bool {
	safeBase, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	under := func(rest ...string) (string, bool) {
		p, err := filepath.Abs(filepath.Join(append([]string{dir}, rest...)...))
		if err != nil || !strings.HasPrefix(p, safeBase) {
			return "", false
		}
		return p, true
	}
	for _, f := range []string{"pnpm-workspace.yaml", "turbo.json", "lerna.json", "nx.json"} {
		p, ok := under(f)
		if !ok {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	if p, ok := under("package.json"); ok {
		if b, err := os.ReadFile(p); err == nil {
			var pkg struct {
				Workspaces json.RawMessage `json:"workspaces"`
			}
			if json.Unmarshal(b, &pkg) == nil && len(pkg.Workspaces) > 0 {
				return true
			}
		}
	}
	return false
}

// FrameworkLib is the framework value for a shared, non-runnable workspace
// package — a token/config/ui library a monorepo's apps depend on but that has
// no framework of its own. It is a real member (kept, not dropped) so the studio
// can show the whole repo; callers gate "runnable" actions on it not being lib.
const FrameworkLib = "lib"

// Members detects the stack of each workspace package in a monorepo. A package
// with a recognised framework carries it; one with none (a shared config/token/
// ui library) is kept as a FrameworkLib member rather than silently dropped, so
// the repo's shared libs stay visible — only a directory that is not a package
// at all (no package.json) is skipped.
func Members(dir string) []Project {
	seen := map[string]bool{}
	var out []Project
	for _, glob := range workspaceGlobs(dir) {
		for _, sub := range expandGlob(dir, glob) {
			if seen[sub] {
				continue
			}
			seen[sub] = true
			fw := Detect(sub)
			if fw == "" {
				if !isWorkspacePackage(sub) {
					continue // not a package at all — nothing to keep
				}
				fw = FrameworkLib // a real package with no framework: a shared lib
			}
			out = append(out, Project{Path: sub, Name: filepath.Base(sub), Framework: fw, Managed: isManaged(sub)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// isWorkspacePackage reports whether sub is a real workspace package (has a
// package.json), distinguishing a shared lib worth keeping from a stray
// directory that happened to match a workspace glob.
func isWorkspacePackage(sub string) bool {
	safeBase, err := filepath.Abs(sub)
	if err != nil {
		return false
	}
	p, err := filepath.Abs(filepath.Join(sub, "package.json"))
	if err != nil || !strings.HasPrefix(p, safeBase) {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// workspaceGlobs reads the workspace patterns from pnpm-workspace.yaml or
// package.json "workspaces", falling back to the common apps/packages layout.
func workspaceGlobs(dir string) []string {
	safeBase, err := filepath.Abs(dir)
	if err != nil {
		return []string{"apps/*", "packages/*", "services/*"}
	}
	readUnder := func(name string) ([]byte, bool) {
		p, err := filepath.Abs(filepath.Join(dir, name))
		if err != nil || !strings.HasPrefix(p, safeBase) {
			return nil, false
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, false
		}
		return b, true
	}
	if b, ok := readUnder("pnpm-workspace.yaml"); ok {
		var ws struct {
			Packages []string `yaml:"packages"`
		}
		if yaml.Unmarshal(b, &ws) == nil && len(ws.Packages) > 0 {
			return ws.Packages
		}
	}
	if b, ok := readUnder("package.json"); ok {
		var pkg struct {
			Workspaces json.RawMessage `json:"workspaces"`
		}
		if json.Unmarshal(b, &pkg) == nil && len(pkg.Workspaces) > 0 {
			var arr []string
			if json.Unmarshal(pkg.Workspaces, &arr) == nil && len(arr) > 0 {
				return arr
			}
			var obj struct {
				Packages []string `json:"packages"`
			}
			if json.Unmarshal(pkg.Workspaces, &obj) == nil && len(obj.Packages) > 0 {
				return obj.Packages
			}
		}
	}
	return []string{"apps/*", "packages/*", "services/*"}
}

// expandGlob expands a workspace pattern to matching immediate directories.
// Supports a trailing /* or /** (the common case); other patterns are treated
// as a literal path.
func expandGlob(root, glob string) []string {
	glob = strings.TrimSpace(strings.Trim(glob, "'\""))
	safeBase, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	if strings.HasSuffix(glob, "/*") || strings.HasSuffix(glob, "/**") {
		base, err := filepath.Abs(filepath.Join(root, strings.TrimSuffix(strings.TrimSuffix(glob, "/**"), "/*")))
		if err != nil || !strings.HasPrefix(base, safeBase) {
			return nil
		}
		entries, err := os.ReadDir(base)
		if err != nil {
			return nil
		}
		var out []string
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				out = append(out, filepath.Join(base, e.Name()))
			}
		}
		return out
	}
	full, err := filepath.Abs(filepath.Join(root, glob))
	if err != nil || !strings.HasPrefix(full, safeBase) {
		return nil
	}
	if info, err := os.Stat(full); err == nil && info.IsDir() {
		return []string{full}
	}
	return nil
}

// Remove drops a project by path (no error if absent).
func (r *Registry) Remove(dir string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	abs, _ := filepath.Abs(Expand(dir))
	out := r.Projects[:0]
	for _, p := range r.Projects {
		if p.Path != abs {
			out = append(out, p)
		}
	}
	r.Projects = out
}

// Prune drops entries whose directory no longer exists.
func (r *Registry) Prune() {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.Projects[:0]
	for _, p := range r.Projects {
		d, err := filepath.Abs(p.Path)
		if err != nil || strings.Contains(d, "..") {
			continue // treat an unresolvable/escaping path as gone
		}
		if _, err := os.Stat(d); err == nil {
			out = append(out, p)
		}
	}
	r.Projects = out
}
