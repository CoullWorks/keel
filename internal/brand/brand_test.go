package brand

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValid(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"three-digit hex", "#abc", true},
		{"six-digit hex", "#5b21b6", true},
		{"upper-case hex", "#5B21B6", true},
		{"surrounding whitespace trimmed", "  #3ab7bf  ", true},
		{"empty", "", false},
		{"missing hash", "5b21b6", false},
		{"four digits", "#abcd", false},
		{"five digits", "#abcde", false},
		{"seven digits", "#abcdef0", false},
		{"non-hex char", "#gggggg", false},
		{"named colour", "red", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Valid(tt.in); got != tt.want {
				t.Fatalf("Valid(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestApplyBadHex(t *testing.T) {
	dir := t.TempDir()
	// A valid Tailwind v4 CSS so detection succeeds and we exercise validation first.
	writeFile(t, dir, "app.css", `@import "tailwindcss";`)

	tests := []struct {
		name    string
		primary string
		accent  string
		wantSub string
	}{
		{"bad primary", "nope", "", "primary must be a hex colour"},
		{"empty primary", "", "", "primary must be a hex colour"},
		{"bad accent", "#5b21b6", "nope", "accent must be a hex colour"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Apply(dir, tt.primary, tt.accent)
			if err == nil {
				t.Fatalf("Apply(%q,%q) = nil error, want error", tt.primary, tt.accent)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantSub)
			}
		})
	}
}

func TestApplyNoFrameworkFound(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "readme.md", "no ui kit here")

	_, err := Apply(dir, "#5b21b6", "")
	if err == nil {
		t.Fatal("want error when no framework is found")
	}
	if !strings.Contains(err.Error(), "no Tailwind or Bootstrap setup found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyTailwindV4(t *testing.T) {
	dir := t.TempDir()
	// A skip-listed dir with a decoy CSS proves the walker skips node_modules.
	writeFile(t, dir, filepath.Join("node_modules", "pkg.css"), `@import "tailwindcss";`)
	writeFile(t, dir, filepath.Join("src", "app.css"), "body { color: red; }\n"+`@import "tailwindcss";`+"\n")

	res, err := Apply(dir, "#5b21b6", "#3ab7bf")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Stack != "tailwind4" {
		t.Fatalf("stack = %q, want tailwind4", res.Stack)
	}
	if res.File != filepath.Join("src", "app.css") {
		t.Fatalf("file = %q, want src/app.css", res.File)
	}
	got := readFile(t, dir, res.File)
	if !strings.Contains(got, startMark) || !strings.Contains(got, endMark) {
		t.Fatalf("markers missing:\n%s", got)
	}
	if !strings.Contains(got, "@theme {") {
		t.Fatalf("no @theme block:\n%s", got)
	}
	if !strings.Contains(got, "--color-brand-500: #5b21b6;") {
		t.Fatalf("primary var missing:\n%s", got)
	}
	if !strings.Contains(got, "--color-accent-500: #3ab7bf;") {
		t.Fatalf("accent var missing:\n%s", got)
	}
	if !strings.Contains(got, "body { color: red; }") {
		t.Fatalf("original content lost:\n%s", got)
	}

	// Idempotency: re-applying replaces the block, not appends a second one.
	if _, err := Apply(dir, "#111222", ""); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	got2 := readFile(t, dir, res.File)
	if n := strings.Count(got2, startMark); n != 1 {
		t.Fatalf("want exactly 1 brand block after re-apply, got %d:\n%s", n, got2)
	}
	// The keel-owned block is replaced, so the old seed's 500 must be gone: the
	// re-apply detects from the stripped body, never merging keel's own last
	// output back in.
	if strings.Contains(got2, "--color-brand-500: #5b21b6;") {
		t.Fatalf("old primary should be gone:\n%s", got2)
	}
	if !strings.Contains(got2, "--color-brand-500: #111222;") {
		t.Fatalf("new primary missing:\n%s", got2)
	}
	// The original user content outside the block is preserved.
	if !strings.Contains(got2, "body { color: red; }") {
		t.Fatalf("original content lost on re-apply:\n%s", got2)
	}
}

