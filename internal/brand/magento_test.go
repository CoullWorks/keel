package brand

import (
	"path/filepath"
	"testing"
)

// writeMagentoTheme lays down one theme: its theme.xml (title + optional parent)
// and a single _theme.less carrying the brand vars.
func writeMagentoTheme(t *testing.T, dir, id, title, parent, less string) {
	t.Helper()
	base := filepath.Join("app", "design", "frontend", id)
	xml := "<theme><title>" + title + "</title>"
	if parent != "" {
		xml += "<parent>" + parent + "</parent>"
	}
	xml += "</theme>"
	writeFile(t, dir, filepath.Join(base, "theme.xml"), xml)
	if less != "" {
		writeFile(t, dir, filepath.Join(base, "web", "css", "source", "_theme.less"), less)
	}
}

// themeByPath finds a detected theme by its Vendor/name id, failing the test if
// it's absent — clearer than assuming a slice index now that the Luma fallback
// may be appended after the custom themes.
func themeByPath(t *testing.T, mb MagentoBrand, path string) MagentoTheme {
	t.Helper()
	for _, th := range mb.Themes {
		if th.Path == path {
			return th
		}
	}
	t.Fatalf("theme %q not found in %d themes", path, len(mb.Themes))
	return MagentoTheme{}
}

func TestDetectMagentoNotAProject(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "readme.md", "no magento here")
	if IsMagentoProject(dir) {
		t.Fatalf("a dir without app/design/frontend must not read as Magento")
	}
	mb, err := DetectMagento(dir)
	if err != nil {
		t.Fatalf("DetectMagento: %v", err)
	}
	if mb.Found {
		t.Fatalf("DetectMagento should report Found=false with no theme tree")
	}
	if mb.DefaultIndex != -1 {
		t.Fatalf("DefaultIndex should be -1 when nothing found, got %d", mb.DefaultIndex)
	}
}

func TestDetectMagentoOwnThemeVars(t *testing.T) {
	dir := t.TempDir()
	writeMagentoTheme(t, dir, "Acme/theme", "Acme Theme", "Magento/luma", `
@primary__color: #5B21B6;
@secondary__color: #3ab7bf;
@page__background-color: #ffffff;
@text__color: #0a0a0a;
@color-success: #22c55e;
`)
	if !IsMagentoProject(dir) {
		t.Fatalf("app/design/frontend must read as a Magento project")
	}
	mb, err := DetectMagento(dir)
	if err != nil {
		t.Fatalf("DetectMagento: %v", err)
	}
	// The custom theme inherits Magento/luma, so the Luma fallback is materialised
	// alongside it (from the built-in palette here — no vendor/ in this fixture).
	if !mb.Found || len(mb.Themes) != 2 {
		t.Fatalf("want the custom theme + the Luma fallback, got found=%v n=%d", mb.Found, len(mb.Themes))
	}
	th := themeByPath(t, mb, "Acme/theme")
	if th.Vendor != "Acme" || th.Name != "theme" {
		t.Fatalf("theme id split wrong: %+v", th)
	}
	if th.Title != "Acme Theme" || th.Parent != "Magento/luma" {
		t.Fatalf("theme.xml not parsed: title=%q parent=%q", th.Title, th.Parent)
	}
	// @primary__color -> brand 500, normalised to lower-case.
	if th.Roles.Brand[500] != "#5b21b6" {
		t.Fatalf("@primary__color -> brand 500 = %q, want #5b21b6", th.Roles.Brand[500])
	}
	if th.Roles.Accent[500] != "#3ab7bf" {
		t.Fatalf("@secondary__color -> accent 500 = %q", th.Roles.Accent[500])
	}
	if th.Roles.Success[500] != "#22c55e" {
		t.Fatalf("@color-success -> success 500 = %q", th.Roles.Success[500])
	}
	if th.Surface.Background != "#ffffff" {
		t.Fatalf("@page__background-color -> surface bg = %q", th.Surface.Background)
	}
	if th.Surface.Foreground != "#0a0a0a" {
		t.Fatalf("@text__color -> surface fg = %q", th.Surface.Foreground)
	}
	if th.Fallback {
		t.Fatalf("a theme with its own resolved vars must not be marked Fallback")
	}
}

