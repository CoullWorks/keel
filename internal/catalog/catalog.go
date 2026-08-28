// Package catalog assembles the recipe Registry: the built-in recipes embedded
// in the binary, plus any user recipes in <config>/recipes, plus the recipes any
// enabled plugin contributes. The embed keeps Keel a single static binary; the
// user dir means anyone can add a stack without recompiling — drop a YAML in
// ~/.config/keel/recipes/ and it's live — and the plugin bridge means an enabled
// plugin can ship new generation types, default env/secrets and
// services-under-a-recipe as data.
//
// Provenance: built-ins are stamped Source="builtin" (trusted), loose user YAML
// is "user" (trusted), a subdir with a keel.pack.yaml manifest is a recipe pack,
// stamped Source="pack:<name>" (untrusted until consented), and a plugin's recipe
// is stamped Source="plugin:<name>" (untrusted, gated the same way a pack is). See
// docs/EXTENDING.md.
package catalog

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/coullworks/keel/internal/pack"
	"github.com/coullworks/keel/internal/profile"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/recipes"
)

// warnW is where a skipped user recipe's reason is reported. It is a package
// var (not a parameter) so the 30-odd callers of Registry keep their signature;
// a test swaps it to capture the warning. Defaults to os.Stderr — a warning is
// progress/diagnostic output, not data.
var warnW io.Writer = os.Stderr

// pluginRecipes is the seam that folds enabled plugins' recipes into the catalog.
//
// It is registered from outside rather than imported here, because the plugin
// registry (internal/plugins) pulls in the TUI, and importing that into catalog
// would create a test-time import cycle (tui and resolver both import catalog in
// their tests). Inverting the dependency — plugins registers its provider into
// catalog via RegisterPluginRecipes at init — keeps catalog free of the plugin
// package, so the fold is present whenever the plugin package is linked (always,
// in the binary) and absent in a pure recipe test that does not import it.
//
// A test can also substitute a fixed set through this var directly.
var pluginRecipes func() []recipe.Recipe

// RegisterPluginRecipes wires the plugin-recipe provider into the catalog. The
// plugins package calls it in an init so that linking the plugin registry is what
// turns the fold on; nothing else may call it, and calling it twice simply
// replaces the provider (the last one linked wins, which in practice is the one
// real provider).
func RegisterPluginRecipes(fn func() []recipe.Recipe) { pluginRecipes = fn }

// Registry loads built-in recipes first, then user recipes / packs (which
// override by id), then enabled plugins' recipes, stamping each with its
// provenance.
//
// A plugin recipe is added through the same Add path as every other recipe, so it
// is validated the same way: a malformed one is dropped with its reason rather
// than corrupting the registry, exactly as a bad user YAML would be — but it is
// dropped, not fatal, because one broken plugin recipe must not deny a user the
// rest of the catalog.
func Registry() (*recipe.Registry, error) { return load(false) }

// RegistryStrict is Registry, but a malformed user recipe or pack is a hard error
// rather than a skipped-with-warning. `keel recipes validate` uses it so a broken
// install is reported instead of silently dropped — the one place the user is
// asking "is everything I installed valid?".
func RegistryStrict() (*recipe.Registry, error) { return load(true) }

// load assembles the registry. A malformed built-in is always fatal (that is a
// keel bug, not the user's). A malformed user recipe/pack is fatal only in strict
// mode; otherwise it is skipped with a warning, so one hand-edited bad file in
// ~/.config/keel/recipes/ cannot lock the user out of every built-in recipe and
// brick every command that needs the catalog.
func load(strict bool) (*recipe.Registry, error) {
	reg := recipe.NewRegistry()
	if err := recipe.LoadInto(reg, recipes.FS, "builtin", ""); err != nil {
		return nil, err
	}

	// skip reports a bad user recipe: fatal in strict mode, otherwise a warning
	// and carry on. Returns the error so the caller can propagate it in strict mode.
	skip := func(name string, err error) error {
		if strict {
			return err
		}
		fmt.Fprintf(warnW, "warning: skipping recipe %s: %v\n", name, err)
		return nil
	}

	userDir := filepath.Join(profile.Dir(), "recipes")
	disabledPacks := pack.DisabledSet()
	entries, err := os.ReadDir(userDir)
	if err == nil {
		for _, e := range entries {
			p := filepath.Join(userDir, e.Name())
			switch {
			case e.IsDir():
				source, packName := "user", ""
				if _, err := os.Stat(filepath.Join(p, "keel.pack.yaml")); err == nil {
					packName = e.Name()
					source = "pack:" + packName
					// A pack the user switched off contributes nothing, the same as a
					// disabled plugin: its files stay on disk but its recipes stay out
					// of the catalog until it is turned back on.
					if disabledPacks[packName] {
						continue
					}
				}
				if err := recipe.LoadInto(reg, os.DirFS(p), source, packName); err != nil {
					if serr := skip(e.Name(), err); serr != nil {
						return nil, serr
					}
				}
			case strings.HasSuffix(e.Name(), ".yaml") || strings.HasSuffix(e.Name(), ".yml"):
				b, err := os.ReadFile(p)
				if err != nil {
					if serr := skip(e.Name(), err); serr != nil {
						return nil, serr
					}
					continue
				}
				if err := recipe.AddYAML(reg, b, "user", ""); err != nil {
					if serr := skip(e.Name(), fmt.Errorf("%s: %w", e.Name(), err)); serr != nil {
						return nil, serr
					}
				}
			}
		}
	}
	// no user recipe dir is fine; keep going and fold in plugin recipes.

	// Recipe packs discovered anywhere under home (the pack twin of plugin
	// home-discovery): a keel.pack.yaml directory outside RecipesDir contributes its
	// recipes with zero configuration, so cloning a pack repo anywhere in the dev
	// tree is enough. Installed packs are already loaded by the RecipesDir scan
	// above (Discovered.Installed), so only the loose home ones are folded here; a
	// disabled pack stays out, exactly as an installed disabled one does.
	for _, dp := range pack.Discover() {
		if dp.Installed || disabledPacks[dp.Name] {
			continue
		}
		if err := recipe.LoadInto(reg, os.DirFS(dp.Dir), "pack:"+dp.Name, dp.Name); err != nil {
			if serr := skip(dp.Name, err); serr != nil {
				return nil, serr
			}
		}
	}

	// Enabled plugins' recipes come last so a plugin can override a built-in the
	// same way a user recipe can — deliberate, and trust-gated at build time by the
	// plugin: source. A plugin recipe that does not validate is skipped rather than
	// failing the whole catalog: a broken plugin must not lock a user out of it.
	if pluginRecipes != nil {
		for _, r := range pluginRecipes() {
			_ = reg.Add(r) // Add validates; a plugin's bad recipe is dropped, not fatal
		}
	}
	// Framework variants (e.g. nextjs-js) inherit every shared recipe that names
	// their family primary. Done once here, after all recipes are loaded, so it
	// also covers user and plugin recipes that target a built-in family.
	reg.PropagateFamilyApplicability()
	return reg, nil
}
