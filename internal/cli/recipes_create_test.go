package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `recipes create` scaffolds a pack from the example template, and the pack it
// writes must pass `recipes validate` and install with `recipes add` — the whole
// point is a fork that works out of the box. This drives the create → validate →
// add lifecycle offline.
func TestRecipesCreateScaffoldsAValidPack(t *testing.T) {
	isolate(t)
	dir := filepath.Join(t.TempDir(), "mypack")

	out, err := runRoot(t, "recipes", "create", "mypack", "-o", dir)
	if err != nil {
		t.Fatalf("recipes create: %v\n%s", err, out)
	}
	// It reports the files it wrote and points at the next step.
	mustContain(t, out, "keel.pack.yaml", "recipes/service.yaml", "recipes/generator.yaml", "scaffolded")

	// The layout is the pack shape the loader reads.
	for _, rel := range []string{"keel.pack.yaml", "recipes/service.yaml", "recipes/generator.yaml", "README.md", "hooks/post_create.sh"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("expected %s in the scaffolded pack: %v", rel, err)
		}
	}

	// The scaffold token is applied to the manifest name...
	manifest, err := os.ReadFile(filepath.Join(dir, "keel.pack.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), "name: mypack") {
		t.Errorf("manifest name not substituted:\n%s", manifest)
	}
	// ...but the generator's runtime {{name}} token survives verbatim (it is the
	// model name a user later types into keel gen, not the pack name).
	gen, err := os.ReadFile(filepath.Join(dir, "recipes/generator.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gen), "docs/models/{{name}}.md") {
		t.Errorf("generator runtime token {{name}} was clobbered by scaffolding:\n%s", gen)
	}
	if strings.Contains(string(gen), "{{PACK}}") || strings.Contains(string(gen), "{{AUTHOR}}") {
		t.Errorf("scaffold tokens left unsubstituted in the generator:\n%s", gen)
	}

	// It validates as a pack (read-only, runs no recipe code)...
	out, err = runRoot(t, "recipes", "validate", dir)
	if err != nil {
		t.Fatalf("recipes validate on the scaffolded pack: %v\n%s", err, out)
	}
	mustContain(t, out, "mypack", "valid")

	// ...and installs offline via recipes add (fetch + validate only).
	out, err = runRoot(t, "recipes", "add", dir)
	if err != nil {
		t.Fatalf("recipes add on the scaffolded pack: %v\n%s", err, out)
	}
	mustContain(t, out, "installed mypack", "UNTRUSTED")
}

// A name is required; with none given non-interactively the command errors
// rather than scaffolding a nameless pack.
func TestRecipesCreateRequiresAName(t *testing.T) {
	isolate(t)
	// An empty positional arg reaches the same "a name is required" guard the
	// interactive prompt would fill; pass an explicit empty string.
	if _, err := runRoot(t, "recipes", "create", "", "-o", filepath.Join(t.TempDir(), "x")); err == nil {
		t.Error("recipes create with an empty name should error, not scaffold a nameless pack")
	}
}
