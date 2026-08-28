package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

// failOn records commands and fails on a specific one.
type failOn struct {
	bad  string
	cmds []string
}

func (f *failOn) Run(_ context.Context, _, cmd string) error {
	f.cmds = append(f.cmds, cmd)
	if cmd == f.bad {
		return fmt.Errorf("boom")
	}
	return nil
}

func TestHooksFireInOrder(t *testing.T) {
	reg := recipe.NewRegistry()
	_ = reg.Add(recipe.Recipe{
		ID: "app", Kind: recipe.Framework, Install: []string{"install-fw"},
		Hooks: recipe.Hooks{
			"pre_build":   {{Run: "pre"}},
			"post_recipe": {{Run: "postrec"}},
			"post_create": {{Run: "postcreate"}},
			"post_build":  {{Run: "postbuild"}},
		},
	})
	p, err := resolver.Resolve(reg, []string{"app"})
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	if err := Build(context.Background(), p, Options{Dir: filepath.Join(t.TempDir(), "a"), Runner: rec, DockerUp: func() bool { return true }}); err != nil {
		t.Fatal(err)
	}
	want := "pre|install-fw|postrec|postcreate|postbuild"
	if got := strings.Join(rec.cmds, "|"); got != want {
		t.Fatalf("hook order:\n got  %s\n want %s", got, want)
	}
}

func TestHookFailureAbortsAndRollsBack(t *testing.T) {
	reg := recipe.NewRegistry()
	_ = reg.Add(recipe.Recipe{ID: "app", Kind: recipe.Framework, Install: []string{"install-fw"},
		Hooks: recipe.Hooks{"post_build": {{Run: "boom-cmd"}}}})
	p, _ := resolver.Resolve(reg, []string{"app"})
	dir := filepath.Join(t.TempDir(), "proj")
	err := Build(context.Background(), p, Options{Dir: dir, Runner: &failOn{bad: "boom-cmd"}, DockerUp: func() bool { return true }})
	if err == nil {
		t.Fatal("expected the failing hook to abort the build")
	}
	if _, e := os.Stat(dir); !os.IsNotExist(e) {
		t.Fatal("fresh dir was not rolled back after a hook failure")
	}
}

func TestUntrustedPlanRefusedWithoutConsent(t *testing.T) {
	reg := recipe.NewRegistry()
	_ = reg.Add(recipe.Recipe{ID: "app", Kind: recipe.Framework, Source: "pack:acme", Install: []string{"x"}})
	p, _ := resolver.Resolve(reg, []string{"app"})
	if err := Build(context.Background(), p, Options{Dir: filepath.Join(t.TempDir(), "u"), Runner: &recorder{}, DockerUp: func() bool { return true }}); err == nil || !strings.Contains(err.Error(), "untrusted") {
		t.Fatalf("want untrusted refusal, got %v", err)
	}
	if err := Build(context.Background(), p, Options{Dir: filepath.Join(t.TempDir(), "t"), Runner: &recorder{}, DockerUp: func() bool { return true }, Trusted: true}); err != nil {
		t.Fatalf("trusted build should run: %v", err)
	}
}

func TestDryRunPrintsHooksWithoutRunning(t *testing.T) {
	reg := recipe.NewRegistry()
	_ = reg.Add(recipe.Recipe{ID: "app", Kind: recipe.Framework,
		Hooks: recipe.Hooks{"post_build": {{Message: "hi"}, {Run: "echo x"}, {Run: "nope", When: "0"}}}})
	p, _ := resolver.Resolve(reg, []string{"app"})
	var buf bytes.Buffer
	rec := &recorder{}
	if err := Build(context.Background(), p, Options{Dir: filepath.Join(t.TempDir(), "d"), DryRun: true, Runner: rec, Out: &buf}); err != nil {
		t.Fatal(err)
	}
	if len(rec.cmds) != 0 {
		t.Fatalf("dry-run executed: %v", rec.cmds)
	}
	if s := buf.String(); !strings.Contains(s, "echo x") || strings.Contains(s, "nope") {
		t.Fatalf("dry-run hook output wrong (want echo x, not nope):\n%s", s)
	}
}

