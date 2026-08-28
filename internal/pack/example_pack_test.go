package pack

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/coullworks/keel/internal/recipe"
)

// examplePackDir locates the pack fixture in this package's testdata, so the test
// does not depend on the working directory. keel ships zero built-in packs — the
// example pack is its own repository now — so the pack loader is exercised against
// a controlled fixture that mirrors the reference pack (every recipe kind, every
// hook stage). runtime.Caller gives this file's path; the fixture sits beside it.
func examplePackDir(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Join(filepath.Dir(self), "testdata", "example-pack")
	if _, err := os.Stat(filepath.Join(dir, "keel.pack.yaml")); err != nil {
		t.Fatalf("pack fixture not found at %s: %v", dir, err)
	}
	return dir
}

// The pack fixture mirrors keel's fork-me reference for packs, so it must load and
// validate exactly as `keel recipes validate <dir>` does: read the manifest, meet
// the keel-version constraint, and parse+validate every recipe file. This is the
// same sequence validatePackDir runs in internal/cli, exercised against a real
// pack on disk so the loader can never rot.
func TestExamplePackValidates(t *testing.T) {
	dir := examplePackDir(t)

	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if m.Name != "example-pack" {
		t.Errorf("manifest name = %q, want example-pack", m.Name)
	}
	if m.SchemaVersion > recipe.SupportedSchema {
		t.Errorf("manifest schema_version %d newer than supported %d", m.SchemaVersion, recipe.SupportedSchema)
	}
	// The pack targets a keel this build satisfies (a real 0.x keel version).
	if ok, err := SatisfiesKeel(m.KeelVersion, "0.1.0"); err != nil {
		t.Fatalf("keel constraint %q: %v", m.KeelVersion, err)
	} else if !ok {
		t.Errorf("example-pack requires keel %q, unsatisfiable by a 0.1.0 build", m.KeelVersion)
	}

	// Every recipe file the pack ships parses and passes recipe.Validate (LoadInto
	// calls Add, which validates). A malformed recipe fails here with its file name.
	reg := recipe.NewRegistry()
	if err := recipe.LoadInto(reg, os.DirFS(dir), "pack:"+m.Name, m.Name); err != nil {
		t.Fatalf("loading pack recipes: %v", err)
	}

	// Each recipe listed in the manifest exists in the loaded registry, so the
	// manifest and recipes/ cannot drift.
	if len(m.Recipes) == 0 {
		t.Fatal("the manifest lists no recipes")
	}
	if reg.Len() != len(m.Recipes) {
		t.Errorf("manifest lists %d recipes, %d loaded from disk", len(m.Recipes), reg.Len())
	}
}

// The pack demonstrates one recipe of each kind a pack can sensibly ship. Assert
// every one of those kinds is present, so the reference stays complete.
func TestExamplePackCoversEveryKind(t *testing.T) {
	dir := examplePackDir(t)
	reg := recipe.NewRegistry()
	if err := recipe.LoadInto(reg, os.DirFS(dir), "pack:example-pack", "example-pack"); err != nil {
		t.Fatalf("loading pack recipes: %v", err)
	}

	want := []recipe.Kind{
		recipe.Env, recipe.DB, recipe.Config, recipe.Service, recipe.Addon, recipe.Generator,
	}
	have := map[recipe.Kind]bool{}
	for _, r := range reg.All() {
		have[r.Kind] = true
	}
	for _, k := range want {
		if !have[k] {
			t.Errorf("the example pack should ship a %q recipe as a reference, but does not", k)
		}
	}
}

// Every lifecycle hook stage a pack recipe can carry is demonstrated somewhere in
// the pack, and each hook's script (when it uses one) exists on disk — a
// `script:` pointing at a missing file would fail at build time, not load time,
// so this catches it early.
func TestExamplePackDemonstratesEveryHookStage(t *testing.T) {
	dir := examplePackDir(t)
	reg := recipe.NewRegistry()
	if err := recipe.LoadInto(reg, os.DirFS(dir), "pack:example-pack", "example-pack"); err != nil {
		t.Fatalf("loading pack recipes: %v", err)
	}

	seen := map[string]bool{}
	for _, r := range reg.All() {
		for stage, hooks := range r.Hooks {
			if !recipe.Stages[stage] {
				t.Errorf("recipe %s uses unknown hook stage %q", r.ID, stage)
			}
			seen[stage] = true
			for i, h := range hooks {
				if h.Script == "" {
					continue
				}
				if _, err := os.Stat(filepath.Join(dir, h.Script)); err != nil {
					t.Errorf("recipe %s hook %s[%d] references missing script %q: %v", r.ID, stage, i, h.Script, err)
				}
			}
		}
	}
	for stage := range recipe.Stages {
		if !seen[stage] {
			t.Errorf("hook stage %q is a valid pack stage but the reference pack never demonstrates it", stage)
		}
	}
}
