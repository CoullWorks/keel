package brand

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// scaleSteps are the shade stops keel generates for every colour role, matching
// the Tailwind/shadcn convention. 50 is the lightest tint, 950 the darkest
// shade, 500 the seed colour itself. Fixed order so a generated map renders
// deterministically (maps have no order; a slice does).
var scaleSteps = []int{50, 100, 200, 300, 400, 500, 600, 700, 800, 900, 950}

// Scale is a full 50-950 tint/shade ramp for one colour role, keyed by step.
// It is a map (not a fixed struct) so a partially-detected ramp — an existing
// project may define only a few stops — round-trips without inventing shades,
// while a generated ramp fills every step. Iterate via ScaleSteps for order.
type Scale map[int]string

// ScaleStop is one (step, hex) pair from a Scale in canonical order — the shape
// the render loops consume. Named so the three token renderers share one type
// rather than repeating an anonymous struct across their signatures.
type ScaleStop struct {
	Step int
	Hex  string
}

// ScaleSteps returns the canonical stop order (50…950) so a Scale renders
// deterministically. Callers must not mutate the returned slice.
func ScaleSteps() []int { return scaleSteps }

// Ordered returns the scale's stops present, in canonical order, as (step, hex)
// pairs — the render loop's single source of order.
func (s Scale) Ordered() []ScaleStop {
	var out []ScaleStop
	for _, step := range scaleSteps {
		if hex, ok := s[step]; ok {
			out = append(out, ScaleStop{Step: step, Hex: hex})
		}
	}
	return out
}

// Radius is the corner-radius token set. One base value (the shadcn `--radius`)
// with the derived sm/md/lg/xl the frameworks expect; kept as strings so a
// non-px unit (rem) round-trips unchanged.
type Radius struct {
	Base string `yaml:"base"` // e.g. "0.5rem" — the --radius shadcn reads
}

// Font is the type-family token set: a sans stack and a mono stack. Values are
// full CSS font-family lists so they drop straight into --font-sans / theme.
type Font struct {
	Sans string `yaml:"sans"`
	Mono string `yaml:"mono"`
}

// Roles is the full semantic palette: brand + accent + the neutral/status roles
// and the surface tokens shadcn/Tailwind projects theme against. Every role is a
// full 50-950 Scale so a consumer can pick any weight; the surface tokens are
// single values because a background/foreground has no ramp.
//
// The zero value is not usable — build one with Generate (from seed hexes) or
// Detect (from an existing project's CSS).
type Roles struct {
	Brand       Scale `yaml:"brand"`
	Accent      Scale `yaml:"accent"`
	Neutral     Scale `yaml:"neutral"`
	Muted       Scale `yaml:"muted"`
	Success     Scale `yaml:"success"`
	Warning     Scale `yaml:"warning"`
	Destructive Scale `yaml:"destructive"`
}

// Surface holds the flat (non-ramped) semantic tokens a theme needs for chrome:
// page background/foreground, card, border and the focus ring. Generated from
// the neutral ramp for light, and re-derived for dark.
type Surface struct {
	Background string `yaml:"background"`
	Foreground string `yaml:"foreground"`
	Card       string `yaml:"card"`
	CardFg     string `yaml:"card_foreground"`
	Border     string `yaml:"border"`
	Ring       string `yaml:"ring"`
}

// BrandTokens is keel's framework-agnostic brand model: the whole design-token
// set (colour roles as 50-950 scales, surface chrome, radius, fonts) plus a
// Dark variant, all derived from one or two seed hexes. It is the value object
// the CLI, studio and build wizard read; each framework renderer maps it into
// idiomatic output (Tailwind @theme, theme.extend, Bootstrap $vars).
//
// The zero value is not usable — call Generate or Detect. Seed records the input
// hexes so `keel brand show` and a re-render can report/round-trip them.
type BrandTokens struct {
	Seed    Seed    `yaml:"seed"`
	Roles   Roles   `yaml:"roles"`
	Surface Surface `yaml:"surface"`
	Dark    Surface `yaml:"dark"`
	Radius  Radius  `yaml:"radius"`
	Font    Font    `yaml:"font"`
}

// Seed is the 1-2 hex inputs a token set was generated from: the primary drives
// the brand ramp, the accent (optional) its own ramp. Persisted so the global
// default and per-project override can be re-derived or edited by hex.
type Seed struct {
	Primary string `yaml:"primary"`
	Accent  string `yaml:"accent,omitempty"`
}

// defaultRadius is the shadcn/Tailwind base radius; a sensible neutral corner.
const defaultRadius = "0.5rem"

