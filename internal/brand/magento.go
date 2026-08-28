package brand

// This file adds Magento theme brand detection to the brand package. Magento
// does not theme against Tailwind or Bootstrap tokens: a frontend theme lives at
// app/design/frontend/<Vendor>/<theme>/, declares itself in theme.xml (a title
// and a <parent> theme it inherits from), and carries its brand as Less @-vars
// under web/css/source/ (_theme.less, _variables.less, _extend.less …).
//
// The active theme is chosen in the database (core_config_data
// design/theme/theme_id), which keel cannot read from disk. So instead of
// guessing, DetectMagento enumerates EVERY theme in the project, parses each
// theme's brand Less vars, resolves @var → @otherVar indirections within the
// theme and up its <parent> chain (ultimately to Magento/luma, the default), and
// maps the Less @-vars onto keel's BrandTokens roles. The studio presents the
// themes and marks the Luma fallback so a user sees the real brand values even
// without a running install.
//
// Crucially, Magento's OWN default themes (Magento/luma and its base
// Magento/blank) are NOT shipped under app/design/frontend — they are Composer
// packages installed under vendor/magento/theme-frontend-luma and
// vendor/magento/theme-frontend-blank. So a custom theme whose <parent> chain
// reaches Magento/luma, and the Luma fallback itself, must have their real brand
// vars read from vendor/ (child-over-parent: Luma over Blank), not just resolved
// to a bare name. DetectMagento therefore materialises those vendor core themes
// into the resolution graph so unresolved vars chase up into them. When vendor/
// is absent (no `composer install` yet), it falls back to a known Luma default
// palette and flags NoVendor so the studio can prompt "install dependencies to
// read the exact theme vars" rather than showing an empty/broken brand.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// MagentoTheme is one frontend theme found in a Magento project: its
// vendor/theme path, human title (from theme.xml), the parent it inherits from,
// the brand tokens resolved from its Less vars (chasing @var indirections and
// the parent chain), and flags marking it as the Luma default / a fallback.
type MagentoTheme struct {
	// Path is the theme's Vendor/theme id, e.g. "Acme/theme" — how Magento names
	// it and how the UI labels the picker.
	Path string
	// Vendor and Name are the split of Path, for display.
	Vendor string
	Name   string
	// Title is theme.xml's <title> (empty if none/unparseable).
	Title string
	// Parent is theme.xml's <parent>, e.g. "Magento/luma" (empty if none).
	Parent string
	// Roles/Surface are the brand tokens mapped from the theme's resolved Less
	// vars. Each Less brand var maps onto a keel role's 500 stop (or a surface
	// token); only vars that resolved to a real hex are present.
	Roles   Roles
	Surface Surface
	// IsLuma marks Magento's own default theme (Magento/luma) — the on-disk
	// fallback for the active theme keel can't read from the DB.
	IsLuma bool
	// Fallback is true when this theme's brand values had to be inherited from a
	// parent (no own brand vars of its own resolved), so the UI can say so.
	Fallback bool
	// FromVendor is true when this theme's resolved brand drew on the core Luma /
	// Blank vars read from vendor/ (a real Composer install), so the UI can say
	// the values are the actual shipped Luma brand rather than a hard-coded guess.
	FromVendor bool
}

// MagentoBrand is the whole-project Magento brand read: every frontend theme
// found, plus which one keel presents as the default (the Luma fallback, since
// the DB-selected active theme isn't on disk).
type MagentoBrand struct {
	// Themes is every app/design/frontend/*/* theme, in stable path order.
	Themes []MagentoTheme
	// DefaultIndex is the index into Themes of the theme to show first: the Luma
	// fallback when present, else the first theme. -1 when there are no themes.
	DefaultIndex int
	// Found is true when at least one frontend theme was parsed.
	Found bool
	// NoVendor is true when the Luma fallback had to be synthesised from keel's
	// built-in Luma default palette because vendor/ was absent (no `composer
	// install`), so the real theme-frontend-luma/blank Less could not be read. The
	// studio surfaces this as an "install dependencies to read the exact Luma
	// theme vars" hint rather than presenting the defaults as detected values.
	NoVendor bool
}

