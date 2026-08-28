package creds

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

func planWith(rs ...recipe.Recipe) *resolver.Plan {
	return &resolver.Plan{Framework: "magento", Recipes: rs}
}

// TestComposerAuthPathFollowsTheEnvironment is the regression test for a bug that
// never announced itself: the old flow always wrote DDEV's homeadditions path, so
// under compose, Sail or a native install it produced a file nothing reads and a
// `composer create-project` that failed to authenticate for no visible reason.
func TestComposerAuthPathFollowsTheEnvironment(t *testing.T) {
	t.Setenv("COMPOSER_HOME", "/home/someone/.composer")
	proj := "/tmp/shop"
	for _, tc := range []struct {
		family, want string
		inProject    bool
	}{
		{recipe.FamilyDDEV, "/tmp/shop/.ddev/homeadditions/.composer/auth.json", true},
		{recipe.FamilyCompose, "/tmp/shop/auth.json", true},
		{recipe.FamilySail, "/tmp/shop/auth.json", true},
		{recipe.FamilyLocal, "/home/someone/.composer/auth.json", false},
	} {
		got, inProject := ComposerAuthPath(tc.family, proj)
		if got != tc.want {
			t.Errorf("%s: auth.json path = %q, want %q", tc.family, got, tc.want)
		}
		if inProject != tc.inProject {
			t.Errorf("%s: inProject = %v, want %v", tc.family, inProject, tc.inProject)
		}
	}
}

