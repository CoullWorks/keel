package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/optimize"
)

// printReport renders findings grouped by category, including the file:line
// location formatting and the repo-level "(repo)" fallback. We build a synthetic
// report because the live rules only emit file/repo-level findings.
func TestPrintReport(t *testing.T) {
	rep := optimize.Report{Findings: []optimize.Finding{
		{Category: optimize.CatSecurity, Rule: "hardcoded-key", Message: "leaked key", File: "app/config.php", Line: 42, Severity: optimize.SevError},
		{Category: optimize.CatPerf, Rule: "img", Message: "use next/image", File: "page.tsx", Severity: optimize.SevWarn},
		{Category: optimize.CatHygiene, Rule: "no-readme", Message: "add a README", File: "", Severity: optimize.SevInfo},
	}}
	// printReport takes a writer, so the test reads a buffer instead of
	// hijacking the process's stdout.
	var buf bytes.Buffer
	printReport(&buf, rep, false)
	mustContain(t, buf.String(), "SECURITY", "app/config.php:42", "PERFORMANCE", "HYGIENE", "(repo)", "finding(s)")

	// securityOnly filters to the security block.
	buf.Reset()
	printReport(&buf, rep, true)
	mustContain(t, buf.String(), "SECURITY", "app/config.php:42")
	mustNotContain(t, buf.String(), "PERFORMANCE", "HYGIENE")
}

// optimize --json on a clean-ish fixture dir emits structured findings and a
// summary, exit nil (no error-level issues).
func TestOptimizeJSON(t *testing.T) {
	wd := isolate(t)
	// A plain dir has hygiene findings (no .gitignore / README) but no errors.
	_ = wd
	out, err := runRoot(t, "optimize", ".", "--json")
	if err != nil {
		t.Fatalf("optimize --json: %v", err)
	}
	mustContain(t, out, `"findings"`, `"summary"`, `"severity"`)
}

// A committed .env is an error-level security finding → non-zero exit.
func TestOptimizeSecurityError(t *testing.T) {
	wd := isolate(t)
	if err := os.WriteFile(filepath.Join(wd, ".env"), []byte("SECRET=abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "optimize", "--security")
	if err == nil {
		t.Fatal("expected a non-nil error when an error-level finding exists")
	}
	mustContain(t, out, "SECURITY", "env-committed")
	mustContain(t, err.Error(), "error-level")
}

// --fix gitignores a committed .env and adds a README, then re-scans clean.
func TestOptimizeFix(t *testing.T) {
	wd := isolate(t)
	if err := os.WriteFile(filepath.Join(wd, ".env"), []byte("SECRET=abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "optimize", "--fix")
	if err != nil {
		t.Fatalf("optimize --fix: %v", err)
	}
	mustContain(t, out, "fixed")
	gi, _ := os.ReadFile(filepath.Join(wd, ".gitignore"))
	if !strings.Contains(string(gi), ".env") {
		t.Errorf(".env not gitignored after --fix: %q", gi)
	}
}

// optimize reads the framework from a keel manifest when present.
func TestOptimizeReadsManifest(t *testing.T) {
	wd := isolate(t)
	if err := os.MkdirAll(filepath.Join(wd, ".keel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, ".keel", "manifest.yaml"),
		[]byte("framework: nextjs\nenv: local\nrecipes: [nextjs, local]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runRoot(t, "optimize", "--json"); err != nil {
		t.Fatalf("optimize with manifest: %v", err)
	}
}

// The text report path (no --json) prints a clean message for a tidy dir.
func TestOptimizeTextClean(t *testing.T) {
	wd := isolate(t)
	// Make it tidy: .gitignore + README so hygiene is happy.
	if err := os.WriteFile(filepath.Join(wd, ".gitignore"), []byte("node_modules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, "README.md"), []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "optimize")
	if err != nil {
		t.Fatalf("optimize: %v", err)
	}
	mustContain(t, out, "no issues found")
}

// optimize --help documents the categories.
func TestOptimizeHelp(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "optimize", "--help")
	if err != nil {
		t.Fatalf("optimize --help: %v", err)
	}
	mustContain(t, out, "security", "performance", "hygiene")
}

// optimize on a path that does not exist errors instead of silently scanning a
// missing directory and reporting misleading (no README/.gitignore) findings.
func TestOptimizeNonexistentPath(t *testing.T) {
	isolate(t)
	_, err := runRoot(t, "optimize", "/tmp/keel-does-not-exist-xyz-123")
	if err == nil {
		t.Fatal("optimize on a nonexistent path should error")
	}
	if !strings.Contains(err.Error(), "no such directory") {
		t.Errorf("error should name the missing directory, got: %v", err)
	}
}