// magentoRoleMap maps Magento's Less brand var names onto keel's roles/surface,
// mirroring scssRoleMap for Bootstrap. Each Magento @-var (without the leading
// @) becomes a role's 500 stop or a flat surface token. The map is intentionally
// broad — Magento themes name the same concept several ways (@primary__color,
// @color-primary, @theme__color__primary) — so a real theme's vars land on a
// role rather than being dropped.
var magentoRoleMap = map[string]magentoTarget{
	// brand / primary
	"primary__color":                        {role: "brand"},
	"color-primary":                         {role: "brand"},
	"theme__color__primary":                 {role: "brand"},
	"button-primary__background":            {role: "brand"},
	"active__color":                         {role: "brand"},
	"link__color":                           {role: "brand"},
	"navigation-level0-item__active__color": {role: "brand"},
	// accent / secondary
	"secondary__color":        {role: "accent"},
	"color-secondary":         {role: "accent"},
	"theme__color__secondary": {role: "accent"},
	// status roles
	"color-success":          {role: "success"},
	"message-success__color": {role: "success"},
	"color-warning":          {role: "warning"},
	"message-warning__color": {role: "warning"},
	"color-error":            {role: "destructive"},
	"message-error__color":   {role: "destructive"},
	// surface / chrome (flat tokens, no ramp)
	"page__background-color":  {surface: "background"},
	"color-white":             {surface: "background"},
	"text__color":             {surface: "foreground"},
	"color-text":              {surface: "foreground"},
	"navigation__background":  {surface: "card"},
	"panel__background-color": {surface: "card"},
	"border-color__base":      {surface: "border"},
	"color-border":            {surface: "border"},
}

// magentoTarget is where a Magento Less var maps: exactly one of role (a ramped
// role, filled at the 500 stop) or surface (a flat chrome token).
type magentoTarget struct {
	role    string
	surface string
}

// magentoVarPriority ranks the vars that share a target so the canonical name for
// a concept wins over softer synonyms when a theme declares several (e.g. a theme
// that sets both @primary__color and @link__color should read its brand from
// @primary__color). Lower is stronger; unlisted vars default to 100.
var magentoVarPriority = map[string]int{
	"primary__color":             0,
	"color-primary":              1,
	"theme__color__primary":      2,
	"button-primary__background": 5,
	"active__color":              8,
	"link__color":                9,
	"secondary__color":           0,
	"color-secondary":            1,
	"theme__color__secondary":    2,
	"page__background-color":     0,
	"color-white":                5,
	"navigation__background":     0,
	"panel__background-color":    1,
}

// varPriority returns the resolution priority of a mapped var (lower = stronger).
func varPriority(name string) int {
	if p, ok := magentoVarPriority[name]; ok {
		return p
	}
	return 100
}

var (
	// @name: value;   — one Less variable declaration. The value runs to the
	// terminating ; and may itself be another @var (an indirection) or a hex.
	magentoVarRe = regexp.MustCompile(`@([A-Za-z0-9_-]+)\s*:\s*([^;]+);`)
	// A hex literal anywhere in a Less value (values can be `lighten(#abc, 5%)`).
	magentoHexRe = regexp.MustCompile(`#[0-9a-fA-F]{3,6}`)
	// theme.xml <title>…</title> and <parent>…</parent>.
	magentoTitleRe  = regexp.MustCompile(`(?s)<title>(.*?)</title>`)
	magentoParentRe = regexp.MustCompile(`(?s)<parent>(.*?)</parent>`)
)

// lumaThemeID / blankThemeID are Magento's own default themes. Luma is the
// storefront default (the on-disk fallback for the active theme keel can't read
// from the DB); Blank is its base. Neither ships under app/design/frontend —
// both are Composer packages under vendor/, so their real brand Less must be
// read from vendor/<vendor>/theme-frontend-<theme>/.
const (
	lumaThemeID  = "Magento/luma"
	blankThemeID = "Magento/blank"
)

