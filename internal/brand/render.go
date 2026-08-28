package brand

import (
	"fmt"
	"strings"
)

// This file maps a BrandTokens set idiomatically into each CSS stack. The block
// each renderer produces is what Apply/ApplyTokens writes between the keel
// markers, so a re-apply replaces exactly this block (the stripBlock contract in
// brand.go). Each renderer is pure (tokens → string) so it is trivially
// table-testable and the same output feeds the studio preview.

// namedRoles is the render order for the ramped colour roles — fixed so output
// is deterministic and diff-stable across runs.
var namedRoles = []struct {
	name  string
	scale func(Roles) Scale
}{
	{"brand", func(r Roles) Scale { return r.Brand }},
	{"accent", func(r Roles) Scale { return r.Accent }},
	{"neutral", func(r Roles) Scale { return r.Neutral }},
	{"muted", func(r Roles) Scale { return r.Muted }},
	{"success", func(r Roles) Scale { return r.Success }},
	{"warning", func(r Roles) Scale { return r.Warning }},
	{"destructive", func(r Roles) Scale { return r.Destructive }},
}

// RenderTailwindV4 produces the full @theme block for a token set: every role's
// 50-950 scale as --color-<role>-<step>, the flat surface tokens, radius and
// font vars, followed by a dark-mode block that overrides the surface tokens.
// This is the idiomatic Tailwind v4 mapping (design tokens as CSS custom
// properties Tailwind turns into utilities).
func RenderTailwindV4(t BrandTokens) string {
	var b strings.Builder
	b.WriteString("@theme {\n")
	for _, r := range namedRoles {
		for _, kv := range r.scale(t.Roles).Ordered() {
			fmt.Fprintf(&b, "  --color-%s-%d: %s;\n", r.name, kv.Step, kv.Hex)
		}
	}
	writeSurfaceVars(&b, t.Surface)
	if t.Radius.Base != "" {
		fmt.Fprintf(&b, "  --radius: %s;\n", t.Radius.Base)
	}
	if t.Font.Sans != "" {
		fmt.Fprintf(&b, "  --font-sans: %s;\n", t.Font.Sans)
	}
	if t.Font.Mono != "" {
		fmt.Fprintf(&b, "  --font-mono: %s;\n", t.Font.Mono)
	}
	b.WriteString("}\n")
	// Dark variant: re-declare only the surface tokens under .dark, which is how
	// a Tailwind v4 + shadcn project themes dark mode.
	if hasDark(t.Dark) {
		b.WriteString(".dark {\n")
		writeSurfaceVars(&b, t.Dark)
		b.WriteString("}\n")
	}
	return b.String()
}

func writeSurfaceVars(b *strings.Builder, s Surface) {
	pairs := []struct{ name, hex string }{
		{"background", s.Background},
		{"foreground", s.Foreground},
		{"card", s.Card},
		{"card-foreground", s.CardFg},
		{"border", s.Border},
		{"ring", s.Ring},
	}
	for _, p := range pairs {
		if p.hex != "" {
			fmt.Fprintf(b, "  --color-%s: %s;\n", p.name, p.hex)
		}
	}
}

func hasDark(s Surface) bool {
	return s.Background != "" || s.Foreground != "" || s.Card != "" ||
		s.CardFg != "" || s.Border != "" || s.Ring != ""
}

// RenderTailwindV3 produces a complete theme.extend colours module (a
// drop-in keel-brand.colors.js) — every role as a full stop map plus the flat
// surface tokens under DEFAULT/foreground keys. We emit a module rather than
// rewriting the user's tailwind.config.* because safely editing arbitrary JS is
// out of scope; the returned Note tells the user how to spread it in.
func RenderTailwindV3(t BrandTokens) string {
	var b strings.Builder
	b.WriteString("// keel brand tokens — spread into tailwind.config theme.extend.colors:\n")
	b.WriteString("//   extend: { colors: { ...require('./keel-brand.colors') } }\n")
	b.WriteString("module.exports = {\n")
	for _, r := range namedRoles {
		stops := r.scale(t.Roles).Ordered()
		if len(stops) == 0 {
			continue
		}
		fmt.Fprintf(&b, "  %s: {", r.name)
		parts := make([]string, 0, len(stops))
		for _, kv := range stops {
			parts = append(parts, fmt.Sprintf(" %d: '%s'", kv.Step, kv.Hex))
		}
		b.WriteString(strings.Join(parts, ","))
		b.WriteString(" },\n")
	}
	// Surface tokens as top-level colours so `bg-background`, `border-border`
	// etc. resolve, matching a shadcn Tailwind v3 setup.
	writeV3Surface(&b, t.Surface)
	b.WriteString("};\n")
	return b.String()
}

