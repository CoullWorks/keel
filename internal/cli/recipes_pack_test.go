package cli

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// makeLocalPack scaffolds a valid keel pack in a temp dir (a local source, so
// `recipes add` copies it in offline — no git clone, no network).
func makeLocalPack(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := scaffoldPack(io.Discard, dir, name); err != nil {
		t.Fatalf("scaffoldPack: %v", err)
	}
	return dir
}

// recipes add <local-pack> validates and installs the pack; list --packs shows
// it; remove --yes uninstalls it. This drives the whole pack lifecycle offline.
func TestRecipesAddListRemove(t *testing.T) {
	isolate(t)
	src := makeLocalPack(t, "demopack")

	// dry-run first: validates but installs nothing.
	out, err := runRoot(t, "recipes", "add", src, "--dry-run")
	if err != nil {
		t.Fatalf("recipes add --dry-run: %v\n%s", err, out)
	}
	mustContain(t, out, "demopack", "nothing installed")

	// real install.
	out, err = runRoot(t, "recipes", "add", src)
	if err != nil {
		t.Fatalf("recipes add: %v\n%s", err, out)
	}
	mustContain(t, out, "installed demopack", "UNTRUSTED")

	// list --packs now shows it as untrusted.
	out, err = runRoot(t, "recipes", "list", "--packs")
	if err != nil {
		t.Fatalf("recipes list --packs: %v", err)
	}
	mustContain(t, out, "demopack", "untrusted")

	// remove --yes uninstalls it.
	out, err = runRoot(t, "recipes", "remove", "demopack", "--yes")
	if err != nil {
		t.Fatalf("recipes remove: %v", err)
	}
	mustContain(t, out, "removed demopack")

	// gone from the packs list.
	out, _ = runRoot(t, "recipes", "list", "--packs")
	mustContain(t, out, "No packs installed")
}

