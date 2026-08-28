package plugins

import (
	"context"
	"strings"
	"testing"

	"github.com/coullworks/keel/plugin"
)

// loadWithDemo isolates the config dir, registers a default-on in-process fixture
// into the compiled-in set, and builds the registry. keel ships zero built-ins,
// so these registry tests provide their own to exercise the compiled-in path —
// the mechanism a future first-party plugin would use. The discovered-plugin path
// (the one every shipped plugin takes) is proven separately, against a real
// on-disk fixture, in the cross-package tests via internal/plugintest.
func loadWithDemo(t *testing.T) *Registry {
	t.Helper()
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	regBuiltin(t, "demo", false, demoPlugin("demo"))
	return Load()
}

func TestBuiltinRegisters(t *testing.T) {
	r := loadWithDemo(t)
	if errs := r.Problems(); len(errs) > 0 {
		t.Fatalf("a plugin failed to register: %v", errs)
	}
	if len(r.Plugins()) == 0 {
		t.Fatal("no plugins registered")
	}
	if _, ok := r.Command("demo"); !ok {
		t.Error("the fixture plugin should add `keel demo`")
	}
	if _, ok := r.Screen("demo-hello"); !ok {
		t.Error("the fixture plugin should add a studio screen")
	}
	if len(r.StepsFor("laravel")) == 0 {
		t.Error("the fixture plugin should add a wizard step")
	}
}

func TestScreenReturnsData(t *testing.T) {
	r := loadWithDemo(t)
	s, ok := r.Screen("demo-hello")
	if !ok {
		t.Fatal("no such screen")
	}
	v, err := s.Render(context.Background(), plugin.Project{Dir: t.TempDir(), Framework: "laravel", Env: "ddev"})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Sections) == 0 {
		t.Fatal("the screen returned nothing to draw")
	}
	// Data, not markup: a plugin cannot break the studio's layout or script
	// into the page.
	for _, sec := range v.Sections {
		if strings.Contains(sec.Title, "<") {
			t.Errorf("a section title contains markup: %q", sec.Title)
		}
		switch sec.Kind {
		case "stat", "list", "text":
		default:
			t.Errorf("section %q has unknown kind %q", sec.Title, sec.Kind)
		}
	}
}

func TestStepOptionsDependOnTheProject(t *testing.T) {
	r := loadWithDemo(t)
	steps := r.StepsFor("laravel")
	if len(steps) == 0 {
		t.Fatal("no steps")
	}
	opts, err := steps[0].Options(context.Background(), plugin.Project{Framework: "laravel"})
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) < 2 {
		t.Fatalf("a step that knows the framework should offer a stack option, got %d", len(opts))
	}
	bare, err := steps[0].Options(context.Background(), plugin.Project{})
	if err != nil {
		t.Fatal(err)
	}
	if len(bare) >= len(opts) {
		t.Error("with no framework the step should offer fewer options, not the same")
	}
}

func TestEventsReachSubscribers(t *testing.T) {
	r := loadWithDemo(t)
	io := &recorder{}
	errs := r.Emit(context.Background(), plugin.EventProjectCreated,
		io, plugin.Project{Name: "shop", Framework: "laravel", Env: "ddev"})
	if len(errs) > 0 {
		t.Fatalf("a listener failed: %v", errs)
	}
	if !strings.Contains(strings.Join(io.lines, "\n"), "shop") {
		t.Errorf("the listener did not run, got %v", io.lines)
	}
}

// recorder captures what a plugin writes. plugin.IO is an interface precisely
// so a test needs no terminal and a plugin cannot tell the difference.
type recorder struct{ lines []string }

func (r *recorder) Title(s string)     { r.lines = append(r.lines, s) }
func (r *recorder) Detail(k, v string) { r.lines = append(r.lines, k+": "+v) }
func (r *recorder) Note(s string)      { r.lines = append(r.lines, s) }
func (r *recorder) OK(s string)        { r.lines = append(r.lines, s) }
func (r *recorder) Warn(s string)      { r.lines = append(r.lines, s) }
func (r *recorder) Bad(s string)       { r.lines = append(r.lines, s) }
func (r *recorder) List(t string, i []string) {
	r.lines = append(r.lines, t+": "+strings.Join(i, ", "))
}

// A subscription with no handler is an event that silently never fires, which
// is the whole failure mode this design exists to prevent.
func TestSubscriptionWithoutHandlerIsRefused(t *testing.T) {
	err := validate("broken", brokenPlugin{})
	if err == nil {
		t.Fatal("a plugin declaring an event it does not handle should be refused")
	}
	if !strings.Contains(err.Error(), "no handler") {
		t.Errorf("the reason should name the problem, got: %v", err)
	}
}

type brokenPlugin struct{}

func (brokenPlugin) Meta() plugin.Meta {
	return plugin.Meta{Schema: 1, Name: "broken", Version: "1.0.0",
		Description: "d", Author: "a", License: "MIT"}
}
func (brokenPlugin) Subscriptions() ([]plugin.Event, error) {
	return []plugin.Event{plugin.EventProjectBuilt}, nil
}

func TestValidationRefusesAnIncompleteRegisterFile(t *testing.T) {
	cases := map[string]plugin.Meta{
		"no schema":      {Name: "x", Version: "1.0.0", Description: "d", Author: "a", License: "MIT"},
		"name mismatch":  {Schema: 1, Name: "other", Version: "1.0.0", Description: "d", Author: "a", License: "MIT"},
		"no version":     {Schema: 1, Name: "x", Description: "d", Author: "a", License: "MIT"},
		"no description": {Schema: 1, Name: "x", Version: "1.0.0", Author: "a", License: "MIT"},
		"no author":      {Schema: 1, Name: "x", Version: "1.0.0", Description: "d", License: "MIT"},
		"no license":     {Schema: 1, Name: "x", Version: "1.0.0", Description: "d", Author: "a"},
		"future schema":  {Schema: 99, Name: "x", Version: "1.0.0", Description: "d", Author: "a", License: "MIT"},
	}
	for name, m := range cases {
		if err := validate("x", metaOnly{m}); err == nil {
			t.Errorf("%s should fail validation", name)
		}
	}
}

type metaOnly struct{ m plugin.Meta }

func (p metaOnly) Meta() plugin.Meta { return p.m }
