package engine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

// planFrom builds a Plan directly from recipes (the fields are exported), so
// coverage tests can craft exact shapes without going through the resolver.
func planFrom(framework string, recs ...recipe.Recipe) *resolver.Plan {
	return &resolver.Plan{Framework: framework, Recipes: recs}
}

func TestReadManifest(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		dir := t.TempDir()
		kd := filepath.Join(dir, ".keel")
		if err := os.MkdirAll(kd, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "framework: laravel\nenv: ddev\nrecipes:\n  - laravel\n  - ddev\n"
		if err := os.WriteFile(filepath.Join(kd, "manifest.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		m, err := ReadManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		if m.Framework != "laravel" || m.Env != "ddev" {
			t.Fatalf("manifest = %+v", m)
		}
		if len(m.Recipes) != 2 || m.Recipes[0] != "laravel" {
			t.Fatalf("recipes = %v", m.Recipes)
		}
	})
	t.Run("missing file", func(t *testing.T) {
		// A missing manifest must classify as not-exist, so the CLI can say "not a
		// keel project" rather than "manifest is malformed".
		_, err := ReadManifest(t.TempDir())
		if err == nil {
			t.Fatal("expected error for missing manifest")
		}
		if !os.IsNotExist(err) {
			t.Fatalf("missing manifest should be an IsNotExist error, got %v", err)
		}
		if errors.Is(err, ErrManifestMalformed) {
			t.Fatal("a missing manifest must not be reported as malformed")
		}
	})
	t.Run("bad yaml", func(t *testing.T) {
		dir := t.TempDir()
		kd := filepath.Join(dir, ".keel")
		_ = os.MkdirAll(kd, 0o755)
		_ = os.WriteFile(filepath.Join(kd, "manifest.yaml"), []byte("framework: [unterminated"), 0o644)
		// A present-but-corrupt manifest must classify as malformed (and carry an
		// actionable message that names the file), so the CLI does not mis-report a
		// hand-edit as "not a keel project".
		_, err := ReadManifest(dir)
		if err == nil {
			t.Fatal("expected yaml unmarshal error")
		}
		if !errors.Is(err, ErrManifestMalformed) {
			t.Fatalf("a corrupt manifest should wrap ErrManifestMalformed, got %v", err)
		}
		if os.IsNotExist(err) {
			t.Fatal("a corrupt manifest must not classify as not-exist")
		}
		if !strings.Contains(err.Error(), "manifest.yaml") {
			t.Fatalf("the error should name the file, got %v", err)
		}
	})
}

func TestSmokeStepsRenders(t *testing.T) {
	p := planFrom("app",
		recipe.Recipe{ID: "ddev", Kind: recipe.Env, Bin: "ddev", Provides: []string{"env"},
			Commands: map[string]string{"exec": "ddev exec"}},
		recipe.Recipe{ID: "app", Kind: recipe.Framework,
			Smoke: []string{"{{exec}} php -v", "   ", "curl {{project}}.test"}},
	)
	got := SmokeSteps(p, "shop")
	// empty (whitespace-only) steps are dropped; tokens render.
	want := []string{"ddev exec php -v", "curl shop.test"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("smoke steps:\n got  %v\n want %v", got, want)
	}
}

func TestHookStepsPreview(t *testing.T) {
	p := planFrom("app",
		recipe.Recipe{ID: "svc", Kind: recipe.Service, Provides: []string{"cache"},
			Hooks: recipe.Hooks{"pre_build": {{Run: "pre-svc"}}}},
		recipe.Recipe{ID: "app", Kind: recipe.Framework,
			Hooks: recipe.Hooks{
				"pre_build":   {{Run: "pre-app"}},
				"post_recipe": {{Message: "just a message"}, {Run: "postrec"}},
				"post_create": {{Script: "setup.sh"}},
				"post_build":  {{Run: "postbuild"}, {Run: "skipped", When: "0"}},
			}},
	)
	got := HookSteps(p)
	joined := strings.Join(got, "|")
	// pre_build (both recipes), then per-recipe post_recipe/post_create, then post_build.
	// Framework has lower rank than Service, so app is ordered first.
	if !strings.Contains(joined, "pre-app") || !strings.Contains(joined, "pre-svc") {
		t.Fatalf("pre_build hooks missing: %v", got)
	}
	if !strings.Contains(joined, "postrec") || !strings.Contains(joined, "sh setup.sh") {
		t.Fatalf("post_recipe/post_create hooks missing: %v", got)
	}
	if !strings.Contains(joined, "postbuild") {
		t.Fatalf("post_build hook missing: %v", got)
	}
	// message-only hooks and skipped (When=0) hooks are excluded.
	if strings.Contains(joined, "just a message") || strings.Contains(joined, "skipped") {
		t.Fatalf("message-only or skipped hook leaked into preview: %v", got)
	}
}

