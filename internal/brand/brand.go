// Package brand applies a project's brand colours to whatever CSS framework it
// uses. It is project-scoped: point it at a project dir and it detects Tailwind
// v4 (@theme in the CSS entry), Tailwind v3 (tailwind.config.*) or Bootstrap
// (Sass $primary), then writes the correct, idiomatic block. Shared by the CLI
// (`keel brand`), studio, and the build wizard.
package brand

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	startMark = "/* keel:brand start */"
	endMark   = "/* keel:brand end */"
	scssStart = "// keel:brand start"
	scssEnd   = "// keel:brand end"
)

var hexRe = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// Result describes what Apply did.
type Result struct {
	Stack string // "tailwind4" | "tailwind3" | "bootstrap"
	File  string // the file written (relative to the project dir)
	Note  string // extra guidance (e.g. Bootstrap needs a Sass recompile)
}

// Valid reports whether s is a #rgb or #rrggbb hex colour.
func Valid(s string) bool { return hexRe.MatchString(strings.TrimSpace(s)) }

// Apply writes primary (+ optional accent) into the project's CSS framework. It
// is the two-colour front door kept for back-compat (`keel brand <primary>
// [accent]`, the wizard, the studio): it generates a full comprehensive token
// set from the two seeds and delegates to ApplyTokens, so the 2-colour path and
// the token path produce identical, framework-idiomatic output and cannot drift.
func Apply(dir, primary, accent string) (Result, error) {
	primary = strings.TrimSpace(primary)
	accent = strings.TrimSpace(accent)
	if !Valid(primary) {
		return Result{}, fmt.Errorf("primary must be a hex colour like #5b21b6 (got %q)", primary)
	}
	if accent != "" && !Valid(accent) {
		return Result{}, fmt.Errorf("accent must be a hex colour like #3ab7bf (got %q)", accent)
	}
	tokens, err := Generate(primary, accent)
	if err != nil {
		return Result{}, err
	}
	return ApplyTokens(dir, tokens)
}

// stack names the CSS framework a project themes against, in keel's detection
// precedence order.
type stack int

const (
	stackNone stack = iota
	stackTailwindV4
	stackTailwindV3
	stackBootstrap
)

// located is the result of one detection pass: which stack the project uses and
// the file its tokens live in. It exists so ApplyTokens and Detect each walk the
// project tree once and agree on the answer, rather than re-walking per call.
type located struct {
	stack stack
	file  string // the CSS entry (v4), config (v3) or Sass entry (bootstrap)
}

// locate detects a project's CSS stack in a single tree walk, in keel's fixed
// precedence: Tailwind v4 (@import "tailwindcss" in a .css entry) → Tailwind v3
// (a tailwind.config.*) → Bootstrap (a .scss that imports bootstrap). The v4 and
// Bootstrap markers both live in files somewhere under the tree, so one WalkDir
// scans for both at once; the v3 marker is a fixed filename, checked without
// walking. Each candidate file is streamed line-by-line and the scan stops at the
// first match rather than reading whole files into memory. This is the one place
// the stack is decided — callers thread the result through instead of re-walking.
// walkDir is a seam over filepath.WalkDir so a test can assert locate does a
// single tree walk per call. The real body is the stdlib function.
var walkDir = filepath.WalkDir

func locate(dir string) located {
	var v4, boot string
	_ = walkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || (v4 != "" && boot != "") {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		switch {
		case v4 == "" && strings.HasSuffix(p, ".css"):
			if importsTailwind(p) {
				v4 = p
			}
		case boot == "" && strings.HasSuffix(p, ".scss"):
			if fileContainsLine(p, "bootstrap") {
				boot = p
			}
		}
		return nil
	})
	// Precedence: v4 wins, then a v3 config (a cheap Stat, no walk), then Bootstrap.
	if v4 != "" {
		return located{stack: stackTailwindV4, file: v4}
	}
	if cfg := findTailwindV3(dir); cfg != "" {
		return located{stack: stackTailwindV3, file: cfg}
	}
	if boot != "" {
		return located{stack: stackBootstrap, file: boot}
	}
	return located{stack: stackNone}
}

// importsTailwind reports whether a CSS entry pulls in Tailwind v4. It matches an
// @import line that names tailwindcss regardless of quote style — Laravel ships
// `@import 'tailwindcss';` (single quotes), Vite/other starters use double quotes,
// and some configs add a source hint (`@import "tailwindcss" source(none)`). All of
// them are the same v4 setup, so we key on the @import + tailwindcss pair.
func importsTailwind(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		ln := sc.Text()
		if strings.Contains(ln, "@import") && strings.Contains(ln, "tailwindcss") {
			return true
		}
	}
	return false
}

// fileContainsLine reports whether any line of the file at p contains sub. It
// streams the file with a bufio.Scanner and stops at the first hit, so a large
// CSS/Sass entry is not read wholesale into memory just to test a substring.
func fileContainsLine(p, sub string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	// A minified CSS entry can be one very long line; lift the token limit so a
	// single-line file is still scannable rather than silently skipped.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		if strings.Contains(sc.Text(), sub) {
			return true
		}
	}
	return false
}

