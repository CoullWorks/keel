package tui

import (
	"strings"

	"github.com/coullworks/keel/internal/brand"
	"github.com/coullworks/keel/internal/recipe"
)

// brandPrefix marks a wizard answer as a brand-colour choice rather than a
// recipe id, so it travels through the same [][]string result as recipe and
// version answers without a second channel. IDsFromResult drops these (they are
// not recipes); BrandFromResult reads them. The value after the prefix is the
// primary hex, optionally followed by ",<accent hex>".
const brandPrefix = "brand:"

// brandNone is the sentinel for "keep the UI kit's own colours" — a real option
// so choosing not to re-brand is a decision the wizard records, and so the step
// has more than one option (and therefore isn't auto-skipped) once a UI kit is
// in the plan.
const brandNone = brandPrefix + "none"

// brandToken encodes a brand-colour answer. accent may be empty.
func brandToken(primary, accent string) string {
	if accent == "" {
		return brandPrefix + primary
	}
	return brandPrefix + primary + "," + accent
}

// brandPreset is one offered palette: a label plus its primary/accent hexes.
// The presets are a convenience, not a limit — the CLI/studio brand command
// takes any hex — but the stepped wizard is selection-only, so a curated set
// keeps it one keystroke like every other step.
type brandPreset struct {
	label, primary, accent string
}

// brandPresets are the palettes the wizard offers when a UI kit is chosen. Kept
// small and neutral; the accent is a complementary tone for each.
var brandPresets = []brandPreset{
	{"Indigo", "#4f46e5", "#06b6d4"},
	{"Emerald", "#059669", "#f59e0b"},
	{"Rose", "#e11d48", "#0ea5e9"},
	{"Violet", "#7c3aed", "#22d3ee"},
	{"Amber", "#d97706", "#4f46e5"},
	{"Slate", "#334155", "#38bdf8"},
}

// hasUIKit reports whether any recipe chosen so far is a UI kit — the only case
// where re-branding is meaningful, since brand.Apply writes into a Tailwind or
// Bootstrap setup. Recipes mark themselves with category "UI kit".
func hasUIKit(reg *recipe.Registry, res [][]string) bool {
	for _, r := range chosenRecipes(reg, res) {
		if r.Category == uiKitCategory {
			return true
		}
	}
	return false
}

const uiKitCategory = "UI kit"

// brandChoices builds the brand step's options: the palette plus a "keep the
// kit's colours" option, but only once a UI kit is in the plan. With no UI kit
// it returns a single option, so the wizard auto-selects and skips the step —
// no brand question where there is nothing to brand.
func brandChoices(reg *recipe.Registry, res [][]string) []Choice {
	if !hasUIKit(reg, res) {
		return []Choice{{Key: brandNone, Label: "No UI kit, nothing to brand", Selected: true}}
	}
	out := []Choice{{Key: brandNone, Label: "Keep the kit's default colours", Selected: true}}
	for _, p := range brandPresets {
		out = append(out, Choice{
			Key:   brandToken(p.primary, p.accent),
			Label: p.label,
			Desc:  p.primary + " / " + p.accent,
		})
	}
	return out
}

// BrandFromResult extracts the chosen brand colours from a completed wizard
// result. Returns empty strings when the user kept the kit's defaults (or no UI
// kit was chosen), so a caller can skip brand.Apply entirely in that case.
func BrandFromResult(res [][]string) (primary, accent string) {
	for _, keys := range res {
		for _, k := range keys {
			if k == brandNone || !strings.HasPrefix(k, brandPrefix) {
				continue
			}
			spec := strings.TrimPrefix(k, brandPrefix)
			if p, a, ok := strings.Cut(spec, ","); ok {
				return p, a
			}
			return spec, ""
		}
	}
	return "", ""
}

// brandResult aliases brand.Result so a test can swap the brandApply seam
// without importing the brand package.
type brandResult = brand.Result

// brandApply is the seam for the real brand.Apply, so ApplyBrand's wiring can be
// covered without touching a real project's CSS on disk.
var brandApply = brand.Apply

// SetBrandApply swaps the brandApply seam and returns a restore func, so a test
// in another package (the CLI wiring test) can cover the post-build apply
// without painting real CSS. The seam itself stays unexported; only this
// swap/restore handle crosses the package boundary. defer the returned func.
func SetBrandApply(f func(dir, primary, accent string) (brand.Result, error)) (restore func()) {
	old := brandApply
	brandApply = f
	return func() { brandApply = old }
}

// ApplyBrand applies the wizard's chosen brand colours to a freshly-built
// project. It is a no-op (nil) when the user kept the kit's defaults or chose no
// UI kit, so a caller can run it unconditionally after a build. It is the
// post-build half of the brand step: the wizard records the choice, this writes
// it into the generated project's CSS framework.
func ApplyBrand(dir string, res [][]string) error {
	primary, accent := BrandFromResult(res)
	if primary == "" {
		return nil
	}
	_, err := brandApply(dir, primary, accent)
	return err
}
