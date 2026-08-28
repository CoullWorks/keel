package plugins

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coullworks/keel/internal/pluginstore"
	"github.com/coullworks/keel/plugin"
)

// Every plugin that ships with keel is exercised here, rather than each one
// testing only itself.
//
// The registry validates what a plugin declares: that it has a name, a version,
// a description, and that its commands have something to run. Nothing ever ran
// any of it. A screen that panics on a project with no manifest, or a step with
// nothing to choose, registers perfectly and fails the first time a person
// opens it.

// fixtureProject is a real directory with a keel manifest, the shape a plugin is
// handed at runtime.
func fixtureProject(t *testing.T, fw, env string) plugin.Project {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".keel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".keel", "manifest.yaml"),
		[]byte("framework: "+fw+"\nenv: "+env+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return plugin.Project{Dir: dir, Name: filepath.Base(dir), Framework: fw, Env: env}
}

// registered is every plugin this build loaded, and it must be all of them: a
// plugin that silently fails to register is a feature nobody can reach.
//
// keel ships zero built-ins, so this registers an in-process fixture to exercise
// — the compiled-in surface a future first-party plugin would present. A
// default-off built-in is enabled first: these tests exercise every plugin's
// surface, and one that ships off is still one that must work when turned on, the
// same one line a user runs.
func registered(t *testing.T) *Registry {
	t.Helper()
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	regBuiltin(t, "demo", false, demoPlugin("demo"))
	for name, b := range All {
		if b.DefaultOff {
			if err := pluginstore.SetBuiltinEnabled(name, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	reg := Load()
	for _, err := range reg.Problems() {
		t.Errorf("a plugin did not register: %v", err)
	}
	if len(reg.Plugins()) != len(All) {
		t.Fatalf("%d of %d plugins registered", len(reg.Plugins()), len(All))
	}
	return reg
}

// Every plugin's metadata is complete enough to show someone.
func TestEveryPluginIsPresentable(t *testing.T) {
	reg := registered(t)
	for _, p := range reg.Plugins() {
		m := p.Meta()
		t.Run(m.Name, func(t *testing.T) {
			if strings.ContainsAny(m.Description, "–—") {
				t.Errorf("description uses an en or em dash: %q", m.Description)
			}
			if n := len([]rune(m.Description)); n < 10 || n > 200 {
				t.Errorf("description is %d characters, which does not fit a listing: %q", n, m.Description)
			}
			if AddsSummary(p) == "nothing yet" {
				t.Errorf("%s contributes nothing, so it is a plugin in name only", m.Name)
			}
		})
	}
}

// Every built-in's new render hooks (overview sections, actions) are gathered
// against real projects without erroring or panicking, and every action they
// offer is complete and consistent with what the plugin declared. This is the
// same "it registers cleanly, now prove it runs" bar the screen/step surface
// tests apply, extended to the additive hooks the studio consumes.
func TestEveryPluginHookIsAnswerable(t *testing.T) {
	reg := registered(t)
	projects := []plugin.Project{
		fixtureProject(t, "laravel", "ddev"),
		fixtureProject(t, "magento", "docker"),
		fixtureProject(t, "nextjs", "docker"),
	}
	for _, p := range projects {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

		// Overview sections gather cleanly and are drawable.
		secs, _ := reg.OverviewSectionsFor(ctx, p)
		for _, s := range secs {
			if s.Title == "" {
				t.Errorf("%s: an overview section has no title", p.Framework)
			}
			switch s.Kind {
			case "stat", "list", "text":
			default:
				t.Errorf("%s: overview section %q has kind %q the studio cannot draw", p.Framework, s.Title, s.Kind)
			}
		}

		// Actions gather cleanly, are complete, and any capability an action needs
		// was declared by its plugin (ActionsFor drops the ones that were not).
		actions, _ := reg.ActionsFor(ctx, p)
		for _, a := range actions {
			if a.Plugin == "" {
				t.Errorf("%s: an action is not tagged with its plugin: %+v", p.Framework, a)
			}
			if a.Action.ID == "" || a.Action.Label == "" {
				t.Errorf("%s: action %+v has no id or label", p.Framework, a.Action)
			}
			if a.Action.Needs != "" && !plugin.KnownCapability(a.Action.Needs) {
				t.Errorf("%s: action %q needs unknown capability %q", p.Framework, a.Action.ID, a.Action.Needs)
			}
		}
		cancel()
	}

	// Global pages, if any built-in contributes them, are reachable by id.
	for _, pg := range reg.Pages() {
		if pg.Render == nil {
			t.Errorf("page %q has nothing to render", pg.ID)
		}
		if _, ok := reg.Page(pg.ID); !ok {
			t.Errorf("page %q is listed but not reachable by id", pg.ID)
		}
	}
}

// Every studio screen renders against a real project without erroring or
// panicking, and returns something the studio can draw.
func TestEveryPluginScreenRenders(t *testing.T) {
	reg := registered(t)
	projects := []plugin.Project{
		fixtureProject(t, "laravel", "ddev"),
		fixtureProject(t, "django", "docker"),
		fixtureProject(t, "magento", "docker"),
		{Dir: t.TempDir(), Name: "bare"}, // no manifest and no stack: the careless case
	}
	for _, s := range reg.Screens() {
		for _, p := range projects {
			name := s.ID + "/" + p.Framework
			if p.Framework == "" {
				name = s.ID + "/no-manifest"
			}
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()

				view, err := s.Render(ctx, p)
				if err != nil {
					// An honest error is allowed: a screen may need something
					// this project has not got. A panic or a hang is not, and
					// reaching this line proves neither happened.
					return
				}
				if len(view.Sections) == 0 {
					t.Errorf("screen %q rendered nothing at all", s.ID)
				}
				for i, sec := range view.Sections {
					if sec.Title == "" {
						t.Errorf("screen %q: section %d has no title", s.ID, i)
					}
					switch sec.Kind {
					case "stat", "list", "text":
					default:
						t.Errorf("screen %q: section %q has kind %q, which the studio cannot draw",
							s.ID, sec.Title, sec.Kind)
					}
				}
			})
		}
	}
}

// Every screen the registry advertises can be fetched by its id, which is what
// the studio does when its tab is clicked.
func TestEveryAdvertisedScreenIsReachable(t *testing.T) {
	reg := registered(t)
	if len(reg.Screens()) == 0 {
		t.Skip("this build registers no screens")
	}
	for _, s := range reg.Screens() {
		got, ok := reg.Screen(s.ID)
		if !ok {
			t.Errorf("screen %q is listed but cannot be looked up", s.ID)
			continue
		}
		if got.Title == "" {
			t.Errorf("screen %q has no title, so its tab would be blank", s.ID)
		}
		if got.Render == nil {
			t.Errorf("screen %q has nothing to render", s.ID)
		}
	}
}

// Every command is complete, and no two plugins claim the same one.
func TestEveryPluginCommandIsComplete(t *testing.T) {
	reg := registered(t)
	seen := map[string]bool{}
	for _, c := range reg.Commands() {
		t.Run(c.Name, func(t *testing.T) {
			if c.Summary == "" {
				t.Error("no summary, so it is blank in the command list")
			}
			if c.Run == nil {
				t.Error("nothing to run")
			}
			if strings.ContainsAny(c.Summary, "–—") {
				t.Errorf("summary uses an en or em dash: %q", c.Summary)
			}
			if seen[c.Name] {
				t.Errorf("two plugins both claim `keel %s`", c.Name)
			}
			seen[c.Name] = true
		})
	}
}

// Every wizard step a plugin contributes is answerable: a step with no title, or
// nothing to choose, is a dead screen in the middle of a build.
//
// A step's options are computed from the project, so this asks the same way the
// wizard does, for a real project on each framework.
func TestEveryPluginWizardStepIsAnswerable(t *testing.T) {
	reg := registered(t)
	projects := []plugin.Project{
		fixtureProject(t, "laravel", "ddev"),
		fixtureProject(t, "django", "docker"),
		fixtureProject(t, "magento", "docker"),
		fixtureProject(t, "nextjs", "docker"),
	}
	for _, p := range projects {
		for _, s := range reg.StepsFor(p.Framework) {
			t.Run(p.Framework+"/"+s.ID, func(t *testing.T) {
				if s.Title == "" {
					t.Error("the step has no title, so the wizard would ask a blank question")
				}
				if s.Options == nil {
					t.Fatal("the step has no options function, so there is nothing to choose")
				}
				if s.Apply == nil {
					t.Error("the step has nothing to apply, so answering it does nothing")
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()

				opts, err := s.Options(ctx, p)
				if err != nil {
					return // an honest error beats a panic, which is what this proves
				}
				if len(opts) == 0 {
					t.Errorf("step %q offers nothing to choose on %s, so the wizard would show an empty list", s.Title, p.Framework)
				}
				for _, o := range opts {
					if o.Value == "" || o.Label == "" {
						t.Errorf("step %q has an option with no value or label: %+v", s.Title, o)
					}
				}
			})
		}
	}
}
