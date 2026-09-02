package brand

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Detected is what Detect found in an existing project: which CSS stack it uses,
// the file the tokens live in, and the roles/surface/radius/font it could parse.
// It is deliberately partial — a project rarely defines every stop or role — so
// a caller merges it onto a generated set (pre-fill + merge) rather than treating
// absence as a value. Stack is "" and Found false when no CSS stack is present.
type Detected struct {
	Stack   string  // "tailwind4" | "tailwind3" | "bootstrap" | ""
	File    string  // the file tokens were read from (relative to dir)
	Found   bool    // whether a CSS stack was found at all
	Roles   Roles   // whatever colour ramps/roles were parsed (may be empty)
	Surface Surface // whatever surface tokens were parsed (fields may be empty)
	Radius  Radius  // parsed base radius, or empty
	Font    Font    // parsed font stacks, or empty
}

// Detect parses an existing project's brand tokens so keel can pre-fill an editor
// and merge rather than blindly overwrite. It recognises the same three stacks
// Apply writes to, in the same precedence (Tailwind v4 → v3 → Bootstrap):
//
//   - Tailwind v4: `--color-<role>-<step>` / `--color-<surface>` / `--radius-*`
//     / `--font-*` inside an @theme block in the CSS entry.
//   - Tailwind v3: `theme.extend.colors` entries in tailwind.config.* (a shallow
//     regex read — we don't execute JS, only lift obvious `role: { 500: '#..' }`
//     and `role: '#..'` shapes).
//   - Bootstrap: `$primary`/`$secondary`/`$success`… Sass variables.
//
// The studio's brand UI consumes this to seed its form; the CLI merge path uses
// it so a re-apply keeps a user's own weights. A project with no CSS stack
// returns Detected{Found:false} and no error.
func Detect(dir string) (Detected, error) {
	loc := locate(dir) // one tree walk, shared precedence with ApplyTokens
	switch loc.stack {
	case stackTailwindV4:
		return detectTailwindV4(dir, loc.file)
	case stackTailwindV3:
		return detectTailwindV3(dir, loc.file)
	case stackBootstrap:
		return detectBootstrap(dir, loc.file)
	default:
		return Detected{Found: false}, nil
	}
}

// --- Tailwind v4: @theme custom properties ---

var (
	// --color-<role>-<step>: <hex>;  e.g. --color-brand-500: #5b21b6;
	twV4ScaleRe = regexp.MustCompile(`--color-([a-z]+)-(\d{2,3})\s*:\s*(#[0-9a-fA-F]{3,6})`)
	// --color-<surface>: <hex>;  e.g. --color-background: #ffffff;
	twV4FlatRe     = regexp.MustCompile(`--color-([a-z-]+)\s*:\s*(#[0-9a-fA-F]{3,6})`)
	twV4RadiusRe   = regexp.MustCompile(`--radius(?:-[a-z]+)?\s*:\s*([0-9.]+[a-z%]+)`)
	twV4FontSansRe = regexp.MustCompile(`--font-sans\s*:\s*([^;]+);`)
	twV4FontMonoRe = regexp.MustCompile(`--font-mono\s*:\s*([^;]+);`)
)

func detectTailwindV4(dir, css string) (Detected, error) {
	safeBase, err := filepath.Abs(dir)
	if err != nil {
		return Detected{}, fmt.Errorf("refusing path outside %q", dir)
	}
	p, err := filepath.Abs(css)
	if err != nil || !strings.HasPrefix(p, safeBase) {
		return Detected{}, fmt.Errorf("refusing path outside %q", dir)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return Detected{}, err
	}
	return detectTailwindV4String(rel(dir, css), string(b)), nil
}

// detectTailwindV4String parses v4 tokens out of already-loaded CSS. Split from
// the file reader so the apply path can detect from the *stripped* body (the
// user's own tokens, minus keel's own previous block) rather than merging keel's
// last output back into itself.
func detectTailwindV4String(file, s string) Detected {
	d := Detected{Stack: "tailwind4", File: file, Found: true}

	for _, m := range twV4ScaleRe.FindAllStringSubmatch(s, -1) {
		role, stepStr, hex := m[1], m[2], m[3]
		step, err := strconv.Atoi(stepStr)
		if err != nil {
			continue
		}
		setRoleStop(&d.Roles, role, step, normHex(hex))
	}
	for _, m := range twV4FlatRe.FindAllStringSubmatch(s, -1) {
		name, hex := m[1], normHex(m[2])
		// Skip the ramped roles the scale regex already handled.
		if strings.ContainsRune(name, '-') || !isSurfaceToken(name) {
			continue
		}
		setSurface(&d.Surface, name, hex)
	}
	// A "--color-card-foreground" style surface token has a dash; catch those.
	for _, m := range twV4FlatRe.FindAllStringSubmatch(s, -1) {
		setSurface(&d.Surface, m[1], normHex(m[2]))
	}
	if m := twV4RadiusRe.FindStringSubmatch(s); m != nil {
		d.Radius.Base = m[1]
	}
	if m := twV4FontSansRe.FindStringSubmatch(s); m != nil {
		d.Font.Sans = strings.TrimSpace(m[1])
	}
	if m := twV4FontMonoRe.FindStringSubmatch(s); m != nil {
		d.Font.Mono = strings.TrimSpace(m[1])
	}
	return d
}