func TestDetectMagentoIndirection(t *testing.T) {
	// @primary__color: @color-primary; @color-primary: #123456; — the brand var
	// chases an indirection to a real hex.
	dir := t.TempDir()
	writeMagentoTheme(t, dir, "Acme/theme", "Acme", "", `
@color-primary: #123456;
@primary__color: @color-primary;
`)
	mb, err := DetectMagento(dir)
	if err != nil {
		t.Fatalf("DetectMagento: %v", err)
	}
	th := mb.Themes[0]
	if th.Roles.Brand[500] != "#123456" {
		t.Fatalf("indirection @primary__color -> @color-primary -> #123456 not resolved, got %q", th.Roles.Brand[500])
	}
}

func TestDetectMagentoParentChainFallback(t *testing.T) {
	// A child theme with NO brand vars of its own inherits its parent's brand and
	// is marked Fallback; the parent resolves normally. The child's parent points
	// at a local luma-ish theme that carries the real hex.
	dir := t.TempDir()
	writeMagentoTheme(t, dir, "Magento/luma", "Luma", "", `@primary__color: #aabbcc;`)
	// Child declares a parent and no brand vars of its own.
	writeMagentoTheme(t, dir, "Acme/child", "Child", "Magento/luma", "")
	mb, err := DetectMagento(dir)
	if err != nil {
		t.Fatalf("DetectMagento: %v", err)
	}
	if len(mb.Themes) != 2 {
		t.Fatalf("want 2 themes, got %d", len(mb.Themes))
	}
	var child, luma MagentoTheme
	for _, th := range mb.Themes {
		if th.Path == "Acme/child" {
			child = th
		}
		if th.Path == "Magento/luma" {
			luma = th
		}
	}
	// The child inherited the parent's brand up the <parent> chain.
	if child.Roles.Brand[500] != "#aabbcc" {
		t.Fatalf("child should inherit parent brand #aabbcc, got %q", child.Roles.Brand[500])
	}
	if !child.Fallback {
		t.Fatalf("a theme with no own brand vars must be marked Fallback")
	}
	// Luma is marked as the default and resolves its own brand.
	if !luma.IsLuma {
		t.Fatalf("Magento/luma should be marked IsLuma")
	}
	if luma.Roles.Brand[500] != "#aabbcc" {
		t.Fatalf("luma brand = %q", luma.Roles.Brand[500])
	}
	// Luma is the default index (the on-disk fallback for the DB active theme).
	if mb.DefaultIndex < 0 || mb.Themes[mb.DefaultIndex].Path != "Magento/luma" {
		t.Fatalf("DefaultIndex should point at Magento/luma, got %d", mb.DefaultIndex)
	}
}

func TestDetectMagentoDefaultsToFirstWithoutLuma(t *testing.T) {
	dir := t.TempDir()
	writeMagentoTheme(t, dir, "Acme/one", "One", "", `@primary__color: #111111;`)
	mb, err := DetectMagento(dir)
	if err != nil {
		t.Fatalf("DetectMagento: %v", err)
	}
	if mb.DefaultIndex != 0 {
		t.Fatalf("with no Luma present, DefaultIndex should be 0, got %d", mb.DefaultIndex)
	}
}

func TestDetectMagentoIndirectionCycleIsSafe(t *testing.T) {
	// A pathological @a: @b; @b: @a; must not loop forever — it resolves to no
	// colour and the role stays empty rather than hanging.
	dir := t.TempDir()
	writeMagentoTheme(t, dir, "Acme/theme", "Acme", "", `
@primary__color: @color-primary;
@color-primary: @primary__color;
`)
	mb, err := DetectMagento(dir)
	if err != nil {
		t.Fatalf("DetectMagento: %v", err)
	}
	if len(mb.Themes[0].Roles.Brand) != 0 {
		t.Fatalf("a cyclic indirection should resolve to nothing, got %v", mb.Themes[0].Roles.Brand)
	}
}

func TestDetectMagentoThemeWithoutThemeXML(t *testing.T) {
	// A theme dir with only Less vars (no theme.xml) still counts.
	dir := t.TempDir()
	base := filepath.Join("app", "design", "frontend", "Acme", "bare")
	writeFile(t, dir, filepath.Join(base, "web", "css", "source", "_theme.less"), "@primary__color: #654321;")
	mb, err := DetectMagento(dir)
	if err != nil {
		t.Fatalf("DetectMagento: %v", err)
	}
	if !mb.Found || len(mb.Themes) != 1 {
		t.Fatalf("a theme with no theme.xml should still be found, got %+v", mb)
	}
	th := mb.Themes[0]
	if th.Title != "" || th.Parent != "" {
		t.Fatalf("no theme.xml should mean empty title/parent, got %q/%q", th.Title, th.Parent)
	}
	if th.Roles.Brand[500] != "#654321" {
		t.Fatalf("brand var should still resolve, got %q", th.Roles.Brand[500])
	}
}