func TestWriteComposerAuthWritesBasicAndBearer(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteComposerAuth(recipe.FamilyCompose, dir, []Value{
		{ID: "repo.magento.com", Kind: recipe.CredComposer, Username: "pub", Secret: "priv"},
		{ID: "repo.example.com", Kind: recipe.CredComposer, Secret: "tok", Auth: recipe.AuthBearer},
		{ID: "OPENAI_API_KEY", Kind: recipe.CredEnv, Secret: "sk-x"}, // not composer, ignored here
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var af authFile
	if err := json.Unmarshal(b, &af); err != nil {
		t.Fatalf("auth.json is not valid JSON: %v", err)
	}
	if af.HTTPBasic["repo.magento.com"].Username != "pub" || af.HTTPBasic["repo.magento.com"].Password != "priv" {
		t.Errorf("http-basic entry wrong: %+v", af.HTTPBasic)
	}
	if af.Bearer["repo.example.com"] != "tok" {
		t.Errorf("bearer entry wrong: %+v", af.Bearer)
	}
	if strings.Contains(string(b), "sk-x") {
		t.Error("an env key must not end up in auth.json")
	}
	// The file is the credential.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("auth.json mode = %o, want 600", perm)
	}
}

// A native install shares one auth.json across every project on the machine, so
// writing must merge. Overwriting would silently drop the credentials for
// everything else the developer works on.
func TestWriteComposerAuthMergesWithExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	existing := `{"http-basic":{"repo.other.com":{"username":"keep","password":"me"}}}`
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteComposerAuth(recipe.FamilyCompose, dir, []Value{
		{ID: "repo.magento.com", Kind: recipe.CredComposer, Username: "pub", Secret: "priv"},
	}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var af authFile
	if err := json.Unmarshal(b, &af); err != nil {
		t.Fatal(err)
	}
	if af.HTTPBasic["repo.other.com"].Username != "keep" {
		t.Fatalf("an unrelated credential was dropped: %+v", af.HTTPBasic)
	}
	if af.HTTPBasic["repo.magento.com"].Username != "pub" {
		t.Fatalf("the new credential was not written: %+v", af.HTTPBasic)
	}
}

// A malformed auth.json is not overwritten: it may be someone's only copy of
// credentials for other projects.
func TestWriteComposerAuthRefusesToClobberInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteComposerAuth(recipe.FamilyCompose, dir, []Value{
		{ID: "repo.magento.com", Kind: recipe.CredComposer, Username: "u", Secret: "p"},
	}); err == nil {
		t.Fatal("expected a refusal rather than an overwrite")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "{not json" {
		t.Fatal("the existing file was modified")
	}
}

func TestWriteComposerAuthSkipsIncompleteValues(t *testing.T) {
	dir := t.TempDir()
	// http-basic needs both halves: a username alone authenticates nothing.
	path, err := WriteComposerAuth(recipe.FamilyCompose, dir, []Value{
		{ID: "repo.magento.com", Kind: recipe.CredComposer, Username: "pub"},
		{ID: "repo.two.com", Kind: recipe.CredComposer, Secret: "  "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Fatalf("nothing complete was supplied, so no file should be written (got %s)", path)
	}
}

func TestRequiredDedupesAndPutsRequiredFirst(t *testing.T) {
	plan := planWith(
		recipe.Recipe{ID: "a", Credentials: []recipe.Credential{
			{ID: "repo.opt.com", Kind: recipe.CredComposer},
			{ID: "repo.magento.com", Kind: recipe.CredComposer, Required: true},
		}},
		recipe.Recipe{ID: "b", Credentials: []recipe.Credential{
			{ID: "repo.magento.com", Kind: recipe.CredComposer, Required: true}, // same one, twice
		}},
	)
	got := Required(plan)
	if len(got) != 2 {
		t.Fatalf("a credential wanted by two recipes should be asked for once: %+v", got)
	}
	if !got[0].Required {
		t.Errorf("required credentials should come first: %+v", got)
	}
}

func TestMissingRequiredOnlyReportsRequired(t *testing.T) {
	plan := planWith(recipe.Recipe{ID: "a", Credentials: []recipe.Credential{
		{ID: "need.me", Kind: recipe.CredComposer, Required: true},
		{ID: "optional.one", Kind: recipe.CredComposer},
	}})
	missing := MissingRequired(plan, nil)
	if len(missing) != 1 || missing[0].ID != "need.me" {
		t.Fatalf("only required credentials block a build: %+v", missing)
	}
	supplied := []Value{{ID: "need.me", Kind: recipe.CredComposer, Username: "u", Secret: "p"}}
	if len(MissingRequired(plan, supplied)) != 0 {
		t.Fatal("a supplied credential should not be reported missing")
	}
}

func TestSuggestionsIncludeCommonAndRecipeNames(t *testing.T) {
	plan := planWith(recipe.Recipe{ID: "a", EnvSuggestions: []string{"MY_VENDOR_TOKEN"}})
	got := strings.Join(Suggestions(plan), ",")
	for _, want := range []string{"GOOGLE_ANALYTICS_ID", "ANTHROPIC_API_KEY", "MY_VENDOR_TOKEN"} {
		if !strings.Contains(got, want) {
			t.Errorf("suggestions missing %s", want)
		}
	}
}

// --- store -------------------------------------------------------------------

func TestStoreRoundTripAndPermissions(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	s.Remember(Value{ID: "repo.magento.com", Kind: recipe.CredComposer, Username: "pub", Secret: "priv", Remember: true})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(Path())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("the credential file must not be readable by others: mode %o", perm)
	}
	again, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	v, ok := again.Get("repo.magento.com")
	if !ok || v.Username != "pub" || v.Secret != "priv" {
		t.Fatalf("value did not round-trip: %+v", v)
	}
}

// Declining to remember also removes a previously saved copy, so "stop saving
// this" does not leave the old secret sitting on disk.
func TestStoreForgetsWhenRememberIsDeclined(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	s, _ := Load()
	s.Remember(Value{ID: "repo.magento.com", Kind: recipe.CredComposer, Username: "u", Secret: "p", Remember: true})
	s.Remember(Value{ID: "repo.magento.com", Kind: recipe.CredComposer, Username: "u", Secret: "p", Remember: false})
	if _, ok := s.Get("repo.magento.com"); ok {
		t.Fatal("declining to remember should drop the stored copy")
	}
}

func TestStoreFillUsesRememberedValues(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	s, _ := Load()
	s.Remember(Value{ID: "repo.magento.com", Kind: recipe.CredComposer, Username: "pub", Secret: "priv", Remember: true})

	filled := s.Fill([]Value{
		{ID: "repo.magento.com", Kind: recipe.CredComposer},
		{ID: "repo.unknown.com", Kind: recipe.CredComposer},
	})
	if !filled[0].Filled() {
		t.Error("a remembered credential should not be asked for again")
	}
	if filled[1].Filled() {
		t.Error("an unknown credential should stay empty for the user to supply")
	}
}

func TestStoreMissingFileIsEmptyNotAnError(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	s, err := Load()
	if err != nil {
		t.Fatalf("no saved credentials is the normal case, not an error: %v", err)
	}
	if len(s.Values) != 0 {
		t.Fatal("a fresh store should be empty")
	}
}