// Laravel 12 ships its Tailwind v4 entry with a single-quoted import
// (`@import 'tailwindcss';`) and a source hint. keel must detect that as v4, not
// fall through to "no UI kit" — a real project should just brand.
func TestApplyTailwindV4SingleQuoteImport(t *testing.T) {
	dir := t.TempDir()
	// The exact shape Laravel 12's resources/css/app.css uses.
	writeFile(t, dir, filepath.Join("resources", "css", "app.css"),
		"@import 'tailwindcss';\n\n@source '../../vendor/laravel/framework';\n\n@theme {\n  --font-sans: 'Instrument Sans', sans-serif;\n}\n")

	res, err := Apply(dir, "#ff6a2c", "#7c5cff")
	if err != nil {
		t.Fatalf("Apply on single-quote Tailwind v4 entry: %v", err)
	}
	if res.Stack != "tailwind4" {
		t.Fatalf("stack = %q, want tailwind4", res.Stack)
	}
	got := readFile(t, dir, res.File)
	if !strings.Contains(got, "--color-brand-500: #ff6a2c;") {
		t.Fatalf("primary var missing:\n%s", got)
	}
	// The project's own @theme font block must survive alongside keel's block.
	if !strings.Contains(got, "Instrument Sans") {
		t.Fatalf("original @theme content lost:\n%s", got)
	}
}

func TestApplyTailwindV4PrefersV4OverV3(t *testing.T) {
	// Both a v4 CSS and a v3 config present — v4 wins (checked first).
	dir := t.TempDir()
	writeFile(t, dir, "app.css", `@import "tailwindcss";`)
	writeFile(t, dir, "tailwind.config.js", "module.exports = {}")

	res, err := Apply(dir, "#5b21b6", "")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Stack != "tailwind4" {
		t.Fatalf("stack = %q, want tailwind4 (v4 takes precedence)", res.Stack)
	}
}

func TestApplyTailwindV3(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tailwind.config.js", "module.exports = { theme: {} }")

	res, err := Apply(dir, "#5b21b6", "#3ab7bf")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Stack != "tailwind3" {
		t.Fatalf("stack = %q, want tailwind3", res.Stack)
	}
	if res.File != "keel-brand.colors.js" {
		t.Fatalf("file = %q, want keel-brand.colors.js", res.File)
	}
	if !strings.Contains(res.Note, "tailwind.config.js") {
		t.Fatalf("note should reference the config: %q", res.Note)
	}
	got := readFile(t, dir, res.File)
	// The v3 module now carries the full ramp; the seed lands at the 500 stop.
	if !strings.Contains(got, "500: '#5b21b6'") {
		t.Fatalf("brand 500 missing:\n%s", got)
	}
	if !strings.Contains(got, "500: '#3ab7bf'") {
		t.Fatalf("accent 500 missing:\n%s", got)
	}
	if !strings.Contains(got, "brand:") || !strings.Contains(got, "accent:") {
		t.Fatalf("brand/accent role blocks missing:\n%s", got)
	}
	// A full ramp means more than one stop per role.
	if !strings.Contains(got, "50: '") || !strings.Contains(got, "950: '") {
		t.Fatalf("expected a full 50-950 ramp:\n%s", got)
	}
}

func TestApplyTailwindV3NoAccent(t *testing.T) {
	dir := t.TempDir()
	// Use a .ts config to cover the other filenames in the lookup list.
	writeFile(t, dir, "tailwind.config.ts", "export default { theme: {} }")

	res, err := Apply(dir, "#5b21b6", "")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Stack != "tailwind3" {
		t.Fatalf("stack = %q, want tailwind3", res.Stack)
	}
	got := readFile(t, dir, res.File)
	// With no accent seed, keel derives a complementary accent ramp (hue-rotated
	// from the primary), so an accent role is still present — but its 500 is the
	// derived colour, not the primary.
	if !strings.Contains(got, "accent:") {
		t.Fatalf("a derived accent ramp should be present:\n%s", got)
	}
	if strings.Contains(got, "accent: { 500: '#5b21b6'") {
		t.Fatalf("derived accent must differ from the primary:\n%s", got)
	}
}