// Default font stacks mirror the Next.js/shadcn defaults (system-ui first so a
// generated theme looks right before a webfont is wired in).
const (
	defaultSans = "ui-sans-serif, system-ui, -apple-system, Segoe UI, Roboto, Helvetica, Arial, sans-serif"
	defaultMono = "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace"
)

// neutralSeed is the seed for the neutral ramp — a near-grey (slight blue tint,
// matching Tailwind's `slate`) so surface chrome reads as neutral, not tinted by
// the brand. muted reuses the neutral ramp; only its rendered role name differs.
const neutralSeed = "#64748b"

// statusSeeds are the fixed seeds for the status roles. They are conventional,
// accessible hues (Tailwind emerald/amber/red 500s) rather than brand-derived,
// because a "success" that shifts with the brand stops reading as success.
var statusSeeds = map[string]string{
	"success":     "#22c55e",
	"warning":     "#f59e0b",
	"destructive": "#ef4444",
}

// Generate builds a full BrandTokens from one or two seed hexes: primary drives
// the brand ramp, accent (or a hue-rotated primary when omitted) the accent
// ramp, plus the fixed neutral/status ramps, the light + dark surface tokens,
// and default radius/fonts. This is the "auto-generate from 1-2 seeds" primitive
// the Fitness Round-2 brand item calls for.
func Generate(primary, accent string) (BrandTokens, error) {
	primary = strings.TrimSpace(primary)
	accent = strings.TrimSpace(accent)
	if !Valid(primary) {
		return BrandTokens{}, fmt.Errorf("primary must be a hex colour like #5b21b6 (got %q)", primary)
	}
	if accent != "" && !Valid(accent) {
		return BrandTokens{}, fmt.Errorf("accent must be a hex colour like #3ab7bf (got %q)", accent)
	}
	// No accent given: rotate the primary hue ~150° for a complementary accent,
	// so a one-colour brand still gets a distinct accent ramp rather than a
	// duplicate of brand.
	accentSeed := accent
	if accentSeed == "" {
		accentSeed = rotateHue(primary, 150)
	}

	neutral := rampFrom(neutralSeed)
	roles := Roles{
		Brand:       rampFrom(primary),
		Accent:      rampFrom(accentSeed),
		Neutral:     neutral,
		Muted:       rampFrom(neutralSeed),
		Success:     rampFrom(statusSeeds["success"]),
		Warning:     rampFrom(statusSeeds["warning"]),
		Destructive: rampFrom(statusSeeds["destructive"]),
	}
	return BrandTokens{
		Seed:    Seed{Primary: primary, Accent: accent},
		Roles:   roles,
		Surface: lightSurface(neutral, roles.Brand),
		Dark:    darkSurface(neutral, roles.Brand),
		Radius:  Radius{Base: defaultRadius},
		Font:    Font{Sans: defaultSans, Mono: defaultMono},
	}, nil
}

// lightSurface derives the light-mode chrome tokens from the neutral ramp: a
// white page, near-black text, the palest neutral for borders, and the brand's
// mid weight for the focus ring.
func lightSurface(neutral, brand Scale) Surface {
	return Surface{
		Background: "#ffffff",
		Foreground: neutral[950],
		Card:       "#ffffff",
		CardFg:     neutral[950],
		Border:     neutral[200],
		Ring:       brand[500],
	}
}

// darkSurface is the inverse: the darkest neutral for the page, the palest for
// text, a slightly-lifted card, a mid neutral border, and a lighter brand ring
// (brand-400) so focus stays visible on a dark ground.
func darkSurface(neutral, brand Scale) Surface {
	return Surface{
		Background: neutral[950],
		Foreground: neutral[50],
		Card:       neutral[900],
		CardFg:     neutral[50],
		Border:     neutral[800],
		Ring:       brand[400],
	}
}

// rampFrom builds a 50-950 scale from a seed hex placed at 500, tinting toward
// white above it and shading toward black below. Mixing in linear-ish sRGB is
// crude but predictable and dependency-free — good enough for a generated
// starting palette a user then tweaks, and it keeps the seed exactly at 500.
func rampFrom(seed string) Scale {
	r, g, b := parseHex(seed)
	out := Scale{}
	for _, step := range scaleSteps {
		switch {
		case step == 500:
			out[step] = normHex(seed)
		case step < 500:
			// Tint toward white; the lighter the step the closer to white.
			t := tintAmount(step)
			out[step] = toHex(mix(r, g, b, 255, 255, 255, t))
		default:
			// Shade toward black; the darker the step the closer to black.
			t := shadeAmount(step)
			out[step] = toHex(mix(r, g, b, 0, 0, 0, t))
		}
	}
	return out
}

