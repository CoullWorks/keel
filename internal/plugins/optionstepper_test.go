package plugins

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/coullworks/keel/plugin"
)

// OptionSchemasFor is the headless companion to StepsFor: a client that cannot
// draw huh prompts (the studio) reads each applicable plugin's steps as data. It
// must return the same choices the interactive step would offer for the project,
// so a studio form and `keel new` install the same thing.
func TestOptionSchemasForMatchesInteractiveSteps(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	regBuiltin(t, "demo", false, demoPlugin("demo"))
	reg := Load()

	p := plugin.Project{Dir: t.TempDir(), Name: "shop", Framework: "laravel", Env: "ddev"}
	schemas, errs := reg.OptionSchemasFor(context.Background(), p)
	if len(errs) > 0 {
		t.Fatalf("computing schema failed: %v", errs)
	}

	byID := map[string]plugin.OptionSchema{}
	for _, s := range schemas {
		byID[s.ID] = s
	}

	// the fixture implements OptionStepper, so its step must appear as schema.
	sc, ok := byID["demo-step"]
	if !ok {
		t.Fatalf("the fixture's step is missing from the declarative schema, got %v", byID)
	}
	if sc.Label == "" {
		t.Error("a schema with no label gives a headless client nothing to draw")
	}
	if sc.Type != "multi" {
		t.Errorf("the fixture's step is multi-select, got type %q", sc.Type)
	}
	if len(sc.Choices) == 0 {
		t.Fatal("the schema offers no choices, so a studio form would be empty")
	}

	// The declarative choices must match what the interactive Options() returns:
	// one source of truth, no drift between the two front ends.
	var interactive plugin.Step
	for _, s := range reg.StepsFor("laravel") {
		if s.ID == "demo-step" {
			interactive = s
		}
	}
	opts, err := interactive.Options(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != len(sc.Choices) {
		t.Fatalf("schema has %d choices, interactive step offers %d", len(sc.Choices), len(opts))
	}
	for i, o := range opts {
		c := sc.Choices[i]
		if c.Value != o.Value || c.Label != o.Label || c.Default != o.Default {
			t.Errorf("choice %d drifted from the interactive option: %+v vs %+v", i, c, o)
		}
	}
}

// Every choice a schema advertises is well-formed, so a client never has to draw
// a control with no value or label.
func TestOptionSchemaChoicesAreWellFormed(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	regBuiltin(t, "demo", false, demoPlugin("demo"))
	reg := Load()
	p := plugin.Project{Dir: t.TempDir(), Framework: "django", Env: "docker"}
	schemas, _ := reg.OptionSchemasFor(context.Background(), p)
	for _, s := range schemas {
		if s.ID == "" || s.Type == "" {
			t.Errorf("schema needs an id and a type: %+v", s)
		}
		for _, c := range s.Choices {
			if c.Value == "" || c.Label == "" {
				t.Errorf("schema %q has a malformed choice: %+v", s.ID, c)
			}
		}
	}
}

// A plugin whose schema cannot be computed is skipped with its reason recorded,
// not allowed to deny the client the other plugins' schema.
func TestOptionSchemasForRecordsAFailingPlugin(t *testing.T) {
	reg := &Registry{plugins: []plugin.Plugin{failingOptionStepper{}}}
	schemas, errs := reg.OptionSchemasFor(context.Background(), plugin.Project{Framework: "laravel"})
	if len(schemas) != 0 {
		t.Errorf("a failing plugin should contribute no schema, got %v", schemas)
	}
	if len(errs) != 1 {
		t.Fatalf("the failure should be reported, got %v", errs)
	}
	if !strings.Contains(errs[0].Error(), "boom") {
		t.Errorf("the error should carry the plugin's reason, got %v", errs[0])
	}
}

type failingOptionStepper struct{}

func (failingOptionStepper) Meta() plugin.Meta {
	return plugin.Meta{Name: "boomer", Schema: 1, Version: "1.0.0", Description: "d", Author: "a", License: "MIT"}
}
func (failingOptionStepper) OptionSteps(context.Context, plugin.Project) ([]plugin.OptionSchema, error) {
	return nil, errors.New("boom")
}

// The additive interface must not disturb the existing one: a plugin that
// implements OptionStepper still satisfies Stepper, so the interactive `keel new`
// path is untouched while the studio gets a form. The registry must load it
// carrying both, not strip one.
func TestOptionStepperStillImplementsStepper(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	regBuiltin(t, "demo", false, demoPlugin("demo"))
	reg := Load()
	var found bool
	for _, pl := range reg.Plugins() {
		if pl.Meta().Name != "demo" {
			continue
		}
		found = true
		if _, ok := pl.(plugin.Stepper); !ok {
			t.Error("the plugin must still be a Stepper: the interactive path depends on it")
		}
		if _, ok := pl.(plugin.OptionStepper); !ok {
			t.Error("the plugin should also be an OptionStepper")
		}
	}
	if !found {
		t.Fatal("the fixture did not load")
	}
}
