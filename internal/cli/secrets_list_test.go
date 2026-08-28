package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeManifest drops a minimal keel manifest into dir so project-scoped commands
// treat it as a project. env must be a real env recipe id (ddev / sail /
// laravel-docker / nestjs-local).
func writeManifest(t *testing.T, dir, framework, env string) {
	t.Helper()
	kd := filepath.Join(dir, ".keel")
	if err := os.MkdirAll(kd, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "framework: " + framework + "\nenv: " + env + "\nrecipes: [" + framework + ", " + env + "]\n"
	if err := os.WriteFile(filepath.Join(kd, "manifest.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSecretsListMasksSecretsShowsPublic: the classification rule end to end -
// NEXT_PUBLIC_ shows its value, a credential-named key is masked to "present"
// (its value never printed), and ordinary config shows.
func TestSecretsListMasksSecretsShowsPublic(t *testing.T) {
	wd := isolate(t)
	writeManifest(t, wd, "nextjs", "nestjs-local")
	env := "NEXT_PUBLIC_SITE=https://app.example.com\n" +
		"DATABASE_PASSWORD=hunter2\n" +
		"APP_NAME=Widgets\n" +
		"DATABASE_URL=postgres://user:pw@db:5432/app\n"
	if err := os.WriteFile(filepath.Join(wd, ".env"), []byte(env), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runRoot(t, "secrets", "list")
	if err != nil {
		t.Fatalf("keel secrets list: %v", err)
	}
	// Public var: value shown.
	mustContain(t, out, "NEXT_PUBLIC_SITE", "https://app.example.com")
	// Ordinary config: value shown.
	mustContain(t, out, "APP_NAME", "Widgets")
	// Secret by name and credential-URL: masked, value NEVER printed.
	mustContain(t, out, "DATABASE_PASSWORD", "(present)")
	mustContain(t, out, "DATABASE_URL", "(present)")
	mustNotContain(t, out, "hunter2", "user:pw")
	// Provenance is shown.
	mustContain(t, out, ".env")
}

// TestSecretsListPrecedence: .env.local overrides .env for the same key (Next.js
// order), and the provenance names the winning file.
func TestSecretsListPrecedence(t *testing.T) {
	wd := isolate(t)
	writeManifest(t, wd, "nextjs", "nestjs-local")
	if err := os.WriteFile(filepath.Join(wd, ".env"), []byte("APP_NAME=Base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, ".env.local"), []byte("APP_NAME=Local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "secrets", "list")
	if err != nil {
		t.Fatalf("keel secrets list: %v", err)
	}
	// Precedence, not layout: .env.local's value wins and its source is named,
	// while .env's overridden value never appears. (secrets list renders as a
	// themed table now, so the key and value are separate cells, not "k = v".)
	mustContain(t, out, "APP_NAME", "Local", ".env.local")
	mustNotContain(t, out, "Base")
}

// TestSecretsListNoEnv: a project with no env at all is a calm note, not an error
// or a blank.
func TestSecretsListNoEnv(t *testing.T) {
	wd := isolate(t)
	writeManifest(t, wd, "laravel", "ddev")
	out, err := runRoot(t, "secrets", "list")
	if err != nil {
		t.Fatalf("keel secrets list: %v", err)
	}
	if !strings.Contains(out, "no env found") {
		t.Errorf("empty project should report no env found, got:\n%s", out)
	}
}

// TestSecretsListRefusesOutsideProject: with no manifest the command refuses (it
// does not resolve some stray directory's env).
func TestSecretsListRefusesOutsideProject(t *testing.T) {
	isolate(t)
	_, err := runRoot(t, "secrets", "list")
	if err == nil {
		t.Fatal("secrets list should refuse outside a keel project")
	}
	if !mentionsAProject(err.Error()) {
		t.Errorf("error should explain there is no project: %v", err)
	}
}