func TestDetectMagentoMultipleLessFiles(t *testing.T) {
	// Vars split across _variables.less and _theme.less must both be read.
	dir := t.TempDir()
	base := filepath.Join("app", "design", "frontend", "Acme/theme")
	writeFile(t, dir, filepath.Join(base, "theme.xml"), "<theme><title>Acme</title></theme>")
	writeFile(t, dir, filepath.Join(base, "web", "css", "source", "_variables.less"), "@color-primary: #001122;")
	writeFile(t, dir, filepath.Join(base, "web", "css", "source", "_theme.less"), "@primary__color: @color-primary;")
	mb, err := DetectMagento(dir)
	if err != nil {
		t.Fatalf("DetectMagento: %v", err)
	}
	if mb.Themes[0].Roles.Brand[500] != "#001122" {
		t.Fatalf("var split across less files not merged, got %q", mb.Themes[0].Roles.Brand[500])
	}
}

// writeVendorTheme lays down a Magento CORE theme where it really lives — the
// Composer package dir vendor/<vendor>/theme-frontend-<theme>/ — with its
// theme.xml (title + optional parent) and a _theme.less carrying brand vars. This
// mirrors vendor/magento/theme-frontend-luma / theme-frontend-blank on a real,
// composer-installed store.
func writeVendorTheme(t *testing.T, dir, vendor, theme, title, parent, less string) {
	t.Helper()
	base := filepath.Join("vendor", vendor, "theme-frontend-"+theme)
	xml := "<theme><title>" + title + "</title>"
	if parent != "" {
		xml += "<parent>" + parent + "</parent>"
	}
	xml += "</theme>"
	writeFile(t, dir, filepath.Join(base, "theme.xml"), xml)
	writeFile(t, dir, filepath.Join(base, "web", "css", "source", "_theme.less"), less)
}

// lumaVendorLess is a representative slice of the real
// theme-frontend-luma/web/css/source/_theme.less + _variables.less brand vars.
// Luma sets @page__background-color from @color-white and references brand/base
// colours defined in Blank/the UI lib; here Blank supplies the primitives so the
// child-over-parent merge and vendor indirection are exercised end to end.
const lumaVendorLess = `
@primary__color: @color-blue1;
@link__color: @color-blue2;
@active__color: @color-orange1;
@secondary__color: @color-orange2;
@page__background-color: @color-white;
@text__color: @color-gray-dark;
@navigation__background: @color-gray94;
@panel__background-color: @color-gray-light;
@border-color__base: @color-gray80;
@color-success: #006400;
@color-error: #e02b27;
@color-warning: #c07600;
`

// blankVendorLess supplies the base colour primitives Luma's vars reference —
// the real Blank theme / Magento UI lib role. A couple of these (the greys, the
// page white) are overridden by Luma above; the rest Luma inherits unchanged.
const blankVendorLess = `
@color-blue1: #1979c3;
@color-blue2: #006bb4;
@color-orange1: #ff5501;
@color-orange2: #eb5202;
@color-white: #ffffff;
@color-gray-dark: #333333;
@color-gray94: #f0f0f0;
@color-gray-light: #fafafa;
@color-gray80: #c1c1c1;
`

