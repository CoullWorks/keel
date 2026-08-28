// Package pluginstore manages plugins installed at runtime.
//
// Until now a keel plugin was a Go package compiled into the binary: the set was
// a map in internal/plugins, and adding one meant rebuilding keel. That is fine
// for the plugins keel ships and useless for anyone else, so the only extension
// point left to a user was dropping a `keel-<name>` executable on their PATH.
//
// This package is the other half: a plugin is a directory carrying
// config/register.yaml, installed from a local path or a git repository, kept
// under the keel config dir, discovered by scanning, and enabled or disabled
// without reinstalling. Nothing here runs a plugin's code — installing and
// listing are pure file operations, so an untrusted plugin cannot do anything
// merely by being present.
package pluginstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coullworks/keel/internal/atomicfile"
	"github.com/coullworks/keel/internal/discover"
	"github.com/coullworks/keel/internal/filelock"
	"github.com/coullworks/keel/internal/pack"
	"github.com/coullworks/keel/internal/profile"
	"github.com/coullworks/keel/plugin"
	"gopkg.in/yaml.v3"
)

// SupportedSchema is the register-file version this keel understands. It is the
// same number the compiled-in plugins use, because they are the same format:
// a runtime plugin and a built-in one differ in where they live, not in shape.
const SupportedSchema = 1

// registerFile is the marker that identifies a plugin directory: its presence,
// and nothing else, is what makes a directory a keel plugin. It is the marker
// handed to the shared discovery walk.
const registerFile = "config/register.yaml"

// Dir is where installed plugins live, one directory per plugin.
func Dir() string { return filepath.Join(profile.Dir(), "plugins") }

// indexPath records which plugins are disabled and where each came from. The
// directories are the source of truth for what is installed; this file only
// carries what cannot be recovered by looking at them.
func indexPath() string { return filepath.Join(profile.Dir(), "plugins.yaml") }

// Installed is one plugin on disk.
type Installed struct {
	Meta    plugin.Meta `yaml:"-"`
	Name    string      `yaml:"name"`
	Source  string      `yaml:"source,omitempty"` // path or repo it came from
	Commit  string      `yaml:"commit,omitempty"` // short sha when it came from git
	Enabled bool        `yaml:"enabled"`
	// Trusted gates the plugin's own executables. Installing never sets it:
	// copying files is safe, running them is a separate decision the user makes
	// once, with `keel plugins trust`. A plugin that only declares data (screens,
	// wizard options) never needs it.
	Trusted bool `yaml:"trusted,omitempty"`
	// TrustedPath is the directory that was trusted, so trust is bound to a
	// specific plugin on disk rather than to its manifest name. Discovery finds a
	// plugin by name anywhere under home, so without this a malicious
	// keel-plugin-foo cloned into the user's tree could inherit the trust the user
	// granted a different "foo". Trust applies only when the discovered directory
	// matches this path; a plugin of the same name found elsewhere is untrusted
	// until the user trusts THAT copy. Empty on an old record or a built-in.
	TrustedPath string `yaml:"trustedPath,omitempty"`
	// GrantedCaps is the set of capabilities (net / secrets / exec) the user has
	// explicitly consented to for this plugin. Trust and grants are separate
	// concerns: Trusted says "keel may run this plugin's code at all", a grant says
	// "and this specific power is allowed". A capability-gated action runs only
	// when it appears here — trusting a plugin no longer blanket-grants every power
	// it might ask for. Stored as a name->true map so the YAML is a readable list
	// and an absent capability is simply not granted.
	GrantedCaps map[plugin.Capability]bool `yaml:"grantedCaps,omitempty"`
	Dir         string                     `yaml:"-"`
	// Builtin marks a record that stands for a plugin compiled into keel rather
	// than a directory on disk. Such a record exists only to carry the one thing a
	// built-in cannot recover by itself: that the user switched it off. Everything
	// else about a built-in is in its Go package.
	Builtin bool `yaml:"builtin,omitempty"`
	// Problem is set when the directory exists but is not a usable plugin. It is
	// reported rather than hidden: a plugin that silently fails to load is the
	// worst outcome, because the feature just appears to be missing.
	Problem string `yaml:"-"`
	// TrustNote is a runtime hint (not persisted) set when a trust record exists
	// for this plugin's NAME but was granted for a different directory than the one
	// discovered here — so this copy is untrusted and the user is told why, rather
	// than silently seeing "untrusted" for a plugin they thought they trusted.
	TrustNote string `yaml:"-"`
}

