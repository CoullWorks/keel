package studio

import (
	"embed"
	"io/fs"
	"os"
)

// webDistFS holds the built React studio (Vite output). It is embedded so keel
// ships as one binary. The build must exist at compile time; `pnpm build` in
// internal/studio/web writes it. See internal/studio/web/README for the flow.
//
//go:embed all:web/dist
var webDistFS embed.FS

// reactEnabled reports whether to serve the React studio (now the DEFAULT) rather
// than the legacy embedded index.html. The React port reached parity, so it ships
// by default. The legacy studio is kept one release as an escape hatch rather than
// deleted outright — KEEL_STUDIO_LEGACY=1 (or the old opt-out KEEL_STUDIO_REACT=0)
// selects it — so a regression has a fallback, and serving also falls back to it
// automatically when the React build is absent (see reactDist).
func reactEnabled() bool { return !legacyForced() }

// legacyForced reports whether the user asked for the legacy studio explicitly:
// KEEL_STUDIO_LEGACY set truthy, or the pre-cutover KEEL_STUDIO_REACT=0/false
// opt-out, which keeps working so anyone scripting it is not surprised.
func legacyForced() bool {
	if v := os.Getenv("KEEL_STUDIO_LEGACY"); v != "" && v != "0" && v != "false" {
		return true
	}
	switch os.Getenv("KEEL_STUDIO_REACT") {
	case "0", "false":
		return true
	}
	return false
}

// reactDist returns the built SPA as an fs.FS rooted at web/dist, and false if
// the build is absent (a checkout that never ran `pnpm build`) so serving falls
// back to the legacy index.html rather than 404-ing the whole UI.
func reactDist() (fs.FS, bool) {
	sub, err := fs.Sub(webDistFS, "web/dist")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, false
	}
	return sub, true
}
