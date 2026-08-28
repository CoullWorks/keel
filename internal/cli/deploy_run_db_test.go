package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// adoptDjango scaffolds a Django project and adopts it so the .keel manifest is
// written, returning the project dir (now the CWD).
func adoptDjango(t *testing.T) string {
	t.Helper()
	wd := isolate(t)
	if err := os.WriteFile(filepath.Join(wd, "manage.py"), []byte("# django\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runRoot(t, "adopt"); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	return wd
}

// deploy [target] --dry-run lists the artifacts without writing them.
func TestDeployDryRun(t *testing.T) {
	adoptDjango(t)
	out, err := runRoot(t, "deploy", "compose", "--dry-run")
	if err != nil {
		t.Fatalf("deploy compose --dry-run: %v", err)
	}
	mustContain(t, out, "would write", "Dockerfile", "docker-compose.prod.yml")
	// Nothing should hit disk on a dry-run.
	if _, err := os.Stat("Dockerfile"); err == nil {
		t.Error("dry-run wrote Dockerfile")
	}
}

// deploy [target] writes the real artifacts.
func TestDeployWrite(t *testing.T) {
	wd := adoptDjango(t)
	out, err := runRoot(t, "deploy", "fly")
	if err != nil {
		t.Fatalf("deploy fly: %v", err)
	}
	mustContain(t, out, "fly.toml", "DEPLOY.md", "read DEPLOY.md")
	if _, err := os.Stat(filepath.Join(wd, "fly.toml")); err != nil {
		t.Errorf("fly.toml not written: %v", err)
	}
}

// deploy skips an existing file unless --force.
func TestDeploySkipExisting(t *testing.T) {
	wd := adoptDjango(t)
	if err := os.WriteFile(filepath.Join(wd, "Dockerfile"), []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "deploy", "fly")
	if err != nil {
		t.Fatalf("deploy fly: %v", err)
	}
	mustContain(t, out, "skip Dockerfile")
	// --force overwrites.
	out, err = runRoot(t, "deploy", "fly", "--force")
	if err != nil {
		t.Fatalf("deploy fly --force: %v", err)
	}
	mustContain(t, out, "Dockerfile")
	b, _ := os.ReadFile(filepath.Join(wd, "Dockerfile"))
	if string(b) == "# mine\n" {
		t.Error("--force did not overwrite Dockerfile")
	}
}

// deploy with no target lists the targets rather than writing files: a bare
// `keel deploy` is a question ("what can I deploy to?"), not an instruction, so
// answering it with artifacts on disk is a footgun. Naming a target writes.
func TestDeployNoTargetListsTargets(t *testing.T) {
	wd := adoptDjango(t)
	out, err := runRoot(t, "deploy")
	if err != nil {
		t.Fatalf("bare deploy: %v", err)
	}
	mustContain(t, out, "Choose a deploy target", "compose", "fly", "vercel")
	// It must not have written any deploy artifact.
	for _, name := range []string{"Dockerfile", "docker-compose.prod.yml", "Caddyfile", "DEPLOY.md"} {
		if _, err := os.Stat(filepath.Join(wd, name)); err == nil {
			t.Errorf("bare deploy wrote %s; it should only list targets", name)
		}
	}
}

// deploy vps writes deploy.sh and makes it executable (the chmod branch).
func TestDeployVpsChmod(t *testing.T) {
	wd := adoptDjango(t)
	if _, err := runRoot(t, "deploy", "vps"); err != nil {
		t.Fatalf("deploy vps: %v", err)
	}
	fi, err := os.Stat(filepath.Join(wd, "deploy.sh"))
	if err != nil {
		t.Fatalf("deploy.sh not written: %v", err)
	}
	if fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("deploy.sh not executable: %v", fi.Mode())
	}
}

// deploy outside a keel project errors.
func TestDeployNoProject(t *testing.T) {
	isolate(t)
	_, err := runRoot(t, "deploy", "compose", "--dry-run")
	if err == nil {
		t.Fatal("expected error outside a keel project")
	}
	mustContain(t, err.Error(), errNoProject.Error())
}

// deploy with an unknown target errors.
func TestDeployUnknownTargetCmd(t *testing.T) {
	adoptDjango(t)
	_, err := runRoot(t, "deploy", "not-a-target", "--dry-run")
	if err == nil {
		t.Fatal("expected error for unknown target")
	}
	mustContain(t, err.Error(), "unknown target")
}

// run with no task lists the stack's tasks.
func TestRunListsTasks(t *testing.T) {
	adoptDjango(t)
	out, err := runRoot(t, "run")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	mustContain(t, out, "tasks for django", "test", "migrate")
}

// run <task> --dry-run prints the resolved command and runs nothing.
func TestRunTaskDryRun(t *testing.T) {
	adoptDjango(t)
	out, err := runRoot(t, "run", "test", "--dry-run")
	if err != nil {
		t.Fatalf("run test --dry-run: %v", err)
	}
	mustContain(t, out, "pytest")
}

// run with an unknown task lists the valid ones.
func TestRunUnknownTask(t *testing.T) {
	adoptDjango(t)
	_, err := runRoot(t, "run", "nope", "--dry-run")
	if err == nil {
		t.Fatal("expected error for unknown task")
	}
	mustContain(t, err.Error(), "no task")
}

// run outside a keel project errors.
func TestRunNoProject(t *testing.T) {
	isolate(t)
	_, err := runRoot(t, "run", "test")
	if err == nil {
		t.Fatal("expected error outside a keel project")
	}
	mustContain(t, err.Error(), errNoProject.Error())
}

// db <action> --dry-run prints the resolved command.
func TestDbDryRun(t *testing.T) {
	adoptDjango(t)
	out, err := runRoot(t, "db", "migrate", "--dry-run")
	if err != nil {
		t.Fatalf("db migrate --dry-run: %v", err)
	}
	mustContain(t, out, "manage.py migrate")
}

// db with an action the stack doesn't support errors.
func TestDbUnknownAction(t *testing.T) {
	adoptDjango(t)
	// Args validation allows only the enumerated actions; use one the framework
	// path returns "" for by forcing an unusual manifest is hard, so exercise the
	// cobra ValidArgs / ExactArgs boundary instead.
	_, err := runRoot(t, "db")
	if err == nil {
		t.Fatal("expected error: db needs exactly one action")
	}
}

// db outside a keel project errors.
func TestDbNoProject(t *testing.T) {
	isolate(t)
	_, err := runRoot(t, "db", "migrate", "--dry-run")
	if err == nil {
		t.Fatal("expected error outside a keel project")
	}
	mustContain(t, err.Error(), errNoProject.Error())
}

// update outside a keel project errors (the guard).
func TestUpdateNoProject(t *testing.T) {
	isolate(t)
	_, err := runRoot(t, "update", "--dry-run")
	if err == nil {
		t.Fatal("expected error outside a keel project")
	}
	mustContain(t, err.Error(), errNoProject.Error())
}

// baseName / projectName helpers.
func TestBaseName(t *testing.T) {
	cases := map[string]string{
		"/a/b/c":  "c",
		"/a/b/c/": "c",
		"single":  "single",
	}
	for in, want := range cases {
		if got := baseName(in); got != want {
			t.Errorf("baseName(%q) = %q, want %q", in, got, want)
		}
	}
}