// lumaDefaultVars is keel's built-in fallback for Magento's Luma brand, used ONLY
// when vendor/ is absent (no `composer install`) so the core theme-frontend-luma
// Less can't be read. These are the real shipped Luma defaults (Luma is a blue
// theme on a white page); the MagentoBrand is flagged NoVendor so the studio
// prompts the user to install dependencies for the exact, current values rather
// than presenting these as detected. Names match magentoRoleMap keys.
var lumaDefaultVars = map[string]string{
	"primary__color":         "#1979c3", // Luma's blue link/primary
	"active__color":          "#ff5501", // Luma's orange active accent
	"link__color":            "#006bb4",
	"secondary__color":       "#eb5202",
	"page__background-color": "#ffffff",
	"text__color":            "#333333",
	"border-color__base":     "#c1c1c1",
	"navigation__background": "#f0f0f0",
	"color-success":          "#006400",
	"color-error":            "#e02b27",
	"color-warning":          "#c07600",
}

// IsMagentoProject reports whether dir looks like a Magento (or Mage-OS) install:
// it has the app/design/frontend theme tree. This is the trigger the studio uses
// to route the brand editor down the Magento path instead of the CSS-kit path.
func IsMagentoProject(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "app", "design", "frontend"))
	return err == nil && info.IsDir()
}

// magentoVendorThemeDir maps a "Vendor/theme" id (as written in theme.xml's
// <parent>) to the Composer package dir the theme is installed under. Magento
// packages a frontend theme as vendor/<vendor-lower>/theme-frontend-<theme-lower>
// (e.g. Magento/luma → vendor/magento/theme-frontend-luma). It returns the
// absolute path within dir and true when the id has a Vendor/theme shape.
//
// Adobe Commerce and Mage-OS keep the SAME package convention for the shared
// Blank/Luma base themes — Mage-OS ships drop-in replacements as
// mage-os/theme-frontend-* but they still declare themselves as Magento/luma /
// Magento/blank and, when installed via the Mage-OS mirror, land under
// vendor/magento/theme-frontend-*. So the vendor prefix follows the id's own
// Vendor segment, and callers probe both the id-derived path and the magento/
// path (see coreThemeDirs) to cover a Mage-OS install where the package vendor
// differs from the theme's declared vendor.
func magentoVendorThemeDir(dir, id string) (string, bool) {
	vendor, name := splitThemePath(id)
	if vendor == "" || name == "" {
		return "", false
	}
	pkg := "theme-frontend-" + strings.ToLower(name)
	return filepath.Join(dir, "vendor", strings.ToLower(vendor), pkg), true
}

// coreThemeDirs returns the candidate vendor dirs a "Vendor/theme" id could be
// installed under, most-specific first: the id-derived path, then the magento/
// path (the shared package vendor for the Blank/Luma base themes even on
// Adobe Commerce / Mage-OS installs). Callers read the first that exists.
func coreThemeDirs(dir, id string) []string {
	var dirs []string
	if p, ok := magentoVendorThemeDir(dir, id); ok {
		dirs = append(dirs, p)
	}
	_, name := splitThemePath(id)
	if name != "" {
		magentoPath := filepath.Join(dir, "vendor", "magento", "theme-frontend-"+strings.ToLower(name))
		if len(dirs) == 0 || dirs[0] != magentoPath {
			dirs = append(dirs, magentoPath)
		}
	}
	return dirs
}

// rawTheme is one theme's parsed inputs in the resolution graph: its id, title,
// the <parent> it inherits from, its own Less vars, and whether those vars came
// from a real vendor/ package (a core Luma/Blank theme) rather than a custom
// theme under app/design/frontend.
type rawTheme struct {
	path, title, parent string
	vars                map[string]string
	fromVendor          bool
}