// ApplyTokens writes a full BrandTokens set into the project's CSS framework,
// mapped idiomatically per stack (Tailwind v4 @theme + dark block, Tailwind v3
// theme.extend module, Bootstrap $vars + CSS-variable overrides). It detects the
// stack in the same precedence as Apply, merges the tokens over whatever the
// project already defined (pre-fill + merge, never blind overwrite), and
// replaces the keel-marked block so a re-apply is idempotent. This is the
// accessor the studio brand UI and `keel brand apply` consume.
func ApplyTokens(dir string, tokens BrandTokens) (Result, error) {
	loc := locate(dir)
	switch loc.stack {
	case stackTailwindV4:
		return applyTailwindV4Tokens(dir, loc.file, tokens)
	case stackTailwindV3:
		return applyTailwindV3Tokens(dir, loc.file, tokens)
	case stackBootstrap:
		return applyBootstrapTokens(dir, loc.file, tokens)
	default:
		return Result{}, fmt.Errorf("no Tailwind or Bootstrap setup found in %s - add a UI kit first", dir)
	}
}

// --- Tailwind v4: @theme block in the CSS entry that imports tailwindcss ---

func applyTailwindV4Tokens(dir, css string, tokens BrandTokens) (Result, error) {
	b, err := os.ReadFile(css)
	if err != nil {
		return Result{}, err
	}
	// Strip keel's own previous block first, then detect from what's left — the
	// user's own tokens — so a re-apply doesn't merge keel's last output back
	// into itself (which would freeze the first seed forever). Whatever the user
	// wrote outside the block still pre-fills + merges.
	body := stripBlock(string(b), startMark, endMark)
	tokens = tokens.Merge(detectTailwindV4String(rel(dir, css), body))
	block := startMark + "\n" + RenderTailwindV4(tokens) + endMark + "\n"
	out := strings.TrimRight(body, "\n") + "\n\n" + block
	if err := os.WriteFile(css, []byte(out), 0o644); err != nil {
		return Result{}, err
	}
	return Result{Stack: "tailwind4", File: rel(dir, css), Note: "Utilities: bg-brand-500, text-accent-500, dark: variants via .dark"}, nil
}

// --- Tailwind v3: theme.extend.colors — we can't safely rewrite arbitrary JS,
// so we write a drop-in colours module and tell the user how to spread it in. ---

func findTailwindV3(dir string) string {
	for _, n := range []string{"tailwind.config.js", "tailwind.config.ts", "tailwind.config.cjs", "tailwind.config.mjs"} {
		if p := filepath.Join(dir, n); fileExists(p) {
			return p
		}
	}
	return ""
}

func applyTailwindV3Tokens(dir, cfg string, tokens BrandTokens) (Result, error) {
	// Pre-fill + merge from the user's own tailwind.config.* (colours they
	// already declared inline), not from keel's generated keel-brand.colors.js —
	// that file is keel-owned and fully rewritten, so reading it back would
	// freeze the first seed. Detecting the hand-written config keeps the user's
	// weights while a new seed still wins the rest.
	if det, derr := detectTailwindV3(dir, cfg); derr == nil {
		tokens = tokens.Merge(det)
	}
	content := RenderTailwindV3(tokens)
	out := filepath.Join(dir, "keel-brand.colors.js")
	if err := os.WriteFile(out, []byte(content), 0o644); err != nil {
		return Result{}, err
	}
	return Result{
		Stack: "tailwind3",
		File:  rel(dir, out),
		Note:  "Tailwind v3: spread it into " + rel(dir, cfg) + " → theme.extend.colors: { ...require('./keel-brand.colors') }",
	}, nil
}

// --- Bootstrap: Sass $primary/$secondary must be set before the bootstrap
// import. We insert the overrides right before the first bootstrap @import. ---

func applyBootstrapTokens(dir, scss string, tokens BrandTokens) (Result, error) {
	b, err := os.ReadFile(scss)
	if err != nil {
		return Result{}, err
	}
	// Detect from the stripped body (the user's own $vars), not keel's own
	// previous block, so a re-apply doesn't fold keel's last output back in.
	body := stripBlock(string(b), scssStart, scssEnd)
	tokens = tokens.Merge(detectBootstrapString(rel(dir, scss), body))
	overrides := scssStart + "\n" + RenderBootstrap(tokens) + scssEnd + "\n"
	// Sass variable overrides only win before the bootstrap import.
	lines := strings.Split(body, "\n")
	inserted := false
	var out []string
	for _, ln := range lines {
		if !inserted && strings.Contains(ln, "@import") && strings.Contains(ln, "bootstrap") {
			out = append(out, strings.TrimRight(overrides, "\n"))
			inserted = true
		}
		out = append(out, ln)
	}
	if !inserted { // no import line found — prepend so it still precedes any later import
		out = append([]string{strings.TrimRight(overrides, "\n")}, out...)
	}
	if err := os.WriteFile(scss, []byte(strings.Join(out, "\n")), 0o644); err != nil {
		return Result{}, err
	}
	return Result{Stack: "bootstrap", File: rel(dir, scss), Note: "Bootstrap needs a Sass recompile to apply the new colours everywhere."}, nil
}

// --- helpers ---

func stripBlock(s, start, end string) string {
	for {
		i := strings.Index(s, start)
		if i < 0 {
			return s
		}
		j := strings.Index(s[i:], end)
		if j < 0 {
			return s[:i]
		}
		s = s[:i] + s[i+j+len(end):]
	}
}

func skipDir(name string) bool {
	switch name {
	case "node_modules", "vendor", ".git", "dist", "build", ".next", ".nuxt", ".svelte-kit", ".venv", "staticfiles",
		// .keel holds keel's own tracking snapshot of the project (.keel/base/*),
		// a byte copy of the source. Walking into it made brand detection pick
		// .keel/base/app/globals.css over the live app/globals.css (dot-dirs sort
		// first), so the brand landed in the snapshot and never rendered. keel's
		// own metadata dir is never a brand target.
		".keel":
		return true
	}
	return false
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func rel(dir, p string) string {
	if r, err := filepath.Rel(dir, p); err == nil {
		return r
	}
	return p
}