func TestApplyBootstrap(t *testing.T) {
	dir := t.TempDir()
	scss := "// custom overrides\n$foo: 1;\n@import \"bootstrap/scss/bootstrap\";\n.after {}\n"
	writeFile(t, dir, filepath.Join("scss", "app.scss"), scss)

	res, err := Apply(dir, "#5b21b6", "#3ab7bf")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Stack != "bootstrap" {
		t.Fatalf("stack = %q, want bootstrap", res.Stack)
	}
	if res.File != filepath.Join("scss", "app.scss") {
		t.Fatalf("file = %q", res.File)
	}
	if !strings.Contains(res.Note, "recompile") {
		t.Fatalf("note should mention recompile: %q", res.Note)
	}
	got := readFile(t, dir, res.File)
	if !strings.Contains(got, scssStart) || !strings.Contains(got, scssEnd) {
		t.Fatalf("scss markers missing:\n%s", got)
	}
	if !strings.Contains(got, "$primary: #5b21b6;") {
		t.Fatalf("primary override missing:\n%s", got)
	}
	if !strings.Contains(got, "$secondary: #3ab7bf;") {
		t.Fatalf("secondary override missing:\n%s", got)
	}
	// Overrides must precede the bootstrap import (Sass variable rule).
	iOverride := strings.Index(got, "$primary:")
	iImport := strings.Index(got, `@import "bootstrap`)
	if iOverride < 0 || iImport < 0 || iOverride > iImport {
		t.Fatalf("override must come before the bootstrap import:\n%s", got)
	}

	// Idempotency: re-applying keeps a single override block.
	if _, err := Apply(dir, "#111222", ""); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	got2 := readFile(t, dir, res.File)
	if n := strings.Count(got2, scssStart); n != 1 {
		t.Fatalf("want exactly 1 override block after re-apply, got %d:\n%s", n, got2)
	}
	// A derived accent means $secondary is always present now; the re-applied
	// primary must be the new seed, and the old one gone from the $primary var.
	if !strings.Contains(got2, "$primary: #111222;") {
		t.Fatalf("re-applied primary missing:\n%s", got2)
	}
	if strings.Contains(got2, "$primary: #5b21b6;") {
		t.Fatalf("old primary should be gone:\n%s", got2)
	}
}

func TestApplyBootstrapNoImportLine(t *testing.T) {
	// A .scss that mentions bootstrap (so it's detected) but has no @import
	// bootstrap line — the overrides must be prepended.
	dir := t.TempDir()
	writeFile(t, dir, "styles.scss", "// uses bootstrap via a bundler\n.foo { color: red; }\n")

	res, err := Apply(dir, "#5b21b6", "")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Stack != "bootstrap" {
		t.Fatalf("stack = %q, want bootstrap", res.Stack)
	}
	got := readFile(t, dir, res.File)
	if !strings.HasPrefix(got, scssStart) {
		t.Fatalf("overrides should be prepended when no import line:\n%s", got)
	}
	if !strings.Contains(got, "$primary: #5b21b6;") {
		t.Fatalf("primary override missing:\n%s", got)
	}
}

func TestStripBlock(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"no markers", "hello world", "hello world"},
		{
			name: "single block removed",
			in:   "before\n" + startMark + "\nX\n" + endMark + "\nafter",
			want: "before\n\nafter",
		},
		{
			name: "two blocks removed",
			in:   startMark + "a" + endMark + "mid" + startMark + "b" + endMark,
			want: "mid",
		},
		{
			// start marker with no matching end: everything from the marker on is dropped.
			name: "start without end truncates",
			in:   "keep me\n" + startMark + "\ndangling",
			want: "keep me\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripBlock(tt.in, startMark, endMark); got != tt.want {
				t.Fatalf("stripBlock() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRelFallback(t *testing.T) {
	// A relative target against an absolute base can't be made relative on some
	// paths; rel returns the input path unchanged rather than erroring.
	if got := rel("/abs/base", "relative/path"); got != "relative/path" {
		t.Fatalf("rel fallback = %q, want the input path", got)
	}
	if got := rel("/abs/base", "/abs/base/sub/file.css"); got != filepath.Join("sub", "file.css") {
		t.Fatalf("rel = %q, want sub/file.css", got)
	}
}

func TestSkipDir(t *testing.T) {
	for _, name := range []string{"node_modules", "vendor", ".git", "dist", "build", ".next", ".nuxt", ".svelte-kit", ".venv", "staticfiles"} {
		if !skipDir(name) {
			t.Fatalf("skipDir(%q) = false, want true", name)
		}
	}
	if skipDir("src") {
		t.Fatal("skipDir(src) = true, want false")
	}
}

// --- helpers ---

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}