// DetectMagento reads every frontend theme in a Magento project and resolves
// each theme's brand tokens. It enumerates app/design/frontend/<Vendor>/<theme>,
// parses theme.xml (title + parent) and the Less brand vars, then resolves each
// mapped var to a real colour by chasing @var indirections within the theme and
// up the <parent> chain to Magento/luma. Themes with no brand vars of their own
// inherit their parent's resolved brand (marked Fallback).
//
// Magento's own Luma/Blank base themes live under vendor/, not
// app/design/frontend, so DetectMagento materialises them into the resolution
// graph: any <parent> that points at a core theme (or the synthetic Luma
// fallback) is loaded from vendor/<vendor>/theme-frontend-<theme>/, child-over-
// parent (Luma over Blank). When vendor/ is absent it falls back to keel's
// built-in Luma default palette and flags NoVendor. A project with no theme tree
// returns MagentoBrand{Found:false}, no error.
func DetectMagento(dir string) (MagentoBrand, error) {
	root := filepath.Join(dir, "app", "design", "frontend")
	vendors, err := os.ReadDir(root)
	if err != nil {
		// No theme tree: not a fully-set-up Magento frontend. Not an error — the
		// studio shows a clear "no themes found" state.
		return MagentoBrand{Found: false, DefaultIndex: -1}, nil
	}

	// First pass: read every CUSTOM theme's raw vars + parent, keyed by
	// "Vendor/name", so the second pass can resolve a var up a theme's parent
	// chain.
	raws := map[string]rawTheme{}
	var order []string
	for _, v := range vendors {
		if !v.IsDir() {
			continue
		}
		vendorDir := filepath.Join(root, v.Name())
		themes, err := os.ReadDir(vendorDir)
		if err != nil {
			continue
		}
		for _, th := range themes {
			if !th.IsDir() {
				continue
			}
			id := v.Name() + "/" + th.Name()
			themeDir := filepath.Join(vendorDir, th.Name())
			title, parent := readThemeXML(filepath.Join(themeDir, "theme.xml"))
			raws[id] = rawTheme{
				path:   id,
				title:  title,
				parent: parent,
				vars:   readMagentoLessVars(themeDir),
			}
			order = append(order, id)
		}
	}
	if len(order) == 0 {
		return MagentoBrand{Found: false, DefaultIndex: -1}, nil
	}

	// Does any theme's declared <parent> reach a core theme (Magento/luma or
	// Magento/blank)? Those live under vendor/, not app/design/frontend, so their
	// real brand vars must be read from vendor/ — and the Luma fallback is only
	// meaningful when the project actually inherits from it.
	inheritsCore := anyThemeInheritsCore(raws)

	// Materialise the core Luma/Blank themes (and any parent that resolves to a
	// vendor/ package) into the graph so the parent chain chases into the REAL
	// shipped brand vars. Returns whether vendor/ could be read — when it could
	// not, the referenced core nodes are absent and we fall back to the built-in
	// Luma palette below.
	vendorReadable := loadVendorCoreThemes(dir, raws)

	// NoVendor is only worth signalling when the project inherits from a core
	// theme keel then couldn't read from vendor/ — that's the "install
	// dependencies to read the exact Luma vars" case. A project that doesn't touch
	// Luma at all isn't blocked on vendor/.
	noVendor := inheritsCore && !vendorReadable

	// Ensure a Luma node exists in the graph when the project inherits from it (or
	// from Blank) but Luma wasn't materialised from vendor/: synthesise it so the
	// studio can present the Luma fallback. Without vendor/ it carries the
	// built-in default palette so the fallback still shows real Luma colours.
	if inheritsCore {
		luma, ok := raws[lumaThemeID]
		switch {
		case !ok:
			// Luma isn't on disk or in vendor/: add the fallback node. Without
			// vendor/ it carries the built-in default palette.
			raws[lumaThemeID] = synthLumaNode(noVendor)
			order = append(order, lumaThemeID)
		case noVendor && len(luma.vars) == 0:
			// A Luma node exists but is an empty vendor stub (probe found no readable
			// package) — fill it with the default palette so the fallback isn't blank.
			raws[lumaThemeID] = synthLumaNode(true)
		}
	}

	out := MagentoBrand{Found: true, DefaultIndex: -1, NoVendor: noVendor}
	for _, id := range order {
		r := raws[id]
		vendor, name := splitThemePath(id)
		th := MagentoTheme{
			Path: id, Vendor: vendor, Name: name,
			Title: r.title, Parent: r.parent,
			IsLuma: strings.EqualFold(id, lumaThemeID),
		}
		ownResolved, usedVendor := 0, false
		// Several Less vars map to the same keel target (e.g. @primary__color,
		// @link__color and @active__color all → brand). Resolving each and applying
		// blindly would let map iteration order (or an inherited parent var) clobber
		// the theme's own colour. So collect every resolved mapping, then apply the
		// single best candidate per target: the theme's own declaration wins over an
		// inherited one, closer-in-the-chain wins over farther, ties broken by var
		// name for determinism.
		best := map[magentoTarget]targetCandidate{}
		for _, varName := range magentoRoleVarOrder {
			target := magentoRoleMap[varName]
			hex, srcTheme, depth, ok := resolveThemeVar(raws, id, varName)
			if !ok {
				continue
			}
			if srcTheme == id {
				ownResolved++
			}
			if raws[srcTheme].fromVendor {
				usedVendor = true
			}
			cand := targetCandidate{
				hex:      normHex(hex),
				own:      srcTheme == id,
				depth:    depth,
				priority: varPriority(varName),
				varName:  varName,
			}
			if cur, seen := best[target]; !seen || cand.better(cur) {
				best[target] = cand
			}
		}
		for target, cand := range best {
			applyMagentoTarget(&th.Roles, &th.Surface, target, cand.hex)
		}
		th.Fallback = ownResolved == 0
		th.FromVendor = usedVendor
		out.Themes = append(out.Themes, th)
		if th.IsLuma && out.DefaultIndex < 0 {
			out.DefaultIndex = len(out.Themes) - 1
		}
	}
	if out.DefaultIndex < 0 && len(out.Themes) > 0 {
		out.DefaultIndex = 0 // no Luma present: default to the first theme
	}
	return out, nil
}

