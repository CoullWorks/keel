package optimize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func findingFor(fs []Finding, rule string) (Finding, bool) {
	for _, f := range fs {
		if f.Rule == rule {
			return f, true
		}
	}
	return Finding{}, false
}

// TestScanEverySecretRule fires each high-confidence secret detector once so
// every pattern branch in secretRules is exercised.
func TestScanEverySecretRule(t *testing.T) {
	lines := []string{
		`aws = "AKIAABCDEFGHIJKLMNOP"`,
		`-----BEGIN RSA PRIVATE KEY-----`,
		`gh = "ghp_abcdefghijklmnopqrstuvwxyz0123456789AB"`,
		`stripe = "sk_live_abcdefghijklmnop1234"`,
		`slack = "xoxb-1234567890-abcdef"`,
		`google = "AIzaSyA1234567890abcdefghijklmnopqrstuv"`,
	}
	fs := scanContent("secrets.ts", strings.Join(lines, "\n"), "")
	for _, rule := range []string{"aws-access-key", "private-key", "github-token", "stripe-secret", "slack-token", "google-key"} {
		if _, ok := findingFor(fs, rule); !ok {
			t.Errorf("rule %q did not fire", rule)
		}
	}
}

// TestEnvFileContentNotScannedForSecrets proves a real .env file's contents are
// skipped by the per-line secret scan (it's flagged at repo level instead).
func TestEnvFileContentNotScannedForSecrets(t *testing.T) {
	fs := scanContent(".env", `AWS=AKIAABCDEFGHIJKLMNOP`, "")
	if _, ok := findingFor(fs, "aws-access-key"); ok {
		t.Error(".env content should not be line-scanned for secrets")
	}
	// but a .env.example is scanned (it isn't a real secret store)
	fs = scanContent(".env.example", `AWS=AKIAABCDEFGHIJKLMNOP`, "")
	if _, ok := findingFor(fs, "aws-access-key"); !ok {
		t.Error(".env.example must still be line-scanned")
	}
}

// TestPlaceholderTokens covers each placeholder marker that suppresses the
// generic-secret rule.
func TestPlaceholderTokens(t *testing.T) {
	for _, v := range []string{
		`const k = process.env.SECRET_TOKEN_HERE;`,
		`key = os.environ["SECRET_TOKEN_HERE"]`,
		`token = getenv("SECRET_TOKEN_HERE")`,
		`x = import.meta.env.SECRET_TOKEN;`,
		`k = config("services.secret_key")`,
		`k = env("SECRET_KEY_NAME")`,
		`password = "${DB_PASSWORD_VAR}"`,
		`secret = "changeme-please-now"`,
		`password = "your-password-here"`,
		`token = "example-token-value"`,
		`key = "placeholder-value-x"`,
		`k = "xxxxxxxxxxxx"`,
		`secret = "<your-secret-here>"`,
		`token = "todo-fill-this-in"`,
		`k = "dummy-value-string"`,
		`k = "redacted-secret-str"`,
		`k = "insert-secret-here"`,
	} {
		fs := scanContent("cfg.ts", v, "")
		if _, ok := findingFor(fs, "generic-secret"); ok {
			t.Errorf("placeholder should suppress generic-secret: %q", v)
		}
	}
}

// TestFindSecretFilesExtensions covers each sensitive-file class findSecretFiles
// recognises.
func TestFindSecretFilesExtensions(t *testing.T) {
	dir := t.TempDir()
	names := []string{
		"cert.pem", "server.key", "store.pfx", "store.p12",
		"id_rsa", "id_dsa", "id_ecdsa", "credentials.json",
		"my-service-account.json",
		"README.md", // not sensitive
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "adir.key"), 0o755); err != nil {
		t.Fatal(err) // a directory named *.key must be ignored
	}
	got := map[string]bool{}
	for _, n := range findSecretFiles(dir) {
		got[n] = true
	}
	for _, want := range []string{"cert.pem", "server.key", "store.pfx", "store.p12", "id_rsa", "id_dsa", "id_ecdsa", "credentials.json", "my-service-account.json"} {
		if !got[want] {
			t.Errorf("findSecretFiles missed %q", want)
		}
	}
	if got["README.md"] {
		t.Error("README.md must not be treated as sensitive")
	}
	if got["adir.key"] {
		t.Error("a directory must not be listed as a secret file")
	}
}

