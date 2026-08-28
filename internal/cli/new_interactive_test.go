package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/coullworks/keel/internal/brand"
	"github.com/coullworks/keel/internal/profile"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/tui"
)

// stubSelectRecipes swaps the interactive recipe picker for one returning canned
// ids, restoring the real picker afterwards. The raw result is empty, so no
// brand colour is applied — use stubSelectRecipesWith to exercise the brand path.
func stubSelectRecipes(t *testing.T, ids []string, err error) {
	t.Helper()
	stubSelectRecipesWith(t, ids, nil, err)
}

// stubSelectRecipesWith is stubSelectRecipes with a canned raw wizard result, so
// a test can feed the brand-colour answer that ids alone doesn't carry.
func stubSelectRecipesWith(t *testing.T, ids []string, res [][]string, err error) {
	t.Helper()
	old := selectRecipes
	selectRecipes = func(_ *recipe.Registry, _ *profile.Profile) ([]string, [][]string, error) {
		return ids, res, err
	}
	t.Cleanup(func() { selectRecipes = old })
}

// `keel new` with no framework arg is the interactive path. With the picker
// stubbed and --yes (skips the directory prompt) + --dry-run (runs nothing), the
// whole interactive build path runs offline.
func TestNewInteractiveDryRun(t *testing.T) {
	isolate(t)
	stubSelectRecipes(t, []string{"laravel", "sail"}, nil)
	out, err := runRoot(t, "new", "--dry-run", "--yes")
	if err != nil {
		t.Fatalf("interactive new --dry-run --yes: %v", err)
	}
	mustContain(t, out, "laravel")
	// dry-run must create nothing.
	if _, err := os.Stat("laravel-app"); err == nil {
		t.Error("dry-run created laravel-app")
	}
}

// The picker returning an error aborts before any build work.
func TestNewInteractivePickerError(t *testing.T) {
	isolate(t)
	stubSelectRecipes(t, nil, os.ErrPermission)
	if err := runNewInteractive(context.Background(), io.Discard, true, true, false); err == nil {
		t.Fatal("expected the picker error to propagate")
	}
}