// anyThemeInheritsCore reports whether any custom theme's declared <parent> is a
// Magento core theme (Magento/luma or Magento/blank) — i.e. whether the project
// depends on the vendor-shipped Luma/Blank brand at all.
func anyThemeInheritsCore(raws map[string]rawTheme) bool {
	for _, r := range raws {
		if isCoreTheme(r.parent) {
			return true
		}
	}
	return false
}

// isCoreTheme reports whether id names a Magento core theme (Luma or Blank),
// case-insensitively.
func isCoreTheme(id string) bool {
	return strings.EqualFold(id, lumaThemeID) || strings.EqualFold(id, blankThemeID)
}

// loadVendorCoreThemes walks the <parent> chain of every theme already in raws
// and, for any parent not already present (the core Magento/luma, Magento/blank,
// and their own parents), reads it from vendor/<vendor>/theme-frontend-<theme>/
// and adds it to raws. It follows the chain transitively (Luma → Blank) so the
// whole inheritance path is materialised. It reports whether at least one core
// theme was actually read from vendor/, so the caller can decide whether to fall
// back to the built-in Luma palette and flag NoVendor.
func loadVendorCoreThemes(dir string, raws map[string]rawTheme) (vendorReadable bool) {
	vendorRoot := filepath.Join(dir, "vendor")
	if info, err := os.Stat(vendorRoot); err != nil || !info.IsDir() {
		return false // no vendor/ at all: nothing to read
	}
	loadedAny := false
	// Iterate to a fixed point: loading a parent can introduce a new grandparent.
	for {
		var pending []string
		for _, r := range raws {
			if r.parent == "" {
				continue
			}
			if _, ok := raws[r.parent]; !ok {
				pending = append(pending, r.parent)
			}
		}
		if len(pending) == 0 {
			break
		}
		progressed := false
		for _, id := range pending {
			if _, ok := raws[id]; ok {
				continue // already loaded this round
			}
			vars, title, parent, ok := readVendorCoreTheme(dir, id)
			if !ok {
				// Parent not installed under vendor/: record an empty node so we don't
				// re-probe it forever and so its own parent (if any) isn't chased.
				raws[id] = rawTheme{path: id, vars: map[string]string{}, fromVendor: true}
				progressed = true
				continue
			}
			raws[id] = rawTheme{path: id, title: title, parent: parent, vars: vars, fromVendor: true}
			loadedAny = true
			progressed = true
		}
		if !progressed {
			break
		}
	}
	return loadedAny
}

