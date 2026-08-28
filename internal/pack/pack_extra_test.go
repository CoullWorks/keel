package pack

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- pack.go path helpers -------------------------------------------------

func TestPathHelpers(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("KEEL_CONFIG_DIR", cfg)

	wantRecipes := filepath.Join(cfg, "recipes")
	if got := RecipesDir(); got != wantRecipes {
		t.Errorf("RecipesDir() = %q, want %q", got, wantRecipes)
	}
	wantDir := filepath.Join(cfg, "recipes", "acme")
	if got := Dir("acme"); got != wantDir {
		t.Errorf("Dir(acme) = %q, want %q", got, wantDir)
	}
}

// --- Load error / empty branches -----------------------------------------

func TestLoadMissingIsEmpty(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir()) // no packs.yaml
	r, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(r.Packs) != 0 {
		t.Fatalf("expected empty registry, got %d packs", len(r.Packs))
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("KEEL_CONFIG_DIR", cfg)
	if err := os.WriteFile(filepath.Join(cfg, "packs.yaml"), []byte("packs: [oops\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("expected Load to error on invalid YAML")
	}
}

func TestLoadReadError(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("KEEL_CONFIG_DIR", cfg)
	// A directory named packs.yaml makes os.ReadFile fail with a non-NotExist error.
	if err := os.Mkdir(filepath.Join(cfg, "packs.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("expected Load to error when packs.yaml is a directory")
	}
}

// --- Save error branch ----------------------------------------------------

func TestSaveMkdirError(t *testing.T) {
	// Point the config dir at a path whose parent is a regular file, so
	// os.MkdirAll fails.
	cfg := t.TempDir()
	blocker := filepath.Join(cfg, "afile")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KEEL_CONFIG_DIR", filepath.Join(blocker, "keel"))
	r := &Registry{}
	if err := r.Save(); err == nil {
		t.Fatal("expected Save to error when the config dir can't be created")
	}
}

// --- Get miss -------------------------------------------------------------

func TestGetMiss(t *testing.T) {
	r := &Registry{Packs: []Installed{{Name: "one"}}}
	if _, ok := r.Get("missing"); ok {
		t.Fatal("Get should miss for an unknown pack")
	}
}

// --- Remove miss ----------------------------------------------------------

func TestRemoveMiss(t *testing.T) {
	r := &Registry{Packs: []Installed{{Name: "one"}}}
	if r.Remove("missing") {
		t.Fatal("Remove should report false for an unknown pack")
	}
	if len(r.Packs) != 1 {
		t.Fatal("Remove of a missing pack must not mutate the slice")
	}
}

// --- ReadManifest ---------------------------------------------------------

func TestReadManifest(t *testing.T) {
	dir := t.TempDir()
	body := "schema_version: 1\nname: acme\nversion: 1.2.3\nkeel_version_constraint: \">= 0.1.0\"\nauthor: Dan\ndescription: A pack\nrecipes: [a, b]\n"
	if err := os.WriteFile(filepath.Join(dir, "keel.pack.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if m.Name != "acme" || m.Version != "1.2.3" || m.SchemaVersion != 1 {
		t.Errorf("manifest fields wrong: %+v", m)
	}
	if m.KeelVersion != ">= 0.1.0" || m.Author != "Dan" {
		t.Errorf("manifest constraint/author wrong: %+v", m)
	}
	if len(m.Recipes) != 2 || m.Recipes[0] != "a" {
		t.Errorf("manifest recipes wrong: %+v", m.Recipes)
	}
}

func TestReadManifestMissing(t *testing.T) {
	if _, err := ReadManifest(t.TempDir()); err == nil {
		t.Fatal("expected ReadManifest to error when keel.pack.yaml is absent")
	}
}

func TestReadManifestInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keel.pack.yaml"), []byte("name: [oops\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(dir); err == nil {
		t.Fatal("expected ReadManifest to error on invalid YAML")
	}
}

// --- SatisfiesKeel error + parseSemver edge cases -------------------------

func TestSatisfiesKeelUnsupportedConstraint(t *testing.T) {
	cases := []string{"~> 1.0.0", "1.0.0", "< 2.0.0", "^1.2.3"}
	for _, c := range cases {
		if _, err := SatisfiesKeel(c, "1.0.0"); err == nil {
			t.Errorf("SatisfiesKeel(%q,...) expected an error", c)
		}
	}
}

func TestSatisfiesKeelWhitespaceAndVPrefix(t *testing.T) {
	// Leading/trailing whitespace, a "v" prefix, and short/over-long version
	// strings all exercise parseSemver's trimming and 3-component cap.
	cases := []struct {
		c, v string
		want bool
	}{
		{"  >= v1.2.0  ", "v1.2.3", true},
		{">= 1.2.0", "1.2", true},        // 1.2 -> [1 2 0], equal to [1 2 0]
		{">= 1.2.1", "1.2", false},       // 1.2 -> [1 2 0] < [1 2 1]
		{">= 1.0", "1.0.0.0", true},      // over-long: 4th component ignored
		{">= 2.0.0", "1.9.99", false},    // major dominates
		{">= 1.5.0", "1.10.0", true},     // numeric, not lexical, compare on minor
		{">= 1.0.0", "1.0.0-rc.1", true}, // pre-release stripped
	}
	for _, tc := range cases {
		got, err := SatisfiesKeel(tc.c, tc.v)
		if err != nil {
			t.Fatalf("SatisfiesKeel(%q,%q): %v", tc.c, tc.v, err)
		}
		if got != tc.want {
			t.Errorf("SatisfiesKeel(%q,%q)=%v want %v", tc.c, tc.v, got, tc.want)
		}
	}
}

// --- ResolveSource --------------------------------------------------------

func TestResolveSource(t *testing.T) {
	localDir := t.TempDir()
	cases := []struct {
		in, want string
	}{
		{"https://github.com/acme/pack", "https://github.com/acme/pack"},
		{"http://example.com/p.git", "http://example.com/p.git"},
		{"git@github.com:acme/pack.git", "git@github.com:acme/pack.git"},
		{localDir, localDir}, // existing local dir, unchanged
		{"not-a-repo-shorthand", "not-a-repo-shorthand"},
	}
	for _, tc := range cases {
		if got := ResolveSource(tc.in); got != tc.want {
			t.Errorf("ResolveSource(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestResolveSourceShorthand covers the owner/repo -> GitHub URL branch. A bare
// two-segment word/word (no scheme, no leading . or /) is the GitHub shorthand
// the `keel recipes add` / `keel plugins add` help advertises, and it must
// expand to the GitHub clone URL on every platform. (This used to be dead code:
// the old guard rejected anything containing os.PathSeparator, which on Linux is
// '/', so every owner/repo failed and the shorthand never fired.)
func TestResolveSourceShorthand(t *testing.T) {
	cases := map[string]string{
		"acme-owner/acme-repo":    "https://github.com/acme-owner/acme-repo",
		"coullworks/keel-recipes": "https://github.com/coullworks/keel-recipes",
		"a.b/c_d-e":               "https://github.com/a.b/c_d-e",
	}
	for in, want := range cases {
		if got := ResolveSource(in); got != want {
			t.Errorf("ResolveSource(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveSourceNotShorthand(t *testing.T) {
	// A leading "./" (or "/") marks a local path, which must never be mistaken for
	// owner/repo; a deeper path (3+ segments) and an existing local dir are left
	// alone too. These stay verbatim so the shorthand cannot swallow a real path.
	existing := t.TempDir()
	verbatim := []string{
		"./a/b",                // explicit relative path
		"/abs/a/b",             // absolute path
		"a/b/c",                // 3 segments, not owner/repo
		".hidden/x",            // leading dot segment
		"not-a-repo-shorthand", // single segment
		existing,               // an existing local dir
	}
	for _, in := range verbatim {
		if got := ResolveSource(in); got != in {
			t.Errorf("ResolveSource(%q) = %q, want unchanged", in, got)
		}
	}
}

// --- Fetch (local dir path only; no network / git) ------------------------

func TestFetchLocalDir(t *testing.T) {
	src := t.TempDir()
	// Populate the source with a manifest, a nested file, and a .git dir that
	// must be skipped by the copy.
	writePackFile(t, filepath.Join(src, "keel.pack.yaml"), "name: acme\n")
	writePackFile(t, filepath.Join(src, "sub", "recipe.yaml"), "id: x\nkind: addon\n")
	writePackFile(t, filepath.Join(src, ".git", "config"), "[core]\n")

	dir, commit, err := Fetch(context.Background(), src, "")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer os.RemoveAll(dir)

	if commit != "" {
		t.Errorf("local-dir Fetch commit = %q, want empty", commit)
	}
	if dir == src {
		t.Error("Fetch should copy into a fresh temp dir, not return the source")
	}
	if _, err := os.Stat(filepath.Join(dir, "keel.pack.yaml")); err != nil {
		t.Errorf("manifest not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub", "recipe.yaml")); err != nil {
		t.Errorf("nested recipe not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
		t.Error(".git should have been skipped by copyDir")
	}
}

func TestFetchRejectsFileTransport(t *testing.T) {
	// file:// is a dangerous git transport (it lets a pack source read a local
	// repo/path); Fetch must refuse it rather than clone. A real local repo is
	// still installable — as a directory path via the copyDir branch, not file://.
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	writePackFile(t, filepath.Join(repo, "keel.pack.yaml"), "name: acme\n")
	run("add", ".")
	run("commit", "-q", "-m", "init")

	// The repo directory itself is a legitimate source (copyDir branch).
	dir, _, err := Fetch(context.Background(), repo, "")
	if err != nil {
		t.Fatalf("Fetch(local dir): %v", err)
	}
	defer os.RemoveAll(dir)
	if _, err := os.Stat(filepath.Join(dir, "keel.pack.yaml")); err != nil {
		t.Errorf("copied manifest missing: %v", err)
	}

	// But cloning it over file:// must be refused.
	if _, _, err := Fetch(context.Background(), "file://"+repo, "main"); err == nil {
		t.Fatal("Fetch should refuse a file:// git transport")
	}
}

func TestValidateGitSource(t *testing.T) {
	cases := []struct {
		src   string
		valid bool
	}{
		{"https://github.com/owner/repo.git", true},
		{"ssh://git@github.com/owner/repo.git", true},
		{"git@github.com:owner/repo.git", true},
		{"ext::sh -c touch /tmp/pwned", false},
		{"ext::sh -c 'id'", false},
		{"file:///etc/passwd", false},
		{"file://" + t.TempDir(), false},
		{"fd::7", false},
		{"http://github.com/owner/repo", false},  // http not in the allowlist
		{"git@evil.com:x::ext::sh -c id", false}, // scp-form smuggling a helper
	}
	for _, tc := range cases {
		err := validateGitSource(tc.src)
		if tc.valid && err != nil {
			t.Errorf("validateGitSource(%q) = %v, want allowed", tc.src, err)
		}
		if !tc.valid && err == nil {
			t.Errorf("validateGitSource(%q) = nil, want rejected", tc.src)
		}
	}
}

func TestFetchRejectsExtTransport(t *testing.T) {
	// ext::sh -c <cmd> is the classic git-clone RCE. Fetch must refuse it before
	// any git process runs. The sentinel file must NOT be created.
	sentinel := filepath.Join(t.TempDir(), "pwned")
	src := "ext::sh -c touch " + sentinel
	if _, _, err := Fetch(context.Background(), src, ""); err == nil {
		t.Fatal("Fetch should refuse an ext:: git transport")
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatal("ext:: transport executed — sentinel file was created (RCE)")
	}
}

func TestFetchLocalCopyError(t *testing.T) {
	// A source dir with an unreadable file makes copyDir fail inside the local-dir
	// branch of Fetch, triggering its cleanup + error return.
	src := t.TempDir()
	secret := filepath.Join(src, "secret.yaml")
	if err := os.WriteFile(secret, []byte("id: x\nkind: addon\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(secret, 0o644) })
	if _, _, err := Fetch(context.Background(), src, ""); err == nil {
		t.Fatal("expected Fetch to error when a source file can't be read")
	}
}

func TestFetchCloneFails(t *testing.T) {
	// An allowed transport (https) to a guaranteed-non-resolvable host (.invalid,
	// RFC 6761) passes validation and reaches `git clone`, which must fail fast
	// with prompting disabled — no network, no tty hang. A short context bounds it.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _, err := Fetch(ctx, "https://nonexistent.invalid/owner/repo.git", "")
	if err == nil {
		t.Fatal("expected Fetch to error when git clone fails")
	}
	if !strings.Contains(err.Error(), "git clone") {
		t.Errorf("error should mention git clone, got: %v", err)
	}
}

// --- Move + copyDir/copyFile ---------------------------------------------

func TestMoveRename(t *testing.T) {
	from := t.TempDir()
	writePackFile(t, filepath.Join(from, "recipe.yaml"), "id: x\nkind: addon\n")
	to := filepath.Join(t.TempDir(), "dest")

	if err := Move(from, to); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if _, err := os.Stat(filepath.Join(to, "recipe.yaml")); err != nil {
		t.Errorf("moved content missing: %v", err)
	}
	// The source should be gone after a rename.
	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Error("source should not remain after Move")
	}
}

func TestMoveOverwritesExistingDest(t *testing.T) {
	from := t.TempDir()
	writePackFile(t, filepath.Join(from, "new.yaml"), "id: new\nkind: addon\n")
	to := t.TempDir() // already exists, with stale content
	writePackFile(t, filepath.Join(to, "stale.yaml"), "id: stale\nkind: addon\n")

	if err := Move(from, to); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if _, err := os.Stat(filepath.Join(to, "new.yaml")); err != nil {
		t.Errorf("new content missing after Move: %v", err)
	}
	if _, err := os.Stat(filepath.Join(to, "stale.yaml")); !os.IsNotExist(err) {
		t.Error("stale dest content should have been removed before Move")
	}
}

func TestMoveRemoveAllError(t *testing.T) {
	from := t.TempDir()
	// `to` is a path under a regular file, so os.RemoveAll(to) itself returns an
	// error (a component of the path is not a directory).
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(blocker, "child", "dest")
	if err := Move(from, to); err == nil {
		t.Fatal("expected Move to error when the destination path is invalid")
	}
}

// writePackFile writes content to path, creating parent dirs.
func writePackFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A source or ref that begins with a dash would be read by git as an option
// rather than a value, so Fetch refuses it outright and passes "--" before the
// positional arguments.
func TestFetchRefusesDashLeadingSourceAndRef(t *testing.T) {
	if _, _, err := Fetch(context.Background(), "--upload-pack=touch pwned", ""); err == nil {
		t.Fatal("a dash-leading source should be refused")
	} else if !strings.Contains(err.Error(), "starts with a dash") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, _, err := Fetch(context.Background(), "https://example.test/x.git", "--upload-pack=touch pwned"); err == nil {
		t.Fatal("a dash-leading ref should be refused")
	} else if !strings.Contains(err.Error(), "starts with a dash") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// A pack must not be able to smuggle files out of the machine by shipping a
// symlink: links are skipped, not followed.
func TestCopyDirSkipsSymlinks(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	secretDir := t.TempDir()
	secret := filepath.Join(secretDir, "id_rsa")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "recipe.yaml"), []byte("id: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(src, "stolen.yaml")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(secretDir, filepath.Join(src, "stolendir")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := copyDir(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "recipe.yaml")); err != nil {
		t.Fatalf("real files should still be copied: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "stolen.yaml")); err == nil {
		t.Fatal("a symlinked file must not be copied into the installed pack")
	}
	if _, err := os.Lstat(filepath.Join(dst, "stolendir", "id_rsa")); err == nil {
		t.Fatal("a symlinked directory must not be walked into the installed pack")
	}
}
