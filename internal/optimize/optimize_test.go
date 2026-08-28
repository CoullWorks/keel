package optimize

import (
	"os"
	"path/filepath"
	"testing"
)

func has(fs []Finding, rule string) bool {
	for _, f := range fs {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

func TestScanSecrets(t *testing.T) {
	src := `const awsKey = "AKIAABCDEFGHIJKLMNOP";
const gh = "ghp_` + "abcdefghijklmnopqrstuvwxyz0123456789AB" + `";
const fromEnv = process.env.API_KEY;
const apiKey = "super-secret-value-123";`
	fs := scanContent("app.ts", src, "")
	if !has(fs, "aws-access-key") {
		t.Error("missed AWS key")
	}
	if !has(fs, "github-token") {
		t.Error("missed GitHub token")
	}
	if !has(fs, "generic-secret") {
		t.Error("missed generic hardcoded secret")
	}
	// The env-var line must NOT be flagged.
	for _, f := range fs {
		if f.Line == 3 {
			t.Errorf("false positive on env-var reference: %+v", f)
		}
	}
}

func TestScanNextImage(t *testing.T) {
	tsx := `export default function P(){return <div><img src="/a.png"/><a href="/about">x</a></div>}`
	fs := scanContent("page.tsx", tsx, "nextjs")
	if !has(fs, "next-image") {
		t.Error("missed raw <img>")
	}
	if !has(fs, "next-link") {
		t.Error("missed internal <a href>")
	}
	// Non-Next framework should not raise perf findings.
	if fs2 := scanContent("page.tsx", tsx, "django"); has(fs2, "next-image") {
		t.Error("next-image should be Next.js-only")
	}
}

func TestPlaceholderNotFlagged(t *testing.T) {
	fs := scanContent(".env.example", `SECRET_KEY=change-me`, "")
	if len(fs) != 0 {
		t.Errorf("env.example placeholders should not be flagged: %+v", fs)
	}
	fs = scanContent("cfg.ts", `const token = "your-token-here";`, "")
	if has(fs, "generic-secret") {
		t.Error("placeholder value should not be flagged")
	}
}

func TestRunRepoChecks(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET_KEY=abc123xyz\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "server.key"), []byte("-----BEGIN PRIVATE KEY-----\n"), 0o600)
	// no .gitignore, no README
	r := Run(dir, "nextjs")
	if !has(r.Findings, "env-committed") {
		t.Error("should flag committed .env")
	}
	if !has(r.Findings, "secret-file") {
		t.Error("should flag committed key file")
	}
	if !has(r.Findings, "no-gitignore") || !has(r.Findings, "no-readme") {
		t.Error("should flag missing gitignore/readme")
	}
	if r.Count(SevError) == 0 {
		t.Error("committed secrets should be errors")
	}

	// With a .gitignore covering them, the security errors clear.
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".env\n*.key\n"), 0o644)
	r2 := Run(dir, "nextjs")
	if has(r2.Findings, "env-committed") || has(r2.Findings, "secret-file") {
		t.Error("gitignored secrets should not be flagged")
	}
}
