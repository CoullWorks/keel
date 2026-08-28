package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// `keel plugins create` scaffolds a directory keel will register: the register
// file it writes must pass the same validation `keel plugins test` runs.
func TestPluginsCreateScaffoldsAValidPlugin(t *testing.T) {
	wd := isolate(t)

	out, err := runRoot(t, "plugins", "create", "my-plugin")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	mustContain(t, out, "created", "my-plugin")

	dest := filepath.Join(wd, "my-plugin")
	for _, rel := range []string{"config/register.yaml", "config/event.yaml", "README.md", "LICENSE"} {
		if _, err := os.Stat(filepath.Join(dest, rel)); err != nil {
			t.Errorf("scaffold missing %s: %v", rel, err)
		}
	}

	// The scaffold must pass `keel plugins test` — create and test agree.
	tout, err := runRoot(t, "plugins", "test", dest)
	if err != nil {
		t.Fatalf("the scaffold did not pass test: %v\n%s", err, tout)
	}
	mustContain(t, tout, "register.yaml valid", "my-plugin")
}

// A bad name is refused before anything is written.
func TestPluginsCreateRefusesABadName(t *testing.T) {
	isolate(t)
	if _, err := runRoot(t, "plugins", "create", "has space"); err == nil {
		t.Error("a name with a space should be refused")
	}
	if _, err := runRoot(t, "plugins", "create", "a/b"); err == nil {
		t.Error("a name with a slash should be refused")
	}
}

// Creating into an existing directory is refused rather than clobbering it.
func TestPluginsCreateRefusesAnExistingDir(t *testing.T) {
	wd := isolate(t)
	if err := os.MkdirAll(filepath.Join(wd, "taken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runRoot(t, "plugins", "create", "taken"); err == nil {
		t.Error("creating over an existing directory should be refused")
	}
}

// `keel plugins test` reports the exact reason a plugin will not register, without
// running any of its code.
func TestPluginsTestReportsAnInvalidPlugin(t *testing.T) {
	wd := isolate(t)
	dir := filepath.Join(wd, "broken")
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A register file missing the required version.
	if err := os.WriteFile(filepath.Join(dir, "config", "register.yaml"),
		[]byte("schema: 1\nname: broken\ndescription: a broken plugin for the test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runRoot(t, "plugins", "test", dir); err == nil {
		t.Error("a plugin missing its version should fail test")
	}
}

// test surfaces a capability the plugin requests, so an author can see what trust
// the plugin will ask for.
func TestPluginsTestSurfacesCapabilities(t *testing.T) {
	wd := isolate(t)
	dir := filepath.Join(wd, "netty")
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	reg := "schema: 1\nname: netty\nversion: 0.1.0\ndescription: a plugin that needs the network to do its job\n" +
		"author: t\nlicense: MIT\ncapabilities: [net]\n"
	if err := os.WriteFile(filepath.Join(dir, "config", "register.yaml"), []byte(reg), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "plugins", "test", dir)
	if err != nil {
		t.Fatalf("a valid plugin with a capability should pass: %v\n%s", err, out)
	}
	mustContain(t, out, "capabilities requested", "net")
}

// --strict is the release bar a plugin's CI gates on: a valid plugin that ships
// no LICENSE/README passes the default check but fails --strict.
func TestPluginsTestStrictEnforcesReleaseBar(t *testing.T) {
	wd := isolate(t)
	dir := filepath.Join(wd, "bare")
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	reg := "schema: 1\nname: bare\nversion: 0.1.0\ndescription: a valid plugin with no license or readme\nauthor: t\nlicense: MIT\n"
	if err := os.WriteFile(filepath.Join(dir, "config", "register.yaml"), []byte(reg), 0o644); err != nil {
		t.Fatal(err)
	}
	// The default check passes a bare-but-valid plugin.
	if _, err := runRoot(t, "plugins", "test", dir); err != nil {
		t.Fatalf("the default check should pass a bare valid plugin: %v", err)
	}
	// --strict fails it: no LICENSE, no README.
	if _, err := runRoot(t, "plugins", "test", dir, "--strict"); err == nil {
		t.Error("--strict should fail a plugin with no LICENSE/README")
	}
}

// `keel plugins publish` prints the checklist and the install line without a tag,
// and refuses a directory that is not a plugin.
func TestPluginsPublishPrintsChecklist(t *testing.T) {
	wd := isolate(t)
	if _, err := runRoot(t, "plugins", "create", "shipit"); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(wd, "shipit")
	out, err := runRoot(t, "plugins", "publish", dest)
	if err != nil {
		t.Fatalf("publish: %v\n%s", err, out)
	}
	mustContain(t, out, "checklist", "keel plugins add", "shipit")

	if _, err := runRoot(t, "plugins", "publish", filepath.Join(wd, "not-a-plugin")); err == nil {
		t.Error("publishing a non-plugin directory should be refused")
	}
}