// TestFrontendRecipeBuildsAndTemplates builds the real Laravel+Next plan and
// checks the {{project}} var renders in generated file content and the join
// files land where expected.
func TestFrontendRecipeBuildsAndTemplates(t *testing.T) {
	reg, err := catalog.Registry()
	if err != nil {
		t.Fatal(err)
	}
	p, err := resolver.Resolve(reg, []string{"laravel", "ddev", "mysql", "laravel-next"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "myshop")
	if err := Build(context.Background(), p, Options{Dir: dir, Runner: &recorder{}, DockerUp: func() bool { return true }}); err != nil {
		t.Fatalf("build: %v", err)
	}
	env, err := os.ReadFile(filepath.Join(dir, "frontend", ".env.local"))
	if err != nil {
		t.Fatalf("frontend/.env.local: %v", err)
	}
	if !strings.Contains(string(env), "myshop.ddev.site") {
		t.Fatalf(".env.local not templated with {{project}}:\n%s", env)
	}
	for _, f := range []string{"config/cors.php", "AUTH-SETUP.md", ".ddev/config.frontend.yaml", "frontend/lib/api.ts", "frontend/app/page.tsx"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("expected %s to be written: %v", f, err)
		}
	}
}

type recorder struct{ cmds []string }

func (r *recorder) Run(_ context.Context, _, cmd string) error {
	r.cmds = append(r.cmds, cmd)
	return nil
}

