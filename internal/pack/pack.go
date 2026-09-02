// Package pack models keel recipe packs: git-distributable bundles of recipe
// YAML + hooks installed via `keel recipes add`, and the installed-packs registry
// (<config>/packs.yaml). See docs/EXTENDING.md.
package pack

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coullworks/keel/internal/atomicfile"
	"github.com/coullworks/keel/internal/discover"
	"github.com/coullworks/keel/internal/filelock"
	"github.com/coullworks/keel/internal/profile"
	"gopkg.in/yaml.v3"
)

// manifestFile is the marker that identifies a recipe pack directory. Its
// presence, and nothing else, is what makes a directory a pack — the pack twin of
// a plugin's config/register.yaml.
const manifestFile = "keel.pack.yaml"

// Manifest is a pack's keel.pack.yaml.
type Manifest struct {
	SchemaVersion int      `yaml:"schema_version"`
	Name          string   `yaml:"name"`
	Version       string   `yaml:"version"`
	KeelVersion   string   `yaml:"keel_version_constraint"`
	Author        string   `yaml:"author"`
	Description   string   `yaml:"description"`
	Recipes       []string `yaml:"recipes"`
}

// Installed is one entry in packs.yaml.
type Installed struct {
	Name        string `yaml:"name"`
	Source      string `yaml:"source"`
	Version     string `yaml:"version"`
	Commit      string `yaml:"commit"`
	InstalledAt string `yaml:"installed_at"`
	Trusted     bool   `yaml:"trusted"`
	// Disabled switches a pack off without removing it: its files stay on disk but
	// its recipes leave the catalog, so turning it back on is not a reinstall. It is
	// the off-switch by absence — a pack with no record of it is on — so every pack
	// installed before this field existed stays enabled.
	Disabled bool `yaml:"disabled,omitempty"`
}

// Registry is the installed-packs file (packs.yaml).
type Registry struct {
	Packs []Installed `yaml:"packs"`
}

// RecipesDir is where packs install (<config>/recipes).
func RecipesDir() string { return filepath.Join(profile.Dir(), "recipes") }

// Dir is a pack's install dir.
func Dir(name string) string { return filepath.Join(RecipesDir(), name) }

func file() string { return filepath.Join(profile.Dir(), "packs.yaml") }