type index struct {
	Plugins []Installed `yaml:"plugins"`
}

func loadIndex() (*index, error) {
	var ix index
	b, err := os.ReadFile(indexPath())
	if errors.Is(err, os.ErrNotExist) {
		return &ix, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(b, &ix); err != nil {
		return nil, fmt.Errorf("%s: %w", indexPath(), err)
	}
	return &ix, nil
}

func (ix *index) save() error {
	if err := os.MkdirAll(filepath.Dir(indexPath()), 0o755); err != nil {
		return err
	}
	b, err := yaml.Marshal(ix)
	if err != nil {
		return err
	}
	// Atomic write: plugins.yaml carries trust/enable/grant state every command
	// reads; a truncated one (crash mid-write) would silently drop a plugin's trust.
	return atomicfile.WriteFile(indexPath(), b, 0o644)
}

// saveAndInvalidate persists the index and clears the List cache, so a mutation
// is visible on the next List rather than up to listCacheTTL later. Every
// index-writing function uses this so none can forget the invalidation and
// reintroduce the "trust/enable state doesn't stick" bug.
func (ix *index) saveAndInvalidate() error {
	if err := ix.save(); err != nil {
		return err
	}
	Invalidate()
	return nil
}

func (ix *index) get(name string) (*Installed, bool) {
	for i := range ix.Plugins {
		if ix.Plugins[i].Name == name {
			return &ix.Plugins[i], true
		}
	}
	return nil, false
}

func (ix *index) upsert(p Installed) {
	if cur, ok := ix.get(p.Name); ok {
		*cur = p
		return
	}
	ix.Plugins = append(ix.Plugins, p)
}

// ReadMeta reads a plugin's identity from its register file without running
// anything. An unreadable or invalid manifest is an error, never a default.
func ReadMeta(dir string) (plugin.Meta, error) {
	var m plugin.Meta
	b, err := os.ReadFile(filepath.Join(dir, "config", "register.yaml"))
	if err != nil {
		return m, fmt.Errorf("no config/register.yaml: %w", err)
	}
	if err := yaml.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("config/register.yaml: %w", err)
	}
	return m, validate(m)
}