func plan(t *testing.T) *resolver.Plan {
	t.Helper()
	reg := recipe.NewRegistry()
	_ = reg.Add(recipe.Recipe{ID: "laravel", Kind: recipe.Framework, Install: []string{"{{env}} composer create laravel/laravel ."}})
	_ = reg.Add(recipe.Recipe{ID: "ddev", Kind: recipe.Env, AppliesTo: []string{"laravel"}, Provides: []string{"env", "docker"}, Priority: -10, Install: []string{"ddev config", "ddev start"}})
	_ = reg.Add(recipe.Recipe{ID: "mysql", Kind: recipe.DB, AppliesTo: []string{"laravel"}, Provides: []string{"db"}})
	p, err := resolver.Resolve(reg, []string{"laravel", "ddev", "mysql"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBuildRunsStepsTemplatedAndWritesManifest(t *testing.T) {
	dir := t.TempDir()
	rec := &recorder{}
	err := Build(context.Background(), plan(t), Options{
		Dir:      dir,
		Runner:   rec,
		DockerUp: func() bool { return true },
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// DDEV (priority -10) provisions first, then the app is created; {{env}} -> ddev
	want := []string{"ddev config", "ddev start", "ddev composer create laravel/laravel ."}
	if strings.Join(rec.cmds, "|") != strings.Join(want, "|") {
		t.Fatalf("commands:\n got  %v\n want %v", rec.cmds, want)
	}
	// manifest written
	b, err := os.ReadFile(filepath.Join(dir, ".keel", "manifest.yaml"))
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if !strings.Contains(string(b), "framework: laravel") {
		t.Fatalf("manifest missing framework:\n%s", b)
	}
}

func TestFilesAreWrittenAndTemplated(t *testing.T) {
	reg := recipe.NewRegistry()
	_ = reg.Add(recipe.Recipe{ID: "laravel", Kind: recipe.Framework})
	_ = reg.Add(recipe.Recipe{ID: "ddev", Kind: recipe.Env, AppliesTo: []string{"laravel"}, Provides: []string{"env"}})
	_ = reg.Add(recipe.Recipe{ID: "agentrules", Kind: recipe.Extra, AppliesTo: []string{"laravel"}, Files: []recipe.File{{Path: ".agent/rules.md", Content: "run: {{env}} exec pest"}}})
	p, err := resolver.Resolve(reg, []string{"laravel", "ddev", "agentrules"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := Build(context.Background(), p, Options{Dir: dir, Runner: &recorder{}, DockerUp: func() bool { return true }}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".agent", "rules.md"))
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if strings.TrimSpace(string(b)) != "run: ddev exec pest" {
		t.Fatalf("content = %q, want {{env}} templated to ddev", b)
	}
}

func TestDryRunDoesNotExecute(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "should-not-exist")
	rec := &recorder{}
	if err := Build(context.Background(), plan(t), Options{Dir: dir, DryRun: true, Runner: rec}); err != nil {
		t.Fatal(err)
	}
	if len(rec.cmds) != 0 {
		t.Fatalf("dry-run ran commands: %v", rec.cmds)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("dry-run created the directory")
	}
}

func TestDockerGateBlocks(t *testing.T) {
	err := Build(context.Background(), plan(t), Options{
		Dir:      t.TempDir(),
		Runner:   &recorder{},
		DockerUp: func() bool { return false }, // daemon down
	})
	if err == nil || !strings.Contains(err.Error(), "docker") {
		t.Fatalf("expected docker gate error, got %v", err)
	}
}

// TestExecRunnerReallyRuns proves the real runner executes commands (not a stub):
// it runs `git init` in a temp dir and checks the repo appears.
func TestExecRunnerReallyRuns(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	if err := (ExecRunner{Out: os.Stdout}).Run(context.Background(), dir, "git init -q"); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("git init did not create .git: %v", err)
	}
}

// TestPinsRenderIntoCommands proves a recipe's version pins are exposed as
// {{pin.<name>}} tokens and substituted into its install commands, so recipes
// can pin installer versions for reproducible builds.
func TestPinsRenderIntoCommands(t *testing.T) {
	reg := recipe.NewRegistry()
	_ = reg.Add(recipe.Recipe{
		ID: "app", Kind: recipe.Framework,
		Pins:    map[string]string{"tool": "1.2.3"},
		Install: []string{"install tool@{{pin.tool}}"},
	})
	p, err := resolver.Resolve(reg, []string{"app"})
	if err != nil {
		t.Fatal(err)
	}
	steps := Steps(p)
	if len(steps) != 1 || steps[0] != "install tool@1.2.3" {
		t.Fatalf("pin token not rendered, got %v", steps)
	}
}

// TestRollbackTearsDownTheEnvironment: removing the directory is only half a
// rollback.
//
// A build that fails partway has usually already started containers. They hold
// a bind mount to the directory about to be deleted, plus a named volume and a
// network. Deleting the directory and walking away orphaned all three, and the
// next attempt at the same path reused the stale container and died with
// "current working directory is outside of container mount namespace root" —
// an error that says nothing about what actually happened.
//
// The teardown has to run BEFORE the directory goes, or it is tearing down a
// container whose working directory no longer exists.
func TestRollbackTearsDownTheEnvironment(t *testing.T) {
	reg := recipe.NewRegistry()
	_ = reg.Add(recipe.Recipe{ID: "app", Kind: recipe.Framework, Install: []string{"boom-cmd"}})
	_ = reg.Add(recipe.Recipe{
		ID: "env", Kind: recipe.Env, EnvFamily: recipe.FamilyCompose, AppliesTo: []string{"app"},
		Commands: map[string]string{
			"start": "compose-up",
			"down":  "compose-down",
		},
	})
	p, err := resolver.Resolve(reg, []string{"app", "env"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "proj")
	r := &failOn{bad: "boom-cmd"}
	if err := Build(context.Background(), p, Options{Dir: dir, Runner: r, DockerUp: func() bool { return true }}); err == nil {
		t.Fatal("expected the failing step to abort the build")
	}
	if _, e := os.Stat(dir); !os.IsNotExist(e) {
		t.Error("the directory was not rolled back")
	}
	var tore bool
	for _, c := range r.cmds {
		if c == "compose-down" {
			tore = true
		}
	}
	if !tore {
		t.Fatalf("rollback did not tear the environment down; commands run: %v", r.cmds)
	}
}

// TestRollbackWithoutATeardownStillRemovesTheDir: a native environment defines
// no teardown, and must not be turned into a failed rollback by that.
func TestRollbackWithoutATeardownStillRemovesTheDir(t *testing.T) {
	reg := recipe.NewRegistry()
	_ = reg.Add(recipe.Recipe{ID: "app", Kind: recipe.Framework, Install: []string{"boom-cmd"}})
	_ = reg.Add(recipe.Recipe{
		ID: "env", Kind: recipe.Env, EnvFamily: recipe.FamilyLocal, AppliesTo: []string{"app"},
		Commands: map[string]string{"start": "", "down": ""},
	})
	p, err := resolver.Resolve(reg, []string{"app", "env"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "proj")
	if err := Build(context.Background(), p, Options{Dir: dir, Runner: &failOn{bad: "boom-cmd"}, DockerUp: func() bool { return true }}); err == nil {
		t.Fatal("expected the failing step to abort the build")
	}
	if _, e := os.Stat(dir); !os.IsNotExist(e) {
		t.Error("the directory was not rolled back when the env defines no teardown")
	}
}

// TestRollbackLeavesAnExistingDirAlone: keel must never delete a directory it
// did not create. Adopting an existing project and having a step fail must not
// take the project with it.
func TestRollbackLeavesAnExistingDirAlone(t *testing.T) {
	reg := recipe.NewRegistry()
	_ = reg.Add(recipe.Recipe{ID: "app", Kind: recipe.Framework, Install: []string{"boom-cmd"}})
	p, _ := resolver.Resolve(reg, []string{"app"})
	dir := t.TempDir() // already exists
	keep := filepath.Join(dir, "important.txt")
	if err := os.WriteFile(keep, []byte("do not delete me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Build(context.Background(), p, Options{Dir: dir, Runner: &failOn{bad: "boom-cmd"}, DockerUp: func() bool { return true }}); err == nil {
		t.Fatal("expected the failing step to abort the build")
	}
	if _, e := os.Stat(keep); e != nil {
		t.Fatal("a pre-existing directory was deleted by the rollback")
	}
}

// SiteURL is what keel tells you to open after a build, and it has to carry the
// port the stack was actually published on.
//
// Both halves matter. An environment that publishes nothing must return "", so
// the build does not offer an address that refuses the connection; and one that
// does must follow HTTP_PORT, because that is the variable its compose file
// reads. Before {{http_port}} existed these disagreed, and Magento installed
// itself pointing at 8080 while nginx listened elsewhere.
func TestSiteURLCarriesThePublishedPort(t *testing.T) {
	t.Setenv("HTTP_PORT", "39123")

	reg := recipe.NewRegistry()
	_ = reg.Add(recipe.Recipe{ID: "app", Kind: recipe.Framework})
	_ = reg.Add(recipe.Recipe{
		ID: "web", Kind: recipe.Env, AppliesTo: []string{"app"},
		Vars: map[string]string{"site_url": "http://localhost:{{http_port}}"},
	})
	_ = reg.Add(recipe.Recipe{ID: "bare", Kind: recipe.Env, AppliesTo: []string{"app"}})

	p, err := resolver.Resolve(reg, []string{"app", "web"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := SiteURL(p, "shop"), "http://localhost:39123"; got != want {
		t.Errorf("SiteURL = %q, want %q", got, want)
	}

	bare, err := resolver.Resolve(reg, []string{"app", "bare"})
	if err != nil {
		t.Fatal(err)
	}
	if got := SiteURL(bare, "shop"); got != "" {
		t.Errorf("SiteURL = %q for an environment that publishes nothing, want empty", got)
	}
}
