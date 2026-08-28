package cli

import (
	"bytes"
	"github.com/charmbracelet/huh"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/creds"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

// stubCredentials replaces the interactive collector with canned answers.
func stubCredentials(t *testing.T, answers []creds.Value, err error) {
	t.Helper()
	orig := askCredentials
	askCredentials = func(_ io.Writer, _ []recipe.Credential, values []creds.Value) ([]creds.Value, error) {
		if err != nil {
			return nil, err
		}
		return append(values[:0:0], answers...), nil
	}
	t.Cleanup(func() { askCredentials = orig })
}

func magentoPlan(t *testing.T, env string) *resolver.Plan {
	t.Helper()
	reg, rerr := catalog.Registry()
	if rerr != nil {
		t.Fatal(rerr)
	}
	plan, rerr := resolver.Resolve(reg, []string{"magento", env})
	if rerr != nil {
		t.Fatal(rerr)
	}
	return plan
}

// Magento declares its Adobe keys in the recipe, so the collector picks them up
// from the environment without keel naming Magento anywhere in Go.
func TestCollectCredentialsReadsTheEnvironment(t *testing.T) {
	isolate(t)
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	t.Setenv("MAGENTO_PUBLIC_KEY", "pub123")
	t.Setenv("MAGENTO_PRIVATE_KEY", "priv456")

	var buf bytes.Buffer
	values, err := collectCredentials(&buf, magentoPlan(t, "magento-ddev"), t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	var got creds.Value
	for _, v := range values {
		if v.ID == "repo.magento.com" {
			got = v
		}
	}
	if got.Username != "pub123" || got.Secret != "priv456" {
		t.Fatalf("keys from the environment were not picked up: %+v", got)
	}
}

// With --yes and nothing supplied, the build fails here rather than several
// minutes later inside composer, against a half-created project. The message
// says what is missing and the three ways to supply it.
func TestCollectCredentialsUnattendedFailsWithGuidance(t *testing.T) {
	isolate(t)
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	t.Setenv("MAGENTO_PUBLIC_KEY", "")
	t.Setenv("MAGENTO_PRIVATE_KEY", "")

	var buf bytes.Buffer
	_, err := collectCredentials(&buf, magentoPlan(t, "magento-ddev"), t.TempDir(), true)
	if err == nil {
		t.Fatal("a required credential that is missing must stop the build")
	}
	mustContain(t, err.Error(), "repo.magento.com", "Adobe Marketplace keys",
		"commercemarketplace.adobe.com", "keel secrets credentials --add")
}

// TestApplyCredentialsWritesWhereTheEnvironmentReads is the regression test for
// the DDEV-only bug: the same credentials must land in a different place for a
// compose project, or Composer never sees them.
func TestApplyCredentialsWritesWhereTheEnvironmentReads(t *testing.T) {
	isolate(t)
	values := []creds.Value{{ID: "repo.magento.com", Kind: recipe.CredComposer, Username: "pub", Secret: "priv"}}

	ddevDir := t.TempDir()
	if err := applyCredentials(&bytes.Buffer{}, magentoPlan(t, "magento-ddev"), ddevDir, values); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ddevDir, ".ddev", "homeadditions", ".composer", "auth.json")); err != nil {
		t.Fatalf("DDEV auth.json missing: %v", err)
	}

	composeDir := t.TempDir()
	if err := applyCredentials(&bytes.Buffer{}, magentoPlan(t, "magento-docker"), composeDir, values); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(composeDir, "auth.json")); err != nil {
		t.Fatalf("compose auth.json missing (this is the bug that made docker Magento unauthenticatable): %v", err)
	}
	if _, err := os.Stat(filepath.Join(composeDir, ".ddev", "homeadditions", ".composer", "auth.json")); err == nil {
		t.Error("a compose project should not get DDEV's path")
	}
}