// tintAmount is how far a light step (50-400) mixes toward white. 50 is nearly
// white (0.95), 400 barely lifted (0.12) — a smooth ramp above the seed.
func tintAmount(step int) float64 {
	switch step {
	case 50:
		return 0.95
	case 100:
		return 0.88
	case 200:
		return 0.72
	case 300:
		return 0.50
	case 400:
		return 0.28
	default:
		return 0.12
	}
}

// shadeAmount is how far a dark step (600-950) mixes toward black.
func shadeAmount(step int) float64 {
	switch step {
	case 600:
		return 0.15
	case 700:
		return 0.32
	case 800:
		return 0.50
	case 900:
		return 0.68
	default: // 950
		return 0.80
	}
}

// mix blends colour (r,g,b) toward target (tr,tg,tb) by t in [0,1].
func mix(r, g, b, tr, tg, tb int, t float64) (int, int, int) {
	blend := func(c, tc int) int {
		return int(math.Round(float64(c) + (float64(tc)-float64(c))*t))
	}
	return blend(r, tr), blend(g, tg), blend(b, tb)
}

// rotateHue returns seed with its hue rotated by deg degrees (HSL), keeping
// saturation and lightness — used to derive a complementary accent from a lone
// primary.
func rotateHue(seed string, deg float64) string {
	r, g, b := parseHex(seed)
	h, s, l := rgbToHSL(r, g, b)
	h = math.Mod(h+deg, 360)
	if h < 0 {
		h += 360
	}
	nr, ng, nb := hslToRGB(h, s, l)
	return toHex(nr, ng, nb)
}

// --- colour primitives (dependency-free; sRGB 0-255) ---

// parseHex parses #rgb or #rrggbb to 0-255 components. Input is validated by
// Valid at the entry points, so a malformed value defaults to black rather than
// panicking — a generated palette from bad input is still a palette.
func parseHex(s string) (int, int, int) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return 0, 0, 0
	}
	r, _ := strconv.ParseInt(s[0:2], 16, 0)
	g, _ := strconv.ParseInt(s[2:4], 16, 0)
	b, _ := strconv.ParseInt(s[4:6], 16, 0)
	return int(r), int(g), int(b)
}

// normHex returns the canonical #rrggbb (lower-case, expanded) form of a hex.
func normHex(s string) string {
	r, g, b := parseHex(s)
	return toHex(r, g, b)
}

// toHex clamps and formats components as #rrggbb.
func toHex(r, g, b int) string {
	clamp := func(c int) int {
		if c < 0 {
			return 0
		}
		if c > 255 {
			return 255
		}
		return c
	}
	return fmt.Sprintf("#%02x%02x%02x", clamp(r), clamp(g), clamp(b))
}

// rgbToHSL converts sRGB 0-255 to HSL (h 0-360, s/l 0-1).
func rgbToHSL(ri, gi, bi int) (float64, float64, float64) {
	r, g, b := float64(ri)/255, float64(gi)/255, float64(bi)/255
	max := math.Max(r, math.Max(g, b))
	min := math.Min(r, math.Min(g, b))
	l := (max + min) / 2
	if max == min {
		return 0, 0, l // achromatic
	}
	d := max - min
	var s float64
	if l > 0.5 {
		s = d / (2 - max - min)
	} else {
		s = d / (max + min)
	}
	var h float64
	switch max {
	case r:
		h = (g - b) / d
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	return h * 60, s, l
}

// hslToRGB converts HSL (h 0-360, s/l 0-1) back to sRGB 0-255.
func hslToRGB(h, s, l float64) (int, int, int) {
	if s == 0 {
		v := int(math.Round(l * 255))
		return v, v, v
	}
	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q
	hk := h / 360
	conv := func(t float64) int {
		if t < 0 {
			t++
		}
		if t > 1 {
			t--
		}
		switch {
		case t < 1.0/6:
			return int(math.Round((p + (q-p)*6*t) * 255))
		case t < 1.0/2:
			return int(math.Round(q * 255))
		case t < 2.0/3:
			return int(math.Round((p + (q-p)*(2.0/3-t)*6) * 255))
		default:
			return int(math.Round(p * 255))
		}
	}
	return conv(hk + 1.0/3), conv(hk), conv(hk - 1.0/3)
}

// mergeScale overlays detected stops onto a generated ramp: a stop present in
// detected wins, the rest fall back to the generated ramp. This is the "pre-fill
// + merge rather than blind-overwrite" behaviour — an existing project's own
// weights are preserved, only the gaps are filled.
func mergeScale(generated, detected Scale) Scale {
	if len(detected) == 0 {
		return generated
	}
	out := Scale{}
	for k, v := range generated {
		out[k] = v
	}
	for k, v := range detected {
		out[k] = v
	}
	return out
}