// Load reads packs.yaml (an empty registry if absent).
func Load() (*Registry, error) {
	safeBase, err := filepath.Abs(profile.Dir())
	if err != nil {
		return nil, err
	}
	p, err := filepath.Abs(file())
	if err != nil || !strings.HasPrefix(p, safeBase) {
		return nil, fmt.Errorf("refusing path outside %q", profile.Dir())
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

// Save writes packs.yaml. It invalidates the Discover cache so a pack just added
// (recipes add → Move + Save) or toggled is reflected on the next catalog build
// rather than up to the TTL later.
func (r *Registry) Save() error {
	if err := os.MkdirAll(profile.Dir(), 0o755); err != nil {
		return err
	}
	b, err := yaml.Marshal(r)
	if err != nil {
		return err
	}
	if err := atomicfile.WriteFile(file(), b, 0o644); err != nil {
		return err
	}
	InvalidateDiscover()
	return nil
}

// Get returns the installed pack by name.
func (r *Registry) Get(name string) (*Installed, bool) {
	for i := range r.Packs {
		if r.Packs[i].Name == name {
			return &r.Packs[i], true
		}
	}
	return nil, false
}

// Upsert adds or replaces a pack entry by name.
func (r *Registry) Upsert(p Installed) {
	for i := range r.Packs {
		if r.Packs[i].Name == p.Name {
			r.Packs[i] = p
			return
		}
	}
	r.Packs = append(r.Packs, p)
}

// SetEnabled turns a pack on or off without removing it, so its recipes leave and
// rejoin the catalog while its files stay on disk. It works for an installed pack
// (a packs.yaml entry it flips) and for a home-discovered one that has no entry
// yet: the latter gets a minimal record carrying only its on/off state, the pack
// twin of how a discovered plugin's disabled flag is recorded, so toggling a pack
// found loose in the dev tree sticks.
func SetEnabled(name string, on bool) error {
	// Hold the whole read-modify-write under a cross-process lock so a concurrent
	// CLI + studio cannot lose each other's enable/disable to a last-writer-wins
	// overwrite of packs.yaml.
	return filelock.With(file()+".lock", func() error {
		r, err := Load()
		if err != nil {
			return err
		}
		if p, ok := r.Get(name); ok {
			p.Disabled = !on // p points into r.Packs, so this mutates the entry Save writes
		} else {
			r.Upsert(Installed{Name: name, Disabled: !on})
		}
		return r.Save()
	})
}

// Uninstall removes an installed pack: its files on disk and its packs.yaml
// entry. It reports whether the pack was installed (false = nothing to do).
// Already-generated projects are untouched — only the pack's own files go.
func Uninstall(name string) (bool, error) {
	var removed bool
	err := filelock.With(file()+".lock", func() error {
		r, err := Load()
		if err != nil {
			return err
		}
		if _, ok := r.Get(name); !ok {
			return nil // not installed; removed stays false
		}
		safeBase, err := filepath.Abs(RecipesDir())
		if err != nil {
			return err
		}
		p, err := filepath.Abs(Dir(name))
		if err != nil || !strings.HasPrefix(p, safeBase) {
			return fmt.Errorf("refusing path outside %q", RecipesDir())
		}
		if err := os.RemoveAll(p); err != nil {
			return err
		}
		r.Remove(name)
		removed = true
		return r.Save()
	})
	return removed, err
}

// DisabledSet is the names of installed packs the user has switched off, so the
// catalog can skip their recipes without every caller re-reading packs.yaml.
func DisabledSet() map[string]bool {
	out := map[string]bool{}
	r, err := Load()
	if err != nil {
		return out
	}
	for _, p := range r.Packs {
		if p.Disabled {
			out[p.Name] = true
		}
	}
	return out
}

// Remove drops a pack entry, reporting whether it existed.
func (r *Registry) Remove(name string) bool {
	for i := range r.Packs {
		if r.Packs[i].Name == name {
			r.Packs = append(r.Packs[:i], r.Packs[i+1:]...)
			return true
		}
	}
	return false
}

// ReadManifest reads keel.pack.yaml from a directory.
func ReadManifest(dir string) (*Manifest, error) {
	safeBase, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	p, err := filepath.Abs(filepath.Join(dir, manifestFile))
	if err != nil || !strings.HasPrefix(p, safeBase) {
		return nil, fmt.Errorf("refusing path outside %q", dir)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Discovered is a recipe pack found on disk: its manifest identity, where it
// lives, and whether it was installed under RecipesDir or found loose in the dev
// tree (Installed=false).
type Discovered struct {
	Name      string
	Dir       string
	Manifest  *Manifest
	Installed bool // true when Dir is directly under RecipesDir (added via `recipes add`)
}

// discoverCache memoizes Discover's result for a short window. Discover walks the
// whole home directory (the pack twin of pluginstore.List's walk), and
// catalog.Registry() — which is built on every studio request and several times
// per CLI command — calls it every time. Without this cache each catalog build
// pays a fresh whole-home walk, defeating the very reason pluginstore.List has its
// own cache; the two together keep a catalog rebuild off the disk. A pack mutation
// (add / remove / enable-disable via Save) calls InvalidateDiscover, so a change
// is visible immediately rather than up to the TTL later.
var (
	discoverMu   sync.Mutex
	discoverList []Discovered
	discoverAt   time.Time
	discoverTTL  = 2 * time.Second
	discoverHeld bool
)

// InvalidateDiscover clears the Discover cache so the next call re-walks. Every
// function that changes what is installed on disk (Save, Uninstall) calls it.
func InvalidateDiscover() {
	discoverMu.Lock()
	discoverHeld = false
	discoverList = nil
	discoverMu.Unlock()
}

// Discover finds every recipe pack available with zero configuration: the ones
// installed under RecipesDir, plus any keel.pack.yaml directory anywhere under the
// user's home — the pack twin of plugin home-discovery, so cloning a pack repo
// anywhere in the dev tree is enough for keel to find it. It shares the one
// bounded, dependency-skipping walk in the discover package, and its result is
// cached for a short window (see discoverCache) because the catalog rebuilds it
// often.
//
// Identity is the manifest name (the key its packs.yaml on/off state is stored
// under), falling back to the directory basename when the manifest cannot be
// read. Installed packs are returned first and win a name clash, so an installed
// copy shadows a stray home checkout of the same pack. A directory whose manifest
// is unreadable is skipped rather than surfaced as a broken pack.
//
// The returned slice is a copy of the cached one, so a caller may sort or slice
// it without corrupting the shared cache (the Manifest pointers are read-only).
func Discover() []Discovered {
	discoverMu.Lock()
	if discoverHeld && time.Since(discoverAt) < discoverTTL {
		out := append([]Discovered(nil), discoverList...)
		discoverMu.Unlock()
		return out
	}
	discoverMu.Unlock()

	scanned := discoverScan()

	discoverMu.Lock()
	discoverList, discoverAt, discoverHeld = scanned, time.Now(), true
	discoverMu.Unlock()
	return append([]Discovered(nil), scanned...)
}

// discoverScan is the uncached walk behind Discover.
func discoverScan() []Discovered {
	recipesDir := filepath.Clean(RecipesDir())
	roots := append([]string{recipesDir}, discover.Home()...)
	seen := map[string]bool{}
	var out []Discovered
	for _, dir := range discover.Walk(manifestFile, roots) {
		m, err := ReadManifest(dir)
		if err != nil {
			continue
		}
		name := m.Name
		if name == "" {
			name = filepath.Base(dir)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, Discovered{
			Name:      name,
			Dir:       dir,
			Manifest:  m,
			Installed: filepath.Dir(filepath.Clean(dir)) == recipesDir,
		})
	}
	return out
}

// SatisfiesKeel reports whether keelVersion meets a simple ">= x.y.z" constraint
// (empty constraint = any). Minimal on purpose — no external semver dependency.
func SatisfiesKeel(constraint, keelVersion string) (bool, error) {
	c := strings.TrimSpace(constraint)
	if c == "" {
		return true, nil
	}
	if !strings.HasPrefix(c, ">=") {
		return false, fmt.Errorf("unsupported version constraint %q (only \">= x.y.z\" is supported)", constraint)
	}
	want, err := parseSemver(strings.TrimSpace(strings.TrimPrefix(c, ">=")))
	if err != nil {
		return false, fmt.Errorf("version constraint %q: %w", constraint, err)
	}
	got, err := parseSemver(keelVersion)
	if err != nil {
		return false, fmt.Errorf("keel version %q: %w", keelVersion, err)
	}
	for i := 0; i < 3; i++ {
		if got[i] != want[i] {
			return got[i] > want[i], nil
		}
	}
	return true, nil
}

// parseSemver turns "0.4.1-dev" into [0 4 1], tolerating pre-release/build
// suffixes and extra dotted components (only major/minor/patch are compared).
// A non-numeric major/minor/patch component is an error rather than a silent
// zero: before this, ">= abc" parsed to [0 0 0] and SatisfiesKeel wrongly passed.
func parseSemver(v string) ([3]int, error) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	for i, part := range strings.Split(v, ".") {
		if i > 2 {
			break // ignore any 4th+ component (e.g. a Windows-style 1.0.0.0)
		}
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return out, fmt.Errorf("invalid version %q: %q is not a number", v, part)
		}
		out[i] = n
	}
	return out, nil
}
