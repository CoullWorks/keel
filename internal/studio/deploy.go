package studio

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// handleDeployTargets lists keel's deploy targets — each with its one-line
// description and, inside a tracked keel project, the artifact filenames it would
// generate — so the studio's Deploy tab can render a real target picker and show
// WHICH files a target writes before the user commits.
//
// It shells the keel binary's own `keel deploy --json` rather than re-deriving
// the list here, exactly as handleRunTasks shells `run --json`: deploy.go's
// deployTargets / deployFiles stay the single source of truth, so the frontend
// can never drift from what `keel deploy` actually does. A read keyed by an
// optional ?dir= (a tracked project, resolved to its framework); with no dir the
// listing still stands, just without per-target artifact filenames.
//
// Loopback-only, GET, token-guarded like every other read.
func handleDeployTargets(w http.ResponseWriter, r *http.Request) {
	dir := "."
	if d := strings.TrimSpace(r.URL.Query().Get("dir")); d != "" {
		abs, err := projectDir(d) // only a tracked project, never an arbitrary path
		if err != nil {
			writeJSON(w, map[string]any{"targets": []any{}, "error": err.Error()})
			return
		}
		// Resolving artifacts needs the manifest; without one keel deploy --json
		// still returns the target list (framework empty), which is a valid answer.
		if _, err := os.Stat(filepath.Join(abs, ".keel", "manifest.yaml")); err == nil {
			dir = abs
		}
	}
	exe, err := os.Executable()
	if err != nil {
		exe = "keel"
	}
	out, err := runCapture(r.Context(), dir, exe, "deploy", "--json")
	if err != nil {
		writeJSON(w, map[string]any{"targets": []any{}, "error": strings.TrimSpace(out) + err.Error()})
		return
	}
	// Pass the binary's JSON straight through: it already carries {framework,
	// targets:[{key,desc,artifacts}]}, the exact shape the tab renders, and keeping
	// it verbatim is what makes deploy.go the one source of truth.
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &parsed); jsonErr != nil {
		writeJSON(w, map[string]any{"targets": []any{}, "error": "could not read the deploy targets"})
		return
	}
	if parsed["targets"] == nil {
		parsed["targets"] = []any{}
	}
	writeJSON(w, parsed)
}
