// This file serves the studio's framework-aware Generate surface and the
// Add-service affordance.
//
// The studio's generate menu used to hardcode Laravel nouns for every framework
// and its "Auth" button called a non-existent `gen auth` verb. Both are fixed by
// reading a project's own framework from its manifest and asking the recipe-driven
// generator catalogue (internal/gen) what that framework actually supports: a Next
// project is offered its components/routes and its auth stack, a Django project
// its apps/models, a Magento project its modules — never the Laravel list
// universally. Auth becomes a `level: stack` generatable that applies via `keel
// add`, not a broken verb. Add-service lists the recipes compatible with the
// project and installs one through the same exec path.
package studio

import (
	"net/http"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/gen"
	"github.com/coullworks/keel/internal/recipe"
)

// genInputDTO is a typed input a generator collects, in the studio's stable
// lowercase JSON shape. A "fields" type tells the UI to render the mage2gen-style
// fields table (name / type dropdown / nullable) rather than a plain text box.
type genInputDTO struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Label    string   `json:"label"`
	Required bool     `json:"required"`
	Choices  []string `json:"choices,omitempty"`
}

// generatableDTO is one thing keel can generate into the focused project, grouped
// in the UI by Level (code-block / module / package / stack). Recipe is set for a
// recipe-driven generator (auth and future stacks) and empty for a built-in
// code-block generator, so the UI knows a stack applies via `keel add`.
type generatableDTO struct {
	Key      string        `json:"key"`
	Label    string        `json:"label"`
	Level    string        `json:"level"`
	Provides []string      `json:"provides,omitempty"`
	Inputs   []genInputDTO `json:"inputs,omitempty"`
	Recipe   string        `json:"recipe,omitempty"`
	// Applies is the addon recipe ids a stack generator installs, in order, so the
	// UI can show "Add Authentication" as "keel add breeze" and stream that add.
	Applies []string `json:"applies,omitempty"`
}

// inputDTOs maps the neutral recipe inputs to the studio's JSON shape.
func inputDTOs(in []recipe.GenInput) []genInputDTO {
	out := make([]genInputDTO, 0, len(in))
	for _, i := range in {
		out = append(out, genInputDTO{Name: i.Name, Type: i.Type, Label: i.Label, Required: i.Required, Choices: i.Choices})
	}
	return out
}

// toGeneratableDTO turns a gen.Generatable into its JSON form, resolving a stack
// generator to the recipe ids it applies so the UI can install them.
func toGeneratableDTO(reg *recipe.Registry, g gen.Generatable) generatableDTO {
	d := generatableDTO{
		Key:      g.Key,
		Label:    g.Label,
		Level:    string(g.Level),
		Provides: g.Provides,
		Inputs:   inputDTOs(g.Inputs),
		Recipe:   g.Recipe,
	}
	if g.Recipe != "" {
		if applies, ok := gen.ResolveStack(reg, g.Recipe); ok {
			d.Applies = applies
		}
	}
	return d
}

// handleGenerators returns the generatables a project's framework supports, so
// the studio builds a framework-aware Generate menu instead of the hardcoded
// Laravel noun list. It resolves the framework from the project's manifest and
// asks gen.Generatables — the same catalogue the CLI uses — so a Next project is
// offered its own generators (and its auth stack), never Laravel's.
//
// It is a read that names a project by ?dir=, so it is a guarded GET like
// /api/db/ping. A project with no manifest is not an error the caller must catch:
// it simply has no framework, so the response carries an empty list and the UI
// shows the "not a keel project" state it already shows elsewhere.
func handleGenerators(w http.ResponseWriter, r *http.Request) {
	dir, err := projectDir(r.URL.Query().Get("dir"))
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error(), "generatables": []generatableDTO{}})
		return
	}
	m, err := engine.ReadManifest(dir)
	if err != nil {
		writeJSON(w, map[string]any{"error": "not a keel project", "generatables": []generatableDTO{}})
		return
	}
	reg, err := catalog.Registry()
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error(), "generatables": []generatableDTO{}})
		return
	}
	gens := gen.Generatables(reg, m.Framework)
	out := make([]generatableDTO, 0, len(gens))
	for _, g := range gens {
		out = append(out, toGeneratableDTO(reg, g))
	}
	// hasModule tells the UI whether this framework has a real module concept
	// (Magento's Vendor_Module, NestJS's @Module). Only then does the Generate
	// surface show a "Module" header and require a module name; every other
	// framework generates components directly — the fix for the "Module" leak on
	// Next/Laravel/Django/Symfony/FastAPI.
	writeJSON(w, map[string]any{
		"framework":    m.Framework,
		"hasModule":    gen.HasModuleConcept(frameworkFamily(reg, m.Framework)),
		"generatables": out,
	})
}

// frameworkFamily maps a framework id onto the family whose generation stance
// applies (a magento-mageos project is a Magento project), so the module-concept
// signal and the plan renderer both see the family, not the variant. An id keel
// does not know is returned unchanged.
func frameworkFamily(reg *recipe.Registry, id string) string {
	if reg != nil {
		if r, ok := reg.Get(id); ok && r.Family != "" {
			return r.Family
		}
	}
	return id
}

// addableKinds are the recipe kinds the Add-service picker offers: a project can
// gain a service (Redis), a database, a UI kit/frontend, an addon (Pest,
// Telescope) or an extra after it is built. The framework and the environment
// define the project — they cannot be added — and starter/config recipes are
// structural, injected by the resolver rather than chosen. This keeps Add-service
// to exactly the recipes `keel add` accepts.
var addableKinds = []recipe.Kind{recipe.Service, recipe.DB, recipe.Addon, recipe.Frontend, recipe.Extra}

// handleAddable lists the recipes compatible with a project's framework that are
// not already in its manifest, so the studio's "Add service" affordance offers a
// real, framework-filtered choice (services / db / ui-kit / addons) rather than
// the universal list. Installing one runs `keel add <recipe>` through the exec
// path, streaming output — this endpoint only supplies the menu.
//
// Like /api/generators it is a guarded GET keyed by ?dir=. It reuses
// Registry.ForFramework (the same filter the build wizard uses) so a recipe that
// does not apply to this framework never appears.
func handleAddable(w http.ResponseWriter, r *http.Request) {
	dir, err := projectDir(r.URL.Query().Get("dir"))
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error(), "addable": []recipeDTO{}})
		return
	}
	m, err := engine.ReadManifest(dir)
	if err != nil {
		writeJSON(w, map[string]any{"error": "not a keel project", "addable": []recipeDTO{}})
		return
	}
	reg, err := catalog.Registry()
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error(), "addable": []recipeDTO{}})
		return
	}
	have := map[string]bool{}
	for _, id := range m.Recipes {
		if canon, ok := reg.Canonical(id); ok {
			have[canon] = true
		} else {
			have[id] = true
		}
	}
	out := []recipeDTO{}
	seen := map[string]bool{}
	for _, k := range addableKinds {
		for _, rc := range reg.ForFramework(m.Framework, k) {
			if have[rc.ID] || seen[rc.ID] {
				continue
			}
			seen[rc.ID] = true
			out = append(out, toDTO(rc))
		}
	}
	writeJSON(w, map[string]any{"framework": m.Framework, "addable": out})
}