// --- Tailwind v3: theme.extend.colors ---

var (
	// role: { 500: '#..', 600: "#.." }  — the role name is restricted to keel's
	// known colour roles so an outer wrapper key (colors, theme) with nested
	// braces isn't matched as a role and doesn't swallow the real role's body
	// (the shallow [^}]* body can't span nested braces).
	twV3RoleBlockRe = regexp.MustCompile(`(brand|accent|neutral|muted|success|warning|destructive)\s*:\s*\{([^{}]*)\}`)
	// <step>: '#..'  inside a role block.
	twV3StopRe = regexp.MustCompile(`(\d{2,3})\s*:\s*['"](#[0-9a-fA-F]{3,6})['"]`)
	// role: '#..'  — a single-value colour (maps to the 500 stop).
	twV3FlatRe = regexp.MustCompile(`([a-zA-Z]+)\s*:\s*['"](#[0-9a-fA-F]{3,6})['"]`)
)

func detectTailwindV3(dir, cfg string) (Detected, error) {
	safeBase, err := filepath.Abs(dir)
	if err != nil {
		return Detected{}, fmt.Errorf("refusing path outside %q", dir)
	}
	p, err := filepath.Abs(cfg)
	if err != nil || !strings.HasPrefix(p, safeBase) {
		return Detected{}, fmt.Errorf("refusing path outside %q", dir)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return Detected{}, err
	}
	return detectTailwindV3String(rel(dir, cfg), string(b)), nil
}

// detectTailwindV3String parses theme.extend colours out of already-loaded config
// text (see detectTailwindV4String for why the split exists).
func detectTailwindV3String(file, s string) Detected {
	d := Detected{Stack: "tailwind3", File: file, Found: true}

	for _, m := range twV3RoleBlockRe.FindAllStringSubmatch(s, -1) {
		role, body := m[1], m[2]
		if !isColourRole(role) {
			continue
		}
		for _, sm := range twV3StopRe.FindAllStringSubmatch(body, -1) {
			step, err := strconv.Atoi(sm[1])
			if err != nil {
				continue
			}
			setRoleStop(&d.Roles, role, step, normHex(sm[2]))
		}
	}
	// Single-value roles (brand: '#..') map to the 500 stop, but only when the
	// role wasn't already captured as a block above.
	for _, m := range twV3FlatRe.FindAllStringSubmatch(s, -1) {
		role := m[1]
		if !isColourRole(role) || roleHasStops(d.Roles, role) {
			continue
		}
		setRoleStop(&d.Roles, role, 500, normHex(m[2]))
	}
	return d
}

// --- Bootstrap: Sass $variables ---

var scssVarRe = regexp.MustCompile(`\$([a-z-]+)\s*:\s*(#[0-9a-fA-F]{3,6})`)

// scssRoleMap maps Bootstrap's Sass theme-colour names onto keel's roles.
var scssRoleMap = map[string]string{
	"primary":   "brand",
	"secondary": "accent",
	"success":   "success",
	"warning":   "warning",
	"danger":    "destructive",
}

func detectBootstrap(dir, scss string) (Detected, error) {
	safeBase, err := filepath.Abs(dir)
	if err != nil {
		return Detected{}, fmt.Errorf("refusing path outside %q", dir)
	}
	p, err := filepath.Abs(scss)
	if err != nil || !strings.HasPrefix(p, safeBase) {
		return Detected{}, fmt.Errorf("refusing path outside %q", dir)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return Detected{}, err
	}
	return detectBootstrapString(rel(dir, scss), string(b)), nil
}

// detectBootstrapString parses Bootstrap Sass $vars out of already-loaded text.
func detectBootstrapString(file, s string) Detected {
	d := Detected{Stack: "bootstrap", File: file, Found: true}
	for _, m := range scssVarRe.FindAllStringSubmatch(s, -1) {
		name, hex := m[1], normHex(m[2])
		if role, ok := scssRoleMap[name]; ok {
			setRoleStop(&d.Roles, role, 500, hex)
		}
	}
	return d
}

