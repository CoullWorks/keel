package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/envfile"
)

// secrets sync creates .env from .env.example, adds the keys, gitignores it, and
// reports which values still need filling in.
func TestSecretsSync(t *testing.T) {
	wd := isolate(t)
	if err := os.WriteFile(filepath.Join(wd, ".env.example"), []byte("APP_NAME=demo\nAPI_URL=\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "secrets", "sync")
	if err != nil {
		t.Fatalf("secrets sync: %v", err)
	}
	mustContain(t, out, ".env synced", "APP_NAME", "still need values")
	if _, err := os.Stat(filepath.Join(wd, ".env")); err != nil {
		t.Fatalf(".env not written: %v", err)
	}
	// .env should have been added to .gitignore.
	gi, _ := os.ReadFile(filepath.Join(wd, ".gitignore"))
	if !strings.Contains(string(gi), ".env") {
		t.Errorf(".env not gitignored: %q", gi)
	}
}

// secrets sync generates the framework secret key (Laravel APP_KEY) when the
// manifest names the stack.
func TestSecretsSyncGeneratesFrameworkKey(t *testing.T) {
	wd := isolate(t)
	if err := os.MkdirAll(filepath.Join(wd, ".keel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, ".keel", "manifest.yaml"),
		[]byte("framework: laravel\nenv: ddev\nrecipes: [laravel, ddev]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, ".env.example"), []byte("APP_NAME=demo\nAPP_KEY=\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "secrets", "sync")
	if err != nil {
		t.Fatalf("secrets sync: %v", err)
	}
	mustContain(t, out, "generated", "APP_KEY")
	env, _ := envfile.Load(filepath.Join(wd, ".env"))
	if !strings.HasPrefix(env.Get("APP_KEY"), "base64:") {
		t.Errorf("APP_KEY = %q, want base64: prefix", env.Get("APP_KEY"))
	}
}

// secrets sync errors clearly when there's no .env.example to sync from.
func TestSecretsSyncNoExample(t *testing.T) {
	isolate(t)
	_, err := runRoot(t, "secrets", "sync")
	if err == nil {
		t.Fatal("expected error with no .env.example")
	}
	mustContain(t, err.Error(), "no .env.example")
}

// secrets generate writes a strong value for an arbitrary key.
func TestSecretsGenerate(t *testing.T) {
	wd := isolate(t)
	out, err := runRoot(t, "secrets", "generate", "MY_TOKEN")
	if err != nil {
		t.Fatalf("secrets generate: %v", err)
	}
	mustContain(t, out, "MY_TOKEN set")
	env, _ := envfile.Load(filepath.Join(wd, ".env"))
	if len(env.Get("MY_TOKEN")) < 20 {
		t.Errorf("MY_TOKEN too short: %q", env.Get("MY_TOKEN"))
	}
}

// secrets generate rejects a KEY that is not a valid env-var name, before
// touching .env — an empty/spaced/=/newline key would write a corrupt line.
func TestSecretsGenerateRejectsBadKey(t *testing.T) {
	for _, bad := range []string{"", "BAD KEY", "A=B", "X\nY=evil", "1LEADING", "with-dash", "wíth-unicode"} {
		t.Run(bad, func(t *testing.T) {
			wd := isolate(t)
			_, err := runRoot(t, "secrets", "generate", bad)
			if err == nil {
				t.Fatalf("secrets generate %q should be rejected", bad)
			}
			// The .env must not have been written or corrupted.
			if _, statErr := os.Stat(filepath.Join(wd, ".env")); statErr == nil {
				t.Errorf("secrets generate %q wrote .env despite the invalid key", bad)
			}
		})
	}
}

// secrets generate accepts valid env-var names (a leading underscore, digits
// after the first char).
func TestSecretsGenerateAcceptsValidKeys(t *testing.T) {
	for _, good := range []string{"APP_KEY", "_PRIVATE", "A1", "SECRET_KEY_2"} {
		t.Run(good, func(t *testing.T) {
			isolate(t)
			if _, err := runRoot(t, "secrets", "generate", good); err != nil {
				t.Fatalf("secrets generate %q should be accepted: %v", good, err)
			}
		})
	}
}

// secrets check on a missing .env tells you to sync AND exits non-zero, so it
// can gate CI (consistent with optimize).
func TestSecretsCheckNoEnv(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "secrets", "check")
	if err == nil {
		t.Fatal("secrets check with no .env should exit non-zero")
	}
	mustContain(t, out, "no .env")
}

// secrets check flags empty values and missing keys.
func TestSecretsCheckProblems(t *testing.T) {
	wd := isolate(t)
	if err := os.WriteFile(filepath.Join(wd, ".env.example"), []byte("APP_NAME=demo\nEXTRA=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// .env is missing EXTRA and has an empty APP_NAME; it isn't gitignored.
	if err := os.WriteFile(filepath.Join(wd, ".env"), []byte("APP_NAME=\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "secrets", "check")
	if err == nil {
		t.Fatal("secrets check with problems should exit non-zero (CI gate)")
	}
	mustContain(t, out, "missing keys", "EXTRA", "empty/placeholder", "not in .gitignore")
}

// secrets check on a healthy .env reports it.
func TestSecretsCheckHealthy(t *testing.T) {
	wd := isolate(t)
	if err := os.WriteFile(filepath.Join(wd, ".env.example"), []byte("APP_NAME=demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, ".env"), []byte("APP_NAME=demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, ".gitignore"), []byte(".env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "secrets", "check")
	if err != nil {
		t.Fatalf("secrets check: %v", err)
	}
	mustContain(t, out, "secrets look healthy")
}

// secrets check flags a .env that git currently tracks (committed secrets).
func TestSecretsCheckTracked(t *testing.T) {
	wd := isolate(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = wd
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	if err := os.WriteFile(filepath.Join(wd, ".env"), []byte("SECRET=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".env")
	run("commit", "-m", "add env")

	out, err := runRoot(t, "secrets", "check")
	if err == nil {
		t.Fatal("secrets check with a git-tracked .env should exit non-zero (CI gate)")
	}
	mustContain(t, out, "tracked by git")
}

// tracked reports false outside a git repo.
func TestTrackedOutsideGit(t *testing.T) {
	isolate(t)
	if tracked(".env") {
		t.Error("tracked should be false with no git repo")
	}
}

// Set replaces an existing value and appends a missing one.
func TestUpsert(t *testing.T) {
	f := envfile.Parse([]byte("A=1\nB=2\n"))
	f.Set("A", "99")
	if f.Get("A") != "99" {
		t.Errorf("Set replace: A = %q", f.Get("A"))
	}
	f.Set("C", "3")
	if f.Get("C") != "3" {
		t.Errorf("Set add: C = %q", f.Get("C"))
	}
}

// frameworkSecret produces a base64 APP_KEY for Laravel and a random string
// elsewhere.
func TestFrameworkSecret(t *testing.T) {
	v, err := frameworkSecret("laravel", "APP_KEY")
	if err != nil || !strings.HasPrefix(v, "base64:") {
		t.Errorf("laravel APP_KEY = %q (err %v)", v, err)
	}
	v, err = frameworkSecret("django", "DJANGO_SECRET_KEY")
	if err != nil || len(v) < 20 {
		t.Errorf("django secret = %q (err %v)", v, err)
	}
}

// gitignored + ensureGitignored round-trip.
func TestGitignoreHelpers(t *testing.T) {
	wd := isolate(t)
	if gitignored(".env") {
		t.Error("clean dir should not report .env gitignored")
	}
	if err := ensureGitignored(".env"); err != nil {
		t.Fatal(err)
	}
	if !gitignored(".env") {
		t.Error(".env should be gitignored after ensureGitignored")
	}
	// Idempotent: a second call is a no-op and leaves a single entry.
	if err := ensureGitignored(".env"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(wd, ".gitignore"))
	if strings.Count(string(b), "\n.env") > 1 {
		t.Errorf(".env added twice: %q", b)
	}
}

// frameworkOf reads the stack from a manifest, empty otherwise.
func TestFrameworkOf(t *testing.T) {
	wd := isolate(t)
	if frameworkOf(wd) != "" {
		t.Error("empty dir should have no framework")
	}
	if err := os.MkdirAll(filepath.Join(wd, ".keel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, ".keel", "manifest.yaml"),
		[]byte("framework: django\nenv: local\nrecipes: [django, local]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if frameworkOf(wd) != "django" {
		t.Errorf("frameworkOf = %q, want django", frameworkOf(wd))
	}
}
