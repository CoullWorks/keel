package tui

import (
	"sort"
	"strings"

	"github.com/coullworks/keel/internal/recipe"
)

// versionPrefix marks a wizard answer as a version choice rather than a recipe
// id, so the two can travel through the same [][]string result without a second
// channel. IDsFromResult drops them; VersionsFromResult reads them.
// The encoding lives in the recipe package so the resolver can read it too.
const versionPrefix = recipe.VersionTokenPrefix

func versionKey(name, value string) string { return recipe.VersionToken(name, value) }

// versionStepNames is every pin name any recipe in the catalogue offers a choice
// for, sorted so the wizard asks them in a stable order.
//
// The steps are built once, up front, but each one's options are resolved
// against the actual selection, so a name no stack in this build offers simply
// renders no options and is skipped.
func versionStepNames(reg *recipe.Registry) []string {
	seen := map[string]bool{}
	for _, r := range reg.All() {
		for name := range r.Versions {
			seen[name] = true
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// versionTitle is the question shown for a pin name, taken from whichever recipe
// labels it. Falling back to the bare name keeps a new pin usable before anyone
// has written a label for it.
func versionTitle(reg *recipe.Registry, name string) string {
	for _, r := range reg.All() {
		if vc, ok := r.Versions[name]; ok && vc.Label != "" {
			return vc.Label
		}
	}
	return strings.ToUpper(name[:1]) + name[1:] + " version"
}

// chosenRecipes turns the answers so far into the recipes they name, so a
// version step can ask only the recipes actually in the plan what they offer.
func chosenRecipes(reg *recipe.Registry, res [][]string) []recipe.Recipe {
	var out []recipe.Recipe
	for i, keys := range res {
		if i == 0 || i == 1 { // language and framework family are groupings, not recipes
			continue
		}
		for _, k := range keys {
			if k == "" || strings.HasPrefix(k, versionPrefix) || strings.HasPrefix(k, brandPrefix) {
				continue
			}
			if r, ok := reg.Get(k); ok {
				out = append(out, r)
			}
		}
	}
	return out
}

// VersionsFromResult extracts the chosen versions, keyed by pin name.
func VersionsFromResult(res [][]string) map[string]string {
	out := map[string]string{}
	for _, keys := range res {
		for _, k := range keys {
			if !strings.HasPrefix(k, versionPrefix) {
				continue
			}
			if name, value, ok := strings.Cut(strings.TrimPrefix(k, versionPrefix), "="); ok {
				out[name] = value
			}
		}
	}
	return out
}