// TestDetectMagentoVendorLumaFullSet is the headline fix: a custom theme inheriting
// Magento/luma, with the REAL Luma/Blank core themes present under vendor/, must
// surface the COMPLETE set of Luma brand vars — resolved through vendor
// indirections and the child-over-parent (Luma over Blank) merge — mapped onto
// keel roles, not an empty fallback.
func TestDetectMagentoVendorLumaFullSet(t *testing.T) {
	dir := t.TempDir()
	// Custom theme with NO brand vars of its own, inheriting Luma.
	writeMagentoTheme(t, dir, "Acme/child", "Acme Child", "Magento/luma", "")
	// The real core themes, where they actually live.
	writeVendorTheme(t, dir, "magento", "luma", "Luma", "Magento/blank", lumaVendorLess)
	writeVendorTheme(t, dir, "magento", "blank", "Blank", "", blankVendorLess)

	mb, err := DetectMagento(dir)
	if err != nil {
		t.Fatalf("DetectMagento: %v", err)
	}
	if mb.NoVendor {
		t.Fatalf("vendor/ is present and readable — NoVendor must be false")
	}
	child := themeByPath(t, mb, "Acme/child")

	// The full Luma brand set, resolved via vendor indirections, mapped to roles.
	want := map[string]string{
		"brand (primary)": child.Roles.Brand[500],
		"accent":          child.Roles.Accent[500],
		"success":         child.Roles.Success[500],
		"warning":         child.Roles.Warning[500],
		"destructive":     child.Roles.Destructive[500],
	}
	expect := map[string]string{
		"brand (primary)": "#1979c3", // @primary__color -> @color-blue1
		"accent":          "#eb5202", // @secondary__color -> @color-orange2
		"success":         "#006400",
		"warning":         "#c07600",
		"destructive":     "#e02b27",
	}
	for k, got := range want {
		if got != expect[k] {
			t.Errorf("%s = %q, want %q", k, got, expect[k])
		}
	}
	// Surface tokens resolve through vendor too.
	if child.Surface.Background != "#ffffff" {
		t.Errorf("surface background = %q, want #ffffff", child.Surface.Background)
	}
	if child.Surface.Foreground != "#333333" {
		t.Errorf("surface foreground = %q, want #333333", child.Surface.Foreground)
	}
	if child.Surface.Border != "#c1c1c1" {
		t.Errorf("surface border = %q, want #c1c1c1", child.Surface.Border)
	}
	// The child had no own vars, so its whole brand is inherited (Fallback) and it
	// drew on the vendor core themes (FromVendor).
	if !child.Fallback {
		t.Errorf("child with no own vars must be marked Fallback")
	}
	if !child.FromVendor {
		t.Errorf("child resolved from vendor Luma/Blank must be marked FromVendor")
	}
}

// TestDetectMagentoVendorChildOverParentMerge asserts Luma's own declaration wins
// over Blank's for the same var (child-over-parent). Blank sets a grey; Luma
// overrides the navigation background; the resolved value must be Luma's.
func TestDetectMagentoVendorChildOverParentMerge(t *testing.T) {
	dir := t.TempDir()
	writeMagentoTheme(t, dir, "Acme/child", "Acme Child", "Magento/luma", "")
	// Blank declares navigation background grey-A; Luma overrides it to grey-B.
	writeVendorTheme(t, dir, "magento", "blank", "Blank", "",
		"@navigation__background: #aaaaaa;\n@border-color__base: #bbbbbb;")
	writeVendorTheme(t, dir, "magento", "luma", "Luma", "Magento/blank",
		"@navigation__background: #cccccc;")

	mb, err := DetectMagento(dir)
	if err != nil {
		t.Fatalf("DetectMagento: %v", err)
	}
	child := themeByPath(t, mb, "Acme/child")
	// Luma (the child of Blank) wins for the overridden var.
	if child.Surface.Card != "#cccccc" {
		t.Errorf("child-over-parent merge: navigation background = %q, want Luma's #cccccc", child.Surface.Card)
	}
	// The var only Blank declares is still inherited.
	if child.Surface.Border != "#bbbbbb" {
		t.Errorf("var only in Blank should still resolve = %q, want #bbbbbb", child.Surface.Border)
	}
}

// TestDetectMagentoVendorIndirection asserts a var chases an indirection defined
// in a DIFFERENT vendor theme up the chain: the child's @primary__color points at
// @color-primary, which is only declared in vendor Blank.
func TestDetectMagentoVendorIndirection(t *testing.T) {
	dir := t.TempDir()
	writeMagentoTheme(t, dir, "Acme/child", "Acme Child", "Magento/luma",
		"@primary__color: @color-primary;")
	writeVendorTheme(t, dir, "magento", "luma", "Luma", "Magento/blank", "@link__color: @color-link;")
	writeVendorTheme(t, dir, "magento", "blank", "Blank", "",
		"@color-primary: #445566;\n@color-link: #778899;")

	mb, err := DetectMagento(dir)
	if err != nil {
		t.Fatalf("DetectMagento: %v", err)
	}
	child := themeByPath(t, mb, "Acme/child")
	// @primary__color (own, canonical) -> @color-primary (in Blank) -> #445566.
	if child.Roles.Brand[500] != "#445566" {
		t.Errorf("vendor indirection @primary__color -> @color-primary -> #445566 = %q", child.Roles.Brand[500])
	}
	// The child declared @primary__color itself, so it is NOT a pure fallback.
	if child.Fallback {
		t.Errorf("child that declares its own @primary__color must not be Fallback")
	}
}

