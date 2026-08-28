// Package discover finds keel extension directories anywhere under a set of
// roots by a marker file. It exists so there is exactly ONE bounded home-walk in
// keel — shared by plugin discovery (marker config/register.yaml) and pack
// discovery (marker keel.pack.yaml) — rather than a copy per extension kind that
// could drift in what it skips or how deep it goes. It is a leaf package
// (standard library only), so anything can import it without a cycle.
//
// The walk is deliberately bounded and skips heavy or irrelevant trees. A user's
// home directory holds a whole dev tree, not only keel extensions; an exhaustive
// walk of it on every studio request would be far too slow, so the traversal
// stops past MaxDepth and never descends into dependency, build or module-cache
// directories.
package discover

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// MaxDepth bounds how deep the walk descends below a root. An extension buried
// more than this many levels down under home is not found — the trade is that the
// walk stays fast on a large home directory rather than exhaustive.
const MaxDepth = 8

// Home returns the user's home directory as a single discovery root, or nil if it
// cannot be determined. Cloning an extension anywhere under home is then enough
// for keel to find it — the "clone anywhere, keel finds it" default — with no
// configuration, since home contains wherever the user keeps their code.
func Home() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	return []string{home}
}

// EnvRoots splits a PATH-style environment variable into discovery roots,
// dropping blanks. An unset or empty variable yields nil. It is the escape hatch
// for keeping extensions somewhere home discovery would not reach.
func EnvRoots(name string) []string {
	v := os.Getenv(name)
	if v == "" {
		return nil
	}
	var roots []string
	for _, r := range filepath.SplitList(v) {
		if r = strings.TrimSpace(r); r != "" {
			roots = append(roots, r)
		}
	}
	return roots
}

// Walk returns every directory at or under each root (inclusive) that carries
// markerRel, walking roots in order. It is bounded to MaxDepth, skips
// dependency/build/cache trees (see skipDir and isGoModCache), and does not
// descend into a directory once it matches — an extension's own subdirectories
// are never separate extensions. The caller dedups across roots (the first root
// to yield a given identity should win).
func Walk(markerRel string, roots []string) []string {
	var found []string
	for _, root := range roots {
		walk(markerRel, root, 0, &found)
	}
	return found
}

// HasMarker reports whether dir carries markerRel — the single definition of
// "this directory is an extension of that kind".
func HasMarker(dir, markerRel string) bool {
	_, err := os.Stat(filepath.Join(dir, markerRel))
	return err == nil
}

func walk(marker, dir string, depth int, found *[]string) {
	if depth > MaxDepth {
		return
	}
	if HasMarker(dir, marker) {
		*found = append(*found, dir) // do not descend into a match
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // missing/unreadable dir is fine
	}
	for _, e := range entries {
		if !e.IsDir() || skipDir(e.Name()) {
			continue
		}
		child := filepath.Join(dir, e.Name())
		if isGoModCache(child) {
			continue
		}
		walk(marker, child, depth+1, found)
	}
}

// skipDir reports whether a directory name should never be descended into during
// discovery: dependency and build trees that can be enormous and hold no keel
// extensions, and any dotdir (which covers .git, .cache, .npm, .pnpm-store,
// .config, and the like in one rule).
func skipDir(name string) bool {
	switch name {
	case "node_modules", "vendor", "dist", "build", "target", "__pycache__", "Library":
		return true
	// testdata is Go's reserved directory for fixtures — the go tool ignores it,
	// and so must discovery: a plugin/pack fixture committed under testdata (keel's
	// own tests ship one) is not a real installed extension. Without this, running
	// keel from inside a checkout that lives under $HOME (every CI runner does)
	// would discover the fixture pack and lint/build its intentionally-broken
	// example recipes.
	case "testdata":
		return true
	}
	return strings.HasPrefix(name, ".")
}

// goModCache is the resolved Go module cache path, computed once. The cache is a
// multi-gigabyte tree of extracted modules — each an ordinary directory — so
// walking it is the single biggest perf killer in a home-directory scan.
var goModCache = sync.OnceValue(resolveGoModCache)

// resolveGoModCache finds the Go module cache: `go env GOMODCACHE` if go is on
// PATH, else the ~/go/pkg/mod default. Returns "" when neither is available, in
// which case isGoModCache falls back to the name/parent heuristic alone.
func resolveGoModCache() string {
	if out, err := exec.Command("go", "env", "GOMODCACHE").Output(); err == nil {
		if p := strings.TrimSpace(string(out)); p != "" {
			return filepath.Clean(p)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "go", "pkg", "mod")
	}
	return ""
}

// isGoModCache reports whether dir is the Go module cache and so must be skipped.
// It matches both ways: the resolved GOMODCACHE path exactly, and — as a robust
// fallback that needs no resolution — any directory named "pkg" whose parent is
// named "go" (the ~/go/pkg/mod layout).
func isGoModCache(dir string) bool {
	clean := filepath.Clean(dir)
	if mc := goModCache(); mc != "" && clean == mc {
		return true
	}
	return filepath.Base(clean) == "pkg" && filepath.Base(filepath.Dir(clean)) == "go"
}