// Env-kind credentials go to .env, and .env is tightened to 0600 because it now
// holds real API keys.
func TestApplyCredentialsWritesEnvKeys(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	err := applyCredentials(&bytes.Buffer{}, magentoPlan(t, "magento-ddev"), dir, []creds.Value{
		{ID: "OPENAI_API_KEY", Kind: recipe.CredEnv, Secret: "sk-test"},
		{ID: "GOOGLE_ANALYTICS_ID", Kind: recipe.CredEnv, Secret: "G-123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, readErr := os.ReadFile(filepath.Join(dir, ".env"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	mustContain(t, string(b), "OPENAI_API_KEY=sk-test", "GOOGLE_ANALYTICS_ID=G-123")
	info, _ := os.Stat(filepath.Join(dir, ".env"))
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf(".env holding API keys should be 0600, got %o", perm)
	}
}

// A plan that declares no credentials asks for nothing: most stacks need none.
func TestCollectCredentialsNoopWhenNothingDeclared(t *testing.T) {
	isolate(t)
	reg, _ := catalog.Registry()
	plan, err := resolver.Resolve(reg, []string{"laravel", "laravel-local"})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	values, err := collectCredentials(&buf, plan, t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 0 || buf.Len() != 0 {
		t.Fatalf("nothing declared should mean nothing asked: %+v %q", values, buf.String())
	}
}

func TestCredentialFromEnvGenericNaming(t *testing.T) {
	t.Setenv("KEEL_AUTH_REPO_AMASTY_COM_USER", "u")
	t.Setenv("KEEL_AUTH_REPO_AMASTY_COM_SECRET", "s")
	u, s := credentialFromEnv(recipe.Credential{ID: "repo.amasty.com", Kind: recipe.CredComposer})
	if u != "u" || s != "s" {
		t.Fatalf("generic env naming failed: %q %q", u, s)
	}
}

func TestFirstURL(t *testing.T) {
	if got := firstURL("see https://example.com/keys (sign in)"); got != "https://example.com/keys" {
		t.Fatalf("firstURL = %q", got)
	}
	if got := firstURL("no link here"); got != "" {
		t.Fatalf("firstURL should be empty when there is no link, got %q", got)
	}
}

// stubPrompts drives the interactive collector without a terminal: confirm and
// selectOne answer from canned queues, huh inputs are covered separately.
func stubPrompts(t *testing.T, confirms []bool, picks []string) {
	t.Helper()
	oc, os_ := confirm, selectOne
	ci, pi := 0, 0
	confirm = func(_ string, v *bool) error {
		if ci >= len(confirms) {
			*v = false
			return nil
		}
		*v = confirms[ci]
		ci++
		return nil
	}
	selectOne = func(_ string, _ []huh.Option[string], v *string) error {
		if pi >= len(picks) {
			return nil
		}
		*v = picks[pi]
		pi++
		return nil
	}
	t.Cleanup(func() { confirm, selectOne = oc, os_ })
}

// `keel secrets credentials` lists what is remembered without ever printing a
// value: terminals get screenshotted and scrollback gets pasted into issues.
func TestSecretsCredentialsListsWithoutPrintingSecrets(t *testing.T) {
	isolate(t)
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	store, err := creds.Load()
	if err != nil {
		t.Fatal(err)
	}
	store.Remember(
		creds.Value{ID: "repo.magento.com", Kind: recipe.CredComposer, Username: "pubkey", Secret: "priv-secret-value", Remember: true},
		creds.Value{ID: "OPENAI_API_KEY", Kind: recipe.CredEnv, Secret: "sk-abcdefgh1234", Remember: true},
	)
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "secrets", "credentials")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "repo.magento.com", "pubkey", "OPENAI_API_KEY")
	mustNotContain(t, out, "priv-secret-value", "sk-abcdefgh1234")
	// The tail of an env value is shown so an entry is recognisable.
	mustContain(t, out, "1234")
}

func TestSecretsCredentialsEmpty(t *testing.T) {
	isolate(t)
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	out, err := runRoot(t, "secrets", "credentials")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "No remembered credentials")
}

func TestSecretsCredentialsRemove(t *testing.T) {
	isolate(t)
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	store, _ := creds.Load()
	store.Remember(creds.Value{ID: "repo.amasty.com", Kind: recipe.CredComposer, Username: "u", Secret: "p", Remember: true})
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "secrets", "credentials", "--remove", "repo.amasty.com")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "removed repo.amasty.com")
	again, _ := creds.Load()
	if _, ok := again.Get("repo.amasty.com"); ok {
		t.Fatal("the credential should be gone")
	}
	// Removing something that was never there says so rather than pretending.
	if _, err := runRoot(t, "secrets", "credentials", "--remove", "nope"); err == nil {
		t.Fatal("removing an unknown id should be an error")
	}
}

// askExtras loops so several extension vendors can be added in one pass, and
// stops when the answer is no.
func TestAskExtrasStopsWhenDeclined(t *testing.T) {
	stubPrompts(t, []bool{false}, nil)
	got, err := askExtras(io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("declining should add nothing, got %+v", got)
	}
}

func TestMasked(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"ab", "••"},
		{"abcd", "••••"},
		{"sk-abcd1234", "••••••••1234"},
		{"  padded  ", "••••••••dded"}, // trimmed before masking
	} {
		if got := masked(tc.in); got != tc.want {
			t.Errorf("masked(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := masked("secretvalue"); strings.Contains(got, "secret") {
		t.Errorf("masked must hide the start of a value, got %q", got)
	}
}