// readVendorCoreTheme reads a core theme's Less vars + theme.xml from its Composer
// package dir (vendor/<vendor>/theme-frontend-<theme>/), trying each candidate in
// coreThemeDirs. ok is false when no candidate dir has readable Less sources.
func readVendorCoreTheme(dir, id string) (vars map[string]string, title, parent string, ok bool) {
	for _, themeDir := range coreThemeDirs(dir, id) {
		if info, err := os.Stat(filepath.Join(themeDir, "web", "css", "source")); err != nil || !info.IsDir() {
			continue
		}
		vars = readMagentoLessVars(themeDir)
		title, parent = readThemeXML(filepath.Join(themeDir, "theme.xml"))
		return vars, title, parent, true
	}
	return nil, "", "", false
}

// synthLumaNode builds the graph node for Magento/luma when it isn't on disk.
// With vendor/ present it is an empty node (its parent, real Blank, will have
// been materialised); without vendor/ it carries the built-in Luma default
// palette so the fallback still surfaces real Luma colours.
func synthLumaNode(noVendor bool) rawTheme {
	vars := map[string]string{}
	if noVendor {
		for k, v := range lumaDefaultVars {
			vars[k] = v
		}
	}
	return rawTheme{path: lumaThemeID, title: "Luma", parent: blankThemeID, vars: vars, fromVendor: true}
}

// resolveThemeVar resolves a mapped brand var for a theme across the whole
// inheritance chain: it accumulates a merged var view (child-over-parent) walking
// startTheme → parent → … and resolves @var indirections against that merged view
// so a value like `@primary__color: @color-primary` lands even when @color-primary
// is defined in a parent (Luma/Blank) rather than the child. It returns the hex,
// the id of the theme whose OWN vars first supplied the mapped name (so the caller
// can tell own from inherited), the 0-based depth of that theme in the chain
// (0 = the starting theme itself), and ok.
func resolveThemeVar(raws map[string]rawTheme, startTheme, name string) (hex, srcTheme string, depth int, ok bool) {
	// Build the merged view once (child-over-parent) plus the chain order so we can
	// attribute which theme introduced the mapped name (own vs inherited).
	merged := map[string]string{}
	seen := map[string]bool{}
	var chain []string
	for theme := startTheme; theme != "" && !seen[theme]; {
		seen[theme] = true
		chain = append(chain, theme)
		theme = raws[theme].parent
	}
	// Merge parent-first so a child's declaration overrides its parent's.
	for i := len(chain) - 1; i >= 0; i-- {
		for k, v := range raws[chain[i]].vars {
			merged[k] = v
		}
	}
	if _, present := merged[name]; !present {
		return "", "", 0, false
	}
	hex, ok = resolveVar(merged, name, 0)
	if !ok {
		return "", "", 0, false
	}
	// Attribute the source: the first theme in the chain (child→ancestor) that
	// declares the mapped name itself.
	srcTheme, depth = startTheme, 0
	for i, theme := range chain {
		if _, has := raws[theme].vars[name]; has {
			srcTheme, depth = theme, i
			break
		}
	}
	return hex, srcTheme, depth, true
}

// targetCandidate is one resolved Less var competing to fill a keel target
// (several vars map to the same role/surface). own/depth/priority/varName order
// the competition so the theme's own, nearest, canonical, deterministically-chosen
// colour wins.
type targetCandidate struct {
	hex      string
	own      bool // the mapped var was declared by the starting theme itself
	depth    int  // distance up the parent chain of the declaring theme (0 = self)
	priority int  // varPriority of the mapped var (lower = stronger)
	varName  string
}

