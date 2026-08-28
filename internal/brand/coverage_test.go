package brand

import (
	"path/filepath"
	"testing"
)

func TestApplyTokensNoFramework(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "readme.md", "no ui kit")
	tk, _ := Generate("#5b21b6", "")
	if _, err := ApplyTokens(dir, tk); err == nil {
		t.Fatal("ApplyTokens must error when no CSS stack is found")
	}
}

func TestScaleStepsOrder(t *testing.T) {
	got := ScaleSteps()
	if len(got) != 11 || got[0] != 50 || got[len(got)-1] != 950 {
		t.Fatalf("ScaleSteps = %v, want 50..950", got)
	}
}

func TestScaleOrderedSkipsAbsentStops(t *testing.T) {
	s := Scale{500: "#123456", 50: "#abcdef"}
	ord := s.Ordered()
	if len(ord) != 2 || ord[0].Step != 50 || ord[1].Step != 500 {
		t.Fatalf("Ordered = %+v, want [50,500] in order", ord)
	}
}

// Detect must parse every surface token and merge every surface field, so a
// re-brand of a shadcn project keeps the user's chrome. This exercises the
// setSurface / mergeSurface field switches end to end.
func TestDetectAndMergeAllSurfaceFields(t *testing.T) {
	dir := t.TempDir()
	css := `@import "tailwindcss";
@theme {
  --color-background: #101010;
  --color-foreground: #f0f0f0;
  --color-card: #202020;
  --color-card-foreground: #e0e0e0;
  --color-border: #303030;
  --color-ring: #404040;
}
`
	writeFile(t, dir, "app.css", css)
	d, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	s := d.Surface
	if s.Background != "#101010" || s.Foreground != "#f0f0f0" || s.Card != "#202020" ||
		s.CardFg != "#e0e0e0" || s.Border != "#303030" || s.Ring != "#404040" {
		t.Fatalf("surface not fully parsed: %+v", s)
	}
	// Merging these onto a generated set: every detected surface field wins.
	gen, _ := Generate("#5b21b6", "")
	m := gen.Merge(d)
	if m.Surface != s {
		t.Fatalf("merged surface = %+v, want detected %+v", m.Surface, s)
	}
}

func TestRoleHasStopsAllRoles(t *testing.T) {
	r := Roles{
		Brand: Scale{500: "#1"}, Accent: Scale{500: "#2"},
		Neutral: Scale{500: "#3"}, Muted: Scale{500: "#4"},
		Success: Scale{500: "#5"}, Warning: Scale{500: "#6"},
		Destructive: Scale{500: "#7"},
	}
	for _, role := range []string{"brand", "accent", "neutral", "muted", "success", "warning", "destructive"} {
		if !roleHasStops(r, role) {
			t.Errorf("roleHasStops(%q) = false, want true", role)
		}
	}
	if roleHasStops(Roles{}, "brand") {
		t.Error("empty roles should have no stops")
	}
	if roleHasStops(r, "unknown") {
		t.Error("unknown role should report no stops")
	}
}

func TestDetectV3MissingFileFallsThrough(t *testing.T) {
	// A dir with a .ts config that references a colour role inline.
	dir := t.TempDir()
	writeFile(t, dir, "tailwind.config.ts",
		"export default { theme: { extend: { colors: { brand: { 500: '#5b21b6' } } } } }")
	d, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.Stack != "tailwind3" || d.Roles.Brand[500] != "#5b21b6" {
		t.Fatalf("v3 .ts detect = %+v", d)
	}
}

// applyBootstrapTokens with no import line prepends the block (covers the
// no-import branch through the token path).
func TestApplyBootstrapTokensNoImport(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "styles.scss", "// uses bootstrap via a bundler\n.foo { color: red; }\n")
	tk, _ := Generate("#5b21b6", "#3ab7bf")
	res, err := ApplyTokens(dir, tk)
	if err != nil {
		t.Fatalf("ApplyTokens: %v", err)
	}
	got := readFile(t, dir, res.File)
	if got[:len(scssStart)] != scssStart {
		t.Fatalf("overrides should be prepended:\n%s", got)
	}
}

// Detect on a v4 file that already has keel-owned tokens still parses them (the
// merge path in applyTailwindV4Tokens strips first, but Detect itself reads all).
func TestDetectV4RadiusVariants(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, filepath.Join("a", "b.css"),
		"@import \"tailwindcss\";\n@theme {\n  --radius-lg: 1rem;\n}\n")
	d, _ := Detect(dir)
	if d.Radius.Base != "1rem" {
		t.Fatalf("radius variant not parsed: %q", d.Radius.Base)
	}
}
