package brand

import (
	"strings"
	"testing"
)

func TestGenerateFullScales(t *testing.T) {
	tk, err := Generate("#5b21b6", "#3ab7bf")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// The seed lands exactly at 500 for both ramps.
	if tk.Roles.Brand[500] != "#5b21b6" {
		t.Fatalf("brand 500 = %q, want #5b21b6", tk.Roles.Brand[500])
	}
	if tk.Roles.Accent[500] != "#3ab7bf" {
		t.Fatalf("accent 500 = %q, want #3ab7bf", tk.Roles.Accent[500])
	}
	// Every role has the full 50-950 ramp (11 stops).
	roles := map[string]Scale{
		"brand": tk.Roles.Brand, "accent": tk.Roles.Accent,
		"neutral": tk.Roles.Neutral, "muted": tk.Roles.Muted,
		"success": tk.Roles.Success, "warning": tk.Roles.Warning,
		"destructive": tk.Roles.Destructive,
	}
	for name, s := range roles {
		if len(s) != len(scaleSteps) {
			t.Fatalf("%s ramp has %d stops, want %d", name, len(s), len(scaleSteps))
		}
		for _, step := range scaleSteps {
			if !Valid(s[step]) {
				t.Fatalf("%s[%d] = %q is not a hex colour", name, step, s[step])
			}
		}
	}
	// Light and dark surfaces are populated and differ (dark bg is the darkest
	// neutral, light bg is white).
	if tk.Surface.Background != "#ffffff" {
		t.Fatalf("light bg = %q, want #ffffff", tk.Surface.Background)
	}
	if tk.Dark.Background == tk.Surface.Background {
		t.Fatalf("dark bg must differ from light bg")
	}
	if tk.Radius.Base == "" || tk.Font.Sans == "" || tk.Font.Mono == "" {
		t.Fatalf("radius/font tokens must be set: %+v %+v", tk.Radius, tk.Font)
	}
	// The seed is recorded for round-trip; accent recorded because it was given.
	if tk.Seed.Primary != "#5b21b6" || tk.Seed.Accent != "#3ab7bf" {
		t.Fatalf("seed = %+v", tk.Seed)
	}
}

func TestGenerateDerivesAccent(t *testing.T) {
	// No accent seed: a complementary accent ramp is derived, distinct from brand.
	tk, err := Generate("#5b21b6", "")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if tk.Roles.Accent[500] == tk.Roles.Brand[500] {
		t.Fatalf("derived accent 500 %q must differ from brand", tk.Roles.Accent[500])
	}
	if len(tk.Roles.Accent) != len(scaleSteps) {
		t.Fatalf("derived accent must be a full ramp, got %d stops", len(tk.Roles.Accent))
	}
	// Seed.Accent stays empty (it was derived, not supplied) so a re-render can
	// re-derive rather than freeze a guess.
	if tk.Seed.Accent != "" {
		t.Fatalf("seed accent should be empty when derived, got %q", tk.Seed.Accent)
	}
}

func TestGenerateRampMonotonicLightness(t *testing.T) {
	// The ramp should get darker as the step rises (50 lightest, 950 darkest).
	tk, _ := Generate("#3b82f6", "")
	prev := 999.0
	for _, step := range scaleSteps {
		r, g, b := parseHex(tk.Roles.Brand[step])
		_, _, l := rgbToHSL(r, g, b)
		if l > prev {
			t.Fatalf("step %d lighter than the previous (l=%.3f > %.3f) — ramp not monotonic", step, l, prev)
		}
		prev = l
	}
}

func TestGenerateBadHex(t *testing.T) {
	if _, err := Generate("nope", ""); err == nil {
		t.Fatal("want error for a bad primary")
	}
	if _, err := Generate("#5b21b6", "bad"); err == nil {
		t.Fatal("want error for a bad accent")
	}
}

func TestThreeDigitHexExpands(t *testing.T) {
	tk, err := Generate("#abc", "")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if tk.Roles.Brand[500] != "#aabbcc" {
		t.Fatalf("3-digit seed should expand to #aabbcc, got %q", tk.Roles.Brand[500])
	}
}

func TestRenderTailwindV4HasScaleDarkAndSurface(t *testing.T) {
	tk, _ := Generate("#5b21b6", "#3ab7bf")
	out := RenderTailwindV4(tk)
	for _, want := range []string{
		"@theme {",
		"--color-brand-50:", "--color-brand-500: #5b21b6;", "--color-brand-950:",
		"--color-accent-500: #3ab7bf;",
		"--color-success-500:", "--color-warning-500:", "--color-destructive-500:",
		"--color-background:", "--color-foreground:", "--color-border:", "--color-ring:",
		"--radius:", "--font-sans:", "--font-mono:",
		".dark {",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("v4 render missing %q:\n%s", want, out)
		}
	}
}

func TestRenderTailwindV3ModuleShape(t *testing.T) {
	tk, _ := Generate("#5b21b6", "#3ab7bf")
	out := RenderTailwindV3(tk)
	for _, want := range []string{
		"module.exports = {",
		"brand: {", "500: '#5b21b6'",
		"accent: {", "500: '#3ab7bf'",
		"background:", "border:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("v3 render missing %q:\n%s", want, out)
		}
	}
}

func TestRenderBootstrapVarsAndCSSVariables(t *testing.T) {
	tk, _ := Generate("#5b21b6", "#3ab7bf")
	out := RenderBootstrap(tk)
	for _, want := range []string{
		"$primary: #5b21b6;",
		"$secondary: #3ab7bf;",
		"$success:", "$warning:", "$danger:",
		":root {", "--keel-brand-500: #5b21b6;", "--keel-accent-950:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("bootstrap render missing %q:\n%s", want, out)
		}
	}
}

func TestTokensString(t *testing.T) {
	tk, _ := Generate("#5b21b6", "#3ab7bf")
	s := tk.String()
	for _, want := range []string{"seed:", "primary=#5b21b6", "brand:", "surface:", "dark:", "radius:", "font.sans:"} {
		if !strings.Contains(s, want) {
			t.Fatalf("String() missing %q:\n%s", want, s)
		}
	}
}

func TestRotateHue(t *testing.T) {
	// Rotating a colour's hue by 360 returns (near) the same colour.
	got := rotateHue("#3b82f6", 360)
	r1, g1, b1 := parseHex("#3b82f6")
	r2, g2, b2 := parseHex(got)
	near := func(a, b int) bool { d := a - b; return d <= 2 && d >= -2 }
	if !near(r1, r2) || !near(g1, g2) || !near(b1, b2) {
		t.Fatalf("rotateHue 360° = %q, want ~#3b82f6", got)
	}
}

func TestParseHexAndToHexRoundTrip(t *testing.T) {
	tests := []string{"#000000", "#ffffff", "#5b21b6", "#3ab7bf"}
	for _, h := range tests {
		r, g, b := parseHex(h)
		if got := toHex(r, g, b); got != h {
			t.Errorf("roundtrip %q -> %q", h, got)
		}
	}
	// Malformed input defaults to black rather than panicking.
	if r, g, b := parseHex("#zz"); r != 0 || g != 0 || b != 0 {
		t.Fatalf("bad hex should default to black, got %d,%d,%d", r, g, b)
	}
}
