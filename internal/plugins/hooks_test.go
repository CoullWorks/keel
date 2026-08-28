package plugins

import (
	"context"
	"testing"

	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/plugin"
)

// hookPlugin is a test double implementing the new render/recipe hooks, so the
// registry's gather methods can be exercised without a real plugin on disk.
type hookPlugin struct {
	meta    plugin.Meta
	actions []plugin.Action
	ran     map[string]map[string]string // id -> args, recording RunAction
	recipes []recipe.Recipe
}

func (h *hookPlugin) Meta() plugin.Meta { return h.meta }

func (h *hookPlugin) OverviewSections(_ context.Context, _ plugin.Project) ([]plugin.Section, error) {
	return []plugin.Section{{Title: h.meta.Name, Kind: "stat", Items: []plugin.Item{{Label: "x", Value: "1"}}}}, nil
}

func (h *hookPlugin) Actions(_ context.Context, _ plugin.Project) ([]plugin.Action, error) {
	return h.actions, nil
}

func (h *hookPlugin) RunAction(_ context.Context, _ plugin.IO, _ plugin.Project, id string, args map[string]string) error {
	if h.ran == nil {
		h.ran = map[string]map[string]string{}
	}
	h.ran[id] = args
	return nil
}

func (h *hookPlugin) Pages() []plugin.Page {
	return []plugin.Page{{ID: h.meta.Name + "-page", Title: "Page", Render: func(context.Context) (plugin.View, error) {
		return plugin.View{Sections: []plugin.Section{{Title: "p", Kind: "text"}}}, nil
	}}}
}

func (h *hookPlugin) Recipes() []recipe.Recipe { return h.recipes }

// regWith builds a registry holding just these plugins, bypassing Load so a test
// controls exactly what is registered.
func regWith(ps ...plugin.Plugin) *Registry {
	r := &Registry{events: map[plugin.Event][]handler{}}
	for _, p := range ps {
		r.add(p.Meta().Name, p)
	}
	return r
}

func meta(name string, caps ...plugin.Capability) plugin.Meta {
	return plugin.Meta{Schema: 1, Name: name, Version: "1.0.0", Description: "a test plugin double", Author: "t", License: "MIT", Capabilities: caps}
}

func TestOverviewSectionsAreGathered(t *testing.T) {
	reg := regWith(&hookPlugin{meta: meta("a")}, &hookPlugin{meta: meta("b")})
	secs, errs := reg.OverviewSectionsFor(context.Background(), plugin.Project{})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(secs) != 2 {
		t.Errorf("want a section from each plugin, got %d", len(secs))
	}
}

func TestActionsAreGatheredWithOwner(t *testing.T) {
	p := &hookPlugin{meta: meta("a"), actions: []plugin.Action{{ID: "go", Label: "Go"}}}
	reg := regWith(p)
	got, errs := reg.ActionsFor(context.Background(), plugin.Project{})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(got) != 1 || got[0].Plugin != "a" || got[0].Action.ID != "go" {
		t.Errorf("action was not tagged with its owner: %+v", got)
	}
}

// An action needing a capability the plugin never declared is dropped from the
// listing: offering a button that can never run is worse than not offering it.
func TestActionNeedingUndeclaredCapabilityIsDropped(t *testing.T) {
	p := &hookPlugin{meta: meta("a"), actions: []plugin.Action{{ID: "deploy", Label: "Deploy", Needs: plugin.CapNet}}}
	reg := regWith(p)
	got, errs := reg.ActionsFor(context.Background(), plugin.Project{})
	if len(got) != 0 {
		t.Errorf("an action needing an undeclared capability should be dropped, got %+v", got)
	}
	if len(errs) == 0 {
		t.Error("the drop should be reported so the author learns why")
	}
}

// RunAction is the trust gate: an action needing a granted capability runs only
// when the plugin declared it AND the grant says yes.
func TestRunActionEnforcesCapability(t *testing.T) {
	p := &hookPlugin{meta: meta("a", plugin.CapNet), actions: []plugin.Action{{ID: "deploy", Label: "Deploy", Needs: plugin.CapNet}}}
	reg := regWith(p)

	deny := func(plugin.Capability) bool { return false }
	if err := reg.RunAction(context.Background(), &recorder{}, plugin.Project{}, "a", "deploy", nil, deny); err == nil {
		t.Error("an ungranted capability should refuse the action")
	}

	grant := func(plugin.Capability) bool { return true }
	if err := reg.RunAction(context.Background(), &recorder{}, plugin.Project{}, "a", "deploy", map[string]string{"k": "v"}, grant); err != nil {
		t.Errorf("a granted capability should run the action: %v", err)
	}
	if p.ran["deploy"]["k"] != "v" {
		t.Error("RunAction did not receive the args")
	}
}

// An action with no capability runs without any grant.
func TestRunActionNeedingNothingRuns(t *testing.T) {
	p := &hookPlugin{meta: meta("a"), actions: []plugin.Action{{ID: "audit", Label: "Audit"}}}
	reg := regWith(p)
	if err := reg.RunAction(context.Background(), &recorder{}, plugin.Project{}, "a", "audit", nil, nil); err != nil {
		t.Errorf("a capability-free action should run without a grant: %v", err)
	}
}

func TestPagesAreGathered(t *testing.T) {
	reg := regWith(&hookPlugin{meta: meta("a")})
	if len(reg.Pages()) != 1 {
		t.Fatalf("want one page, got %d", len(reg.Pages()))
	}
	if _, ok := reg.Page("a-page"); !ok {
		t.Error("a page advertised must be reachable by id")
	}
	if _, ok := reg.Page("nope"); ok {
		t.Error("an unknown page id should not resolve")
	}
}

// Recipes gathered from a Reciper are stamped with the owning plugin so the
// catalog can trust-gate them.
func TestReciperRecipesAreStamped(t *testing.T) {
	p := &hookPlugin{meta: meta("a"), recipes: []recipe.Recipe{{ID: "widget", Kind: recipe.Addon, Label: "W"}}}
	reg := regWith(p)
	got := reg.Recipes()
	if len(got) != 1 {
		t.Fatalf("want one recipe, got %d", len(got))
	}
	if got[0].Source != "plugin:a" || got[0].Pack != "a" {
		t.Errorf("recipe not stamped with its plugin: source=%q pack=%q", got[0].Source, got[0].Pack)
	}
}

// An unknown capability in a register file is refused: a typo in a
// security-relevant declaration must fail loudly, not be ignored.
func TestUnknownCapabilityIsRefused(t *testing.T) {
	m := meta("x")
	m.Capabilities = []plugin.Capability{"teleport"}
	if err := validate("x", metaOnly{m}); err == nil {
		t.Error("an unknown capability should fail validation")
	}
}

// A known capability passes validation.
func TestKnownCapabilityValidates(t *testing.T) {
	m := meta("x", plugin.CapNet, plugin.CapExec)
	if err := validate("x", metaOnly{m}); err != nil {
		t.Errorf("declared known capabilities should validate: %v", err)
	}
}