// A full interactive build (not dry-run) of a trivial offline pack: the picker
// returns the pack id, --yes skips both the dir prompt and the confirm, --trust
// skips the untrusted-consent prompt, so build() runs end-to-end with no TTY.
func TestNewInteractiveBuildsTrivialPack(t *testing.T) {
	wd := isolate(t)
	src := filepath.Join(t.TempDir(), "echopack")
	if err := scaffoldPack(io.Discard, src, "echopack"); err != nil {
		t.Fatalf("scaffoldPack: %v", err)
	}
	if _, err := runRoot(t, "recipes", "add", src); err != nil {
		t.Fatalf("recipes add: %v", err)
	}
	stubSelectRecipes(t, []string{"echopack"}, nil)
	// runNewInteractive builds into <framework>-app since --yes skips the dir
	// input; run it directly so we control cwd for the assertion.
	if err := runNewInteractive(context.Background(), io.Discard, false, true, true); err != nil {
		t.Fatalf("interactive build: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wd, "echopack-app", "NOTES-echopack.md")); err != nil {
		t.Errorf("recipe file not written: %v", err)
	}
}

// A chosen brand colour is applied to the built project after a successful
// build. The recipe picker is stubbed with a raw result carrying the brand
// token, and the brand-apply seam records the call, so no real CSS is painted —
// this covers the new.go wiring (BrandFromResult non-empty → tui.ApplyBrand).
func TestNewInteractiveAppliesBrand(t *testing.T) {
	wd := isolate(t)
	src := filepath.Join(t.TempDir(), "echopack")
	if err := scaffoldPack(io.Discard, src, "echopack"); err != nil {
		t.Fatalf("scaffoldPack: %v", err)
	}
	if _, err := runRoot(t, "recipes", "add", src); err != nil {
		t.Fatalf("recipes add: %v", err)
	}

	var gotDir, gotPrimary, gotAccent string
	called := false
	restore := tui.SetBrandApply(func(dir, primary, accent string) (brand.Result, error) {
		called, gotDir, gotPrimary, gotAccent = true, dir, primary, accent
		return brand.Result{}, nil
	})
	defer restore()

	// Raw wizard result with the brand token that ids alone can't carry;
	// "brand:<primary>,<accent>" is the token format documented in tui/brand.go.
	res := [][]string{{"node"}, {"echopack"}, {"echopack"}, {"brand:#4f46e5,#06b6d4"}}
	stubSelectRecipesWith(t, []string{"echopack"}, res, nil)

	if err := runNewInteractive(context.Background(), io.Discard, false, true, true); err != nil {
		t.Fatalf("interactive build: %v", err)
	}

	if !called {
		t.Fatal("a chosen brand colour must apply after the build")
	}
	// build applies to the same dir it built into ("<framework>-app" when --yes
	// skips the dir prompt), and that dir must hold the real project.
	if gotDir != "echopack-app" {
		t.Errorf("brand applied to %q, want the built dir %q", gotDir, "echopack-app")
	}
	if _, err := os.Stat(filepath.Join(wd, gotDir, "NOTES-echopack.md")); err != nil {
		t.Errorf("brand applied to a dir with no built project: %v", err)
	}
	if gotPrimary != "#4f46e5" || gotAccent != "#06b6d4" {
		t.Errorf("brand colours = %q/%q, want #4f46e5/#06b6d4", gotPrimary, gotAccent)
	}
}

// Bug A: the interactive `keel new` must not build a stack with no ingress. The
// picker returns a laravel plan with a web server absent (only the framework +
// env), and the safety net must re-add the framework's default (NGINX) so the
// build is reachable. res is nil here, so DeclinedWebServer is false — the user
// never chose "None", which is the case that used to slip through: the flag path
// had the guard, runNewInteractive did not.
func TestNewInteractiveReAddsWebServerWhenPlanHasNone(t *testing.T) {
	isolate(t)
	stubSelectRecipes(t, []string{"laravel", "sail"}, nil)
	out, err := runRoot(t, "new", "--dry-run", "--yes")
	if err != nil {
		t.Fatalf("interactive new --dry-run --yes: %v\n%s", err, out)
	}
	mustContain(t, out, "NGINX")
}

// Bug A, the other half: an explicit "None" on the web server step is honoured —
// re-adding a web server behind the user's back would be as wrong as leaving a
// stale-profile stack unreachable. res carries the profile.NoWebServer decline at
// the web server step (index 4 in BuildSteps: language, framework, type, env, WEB
// SERVER, …). The sentinel is profile.NoWebServer, unified with `keel init`: a
// bare "" here now means "no answer" and the default is re-added, so the decline
// must carry the real sentinel to be honoured.
func TestNewInteractiveHonoursExplicitWebServerDecline(t *testing.T) {
	isolate(t)
	// The wizard result for a laravel plan where the user picked "None (I'll
	// front it myself)"; only index 4 (web server) is load-bearing for the guard.
	res := [][]string{
		{"php"},               // language
		{"laravel"},           // framework family
		{"laravel"},           // framework type
		{"sail"},              // env
		{profile.NoWebServer}, // web server: explicit decline
		{"mysql"},             // db
	}
	stubSelectRecipesWith(t, []string{"laravel", "sail"}, res, nil)
	out, err := runRoot(t, "new", "--dry-run", "--yes")
	if err != nil {
		t.Fatalf("interactive new --dry-run --yes: %v\n%s", err, out)
	}
	mustNotContain(t, out, "NGINX", "Apache")
}

// A dry-run builds nothing, so it must not apply a brand even when one was
// chosen: no project on disk means no CSS to paint.
func TestNewInteractiveDryRunSkipsBrand(t *testing.T) {
	isolate(t)
	called := false
	restore := tui.SetBrandApply(func(dir, primary, accent string) (brand.Result, error) {
		called = true
		return brand.Result{}, nil
	})
	defer restore()

	res := [][]string{{"laravel"}, {"laravel"}, {"laravel"}, {"brand:#4f46e5,#06b6d4"}}
	stubSelectRecipesWith(t, []string{"laravel", "sail"}, res, nil)
	if _, err := runRoot(t, "new", "--dry-run", "--yes"); err != nil {
		t.Fatalf("interactive new --dry-run --yes: %v", err)
	}
	if called {
		t.Fatal("dry-run must not apply a brand — nothing was built")
	}
}
