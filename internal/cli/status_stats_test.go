package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// mkFile writes a file (and its parents) with trivial content, for stat fixtures.
func mkFile(t *testing.T, dir string, parts ...string) {
	t.Helper()
	p := filepath.Join(append([]string{dir}, parts...)...)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestFrameworkStats exercises every framework's cheap stat collector against a
// tiny fixture tree, so the per-framework branches and filename predicates are
// covered and the counts are honest.
func TestFrameworkStats(t *testing.T) {
	t.Run("django", func(t *testing.T) {
		dir := t.TempDir()
		mkFile(t, dir, "app", "migrations", "0001_initial.py")
		mkFile(t, dir, "app", "migrations", "__init__.py") // must NOT count
		if err := os.WriteFile(filepath.Join(dir, "app", "urls.py"),
			[]byte("urlpatterns = [path('a', v), re_path(r'^b$', w)]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		facts := frameworkStats(dir, "django")
		if got := statValue(facts, "migrations"); got != "1" {
			t.Errorf("django migrations = %q, want 1 (init.py excluded)", got)
		}
		if got := statValue(facts, "routes"); got != "2" {
			t.Errorf("django routes = %q, want 2", got)
		}
	})

	t.Run("fastapi", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "main.py"),
			[]byte("@app.get('/a')\n@router.post('/b')\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := statValue(frameworkStats(dir, "fastapi"), "endpoints"); got != "2" {
			t.Errorf("fastapi endpoints = %q, want 2", got)
		}
	})

	t.Run("next app router", func(t *testing.T) {
		dir := t.TempDir()
		mkFile(t, dir, "app", "page.tsx")
		mkFile(t, dir, "app", "blog", "route.ts")
		if got := statValue(frameworkStats(dir, "nextjs"), "routes"); got != "2" {
			t.Errorf("next routes = %q, want 2", got)
		}
	})

	t.Run("next pages router", func(t *testing.T) {
		dir := t.TempDir()
		mkFile(t, dir, "pages", "index.tsx")
		mkFile(t, dir, "pages", "about.jsx")
		if got := statValue(frameworkStats(dir, "next"), "routes"); got != "2" {
			t.Errorf("next pages routes = %q, want 2", got)
		}
	})

	t.Run("magento modules", func(t *testing.T) {
		dir := t.TempDir()
		mkFile(t, dir, "app", "code", "Vendor", "Mod", "etc", "module.xml")
		if got := statValue(frameworkStats(dir, "magento"), "modules"); got != "1" {
			t.Errorf("magento modules = %q, want 1", got)
		}
	})

	t.Run("unknown framework has no stats", func(t *testing.T) {
		if facts := frameworkStats(t.TempDir(), "cobol"); facts != nil {
			t.Errorf("an unknown framework should have no stats, got %v", facts)
		}
	})
}

// statValue returns the value of the named stat, or "" if absent.
func statValue(facts []statFact, label string) string {
	for _, f := range facts {
		if f.label == label {
			return f.value
		}
	}
	return ""
}

// TestCountMatchesSkipsHeavyDirs: a match inside node_modules/vendor is not
// counted, so the walk stays cheap and honest.
func TestCountMatchesSkipsHeavyDirs(t *testing.T) {
	dir := t.TempDir()
	mkFileWith(t, filepath.Join(dir, "src", "app.py"), "path( a\n")
	mkFileWith(t, filepath.Join(dir, "node_modules", "pkg", "x.js"), "path( ignored\n")
	if n := countMatchesInDir(dir, "path("); n != 1 {
		t.Errorf("countMatchesInDir = %d, want 1 (node_modules pruned)", n)
	}
}

func mkFileWith(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
