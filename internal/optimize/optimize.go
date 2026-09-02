// Package optimize runs static checks over a project and reports findings:
// security (committed secrets / .env), performance (Next.js image/link hygiene)
// and general repo hygiene. It reads only — it never modifies the project.
package optimize

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Severity ranks a finding. Error = fix before shipping.
type Severity string

const (
	SevError Severity = "error"
	SevWarn  Severity = "warn"
	SevInfo  Severity = "info"
)

// Category groups findings for the report.
type Category string

const (
	CatSecurity Category = "security"
	CatPerf     Category = "performance"
	CatHygiene  Category = "hygiene"
)

// Finding is one issue at a location.
type Finding struct {
	Category Category `json:"category"`
	Rule     string   `json:"rule"`
	Message  string   `json:"message"`
	File     string   `json:"file"` // relative to the scanned dir
	Line     int      `json:"line"` // 0 = file/repo-level
	Severity Severity `json:"severity"`
}

// Report is the collected findings.
type Report struct{ Findings []Finding }

func (r *Report) add(f Finding) { r.Findings = append(r.Findings, f) }

// Count returns how many findings have the given severity.
func (r *Report) Count(s Severity) int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == s {
			n++
		}
	}
	return n
}

// --- secret detectors (high-confidence patterns fire always; the generic one
// is suppressed on env-var references / placeholders to avoid noise) ---

type secretRule struct {
	rule, msg string
	sev       Severity
	re        *regexp.Regexp
	generic   bool
}

var secretRules = []secretRule{
	{"aws-access-key", "Hardcoded AWS access key ID", SevError, regexp.MustCompile(`AKIA[0-9A-Z]{16}`), false},
	{"private-key", "Committed private key block", SevError, regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`), false},
	{"github-token", "Hardcoded GitHub token", SevError, regexp.MustCompile(`ghp_[0-9A-Za-z]{36}`), false},
	{"stripe-secret", "Hardcoded Stripe live secret key", SevError, regexp.MustCompile(`sk_live_[0-9A-Za-z]{16,}`), false},
	{"slack-token", "Hardcoded Slack token", SevError, regexp.MustCompile(`xox[baprs]-[0-9A-Za-z-]{10,}`), false},
	{"google-key", "Hardcoded Google API key", SevError, regexp.MustCompile(`AIza[0-9A-Za-z_\-]{35}`), false},
	{"generic-secret", "Possible hardcoded secret (assign from an env var instead)", SevWarn,
		regexp.MustCompile(`(?i)(api[_-]?key|secret|token|passwd|password|client[_-]?secret)\s*[:=]\s*["'][^"']{8,}["']`), true},
}

func looksPlaceholder(line string) bool {
	l := strings.ToLower(line)
	for _, p := range []string{
		"process.env", "os.environ", "getenv", "import.meta.env", "config(", "env(", "${",
		"changeme", "change-me", "your-", "your_", "example", "placeholder", "xxxxxxxx",
		"<", "todo", "dummy", "redacted", "insert",
	} {
		if strings.Contains(l, p) {
			return true
		}
	}
	return false
}

var (
	reRawImg    = regexp.MustCompile(`<img[\s>]`)
	reRawAnchor = regexp.MustCompile(`<a\s[^>]*href=["']/[^/]`) // internal href starting with a single /
)

// scanContent applies per-line security + (Next.js) performance rules.
func scanContent(rel, content, framework string) []Finding {
	var out []Finding
	tsx := strings.HasSuffix(rel, ".tsx") || strings.HasSuffix(rel, ".jsx")
	scanSecrets := !isEnvFile(filepath.Base(rel)) // env files hold secrets by design; flagged at repo level
	for i, line := range strings.Split(content, "\n") {
		ln := i + 1
		if scanSecrets {
			for _, sr := range secretRules {
				if sr.re.MatchString(line) && !(sr.generic && looksPlaceholder(line)) {
					out = append(out, Finding{CatSecurity, sr.rule, sr.msg, rel, ln, sr.sev})
				}
			}
		}
		if framework == "nextjs" && tsx {
			if reRawImg.MatchString(line) {
				out = append(out, Finding{CatPerf, "next-image", "Use next/image <Image> for automatic optimization (lazy-load, resize, AVIF/WebP).", rel, ln, SevWarn})
			}
			if reRawAnchor.MatchString(line) {
				out = append(out, Finding{CatPerf, "next-link", "Use next/link <Link> for internal navigation (prefetch + client routing).", rel, ln, SevInfo})
			}
		}
	}
	return out
}

func isEnvFile(name string) bool {
	return name == ".env" || (strings.HasPrefix(name, ".env.") && !strings.HasSuffix(name, ".example"))
}

var skipDirs = map[string]bool{
	"node_modules": true, ".git": true, ".next": true, "vendor": true, ".venv": true,
	"dist": true, "build": true, ".turbo": true, "staticfiles": true, "__pycache__": true,
	".idea": true, ".vscode": true, "coverage": true,
}

var sourceExt = map[string]bool{
	".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
	".php": true, ".py": true, ".go": true, ".rb": true, ".vue": true, ".svelte": true,
	".json": true, ".yml": true, ".yaml": true, ".toml": true, ".tf": true, ".sh": true, ".env": false,
}

const maxRead = 512 * 1024

// Run scans dir for the given framework ("" = generic) and returns the report.
func Run(dir, framework string) Report {
	r := Report{}
	repoChecks(&r, dir)
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if info, e := d.Info(); e == nil && info.Size() > 5<<20 && !skipDirs[filepath.Base(filepath.Dir(p))] {
			r.add(Finding{CatHygiene, "large-file", "Large file committed to the repo (>5MB). Consider Git LFS or excluding it.", rel, 0, SevInfo})
		}
		if sourceExt[strings.ToLower(filepath.Ext(p))] || isEnvFile(d.Name()) {
			b, e := os.ReadFile(p)
			if e != nil {
				return nil
			}
			if len(b) > maxRead {
				b = b[:maxRead]
			}
			for _, f := range scanContent(rel, string(b), framework) {
				r.add(f)
			}
		}
		return nil
	})
	nextConfigCheck(&r, dir)
	return r
}

