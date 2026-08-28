package plugins

import (
	"context"
	"testing"

	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/plugin"
)

// This file gives the plugins-package tests an in-process fixture to exercise the
// compiled-in machinery with. keel ships zero built-ins, so `All` is empty in a
// real build; the built-in type, the default-off seed, the disabled-set and the
// bundled-provenance labelling all remain as the seam for a future first-party
// plugin, and they must not rot. A test registers a fixture into `All` (with
// cleanup) and asserts the machinery treats it correctly — the honest way to test
// the compiled-in path without keel actually bundling anything.
//
// The discovered-plugin path (the one every shipped plugin takes) is tested
// separately, against a real on-disk fixture, in the cross-package tests via
// internal/plugintest.

// fakePlugin is a configurable in-process plugin. Every interface method is
// present, so it satisfies Commander/Screener/Stepper/OptionStepper/Reciper and
// the listener contract; empty slices make a capability "absent" by content,
// which is exactly how the registry reports what a plugin really adds.
type fakePlugin struct {
	meta      plugin.Meta
	commands  []plugin.Command
	screens   []plugin.Screen
	steps     []plugin.Step
	recipes   []recipe.Recipe
	listeners map[plugin.Event]func(context.Context, plugin.IO, plugin.Project) error
	subs      []plugin.Event
}

func (f fakePlugin) Meta() plugin.Meta          { return f.meta }
func (f fakePlugin) Commands() []plugin.Command { return f.commands }
func (f fakePlugin) Screens() []plugin.Screen   { return f.screens }
func (f fakePlugin) Steps() []plugin.Step       { return f.steps }
func (f fakePlugin) Recipes() []recipe.Recipe   { return f.recipes }

func (f fakePlugin) Subscriptions() ([]plugin.Event, error) { return f.subs, nil }
func (f fakePlugin) Listeners() map[plugin.Event]func(context.Context, plugin.IO, plugin.Project) error {
	return f.listeners
}

// OptionSteps derives one schema per step from the step's own Options, so the
// declarative schema and the interactive prompt can never drift — the same
// guarantee the real declarative adapter gives.
func (f fakePlugin) OptionSteps(ctx context.Context, p plugin.Project) ([]plugin.OptionSchema, error) {
	out := make([]plugin.OptionSchema, 0, len(f.steps))
	for _, st := range f.steps {
		opts, err := st.Options(ctx, p)
		if err != nil {
			return nil, err
		}
		typ := "select"
		if st.Multi {
			typ = "multi"
		}
		choices := make([]plugin.OptionChoice, 0, len(opts))
		for _, o := range opts {
			choices = append(choices, plugin.OptionChoice{Value: o.Value, Label: o.Label, Description: o.Description, Default: o.Default})
		}
		out = append(out, plugin.OptionSchema{ID: st.ID, Label: st.Title, Help: st.Help, Type: typ, Choices: choices})
	}
	return out, nil
}

// demoPlugin is a fully-featured fixture: a command, a screen, a framework-aware
// multi-select step, an event listener and a shipped recipe. Tests that only care
// about one facet ignore the rest.
func demoPlugin(name string) fakePlugin {
	return fakePlugin{
		meta: plugin.Meta{
			Schema: 1, Name: name, Version: "1.0.0",
			Description: "an in-process test plugin", Author: "CoullWorks",
			License: "MIT", Homepage: "https://example.test/" + name,
		},
		commands: []plugin.Command{{
			Name:    name,
			Summary: "the test plugin command",
			Run:     func(context.Context, plugin.IO, plugin.Project, []string) error { return nil },
		}},
		screens: []plugin.Screen{{
			ID:    name + "-hello",
			Title: "Demo",
			Render: func(_ context.Context, p plugin.Project) (plugin.View, error) {
				return plugin.View{Sections: []plugin.Section{{
					Kind: "stat", Title: "This project",
					Items: []plugin.Item{{Label: "Framework", Value: p.Framework}},
				}}}, nil
			},
		}},
		steps: []plugin.Step{{
			ID: name + "-step", Title: "Demo extras", Multi: true,
			Options: func(_ context.Context, p plugin.Project) ([]plugin.Option, error) {
				opts := []plugin.Option{{Value: "greeting", Label: "Add a greeting", Default: true}}
				if p.Framework != "" {
					// A step that knows the framework offers more, so a form is
					// framework-aware the way the interactive prompt is.
					opts = append(opts,
						plugin.Option{Value: "analytics", Label: "Add analytics"},
						plugin.Option{Value: "docs", Label: "Add docs"})
				}
				return opts, nil
			},
			Apply: func(context.Context, plugin.IO, plugin.Project, []string) error { return nil },
		}},
		listeners: map[plugin.Event]func(context.Context, plugin.IO, plugin.Project) error{
			plugin.EventProjectCreated: func(_ context.Context, io plugin.IO, p plugin.Project) error {
				io.Title(p.Name)
				return nil
			},
		},
		subs: []plugin.Event{plugin.EventProjectCreated},
		recipes: []recipe.Recipe{{
			ID: name + "-recipe", Kind: recipe.Extra, Label: "a recipe from the test plugin",
		}},
	}
}

// regBuiltin registers a fixture into `All` for the test's duration and removes
// it after, so the compiled-in machinery can be exercised without keel shipping a
// real built-in.
func regBuiltin(t *testing.T, name string, defaultOff bool, p plugin.Plugin) {
	t.Helper()
	All[name] = builtin{new: func() (plugin.Plugin, error) { return p, nil }, DefaultOff: defaultOff}
	t.Cleanup(func() { delete(All, name) })
}

// markBundled marks a fixture as a bundled separate tool for the test's duration,
// so the provenance-labelling path (author/homepage shown for a separate tool) is
// exercised.
func markBundled(t *testing.T, name string) {
	t.Helper()
	bundled[name] = true
	t.Cleanup(func() { delete(bundled, name) })
}