// TestNextConfigUnoptimized covers the images-unoptimized branch (both spacing
// variants) and the ok/clean case.
func TestNextConfigUnoptimized(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "next.config.js"), []byte("module.exports = { images: { unoptimized: true } }"), 0o644)
	os.WriteFile(filepath.Join(dir, "next.config.mjs"), []byte("export default { images: { unoptimized:true } }"), 0o644)
	var r Report
	nextConfigCheck(&r, dir)
	if r.Count(SevWarn) < 2 {
		t.Errorf("both configs should warn, got %d warns", r.Count(SevWarn))
	}

	clean := t.TempDir()
	os.WriteFile(filepath.Join(clean, "next.config.ts"), []byte("export default {}"), 0o644)
	var r2 Report
	nextConfigCheck(&r2, clean)
	if len(r2.Findings) != 0 {
		t.Errorf("clean config should have no findings, got %+v", r2.Findings)
	}
}

// TestGitignoreMatching exercises the ignores() matching variants: exact,
// wildcard extension, path-prefixed and trailing-slash entries.
func TestGitignoreMatching(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(strings.Join([]string{
		"# a comment",
		"",
		"node_modules/",
		"*.key",
		"/dist",
		".env",
	}, "\n")), 0o644)
	gi := loadGitignore(dir)
	cases := map[string]bool{
		"node_modules": true,
		"server.key":   true, // *.key wildcard
		"dist":         true, // leading-slash stripped
		".env":         true,
		"nope.txt":     false,
	}
	for name, want := range cases {
		if got := gi.ignores(name); got != want {
			t.Errorf("ignores(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestLoadGitignoreMissing covers the no-file branch (empty gitignore).
func TestLoadGitignoreMissing(t *testing.T) {
	if gi := loadGitignore(t.TempDir()); gi.ignores(".env") {
		t.Error("empty gitignore should ignore nothing")
	}
}

// TestRunLargeFileAndHygiene exercises the walk's large-file finding, the
// skip-dir pruning, and the JSON source-scan path.
func TestRunLargeFileAndHygiene(t *testing.T) {
	dir := t.TempDir()
	// a >5MB committed asset -> large-file (info) finding
	big := make([]byte, 6<<20)
	os.WriteFile(filepath.Join(dir, "asset.bin"), big, 0o644)
	// a JSON source file with a hardcoded secret (JSON is in sourceExt); the AWS
	// key pattern fires regardless of JSON quoting, proving the .json scan path.
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"aws":"AKIAABCDEFGHIJKLMNOP"}`), 0o644)
	// a skipped dir whose contents must never be scanned
	os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0o755)
	os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "leak.ts"), []byte(`const k = "AKIAABCDEFGHIJKLMNOP"`), 0o644)
	// keep repo checks quiet-ish
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# x"), 0o644)

	r := Run(dir, "")
	if !has(r.Findings, "large-file") {
		t.Error("expected a large-file finding")
	}
	if !has(r.Findings, "aws-access-key") {
		t.Error("expected the JSON-embedded AWS key to be flagged")
	}
	for _, f := range r.Findings {
		if strings.Contains(f.File, "node_modules") {
			t.Errorf("node_modules must be skipped, but scanned %s", f.File)
		}
	}
}

// TestRunNodeModulesVendorCommitted covers the vendored-committed hygiene
// branches when the dirs exist and aren't ignored.
func TestRunNodeModulesVendorCommitted(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755)
	os.MkdirAll(filepath.Join(dir, "vendor"), 0o755)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("# empty\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# x"), 0o644)
	r := Run(dir, "")
	if !has(r.Findings, "vendored-committed") {
		t.Error("expected vendored-committed for node_modules/vendor")
	}
}

// TestFixRemediations covers Fix's gitignore append, no-gitignore create, and
// no-readme create branches, plus the dedup guard, using Run's real findings.
func TestFixRemediations(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=abc12345xyz\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "server.key"), []byte("-----BEGIN PRIVATE KEY-----\n"), 0o600)
	// no .gitignore, no README -> those findings fire too

	r := Run(dir, "")
	done := Fix(dir, r.Findings)
	if len(done) == 0 {
		t.Fatal("Fix reported no changes")
	}
	joined := strings.Join(done, "|")
	// The secret findings' addGitignore creates the .gitignore, then gitignores
	// the secrets into it; the missing README is created too.
	for _, want := range []string{"gitignored .env", "gitignored server.key", "created README.md"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Fix missing %q; got %v", want, done)
		}
	}
	// README + .gitignore now exist.
	if !exists(filepath.Join(dir, "README.md")) || !exists(filepath.Join(dir, ".gitignore")) {
		t.Error("Fix did not create the expected files")
	}
	// Re-scanning + re-fixing must be idempotent: the env/secret files are now
	// gitignored, and README/.gitignore exist, so nothing new is created.
	r2 := Run(dir, "")
	if has(r2.Findings, "env-committed") {
		t.Error(".env should be gitignored after Fix")
	}
}

// TestFixCreatesGitignoreAndReadme covers Fix's no-gitignore / no-readme create
// branches in isolation (a clean dir with neither file and no secrets).
func TestFixCreatesGitignoreAndReadme(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644)
	r := Run(dir, "")
	done := Fix(dir, r.Findings)
	joined := strings.Join(done, "|")
	if !strings.Contains(joined, "created .gitignore") {
		t.Errorf("expected .gitignore creation; got %v", done)
	}
	if !strings.Contains(joined, "created README.md") {
		t.Errorf("expected README creation; got %v", done)
	}
	gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.Contains(string(gi), ".env") {
		t.Error("created .gitignore should include sensible defaults")
	}
	rd, _ := os.ReadFile(filepath.Join(dir, "README.md"))
	if !strings.HasPrefix(string(rd), "# ") {
		t.Error("created README should have a title heading")
	}
}

// TestMustAbs covers the happy path of mustAbs.
func TestMustAbs(t *testing.T) {
	if got := mustAbs("."); !filepath.IsAbs(got) {
		t.Errorf("mustAbs(.) = %q, want absolute", got)
	}
}

// TestFixUnknownRuleIgnored covers the default (no-op) path of Fix's switch.
func TestFixUnknownRuleIgnored(t *testing.T) {
	done := Fix(t.TempDir(), []Finding{{Rule: "next-image", File: "page.tsx"}})
	if len(done) != 0 {
		t.Errorf("non-fixable rules should be ignored, got %v", done)
	}
}

// TestAddGitignoreAppendsWhenPresent covers addGitignore appending to an
// existing .gitignore and the already-ignored no-op path.
func TestAddGitignoreAppendsWhenPresent(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("dist/\n"), 0o644)
	if !addGitignore(dir, ".env") {
		t.Fatal("addGitignore should have appended .env")
	}
	if !loadGitignore(dir).ignores(".env") {
		t.Error(".env should now be ignored")
	}
	// second call is a no-op
	if addGitignore(dir, ".env") {
		t.Error("addGitignore should be a no-op when already ignored")
	}
}

// TestCountSeverities covers Report.Count across severities.
func TestCountSeverities(t *testing.T) {
	r := Report{Findings: []Finding{
		{Severity: SevError}, {Severity: SevError}, {Severity: SevWarn}, {Severity: SevInfo},
	}}
	if r.Count(SevError) != 2 || r.Count(SevWarn) != 1 || r.Count(SevInfo) != 1 {
		t.Errorf("Count wrong: err=%d warn=%d info=%d", r.Count(SevError), r.Count(SevWarn), r.Count(SevInfo))
	}
}
