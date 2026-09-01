// Package safepath confines a path built from recipe data or a request to a base
// directory. keel writes files whose relative paths come from recipe data (a
// pack's files: entries, a patch's file:) and reads paths named in studio
// requests; a name that climbs out with ".." or is absolute must never reach an
// os file call, or an untrusted pack could write outside the project it is
// scaffolding.
package safepath

import (
	"fmt"
	"path/filepath"
)

// Join returns base joined with rel, but only when rel stays inside base.
//
// filepath.IsLocal reports whether rel is a local path: not absolute, not rooted
// (no leading slash or Windows volume), and not escaping its directory with "..".
// That is exactly the guarantee a recipe file path needs, and it is a barrier the
// path-traversal analysis recognizes, so a write through Join is provably
// confined to base.
func Join(base, rel string) (string, error) {
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("refusing path %q: it escapes the project directory %q", rel, base)
	}
	return filepath.Join(base, rel), nil
}