// --- role/surface helpers ---

// colourRoles is the set of role names keel understands, so a stray Tailwind
// entry (spacing, screens) isn't misread as a colour ramp.
var colourRoles = map[string]bool{
	"brand": true, "accent": true, "neutral": true, "muted": true,
	"success": true, "warning": true, "destructive": true,
}

func isColourRole(name string) bool { return colourRoles[strings.ToLower(name)] }

// surfaceTokens is the set of flat (non-ramped) chrome tokens.
var surfaceTokens = map[string]bool{
	"background": true, "foreground": true, "card": true,
	"card-foreground": true, "border": true, "ring": true,
}

func isSurfaceToken(name string) bool { return surfaceTokens[name] }

// setRoleStop records a parsed stop for a role on d.Roles, creating the Scale on
// first use. Unknown roles are ignored (guarded by isColourRole at call sites).
func setRoleStop(r *Roles, role string, step int, hex string) {
	s := roleScale(r, role)
	if s == nil {
		return
	}
	(*s)[step] = hex
}

// roleScale returns a pointer to the named role's Scale, initialising it.
func roleScale(r *Roles, role string) *Scale {
	var p *Scale
	switch strings.ToLower(role) {
	case "brand":
		p = &r.Brand
	case "accent":
		p = &r.Accent
	case "neutral":
		p = &r.Neutral
	case "muted":
		p = &r.Muted
	case "success":
		p = &r.Success
	case "warning":
		p = &r.Warning
	case "destructive":
		p = &r.Destructive
	default:
		return nil
	}
	if *p == nil {
		*p = Scale{}
	}
	return p
}

// roleHasStops reports whether a role already has any parsed stops.
func roleHasStops(r Roles, role string) bool {
	switch strings.ToLower(role) {
	case "brand":
		return len(r.Brand) > 0
	case "accent":
		return len(r.Accent) > 0
	case "neutral":
		return len(r.Neutral) > 0
	case "muted":
		return len(r.Muted) > 0
	case "success":
		return len(r.Success) > 0
	case "warning":
		return len(r.Warning) > 0
	case "destructive":
		return len(r.Destructive) > 0
	}
	return false
}

// setSurface records a parsed flat chrome token onto d.Surface.
func setSurface(s *Surface, name, hex string) {
	switch name {
	case "background":
		s.Background = hex
	case "foreground":
		s.Foreground = hex
	case "card":
		s.Card = hex
	case "card-foreground":
		s.CardFg = hex
	case "border":
		s.Border = hex
	case "ring":
		s.Ring = hex
	}
}

// Merge overlays a detected token set onto tokens: each detected ramp/stop and
// non-empty surface/radius/font value wins, the rest of tokens stands. This is
// the "pre-fill + merge rather than blind-overwrite" primitive — Generate builds
// a full set from the seed, Merge folds in whatever the project already defined.
func (t BrandTokens) Merge(d Detected) BrandTokens {
	out := t
	out.Roles = Roles{
		Brand:       mergeScale(t.Roles.Brand, d.Roles.Brand),
		Accent:      mergeScale(t.Roles.Accent, d.Roles.Accent),
		Neutral:     mergeScale(t.Roles.Neutral, d.Roles.Neutral),
		Muted:       mergeScale(t.Roles.Muted, d.Roles.Muted),
		Success:     mergeScale(t.Roles.Success, d.Roles.Success),
		Warning:     mergeScale(t.Roles.Warning, d.Roles.Warning),
		Destructive: mergeScale(t.Roles.Destructive, d.Roles.Destructive),
	}
	out.Surface = mergeSurface(t.Surface, d.Surface)
	if d.Radius.Base != "" {
		out.Radius.Base = d.Radius.Base
	}
	if d.Font.Sans != "" {
		out.Font.Sans = d.Font.Sans
	}
	if d.Font.Mono != "" {
		out.Font.Mono = d.Font.Mono
	}
	return out
}

// mergeSurface overlays non-empty detected surface fields onto base.
func mergeSurface(base, over Surface) Surface {
	if over.Background != "" {
		base.Background = over.Background
	}
	if over.Foreground != "" {
		base.Foreground = over.Foreground
	}
	if over.Card != "" {
		base.Card = over.Card
	}
	if over.CardFg != "" {
		base.CardFg = over.CardFg
	}
	if over.Border != "" {
		base.Border = over.Border
	}
	if over.Ring != "" {
		base.Ring = over.Ring
	}
	return base
}
