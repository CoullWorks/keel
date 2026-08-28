package studio

// installed.go backs the Manage-services tab's EXISTING-services list — the
// "I can't see existing services to remove them" fix. The Add tab was add-only;
// this endpoint gives it the other half: the recipes THIS project already has,
// read from the real manifest (.keel/manifest.yaml recipes:), each resolved to a
// label + kind so the UI can list them with a Remove control.
//
// Removal itself already exists: the UI wires each Remove to `keel remove
// <recipe> --yes` through the studio's exec/stream path, which drops the recipe
// from the manifest (it does not delete files — that stays the framework's call).
// This endpoint only supplies the list; it never mutates. It works for any
// framework because it reads the manifest, not a per-framework guess.

import (
	"net/http"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/recipe"
)

// handleInstalled is GET /api/installed?dir= — the Manage-services tab's list of
// recipes this project currently has, so each can be shown with a Remove button.
// It is a read that names a project by ?dir=, so it is a guarded GET. A project
// with no manifest is a normal answer (200 + error field the UI shows), not a
// 500, matching handleAddable's shape.
//
// The framework and env recipes are surfaced but flagged removable:false — they
// define the project (keel remove refuses them), so the UI shows them for context
// without offering a Remove that would fail.
func handleInstalled(w http.ResponseWriter, r *http.Request) {
	dir, err := projectDir(r.URL.Query().Get("dir"))
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error(), "installed": []installedDTO{}})
		return
	}
	m, err := engine.ReadManifest(dir)
	if err != nil {
		writeJSON(w, map[string]any{"error": "not a keel project", "installed": []installedDTO{}})
		return
	}
	reg, err := catalog.Registry()
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error(), "installed": []installedDTO{}})
		return
	}

	out := []installedDTO{}
	seen := map[string]bool{}
	for _, id := range m.Recipes {
		canon := id
		var rc = installedDTO{ID: id, Label: id, Removable: true}
		if r, ok := reg.Get(id); ok {
			canon = r.ID
			rc = installedDTO{
				ID:        r.ID,
				Kind:      string(r.Kind),
				Label:     firstNonEmpty(r.Label, r.ID),
				Source:    firstNonEmpty(r.Source, "builtin"),
				Removable: isRemovable(r),
			}
		}
		if seen[canon] {
			continue
		}
		seen[canon] = true
		out = append(out, rc)
	}
	writeJSON(w, map[string]any{"framework": m.Framework, "env": m.Env, "installed": out})
}

// installedDTO is one installed recipe row the Manage-services tab draws: its id,
// kind, label, source, and whether it can be removed. Removable is false for the
// framework and env recipes — `keel remove` refuses those (they define the
// project), so the UI shows them without a Remove that would only error.
type installedDTO struct {
	ID        string `json:"id"`
	Kind      string `json:"kind,omitempty"`
	Label     string `json:"label"`
	Source    string `json:"source,omitempty"`
	Removable bool   `json:"removable"`
}

// isRemovable reports whether a recipe can be dropped from the manifest. The
// framework and env define the project — `keel remove` refuses them — so they are
// shown for context but not offered a Remove.
func isRemovable(r recipe.Recipe) bool {
	return r.Kind != recipe.Framework && r.Kind != recipe.Env
}