func writeV3Surface(b *strings.Builder, s Surface) {
	pairs := []struct{ name, hex string }{
		{"background", s.Background},
		{"foreground", s.Foreground},
		{"card", s.Card},
		{"border", s.Border},
		{"ring", s.Ring},
	}
	for _, p := range pairs {
		if p.hex != "" {
			fmt.Fprintf(b, "  %s: '%s',\n", p.name, p.hex)
		}
	}
}

// RenderBootstrap produces the Sass override block: the theme-colour $vars
// Bootstrap reads before its import ($primary/$secondary/$success/…), plus a
// :root CSS-variable block so the full ramps are usable at runtime too (the
// modern Bootstrap 5.3 CSS-variable surface). The $vars must precede the
// bootstrap import; applyBootstrapTokens handles that placement.
func RenderBootstrap(t BrandTokens) string {
	var b strings.Builder
	// Sass theme-colour overrides (compile-time).
	writeScssVar(&b, "primary", t.Roles.Brand[500])
	writeScssVar(&b, "secondary", t.Roles.Accent[500])
	writeScssVar(&b, "success", t.Roles.Success[500])
	writeScssVar(&b, "warning", t.Roles.Warning[500])
	writeScssVar(&b, "danger", t.Roles.Destructive[500])
	if t.Radius.Base != "" {
		fmt.Fprintf(&b, "$border-radius: %s;\n", t.Radius.Base)
	}
	// CSS-variable overrides (run-time) for the full ramps, so utilities and
	// custom CSS can reach any weight, not just the theme-colour 500s.
	b.WriteString(":root {\n")
	for _, r := range namedRoles {
		for _, kv := range r.scale(t.Roles).Ordered() {
			fmt.Fprintf(&b, "  --keel-%s-%d: %s;\n", r.name, kv.Step, kv.Hex)
		}
	}
	b.WriteString("}\n")
	return b.String()
}

func writeScssVar(b *strings.Builder, name, hex string) {
	if hex != "" {
		fmt.Fprintf(b, "$%s: %s;\n", name, hex)
	}
}

// String renders a deterministic multi-line dump of a token set for
// `keel brand show`: the seed, then each role's stops, surface, radius and font.
// Kept stable (roles in namedRoles order, stops in scale order) so it diffs.
func (t BrandTokens) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "seed:    primary=%s", t.Seed.Primary)
	if t.Seed.Accent != "" {
		fmt.Fprintf(&b, " accent=%s", t.Seed.Accent)
	}
	b.WriteString("\n")
	for _, r := range namedRoles {
		stops := r.scale(t.Roles).Ordered()
		if len(stops) == 0 {
			continue
		}
		fmt.Fprintf(&b, "%-12s", r.name+":")
		parts := make([]string, 0, len(stops))
		for _, kv := range stops {
			parts = append(parts, fmt.Sprintf("%d=%s", kv.Step, kv.Hex))
		}
		b.WriteString(strings.Join(parts, " "))
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "surface:     bg=%s fg=%s card=%s border=%s ring=%s\n",
		t.Surface.Background, t.Surface.Foreground, t.Surface.Card, t.Surface.Border, t.Surface.Ring)
	fmt.Fprintf(&b, "dark:        bg=%s fg=%s card=%s border=%s ring=%s\n",
		t.Dark.Background, t.Dark.Foreground, t.Dark.Card, t.Dark.Border, t.Dark.Ring)
	fmt.Fprintf(&b, "radius:      %s\n", t.Radius.Base)
	fmt.Fprintf(&b, "font.sans:   %s\n", t.Font.Sans)
	fmt.Fprintf(&b, "font.mono:   %s\n", t.Font.Mono)
	return b.String()
}
