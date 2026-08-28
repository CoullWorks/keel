package pluginstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLintPlugin writes a plugin dir with the given register.yaml body and the
// given extra files (path -> content); a file whose path is under bin/ is written
// executable, everything else 0644. Returns the plugin dir.
func writeLintPlugin(t *testing.T, register string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "config", "register.yaml"), register, 0o644)
	for rel, content := range files {
		mode := os.FileMode(0o644)
		if strings.HasPrefix(rel, "bin/") {
			mode = 0o755
		}
		mustWrite(t, filepath.Join(dir, rel), content, mode)
	}
	return dir
}

func mustWrite(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

const lintFullRegister = `schema: 1
name: widget
version: 1.0.0
description: a conformant test plugin
author: t
license: MIT
homepage: https://example.test/widget
capabilities: [exec]
commands:
  - name: widget
    summary: do the thing
    run: ["bin/hello"]
`

// A plugin that ships everything the standard asks for passes both bars.
func TestLintConformantPluginHasNoProblems(t *testing.T) {
	dir := writeLintPlugin(t, lintFullRegister, map[string]string{
		"bin/hello": "#!/bin/sh\necho hi\n",
		"LICENSE":   "MIT\n",
		"README.md": "# widget\n",
	})
	if probs := Lint(dir, true); len(probs) != 0 {
		t.Errorf("a conformant plugin should have no problems, got %v", probs)
	}
}

// A declared executable that is not present is a problem even in the lenient bar:
// the plugin would fail the moment that command/screen ran.
func TestLintMissingExecutableAlwaysFails(t *testing.T) {
	dir := writeLintPlugin(t, lintFullRegister, map[string]string{
		"LICENSE":   "MIT\n",
		"README.md": "# widget\n",
	}) // no bin/hello
	probs := Lint(dir, false)
	if len(probs) == 0 || !strings.Contains(strings.Join(probs, "\n"), "bin/hello") {
		t.Errorf("a missing declared executable should be reported, got %v", probs)
	}
}

// The executable bit is a strict-only check: a present-but-not-executable file
// passes the lenient bar and fails the strict one.
func TestLintExecutableBitIsStrictOnly(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "config", "register.yaml"), lintFullRegister, 0o644)
	mustWrite(t, filepath.Join(dir, "bin", "hello"), "#!/bin/sh\n", 0o644) // NOT executable
	mustWrite(t, filepath.Join(dir, "LICENSE"), "MIT\n", 0o644)
	mustWrite(t, filepath.Join(dir, "README.md"), "# widget\n", 0o644)

	if probs := Lint(dir, false); len(probs) != 0 {
		t.Errorf("lenient lint should not flag the executable bit, got %v", probs)
	}
	probs := Lint(dir, true)
	if len(probs) == 0 || !strings.Contains(strings.Join(probs, "\n"), "not executable") {
		t.Errorf("strict lint should flag a non-executable declared file, got %v", probs)
	}
}

// LICENSE and README are the strict release bar: a bare but valid plugin passes
// the lenient check and fails the strict one, naming both.
func TestLintStrictRequiresLicenseAndReadme(t *testing.T) {
	dir := writeLintPlugin(t, lintFullRegister, map[string]string{
		"bin/hello": "#!/bin/sh\n",
	}) // no LICENSE, no README
	if probs := Lint(dir, false); len(probs) != 0 {
		t.Errorf("lenient lint should not require LICENSE/README, got %v", probs)
	}
	joined := strings.Join(Lint(dir, true), "\n")
	if !strings.Contains(joined, "LICENSE") || !strings.Contains(joined, "README") {
		t.Errorf("strict lint should require LICENSE and README, got %q", joined)
	}
}

// A valid data-only plugin (no executables) conforms — the standard does not
// invent executables a plugin never declared.
func TestLintDataOnlyPluginConforms(t *testing.T) {
	reg := "schema: 1\nname: data\nversion: 1.0.0\ndescription: a data-only plugin\nauthor: t\nlicense: MIT\n"
	dir := writeLintPlugin(t, reg, map[string]string{"LICENSE": "MIT\n", "README.md": "# data\n"})
	if probs := Lint(dir, true); len(probs) != 0 {
		t.Errorf("a data-only plugin should conform, got %v", probs)
	}
}

// An invalid manifest is the single problem reported — there is nothing else to
// check on a plugin that will not even parse.
func TestLintInvalidManifest(t *testing.T) {
	dir := writeLintPlugin(t, "schema: 1\nname: x\n", nil) // missing version/description
	if probs := Lint(dir, false); len(probs) != 1 {
		t.Errorf("an invalid manifest should be the one problem, got %v", probs)
	}
}