// validate refuses a manifest that would produce a half-loaded plugin.
func validate(m plugin.Meta) error {
	if m.Schema != SupportedSchema {
		return fmt.Errorf("schema %d is not supported (this keel understands %d)", m.Schema, SupportedSchema)
	}
	if m.Name == "" {
		return errors.New("name is required")
	}
	// The name becomes a directory under the keel config dir, so it must not be
	// able to climb out of it or collide with a path.
	if m.Name != filepath.Base(m.Name) || strings.ContainsAny(m.Name, `/\`) || m.Name == "." || m.Name == ".." {
		return fmt.Errorf("name %q is not a plain directory name", m.Name)
	}
	if m.Version == "" {
		return errors.New("version is required")
	}
	if m.Description == "" {
		return errors.New("description is required")
	}
	return nil
}

// listCache memoizes List's result for a short window. List walks the whole home
// directory, which is far too slow to repeat on every call — and it is called on
// every studio endpoint and on every /plugin-assets request. The cache is
// package-level and mutex-guarded; a mutation (trust / enable / add / remove)
// calls Invalidate so the change is visible on the next call rather than up to
// the TTL later. Without that invalidation, trust and on/off state would appear
// not to stick for up to listCacheTTL.
var (
	listCacheMu   sync.Mutex
	listCache     []Installed
	listCacheAt   time.Time
	listCacheTTL  = 2 * time.Second
	listCacheErr  error
	listCacheHeld bool
)

// Invalidate clears the List cache so the next List reflects a mutation
// immediately. Every function that writes the index must call this.
func Invalidate() {
	listCacheMu.Lock()
	listCacheHeld = false
	listCache, listCacheErr = nil, nil
	listCacheMu.Unlock()
}

// List returns every plugin directory found, in name order, each carrying its
// enabled state and any reason it is unusable.
//
// The scan is the source of truth: a plugin dropped into the plugins directory
// by hand shows up here without anything having to register it, which is what
// "find my local plugins" means. The result is cached for listCacheTTL because
// discovery walks the whole home directory; mutations call Invalidate.
//
// A COPY of the cached slice is returned, never the backing slice itself, so a
// caller may sort or re-slice its result without corrupting the shared cache that
// every other concurrent caller holds within the TTL window. The element maps
// (GrantedCaps, Meta.Capabilities) are shared and must be treated as read-only.
func List() ([]Installed, error) {
	listCacheMu.Lock()
	if listCacheHeld && time.Since(listCacheAt) < listCacheTTL {
		out, err := append([]Installed(nil), listCache...), listCacheErr
		listCacheMu.Unlock()
		return out, err
	}
	listCacheMu.Unlock()

	out, err := list()

	listCacheMu.Lock()
	listCache, listCacheErr, listCacheAt, listCacheHeld = out, err, time.Now(), true
	listCacheMu.Unlock()
	return append([]Installed(nil), out...), err
}

// list is the uncached scan behind List.
func list() ([]Installed, error) {
	ix, err := loadIndex()
	if err != nil {
		return nil, err
	}
	var out []Installed
	seen := map[string]bool{}
	add := func(dir string) {
		// The manifest name is the plugin's identity and the key its index record
		// (enabled / trusted / granted caps) is stored under; the directory basename
		// is only a fallback for a plugin whose manifest cannot be read. The two
		// differ whenever a plugin repo is cloned under its own name — keel-plugin-foo
		// carries the manifest "foo" — so the index MUST be matched by the manifest
		// name. Keying it by the directory name meant trust, on/off state and grants
		// saved under "foo" never stuck to the plugin discovered at keel-plugin-foo/,
		// so no such plugin could ever be trusted or disabled.
		p := Installed{Name: filepath.Base(dir), Dir: dir, Enabled: true}
		if m, err := ReadMeta(dir); err != nil {
			p.Problem = err.Error()
		} else {
			p.Meta, p.Name = m, m.Name
		}
		if rec, ok := ix.get(p.Name); ok {
			// Enabled/source/commit are metadata and apply by name. Granted caps stay
			// visible from the record regardless of trust — they PERSIST across an
			// untrust (kept inert, so re-trusting restores the user's earlier choices)
			// and are effective only in combination with trust anyway. TRUST itself is
			// the security gate, and it is bound to a path: it applies only when this
			// discovered directory is the one that was trusted, so trust never transfers
			// to a same-named plugin found elsewhere (a planted or cloned
			// keel-plugin-foo). A built-in has no directory, so its record applies
			// as-is. An old on-disk record with Trusted but no TrustedPath is treated as
			// untrusted-until-re-trusted rather than silently honoured, closing the
			// name-shadowing hole for pre-existing installs too. Because the exec-time
			// check is (trusted-at-this-path AND granted), exposing the grants here is
			// safe: a path-mismatched plugin has Trusted=false and so runs nothing.
			p.Enabled, p.Source, p.Commit, p.GrantedCaps = rec.Enabled, rec.Source, rec.Commit, rec.GrantedCaps
			switch {
			case rec.Builtin:
				p.Trusted = rec.Trusted
			case rec.Trusted && rec.TrustedPath == dir:
				p.Trusted, p.TrustedPath = true, rec.TrustedPath
			case rec.Trusted && rec.TrustedPath != "":
				p.TrustNote = "trusted at a different path (" + rec.TrustedPath + "); re-run `keel plugins trust " + p.Name + "` to trust this copy"
			case rec.Trusted:
				p.TrustNote = "an older trust record has no path; re-run `keel plugins trust " + p.Name + "` to trust this copy"
			}
		}
		if seen[p.Name] {
			return // the managed dir is scanned first, so it wins on a name clash
		}
		seen[p.Name] = true
		out = append(out, p)
	}

	// The managed dir: every subdirectory is an installed plugin, so a broken one
	// is listed with its problem rather than hidden (someone must see why it does
	// nothing).
	if entries, err := os.ReadDir(Dir()); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				add(filepath.Join(Dir(), e.Name()))
			}
		}
	}

	// Search roots beyond the managed dir (the home directory + KEEL_PLUGIN_PATH):
	// only real plugins — a dir carrying a register file — found by the shared
	// bounded walk, since these roots hold a whole dev tree, not only plugins.
	// Cloning a plugin anywhere under home is enough for keel to find it, with no
	// configuration.
	for _, d := range discover.Walk(registerFile, discoverRoots()) {
		add(d)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SearchRoots is the ordered set of directories keel scans for plugins: the
// managed install dir first, then the projects directory, then every root on
// KEEL_PLUGIN_PATH.
func SearchRoots() []string { return append([]string{Dir()}, discoverRoots()...) }

// discoverRoots are the roots beyond the managed dir that keel scans for plugins:
// the user's home directory (a default, so a plugin cloned anywhere in their tree
// is found with no configuration) followed by every root on KEEL_PLUGIN_PATH.
func discoverRoots() []string { return append(defaultRoots(), extraRoots()...) }

// defaultRoots is the always-on search root: the user's home directory. Cloning a
// plugin anywhere under home is enough for keel to find it — the "clone anywhere,
// keel finds it" default — with no KEEL_PLUGIN_PATH and no profile setting, since
// home contains wherever the user keeps their code. The bounded, dependency- and
// cache-skipping walk in the discover package is what keeps scanning a whole home
// directory fast. If home cannot be determined, there is no default root.
func defaultRoots() []string { return discover.Home() }

// extraRoots is the directories beyond the managed dir that keel scans for
// plugins, from KEEL_PLUGIN_PATH. Clone a plugin repo into any of these and keel
// finds it with no install step. A stale or missing root is skipped, never fatal.
func extraRoots() []string { return discover.EnvRoots("KEEL_PLUGIN_PATH") }

// isPluginDir reports whether dir is a keel plugin: it carries the register file.
func isPluginDir(dir string) bool { return discover.HasMarker(dir, registerFile) }

// Get returns one installed plugin by name.
func Get(name string) (Installed, bool) {
	all, err := List()
	if err != nil {
		return Installed{}, false
	}
	for _, p := range all {
		if p.Name == name {
			return p, true
		}
	}
	return Installed{}, false
}

// Install copies a plugin from a local directory or a git repository into the
// plugins directory and records it as enabled.
//
// The manifest is validated in the temporary directory, before anything is
// moved: a source that is not a plugin leaves nothing behind. Nothing from the
// source is executed at any point.
func Install(ctx context.Context, source, ref string) (Installed, error) {
	tmp, commit, err := pack.Fetch(ctx, source, ref)
	if err != nil {
		return Installed{}, err
	}
	defer os.RemoveAll(tmp)

	// A repository may hold the plugin at its root or one level down, which is
	// how a monorepo of several plugins is laid out.
	dir := tmp
	if _, err := os.Stat(filepath.Join(dir, "config", "register.yaml")); err != nil {
		if nested, ok := findManifest(tmp); ok {
			dir = nested
		}
	}
	m, err := ReadMeta(dir)
	if err != nil {
		return Installed{}, fmt.Errorf("%s is not a keel plugin: %w", source, err)
	}

	dest := filepath.Join(Dir(), m.Name)
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return Installed{}, err
	}
	if err := pack.Move(dir, dest); err != nil {
		return Installed{}, err
	}

	rec := Installed{Name: m.Name, Meta: m, Source: source, Commit: commit, Enabled: true, Dir: dest}
	ix, err := loadIndex()
	if err != nil {
		return rec, err
	}
	ix.upsert(rec)
	if err := ix.save(); err != nil {
		return rec, err
	}
	Invalidate()
	return rec, nil
}

// findManifest looks one level down for a plugin directory, so
// `keel plugins add owner/monorepo` finds the plugin inside it.
func findManifest(root string) (string, bool) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		d := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(d, "config", "register.yaml")); err == nil {
			return d, true
		}
	}
	return "", false
}

// mutateRecord applies fn to the PERSISTED index record for name — creating a
// minimal one if none exists yet (a freshly discovered plugin has no record until
// its first mutation) — and saves. It exists because the Installed that List/Get
// return is a DERIVED view: Trusted there is computed from whether the discovered
// directory matches the trusted one, so writing that view back would clobber the
// raw fields it was derived from. Every state change goes through the raw record
// here so a change to one field (enable, grant) never wipes another (trust).
func mutateRecord(name string, fn func(rec *Installed)) error {
	// The whole read-modify-write is held under a cross-process lock so a
	// concurrent CLI + studio (both supported) cannot lose each other's trust /
	// enable / grant change to a last-writer-wins overwrite.
	return filelock.With(indexPath()+".lock", func() error {
		ix, err := loadIndex()
		if err != nil {
			return err
		}
		rec, ok := ix.get(name)
		if !ok {
			// A discovered plugin has no index record until its first mutation, and it
			// is enabled by default (list() defaults Enabled=true for a plugin with no
			// record). So a record created here for a trust/grant must start Enabled,
			// or trusting/granting a plugin would silently DISABLE it — the zero value
			// of the new record is false. SetEnabled sets Enabled explicitly, so this
			// default is only the starting point.
			ix.Plugins = append(ix.Plugins, Installed{Name: name, Enabled: true})
			rec = &ix.Plugins[len(ix.Plugins)-1]
		}
		fn(rec)
		return ix.saveAndInvalidate()
	})
}

// SetEnabled turns a plugin on or off without removing it, so trying one out and
// putting it back is not a reinstall.
func SetEnabled(name string, on bool) error {
	if _, ok := Get(name); !ok {
		return fmt.Errorf("plugin %q is not installed", name)
	}
	return mutateRecord(name, func(rec *Installed) { rec.Enabled = on })
}

// SetBuiltinEnabled turns a compiled-in plugin on or off.
//
// A built-in has no directory to consult, so the only place its on/off state can
// live is this index. The record carries the name and the enabled flag, and it is
// written explicitly for both states rather than only for "off". The earlier
// design expressed "on" as the absence of a record — which works for a plugin
// that is on by default but breaks a default-off one: keel seeds a default-off
// plugin's off record on first run, so if enabling it merely deleted that record,
// the next Load would re-seed it off and the user's choice would silently revert.
// An explicit "on" record is what makes enabling a default-off built-in stick.
func SetBuiltinEnabled(name string, on bool) error {
	return filelock.With(indexPath()+".lock", func() error {
		ix, err := loadIndex()
		if err != nil {
			return err
		}
		ix.upsert(Installed{Name: name, Enabled: on, Builtin: true})
		return ix.saveAndInvalidate()
	})
}

// DisabledBuiltins is the set of compiled-in plugins the user has switched off.
// keel's Load reads it to skip a built-in the user disabled, which is the one
// thing the built-in's own Go package cannot know about itself.
func DisabledBuiltins() map[string]bool {
	out := map[string]bool{}
	ix, err := loadIndex()
	if err != nil {
		return out
	}
	for _, p := range ix.Plugins {
		if p.Builtin && !p.Enabled {
			out[p.Name] = true
		}
	}
	return out
}

// KnownBuiltins is the set of compiled-in plugins that have a record either way —
// the user (or a default-off seed) decided their on/off state. keel's Load reads
// it to tell "no decision has been made yet" (seed the default) from "the user
// chose": a built-in with a record is never re-seeded, so enabling a default-off
// plugin sticks instead of reverting on the next run.
func KnownBuiltins() map[string]bool {
	out := map[string]bool{}
	ix, err := loadIndex()
	if err != nil {
		return out
	}
	for _, p := range ix.Plugins {
		if p.Builtin {
			out[p.Name] = true
		}
	}
	return out
}

// HasBuiltinRecord reports whether one compiled-in plugin has a record either
// way. It is the single-name form of KnownBuiltins, for the read-only-config
// fallback that must decide a default-off plugin's state without having been able
// to write a seed.
func HasBuiltinRecord(name string) bool {
	return KnownBuiltins()[name]
}

// Remove deletes an installed plugin and forgets it.
func Remove(name string) error {
	p, ok := Get(name)
	if !ok {
		return fmt.Errorf("plugin %q is not installed", name)
	}
	if err := os.RemoveAll(p.Dir); err != nil {
		return err
	}
	return filelock.With(indexPath()+".lock", func() error {
		ix, err := loadIndex()
		if err != nil {
			return err
		}
		for i := range ix.Plugins {
			if ix.Plugins[i].Name == name {
				ix.Plugins = append(ix.Plugins[:i], ix.Plugins[i+1:]...)
				break
			}
		}
		return ix.saveAndInvalidate()
	})
}

// SetTrusted allows or forbids keel running the plugin's own executables.
//
// Trust is deliberately separate from installing and from enabling. Copying a
// plugin's files is safe and reversible; running them is not, so it stays an
// explicit, separate decision rather than something bundled into `add`.
func SetTrusted(name string, on bool) error {
	p, ok := Get(name)
	if !ok {
		return fmt.Errorf("plugin %q is not installed", name)
	}
	return mutateRecord(name, func(rec *Installed) {
		rec.Trusted = on
		// Bind trust to the directory being trusted, so it applies only to THIS
		// plugin on disk and never transfers to a same-named copy found elsewhere.
		// Cleared on untrust so no stale path lingers. A built-in has no dir, so its
		// TrustedPath stays empty and list() honours a built-in record as-is.
		if on {
			rec.TrustedPath = p.Dir
		} else {
			rec.TrustedPath = ""
		}
	})
}

// SetCapabilityGranted grants or revokes one capability for a plugin.
//
// This is deliberately separate from SetTrusted. Trust is "keel may run this
// plugin's executables at all"; a grant is "and this specific power (net /
// secrets / exec) is allowed". Keeping them apart is what stops trust from being
// an all-or-nothing switch that silently hands a plugin every power it declared:
// the user trusts the plugin once, then grants only the capabilities they mean
// to. Granting an unknown capability is refused so a typo can never widen the
// consent surface past the closed set keel understands.
func SetCapabilityGranted(name string, cap plugin.Capability, on bool) error {
	if !plugin.KnownCapability(cap) {
		return fmt.Errorf("unknown capability %q", cap)
	}
	if _, ok := Get(name); !ok {
		return fmt.Errorf("plugin %q is not installed", name)
	}
	return mutateRecord(name, func(rec *Installed) {
		if rec.GrantedCaps == nil {
			rec.GrantedCaps = map[plugin.Capability]bool{}
		}
		if on {
			rec.GrantedCaps[cap] = true
		} else {
			delete(rec.GrantedCaps, cap)
		}
	})
}

// CapabilityGranted reports whether the user has explicitly granted one
// capability to a plugin. It answers only the grant question — a caller still
// gates on Trusted (may keel run this plugin's code at all) separately, so a
// power is available only when both are true.
func CapabilityGranted(name string, cap plugin.Capability) bool {
	p, ok := Get(name)
	if !ok {
		return false
	}
	return p.GrantedCaps[cap]
}