// TestDetectMagentoNoVendorFallback asserts graceful degradation: a theme inherits
// Luma but vendor/ is absent (no composer install). The Luma fallback is
// synthesised from keel's built-in default palette, NoVendor is flagged, and the
// brand is never empty/broken.
func TestDetectMagentoNoVendorFallback(t *testing.T) {
	dir := t.TempDir()
	writeMagentoTheme(t, dir, "Acme/child", "Acme Child", "Magento/luma", "")

	mb, err := DetectMagento(dir)
	if err != nil {
		t.Fatalf("DetectMagento: %v", err)
	}
	if !mb.NoVendor {
		t.Fatalf("no vendor/ but inherits Luma — NoVendor must be true")
	}
	// A Luma fallback node is present as the default.
	if mb.DefaultIndex < 0 || mb.Themes[mb.DefaultIndex].Path != "Magento/luma" {
		t.Fatalf("DefaultIndex should point at the Luma fallback, got %d", mb.DefaultIndex)
	}
	luma := themeByPath(t, mb, "Magento/luma")
	if !luma.IsLuma {
		t.Fatalf("the fallback must be marked IsLuma")
	}
	// The built-in palette gives a real, non-empty Luma brand.
	if luma.Roles.Brand[500] == "" {
		t.Fatalf("the Luma fallback palette must fill the brand role, got empty")
	}
	// The child still inherits a real (non-empty) brand via the fallback.
	child := themeByPath(t, mb, "Acme/child")
	if child.Roles.Brand[500] == "" {
		t.Fatalf("child inheriting the Luma fallback must get a real brand, got empty")
	}
	if !child.Fallback {
		t.Errorf("child with no own vars must be Fallback")
	}
}

// TestDetectMagentoNoVendorNotFlaggedWithoutCoreParent asserts NoVendor is NOT set
// for a self-contained theme that doesn't inherit Luma/Blank — such a project
// isn't blocked on `composer install`.
func TestDetectMagentoNoVendorNotFlaggedWithoutCoreParent(t *testing.T) {
	dir := t.TempDir()
	writeMagentoTheme(t, dir, "Acme/standalone", "Standalone", "", "@primary__color: #123123;")
	mb, err := DetectMagento(dir)
	if err != nil {
		t.Fatalf("DetectMagento: %v", err)
	}
	if mb.NoVendor {
		t.Fatalf("a theme with no core parent must not flag NoVendor")
	}
	// No Luma fallback invented; the sole theme is the default.
	if len(mb.Themes) != 1 || mb.DefaultIndex != 0 {
		t.Fatalf("want the single standalone theme as default, got n=%d idx=%d", len(mb.Themes), mb.DefaultIndex)
	}
}

// TestMagentoVendorThemeDir asserts the Vendor/theme -> vendor package path
// mapping, including the case-normalisation Magento applies.
func TestMagentoVendorThemeDir(t *testing.T) {
	tests := []struct {
		id   string
		want string // path under the project dir (forward-slash form)
		ok   bool
	}{
		{"Magento/luma", filepath.Join("vendor", "magento", "theme-frontend-luma"), true},
		{"Magento/blank", filepath.Join("vendor", "magento", "theme-frontend-blank"), true},
		{"Acme/Storefront", filepath.Join("vendor", "acme", "theme-frontend-storefront"), true},
		{"noslash", "", false},
		{"", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			got, ok := magentoVendorThemeDir("/proj", tc.id)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !tc.ok {
				return
			}
			if got != filepath.Join("/proj", tc.want) {
				t.Fatalf("magentoVendorThemeDir(%q) = %q, want %q", tc.id, got, filepath.Join("/proj", tc.want))
			}
		})
	}
}

// TestDetectMagentoMageOSVendorPath asserts a Mage-OS style install resolves: a
// theme declaring parent Magento/luma while the actual package sits under
// vendor/magento/theme-frontend-luma (the shared package vendor coreThemeDirs
// probes) still reads the vendor brand.
func TestDetectMagentoMageOSVendorPath(t *testing.T) {
	dir := t.TempDir()
	writeMagentoTheme(t, dir, "Acme/child", "Acme Child", "Magento/luma", "")
	// Package installed under magento/ (the shared vendor for the base themes).
	writeVendorTheme(t, dir, "magento", "luma", "Luma", "Magento/blank", "@primary__color: #0abcde;")
	writeVendorTheme(t, dir, "magento", "blank", "Blank", "", "")

	mb, err := DetectMagento(dir)
	if err != nil {
		t.Fatalf("DetectMagento: %v", err)
	}
	if mb.NoVendor {
		t.Fatalf("vendor Luma present — NoVendor must be false")
	}
	child := themeByPath(t, mb, "Acme/child")
	if child.Roles.Brand[500] != "#0abcde" {
		t.Errorf("Mage-OS vendor path Luma brand = %q, want #0abcde", child.Roles.Brand[500])
	}
}