// Adding the same pack twice without --force collides on the recipe id.
func TestRecipesAddCollision(t *testing.T) {
	isolate(t)
	src := makeLocalPack(t, "collidepack")
	if _, err := runRoot(t, "recipes", "add", src); err != nil {
		t.Fatalf("first add: %v", err)
	}
	// A second, differently-named pack that ships a recipe id already present.
	src2 := makeLocalPack(t, "collidepack") // same recipe id "collidepack"
	// Reinstall the same name is fine (same pack owns the id); to force a
	// collision, add a pack whose recipe id matches a built-in framework.
	_ = src2
	fwPack := filepath.Join(t.TempDir(), "laravelclash")
	if err := scaffoldPack(io.Discard, fwPack, "laravelclash"); err != nil {
		t.Fatal(err)
	}
	// Rename its recipe to collide with the built-in "laravel".
	recipesDir := filepath.Join(fwPack, "recipes")
	if err := os.Rename(filepath.Join(recipesDir, "laravelclash.yaml"), filepath.Join(recipesDir, "laravel.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recipesDir, "laravel.yaml"), []byte(starterRecipe("laravel", "framework")), 0o644); err != nil {
		t.Fatal(err)
	}
	// Point the manifest at the renamed recipe.
	if err := os.WriteFile(filepath.Join(fwPack, "keel.pack.yaml"),
		[]byte("schema_version: 1\nname: laravelclash\nversion: 0.1.0\nkeel_version_constraint: \">= 0.1.0\"\nrecipes:\n  - recipes/laravel.yaml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := runRoot(t, "recipes", "add", fwPack)
	if err == nil {
		t.Fatal("expected an id collision with the built-in laravel recipe")
	}
	mustContain(t, err.Error(), "collide")
}

// writePackFile is a small helper for assembling a pack on disk.
func writePackFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// recipes verify actually builds + smoke-tests a stack. We use an echo-only
// framework + a no-Docker local env so the whole verifyOne path (build + smoke
// steps) runs offline, exercising the success branch end-to-end.
func TestRecipesVerifyBuildsOffline(t *testing.T) {
	isolate(t)
	src := filepath.Join(t.TempDir(), "vpack")
	writePackFile(t, src, "keel.pack.yaml",
		"schema_version: 1\nname: vpack\nversion: 0.1.0\nkeel_version_constraint: \">= 0.1.0\"\nrecipes:\n  - recipes/fw.yaml\n  - recipes/env.yaml\n")
	writePackFile(t, src, "recipes/fw.yaml",
		"schema_version: 1\nid: vfw\nkind: framework\nlabel: VFW\nlang: other\ninstall:\n  - \"echo building vfw\"\nsmoke:\n  - \"echo smoke-fw\"\n")
	writePackFile(t, src, "recipes/env.yaml",
		"schema_version: 1\nid: venv\nkind: env\nlabel: VEnv\nappliesTo: [vfw]\nprovides: [env]\ndefault: true\ncommands:\n  start: \"\"\n  exec: \"\"\nsmoke:\n  - \"echo smoke-env\"\n")
	if _, err := runRoot(t, "recipes", "add", src); err != nil {
		t.Fatalf("recipes add: %v", err)
	}
	out, err := runRoot(t, "recipes", "verify", "vfw")
	if err != nil {
		t.Fatalf("recipes verify vfw: %v\n%s", err, out)
	}
	mustContain(t, out, "booted", "all stacks verified", "smoke")
}

// recipes verify on a framework with no env recipe reports it as skipped and
// still succeeds — this drives recipesVerifyCmd's loop + the "no env" branch
// without needing Docker (verifyOne is never entered).
func TestRecipesVerifyNoEnvSkipped(t *testing.T) {
	isolate(t)
	src := makeLocalPack(t, "noenvpack")
	if _, err := runRoot(t, "recipes", "add", src); err != nil {
		t.Fatalf("recipes add: %v", err)
	}
	out, err := runRoot(t, "recipes", "verify", "noenvpack")
	if err != nil {
		t.Fatalf("recipes verify noenvpack: %v", err)
	}
	mustContain(t, out, "no env, skipped", "all stacks verified")
}

// recipes add rejects a pack that requires a newer keel than we are.
func TestRecipesAddKeelConstraint(t *testing.T) {
	isolate(t)
	src := filepath.Join(t.TempDir(), "futurepack")
	writePackFile(t, src, "keel.pack.yaml",
		"schema_version: 1\nname: futurepack\nversion: 0.1.0\nkeel_version_constraint: \">= 99.0.0\"\nrecipes:\n  - recipes/f.yaml\n")
	writePackFile(t, src, "recipes/f.yaml",
		"schema_version: 1\nid: fp\nkind: framework\nlabel: FP\nlang: other\ninstall: [\"echo hi\"]\n")
	_, err := runRoot(t, "recipes", "add", src)
	if err == nil {
		t.Fatal("expected a keel-version-constraint error")
	}
	mustContain(t, err.Error(), "requires keel")
}

// recipes add rejects a pack whose schema_version is newer than we support.
func TestRecipesAddSchemaTooNew(t *testing.T) {
	isolate(t)
	src := filepath.Join(t.TempDir(), "newschema")
	writePackFile(t, src, "keel.pack.yaml",
		"schema_version: 999\nname: newschema\nversion: 0.1.0\nrecipes:\n  - recipes/f.yaml\n")
	writePackFile(t, src, "recipes/f.yaml",
		"schema_version: 1\nid: ns\nkind: framework\nlabel: NS\nlang: other\ninstall: [\"echo hi\"]\n")
	_, err := runRoot(t, "recipes", "add", src)
	if err == nil {
		t.Fatal("expected a schema-version error")
	}
	mustContain(t, err.Error(), "schema_version")
}

// recipes add rejects a pack manifest with no name.
func TestRecipesAddNoName(t *testing.T) {
	isolate(t)
	src := filepath.Join(t.TempDir(), "noname")
	writePackFile(t, src, "keel.pack.yaml",
		"schema_version: 1\nname: \"\"\nversion: 0.1.0\nrecipes:\n  - recipes/f.yaml\n")
	writePackFile(t, src, "recipes/f.yaml",
		"schema_version: 1\nid: nn\nkind: framework\nlabel: NN\nlang: other\ninstall: [\"echo hi\"]\n")
	_, err := runRoot(t, "recipes", "add", src)
	if err == nil {
		t.Fatal("expected a missing-name error")
	}
	mustContain(t, err.Error(), "missing a name")
}

// recipes add rejects a pack with an invalid recipe file.
func TestRecipesAddInvalidRecipe(t *testing.T) {
	isolate(t)
	src := filepath.Join(t.TempDir(), "badrecipe")
	writePackFile(t, src, "keel.pack.yaml",
		"schema_version: 1\nname: badrecipe\nversion: 0.1.0\nrecipes:\n  - recipes/f.yaml\n")
	// Missing required fields (no id/kind) → recipe load fails.
	writePackFile(t, src, "recipes/f.yaml", "schema_version: 1\nlabel: broken\n")
	_, err := runRoot(t, "recipes", "add", src)
	if err == nil {
		t.Fatal("expected an invalid-recipe error")
	}
}

// validatePackDir rejects a pack requiring a newer keel.
func TestValidatePackDirKeelConstraint(t *testing.T) {
	isolate(t)
	src := filepath.Join(t.TempDir(), "vconstraint")
	writePackFile(t, src, "keel.pack.yaml",
		"schema_version: 1\nname: vconstraint\nversion: 0.1.0\nkeel_version_constraint: \">= 99.0.0\"\nrecipes:\n  - recipes/f.yaml\n")
	writePackFile(t, src, "recipes/f.yaml",
		"schema_version: 1\nid: vc\nkind: framework\nlabel: VC\nlang: other\ninstall: [\"echo hi\"]\n")
	if err := validatePackDir(io.Discard, src); err == nil {
		t.Fatal("expected validatePackDir to reject a future-keel pack")
	}
}

// freshness (text) with a framework filter and pins renders the per-recipe rows.
func TestRecipesFreshnessTextFramework(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "recipes", "freshness", "laravel")
	if err != nil {
		t.Fatalf("freshness laravel: %v", err)
	}
	mustContain(t, out, "laravel", "recipes")
}

// freshness --stale shows only review-due recipes (may be empty, but must not
// error and must print the summary line).
func TestRecipesFreshnessStaleOnly(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "recipes", "freshness", "--stale", "--stale-after", "1")
	if err != nil {
		t.Fatalf("freshness --stale: %v", err)
	}
	mustContain(t, out, "recipes")
}
