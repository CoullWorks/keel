package brand

import (
	"io/fs"
	"path/filepath"
	"testing"
)

// countingWalk wraps the walkDir seam to count how many tree walks a call makes,
// restoring the real function on cleanup.
func countingWalk(t *testing.T) *int {
	t.Helper()
	n := 0
	orig := walkDir
	walkDir = func(root string, fn fs.WalkDirFunc) error {
		n++
		return orig(root, fn)
	}
	t.Cleanup(func() { walkDir = orig })
	return &n
}

// Detect and ApplyTokens each used to re-walk the tree once per stack probe
// (Tailwind then Bootstrap), so a single operation walked 2-4 times. Both now go
// through locate, which walks once. This pins that: one call, one walk.
func TestDetectWalksTreeOnce(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/app.css", `@import "tailwindcss";`)

	n := countingWalk(t)
	if _, err := Detect(dir); err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if *n != 1 {
		t.Errorf("Detect walked the tree %d times, want 1", *n)
	}
}

func TestApplyTokensWalksTreeOnce(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/app.css", `@import "tailwindcss";`)
	tokens, err := Generate("#5b21b6", "")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	n := countingWalk(t)
	if _, err := ApplyTokens(dir, tokens); err != nil {
		t.Fatalf("ApplyTokens: %v", err)
	}
	if *n != 1 {
		t.Errorf("ApplyTokens walked the tree %d times, want 1", *n)
	}
}

// locate honours the fixed precedence in one pass: a project that has BOTH a
// Tailwind v4 entry and a Bootstrap Sass entry resolves to Tailwind v4, and the
// single walk finds the v4 file.
func TestLocatePrecedenceInOneWalk(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "styles/main.scss", `@import "bootstrap";`)
	writeFile(t, dir, "styles/app.css", `@import "tailwindcss";`)

	n := countingWalk(t)
	loc := locate(dir)
	if *n != 1 {
		t.Errorf("locate walked the tree %d times, want 1", *n)
	}
	if loc.stack != stackTailwindV4 {
		t.Fatalf("locate stack = %v, want stackTailwindV4", loc.stack)
	}
	if filepath.Base(loc.file) != "app.css" {
		t.Errorf("locate file = %q, want the app.css v4 entry", loc.file)
	}
}

// locate ignores keel's own .keel tracking snapshot. A keel project keeps a byte
// copy of its source under .keel/base/, so the live app/globals.css and a
// .keel/base/app/globals.css both look like Tailwind v4 entries. Because .keel
// sorts before app, the walk used to pick the snapshot and the brand landed
// there, never rendering. locate must resolve to the LIVE file.
func TestLocateSkipsKeelTrackingDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".keel/base/app/globals.css", `@import "tailwindcss";`)
	writeFile(t, dir, "app/globals.css", `@import "tailwindcss";`)

	loc := locate(dir)
	if loc.stack != stackTailwindV4 {
		t.Fatalf("locate stack = %v, want stackTailwindV4", loc.stack)
	}
	rel, _ := filepath.Rel(dir, loc.file)
	if rel != filepath.Join("app", "globals.css") {
		t.Errorf("locate picked %q, want the live app/globals.css (not the .keel snapshot)", rel)
	}
}

// A Tailwind v3 config wins over Bootstrap and needs no file match from the walk
// (it is a fixed filename), so locate still returns after a single walk.
func TestLocateTailwindV3(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tailwind.config.js", "module.exports = {}")
	writeFile(t, dir, "styles/main.scss", `@import "bootstrap";`)

	loc := locate(dir)
	if loc.stack != stackTailwindV3 {
		t.Fatalf("locate stack = %v, want stackTailwindV3", loc.stack)
	}
}
