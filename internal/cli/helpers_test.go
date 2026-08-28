package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/recipe"
)

// loadCatalog returns the built-in recipe registry (helper wrapper).
func loadCatalog(t *testing.T) (*recipe.Registry, error) {
	t.Helper()
	return catalog.Registry()
}

// frameworkIDs lists every framework recipe id in the registry.
func frameworkIDs(reg *recipe.Registry) []string {
	var ids []string
	for _, fw := range reg.OfKind(recipe.Framework) {
		ids = append(ids, fw.ID)
	}
	return ids
}

// isolate points keel's config (profile, projects.yaml, packs.yaml, recipe packs)
// at a throwaway dir and runs the test inside a fresh working directory, so no
// test touches the real user config or CWD. Returns the temp working dir.
func isolate(t *testing.T) string {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("KEEL_CONFIG_DIR", cfg)
	// Nothing should reach out for a commerce tool or magento keys under test.
	t.Setenv("KEEL_COMMERCE_TOOL", "")
	t.Setenv("MAGENTO_PUBLIC_KEY", "")
	t.Setenv("MAGENTO_PRIVATE_KEY", "")

	wd := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(wd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	return wd
}

// runRoot drives the whole command tree via the exported constructor, exactly as
// Execute() does, and returns the combined output plus the command error.
//
// Commands write through cmd.OutOrStdout()/ErrOrStderr(), so setting cobra's
// writers is enough. This used to swap the process's real stdout file
// descriptor, which made the tests unsafe to run in parallel.
func runRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := rootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

// mustContain fails unless out contains every substring in wants.
func mustContain(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n--- got ---\n%s", w, out)
		}
	}
}

// mustNotContain fails if out contains any of the substrings.
func mustNotContain(t *testing.T, out string, notWants ...string) {
	t.Helper()
	for _, w := range notWants {
		if strings.Contains(out, w) {
			t.Errorf("output unexpectedly contains %q\n--- got ---\n%s", w, out)
		}
	}
}