// repoChecks covers repo-level security + hygiene.
func repoChecks(r *Report, dir string) {
	gi := loadGitignore(dir)
	// .env / secret files present but not ignored → likely committed secrets.
	for _, name := range []string{".env", ".env.local", ".env.production", ".env.development"} {
		if exists(filepath.Join(dir, name)) && !gi.ignores(name) {
			r.add(Finding{CatSecurity, "env-committed", name + " is present and not in .gitignore. Secrets may be committed. Add it to .gitignore and `git rm --cached " + name + "`.", name, 0, SevError})
		}
	}
	for _, secret := range findSecretFiles(dir) {
		if !gi.ignores(secret) {
			r.add(Finding{CatSecurity, "secret-file", "Sensitive file not ignored: " + secret + " (keys/credentials should never be committed).", secret, 0, SevError})
		}
	}
	if !exists(filepath.Join(dir, ".gitignore")) {
		r.add(Finding{CatHygiene, "no-gitignore", "No .gitignore. Add one so build output and secrets aren't committed.", "", 0, SevWarn})
	}
	if !hasAny(dir, "README.md", "README", "readme.md") {
		r.add(Finding{CatHygiene, "no-readme", "No README. Add one so others (and future you) can run the project.", "", 0, SevInfo})
	}
	if exists(filepath.Join(dir, "node_modules")) && !gi.ignores("node_modules") {
		r.add(Finding{CatHygiene, "vendored-committed", "node_modules is present and not in .gitignore.", "node_modules", 0, SevWarn})
	}
	if exists(filepath.Join(dir, "vendor")) && !gi.ignores("vendor") {
		r.add(Finding{CatHygiene, "vendored-committed", "vendor/ is present and not in .gitignore.", "vendor", 0, SevInfo})
	}
}

// nextConfigCheck flags disabled image optimization in a Next.js config.
func nextConfigCheck(r *Report, dir string) {
	for _, name := range []string{"next.config.js", "next.config.mjs", "next.config.ts"} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			if strings.Contains(string(b), "unoptimized: true") || strings.Contains(string(b), "unoptimized:true") {
				r.add(Finding{CatPerf, "images-unoptimized", "next.config disables image optimization (images.unoptimized: true). Remove it to get automatic resizing/formats.", name, 0, SevWarn})
			}
		}
	}
}

// findSecretFiles returns top-level sensitive files by name/extension.
func findSecretFiles(dir string) []string {
	var out []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		ext := strings.ToLower(filepath.Ext(n))
		switch {
		case ext == ".pem", ext == ".key", ext == ".pfx", ext == ".p12":
			out = append(out, n)
		case n == "id_rsa", n == "id_dsa", n == "id_ecdsa", n == "credentials.json":
			out = append(out, n)
		case strings.Contains(strings.ToLower(n), "service-account") && ext == ".json":
			out = append(out, n)
		}
	}
	return out
}

// --- small fs helpers ---

type gitignore struct{ lines []string }

func loadGitignore(dir string) gitignore {
	b, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		return gitignore{}
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, strings.TrimSuffix(l, "/"))
		}
	}
	return gitignore{out}
}

func (g gitignore) ignores(name string) bool {
	base := strings.TrimSuffix(name, "/")
	for _, l := range g.lines {
		l = strings.TrimPrefix(strings.TrimSuffix(l, "/"), "/")
		if l == base || l == filepath.Base(base) || l == "*"+filepath.Ext(base) {
			return true
		}
	}
	return false
}

// Fix applies safe, mechanical remediations for the fixable findings and returns
// a description of each change. Only touches hygiene/secret-hygiene rules; never
// edits your source. Re-scan afterwards to confirm.
func Fix(dir string, findings []Finding) []string {
	var done []string
	seen := map[string]bool{}
	for _, f := range findings {
		switch f.Rule {
		case "env-committed", "secret-file", "vendored-committed":
			if f.File != "" && !seen["ig:"+f.File] {
				if addGitignore(dir, f.File) {
					done = append(done, "gitignored "+f.File)
				}
				seen["ig:"+f.File] = true
			}
		case "no-gitignore":
			if !seen["gi"] && !exists(filepath.Join(dir, ".gitignore")) {
				_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".env\n.env.*\n!.env.example\nnode_modules/\nvendor/\n.venv/\n"), 0o644)
				done = append(done, "created .gitignore")
				seen["gi"] = true
			}
		case "no-readme":
			if !seen["rd"] && !hasAny(dir, "README.md", "README", "readme.md") {
				_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("# "+filepath.Base(mustAbs(dir))+"\n"), 0o644)
				done = append(done, "created README.md")
				seen["rd"] = true
			}
		}
	}
	return done
}

func mustAbs(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// addGitignore appends name to .gitignore if not already ignored. Returns true
// if it changed the file.
func addGitignore(dir, name string) bool {
	if loadGitignore(dir).ignores(name) {
		return false
	}
	f, err := os.OpenFile(filepath.Join(dir, ".gitignore"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	if _, werr := f.WriteString("\n# added by keel optimize --fix\n" + name + "\n"); werr != nil {
		f.Close()
		return false
	}
	return f.Close() == nil
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func hasAny(dir string, names ...string) bool {
	for _, n := range names {
		if exists(filepath.Join(dir, n)) {
			return true
		}
	}
	return false
}