// better reports whether c should replace cur for the same target: an own
// declaration beats an inherited one, then a shallower chain depth wins, then the
// stronger (canonical) var wins, then the lexically-smaller var name breaks the
// tie so the result is deterministic regardless of map iteration order.
func (c targetCandidate) better(cur targetCandidate) bool {
	if c.own != cur.own {
		return c.own
	}
	if c.depth != cur.depth {
		return c.depth < cur.depth
	}
	if c.priority != cur.priority {
		return c.priority < cur.priority
	}
	return c.varName < cur.varName
}

// magentoRoleVarOrder is the sorted list of magentoRoleMap keys, iterated instead
// of ranging the map so var resolution is deterministic (map order is random).
var magentoRoleVarOrder = sortedKeys(magentoRoleMap)

// sortedKeys returns m's keys in lexical order.
func sortedKeys(m map[string]magentoTarget) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// resolveVar returns the concrete hex a Less var resolves to within one var map,
// chasing @var → @otherVar indirections up to a small depth (Less brand vars are
// rarely nested more than a couple deep; the bound guards against a cycle). A
// value that already contains a hex resolves immediately; a value that is another
// @var recurses; anything else fails. The ok is false when the var is absent or
// resolves to no colour, so a caller can fall through to the parent chain.
func resolveVar(vars map[string]string, name string, depth int) (string, bool) {
	if depth > 8 {
		return "", false
	}
	raw, ok := vars[name]
	if !ok {
		return "", false
	}
	raw = strings.TrimSpace(raw)
	if hex := magentoHexRe.FindString(raw); hex != "" {
		return hex, true
	}
	// The value is (or references) another @var — chase the first one.
	if m := magentoVarRefRe.FindStringSubmatch(raw); m != nil {
		return resolveVar(vars, m[1], depth+1)
	}
	return "", false
}

// magentoVarRefRe matches a leading @var reference in a Less value.
var magentoVarRefRe = regexp.MustCompile(`@([A-Za-z0-9_-]+)`)

// readMagentoLessVars reads all @name: value; declarations from the theme's Less
// brand sources under web/css/source/. It merges every .less file found (a later
// file's declaration wins, matching Less's own last-wins semantics), so a theme
// that splits vars across _variables.less/_theme.less/_extend.less is read whole.
func readMagentoLessVars(themeDir string) map[string]string {
	vars := map[string]string{}
	srcDir := filepath.Join(themeDir, "web", "css", "source")
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return vars
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".less") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(srcDir, e.Name()))
		if err != nil {
			continue
		}
		for _, m := range magentoVarRe.FindAllStringSubmatch(string(b), -1) {
			vars[m[1]] = strings.TrimSpace(m[2])
		}
	}
	return vars
}

// readThemeXML parses a theme.xml for its <title> and <parent>. A missing or
// unparseable file yields empty strings — a theme without a theme.xml still
// counts (its brand vars alone are enough to show).
func readThemeXML(path string) (title, parent string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	s := string(b)
	if m := magentoTitleRe.FindStringSubmatch(s); m != nil {
		title = strings.TrimSpace(m[1])
	}
	if m := magentoParentRe.FindStringSubmatch(s); m != nil {
		parent = strings.TrimSpace(m[1])
	}
	return title, parent
}

// applyMagentoTarget writes a resolved hex onto the theme's Roles (at the 500
// stop) or Surface, per the var's mapping target.
func applyMagentoTarget(roles *Roles, surface *Surface, target magentoTarget, hex string) {
	if target.role != "" {
		setRoleStop(roles, target.role, 500, hex)
		return
	}
	if target.surface != "" {
		setSurface(surface, target.surface, hex)
	}
}

// splitThemePath splits a "Vendor/name" id into its two parts.
func splitThemePath(id string) (vendor, name string) {
	if i := strings.IndexByte(id, '/'); i >= 0 {
		return id[:i], id[i+1:]
	}
	return id, ""
}