func TestFireOpenHooks(t *testing.T) {
	// Use a message-only post_open hook so no shell actually runs (FireOpenHooks
	// uses the real ExecRunner). This exercises the whole path safely.
	p := planFrom("app",
		recipe.Recipe{ID: "app", Kind: recipe.Framework,
			Hooks: recipe.Hooks{"post_open": {{Message: "opening {{project}}"}}}},
	)
	var buf bytes.Buffer
	if err := FireOpenHooks(context.Background(), p, filepath.Join(t.TempDir(), "myproj"), &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "opening myproj") {
		t.Fatalf("post_open message not rendered/printed: %q", buf.String())
	}
}

func TestFireOpenHooksNilOutDefaultsStdout(t *testing.T) {
	// nil out -> os.Stdout; no post_open hooks means nothing prints. Just proves the
	// nil-out branch doesn't panic.
	p := planFrom("app", recipe.Recipe{ID: "app", Kind: recipe.Framework})
	if err := FireOpenHooks(context.Background(), p, t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestWriteAndReadBase(t *testing.T) {
	dir := t.TempDir()
	if err := WriteBase(dir, "config/app.php", "<?php return [];"); err != nil {
		t.Fatal(err)
	}
	got, ok := ReadBase(dir, "config/app.php")
	if !ok || got != "<?php return [];" {
		t.Fatalf("ReadBase = %q, %v", got, ok)
	}
	if _, ok := ReadBase(dir, "nope.txt"); ok {
		t.Fatal("ReadBase should report false for a missing snapshot")
	}
}

func TestWriteManifestFile(t *testing.T) {
	dir := t.TempDir()
	m := &Manifest{Framework: "django", Env: "django-ddev", Recipes: []string{"django", "django-ddev"},
		Files: map[string]string{"manage.py": "abc123"}}
	if err := WriteManifestFile(dir, m); err != nil {
		t.Fatal(err)
	}
	back, err := ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if back.Framework != "django" || back.Files["manage.py"] != "abc123" {
		t.Fatalf("round-trip manifest = %+v", back)
	}
}

func TestEnvBinPrefersBinThenFallsBack(t *testing.T) {
	// Bin set wins.
	p := planFrom("app", recipe.Recipe{ID: "sail-env", Kind: recipe.Env, Bin: "sail"})
	if got := envBin(p); got != "sail" {
		t.Fatalf("envBin with Bin set = %q, want sail", got)
	}
	// No Bin -> falls back to the env recipe ID.
	p = planFrom("app", recipe.Recipe{ID: "ddev", Kind: recipe.Env})
	if got := envBin(p); got != "ddev" {
		t.Fatalf("envBin without Bin = %q, want ddev", got)
	}
	// No env recipe at all -> empty.
	p = planFrom("app", recipe.Recipe{ID: "app", Kind: recipe.Framework})
	if got := envBin(p); got != "" {
		t.Fatalf("envBin with no env = %q, want empty", got)
	}
}

func TestProjectNameFallsBackToApp(t *testing.T) {
	// A base name with no alphanumerics collapses to "" then -> "app".
	if got := projectName("/some/path/___"); got != "app" {
		t.Fatalf("projectName of non-alphanumeric = %q, want app", got)
	}
	if got := projectName("/some/path/MyShop_2"); got != "myshop-2" {
		t.Fatalf("projectName = %q, want myshop-2", got)
	}
}

func TestPatchFileLoadError(t *testing.T) {
	// A directory passes os.Stat but envfile.Load's ReadFile fails on it, hitting
	// PatchFile's Load error branch.
	dir := t.TempDir()
	sub := filepath.Join(dir, "adir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := PatchFile(sub, map[string]string{"X": "1"}); err == nil {
		t.Fatal("expected a load error patching a directory")
	}
}

func TestApplyPatchesRendersAndReports(t *testing.T) {
	dir := t.TempDir()
	// Seed a dotenv the patch will edit.
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("APP=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	patches := []recipe.Patch{{File: "{{project}}.env", Set: map[string]string{"DB": "{{env}}"}}}
	// Rename so the templated path resolves to the seeded file.
	_ = os.Rename(filepath.Join(dir, ".env"), filepath.Join(dir, "shop.env"))
	err := applyPatches(dir, patches, map[string]string{"project": "shop", "env": "ddev"}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "patched shop.env") {
		t.Fatalf("expected patched notice, got %q", buf.String())
	}
	got, _ := os.ReadFile(filepath.Join(dir, "shop.env"))
	if !strings.Contains(string(got), "DB=ddev") {
		t.Fatalf("patch not applied/rendered:\n%s", got)
	}
}

func TestApplyPatchesErrorPropagates(t *testing.T) {
	dir := t.TempDir()
	// Point the patch at a directory so PatchFile's Load fails and applyPatches
	// returns the error.
	if err := os.MkdirAll(filepath.Join(dir, "isdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	patches := []recipe.Patch{{File: "isdir", Set: map[string]string{"X": "1"}}}
	if err := applyPatches(dir, patches, map[string]string{}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected applyPatches to propagate the load error")
	}
}

func TestHookCmdVariants(t *testing.T) {
	vars := map[string]string{"env": "ddev", "project": "shop"}
	dir := "/base"

	// Run hook with a working_directory: command rendered, wdir joined.
	cmd, wdir := hookCmd(recipe.Hook{Run: "{{env}} start", WorkingDir: "frontend"}, vars, dir)
	if cmd != "ddev start" {
		t.Fatalf("run cmd = %q", cmd)
	}
	if wdir != filepath.Join(dir, "frontend") {
		t.Fatalf("wdir = %q", wdir)
	}

	// Script hook: prefixed with "sh ".
	cmd, wdir = hookCmd(recipe.Hook{Script: "setup.sh"}, vars, dir)
	if cmd != "sh setup.sh" || wdir != dir {
		t.Fatalf("script hook = %q / %q", cmd, wdir)
	}

	// Message-only hook: no command.
	cmd, _ = hookCmd(recipe.Hook{Message: "hello"}, vars, dir)
	if cmd != "" {
		t.Fatalf("message hook should yield empty cmd, got %q", cmd)
	}
}

func TestRunHooksMessageAndFailure(t *testing.T) {
	t.Run("message printed, no run", func(t *testing.T) {
		var buf bytes.Buffer
		rec := &recorder{}
		rc := recipe.Recipe{Hooks: recipe.Hooks{"post_recipe": {{Message: "hi {{project}}"}}}}
		err := runHooks(context.Background(), "post_recipe", &rc, nil,
			map[string]string{"project": "shop"}, "/base", rec, &buf)
		if err != nil {
			t.Fatal(err)
		}
		if len(rec.cmds) != 0 {
			t.Fatalf("message hook should not run a command: %v", rec.cmds)
		}
		if !strings.Contains(buf.String(), "hi shop") {
			t.Fatalf("message not rendered/printed: %q", buf.String())
		}
	})

	t.Run("run failure wraps error", func(t *testing.T) {
		var buf bytes.Buffer
		rc := recipe.Recipe{Hooks: recipe.Hooks{"post_build": {{Run: "boom-cmd"}}}}
		err := runHooks(context.Background(), "post_build", &rc, nil,
			map[string]string{}, "/base", &failOn{bad: "boom-cmd"}, &buf)
		if err == nil || !strings.Contains(err.Error(), "hook post_build failed") {
			t.Fatalf("want wrapped hook failure, got %v", err)
		}
	})

	t.Run("skipped hook and empty-command hook are no-ops", func(t *testing.T) {
		var buf bytes.Buffer
		rec := &recorder{}
		rc := recipe.Recipe{Hooks: recipe.Hooks{"post_build": {
			{Run: "gone", When: "false"}, // skipped by guard
			{Run: "{{missing}}"},         // renders to "" -> empty command, continue
		}}}
		if err := runHooks(context.Background(), "post_build", &rc, nil,
			map[string]string{}, "/base", rec, &buf); err != nil {
			t.Fatal(err)
		}
		// "{{missing}}" has no matching var, so it renders literally (non-empty). Only
		// the When=false hook is skipped; the literal command runs.
		if len(rec.cmds) != 1 || rec.cmds[0] != "{{missing}}" {
			t.Fatalf("unexpected commands: %v", rec.cmds)
		}
	})
}

func TestBuildInstallStepFailure(t *testing.T) {
	p := planFrom("app", recipe.Recipe{ID: "app", Kind: recipe.Framework,
		Install: []string{"ok-step", "bad-step"}})
	dir := filepath.Join(t.TempDir(), "proj")
	err := Build(context.Background(), p, Options{Dir: dir, Runner: &failOn{bad: "bad-step"},
		DockerUp: func() bool { return true }})
	if err == nil || !strings.Contains(err.Error(), "step failed") {
		t.Fatalf("want step failure, got %v", err)
	}
	// Fresh dir rolled back after the failure.
	if _, e := os.Stat(dir); !os.IsNotExist(e) {
		t.Fatal("fresh dir was not rolled back after a step failure")
	}
}

func TestBuildNilOutAndNilRunnerNoOpPlan(t *testing.T) {
	// A plan with no install steps and no docker requirement: exercises the
	// nil-Out (->stdout) and nil-Runner (->ExecRunner) defaults without running any
	// real shell, and writes a manifest.
	p := planFrom("app", recipe.Recipe{ID: "app", Kind: recipe.Framework,
		Files: []recipe.File{{Path: "README.md", Content: "hi"}}})
	dir := filepath.Join(t.TempDir(), "proj")
	if err := Build(context.Background(), p, Options{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".keel", "manifest.yaml")); err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
		t.Fatalf("file not written: %v", err)
	}
}

func TestBuildOnExistingDirNotRolledBack(t *testing.T) {
	// When the target dir already exists, a failure must NOT delete it.
	dir := t.TempDir() // already exists
	marker := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := planFrom("app", recipe.Recipe{ID: "app", Kind: recipe.Framework,
		Install: []string{"bad-step"}})
	err := Build(context.Background(), p, Options{Dir: dir, Runner: &failOn{bad: "bad-step"},
		DockerUp: func() bool { return true }})
	if err == nil {
		t.Fatal("expected step failure")
	}
	if _, e := os.Stat(marker); e != nil {
		t.Fatal("pre-existing dir was wrongly rolled back")
	}
}

func TestBuildMkdirAllError(t *testing.T) {
	// Target dir path descends through a regular file, so MkdirAll fails.
	tmp := t.TempDir()
	file := filepath.Join(tmp, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := planFrom("app", recipe.Recipe{ID: "app", Kind: recipe.Framework})
	err := Build(context.Background(), p, Options{Dir: filepath.Join(file, "sub"),
		Runner: &recorder{}, DockerUp: func() bool { return true }})
	if err == nil {
		t.Fatal("expected MkdirAll error when a path component is a file")
	}
}

func TestWriteFileMkdirError(t *testing.T) {
	// A parent path component is a regular file -> MkdirAll fails inside WriteFile.
	tmp := t.TempDir()
	file := filepath.Join(tmp, "afile")
	_ = os.WriteFile(file, []byte("x"), 0o644)
	if err := WriteFile(tmp, "afile/child.txt", "data"); err == nil {
		t.Fatal("expected WriteFile MkdirAll error")
	}
}

func TestWriteBaseMkdirError(t *testing.T) {
	tmp := t.TempDir()
	// Make .keel a file so basePath's MkdirAll under it fails.
	if err := os.WriteFile(filepath.Join(tmp, ".keel"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteBase(tmp, "config/app.php", "data"); err == nil {
		t.Fatal("expected WriteBase MkdirAll error when .keel is a file")
	}
}

func TestExecRunnerNilOutDefaultsStdout(t *testing.T) {
	// Out nil -> os.Stdout branch; a trivial true command must succeed.
	if err := (ExecRunner{}).Run(context.Background(), t.TempDir(), "true"); err != nil {
		t.Fatalf("ExecRunner with nil Out: %v", err)
	}
}

// RunArgv executes a command directly, with no shell: an argument containing
// shell syntax is data, not code. This is what `keel gen` relies on so a
// component name can never run a second command.
func TestRunArgvTreatsArgumentsAsData(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	r := ExecRunner{Out: &out}

	// A ";" inside an argument must be echoed, not obeyed.
	arg := "hello; touch pwned"
	if err := r.RunArgv(context.Background(), dir, []string{"echo", arg}); err != nil {
		t.Fatalf("RunArgv: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != arg {
		t.Fatalf("argument should reach the command intact, got %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
		t.Fatal("the argument was executed as a command")
	}

	// The same string through the shell runner DOES execute — which is exactly
	// why anything user-supplied must use RunArgv.
	if err := (ExecRunner{Out: io.Discard}).Run(context.Background(), dir, "echo "+arg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); err != nil {
		t.Fatal("expected the shell runner to obey the ';' (documents the difference)")
	}

	if err := r.RunArgv(context.Background(), dir, nil); err == nil {
		t.Fatal("an empty argv should be an error")
	}
}
