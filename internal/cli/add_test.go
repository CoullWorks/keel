package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/profile"
)

// writeUserRecipe drops a loose YAML recipe into the isolated config dir, where
// catalog.Registry loads it stamped Source="user" (trusted). This lets the
// add/remove tests build and mutate a real project fully offline, without Docker
// or a pack's untrusted-consent prompt.
func writeUserRecipe(t *testing.T, name, yaml string) {
	t.Helper()
	dir := filepath.Join(profile.Dir(), "recipes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
}

// tinyStack registers a self-contained, container-free stack: a framework whose
// "create" just makes the dir, a native (local) env, an addon that renders a
// file so we can prove it landed in the project, and a service. Everything is
// echo/mkdir, so a real build and a real add run offline.
func tinyStack(t *testing.T) {
	t.Helper()
	writeUserRecipe(t, "tinyfw", `
id: tinyfw
kind: framework
label: Tiny
lang: go
create:
  tinyenv: "mkdir -p ."
`)
	writeUserRecipe(t, "tinyenv", `
id: tinyenv
kind: env
label: Local
appliesTo: [tinyfw]
provides: [env]
env_family: local
default: true
commands:
  start: ""
`)
	writeUserRecipe(t, "tinyaddon", `
id: tinyaddon
kind: addon
label: Tiny addon
appliesTo: [tinyfw]
install:
  - "echo installing tinyaddon"
files:
  - path: TINYADDON.md
    content: "added by keel add\n"
`)
	writeUserRecipe(t, "tinysvc", `
id: tinysvc
kind: service
label: Tiny service
appliesTo: [tinyfw]
install:
  - "echo installing tinysvc"
`)
}

// buildTinyProject builds the tiny stack into a fresh dir and returns its path.
// It uses the real `keel new` path (--yes --trust, offline), so the project on
// disk is exactly what add/remove then operate on.
func buildTinyProject(t *testing.T) string {
	t.Helper()
	proj := filepath.Join(t.TempDir(), "app")
	out, err := runRoot(t, "new", "tinyfw", "--env", "tinyenv", "--yes", "--trust", "-o", proj)
	if err != nil {
		t.Fatalf("building the tiny project: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(proj, ".keel", "manifest.yaml")); err != nil {
		t.Fatalf("tiny project has no manifest: %v", err)
	}
	return proj
}

// chdir moves into dir for the duration of the test.
func chdirTo(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

// The core proof: `keel add` renders the new recipe's files into an existing
// project AND appends the recipe to the manifest, without rebuilding.
func TestAddRendersAndAppends(t *testing.T) {
	isolate(t)
	tinyStack(t)
	proj := buildTinyProject(t)
	chdirTo(t, proj)

	// Precondition: the addon is not in the project yet.
	m0, err := engine.ReadManifest(".")
	if err != nil {
		t.Fatal(err)
	}
	if contains(m0.Recipes, "tinyaddon") {
		t.Fatal("tinyaddon should not be present before add")
	}

	out, err := runRoot(t, "add", "tinyaddon", "--yes")
	if err != nil {
		t.Fatalf("keel add tinyaddon: %v\n%s", err, out)
	}

	// The addon's file was rendered into the existing project.
	if _, err := os.Stat(filepath.Join(proj, "TINYADDON.md")); err != nil {
		t.Errorf("add did not render the recipe file into the project: %v", err)
	}
	// And the recipe was appended to the manifest.
	m1, err := engine.ReadManifest(".")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(m1.Recipes, "tinyaddon") {
		t.Errorf("manifest recipes = %v, want tinyaddon appended", m1.Recipes)
	}
	// The install step ran (its output is streamed).
	mustContain(t, out, "installing tinyaddon", "added tinyaddon")
}

// Adding a recipe already in the manifest is a no-op with a clear message, and
// nothing is re-run.
func TestAddIdempotent(t *testing.T) {
	isolate(t)
	tinyStack(t)
	proj := buildTinyProject(t)
	chdirTo(t, proj)

	if _, err := runRoot(t, "add", "tinysvc", "--yes"); err != nil {
		t.Fatalf("first add: %v", err)
	}
	before, err := engine.ReadManifest(".")
	if err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "add", "tinysvc", "--yes")
	if err != nil {
		t.Fatalf("second add: %v\n%s", err, out)
	}
	mustContain(t, out, "already present", "nothing to add")
	mustNotContain(t, out, "installing tinysvc")
	after, err := engine.ReadManifest(".")
	if err != nil {
		t.Fatal(err)
	}
	// The manifest must not gain a duplicate.
	if n := countID(after.Recipes, "tinysvc"); n != 1 {
		t.Errorf("tinysvc appears %d times in the manifest, want exactly 1 (%v vs %v)", n, before.Recipes, after.Recipes)
	}
}

// --dry-run previews the steps and changes nothing on disk or in the manifest.
func TestAddDryRun(t *testing.T) {
	isolate(t)
	tinyStack(t)
	proj := buildTinyProject(t)
	chdirTo(t, proj)

	out, err := runRoot(t, "add", "tinyaddon", "--dry-run")
	if err != nil {
		t.Fatalf("keel add --dry-run: %v\n%s", err, out)
	}
	mustContain(t, out, "dry-run")
	if _, err := os.Stat(filepath.Join(proj, "TINYADDON.md")); err == nil {
		t.Error("dry-run rendered a file")
	}
	m, err := engine.ReadManifest(".")
	if err != nil {
		t.Fatal(err)
	}
	if contains(m.Recipes, "tinyaddon") {
		t.Error("dry-run mutated the manifest")
	}
}

// An unknown recipe id fails before touching the project.
func TestAddUnknownRecipe(t *testing.T) {
	isolate(t)
	tinyStack(t)
	proj := buildTinyProject(t)
	chdirTo(t, proj)

	if _, err := runRoot(t, "add", "no-such-recipe-xyz", "--yes"); err == nil {
		t.Fatal("expected an error for an unknown recipe")
	}
}

// Outside a keel project, add reports the single no-project error.
func TestAddNotAKeelProject(t *testing.T) {
	isolate(t) // isolate chdirs into a fresh empty dir
	tinyStack(t)
	_, err := runRoot(t, "add", "tinyaddon", "--yes")
	if err == nil {
		t.Fatal("expected the no-project error outside a keel project")
	}
}

// remove drops a recipe from the manifest but leaves its files in place.
func TestRemoveDropsFromManifestKeepsFiles(t *testing.T) {
	isolate(t)
	tinyStack(t)
	proj := buildTinyProject(t)
	chdirTo(t, proj)

	if _, err := runRoot(t, "add", "tinyaddon", "--yes"); err != nil {
		t.Fatalf("add: %v", err)
	}
	out, err := runRoot(t, "remove", "tinyaddon", "--yes")
	if err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}
	m, err := engine.ReadManifest(".")
	if err != nil {
		t.Fatal(err)
	}
	if contains(m.Recipes, "tinyaddon") {
		t.Errorf("remove left tinyaddon in the manifest: %v", m.Recipes)
	}
	// The file the recipe installed is deliberately left behind.
	if _, err := os.Stat(filepath.Join(proj, "TINYADDON.md")); err != nil {
		t.Errorf("remove deleted an installed file, which it must not: %v", err)
	}
}

// The framework and env define the project; remove refuses them.
func TestRemoveRefusesFrameworkAndEnv(t *testing.T) {
	isolate(t)
	tinyStack(t)
	proj := buildTinyProject(t)
	chdirTo(t, proj)

	for _, id := range []string{"tinyfw", "tinyenv"} {
		if _, err := runRoot(t, "remove", id, "--yes"); err == nil {
			t.Errorf("remove %s should be refused", id)
		}
	}
}

// countID counts occurrences of id in a recipe list.
func countID(ids []string, id string) int {
	n := 0
	for _, x := range ids {
		if x == id {
			n++
		}
	}
	return n
}
