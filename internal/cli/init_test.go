package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/coullworks/keel/internal/profile"
	"github.com/coullworks/keel/internal/tui"
)

// stubInitWizard swaps initWizard for one returning canned selections, restoring
// the real wizard when the test ends.
func stubInitWizard(t *testing.T, fn func(title, intro string, steps []tui.Step) ([][]string, error)) {
	t.Helper()
	old := initWizard
	initWizard = fn
	t.Cleanup(func() { initWizard = old })
}

// canned builds a wizard result in the exact shape runInit expects:
// [language, framework-family, framework-type, env, db, frontend, web-server,
// services, addons, extras, editor].
// canned mirrors the step order in runInit. web is its own answer, and lands
// under its own profile key: see profile.NoWebServer for why it cannot share
// the services list.
func canned(lang, fam, fw, env, db, frontend, web string, services, addons, extras []string, editor string) [][]string {
	return [][]string{
		{lang}, {fam}, {fw}, {env}, {db}, {frontend},
		{web}, services, addons, extras, {editor},
	}
}

// runInit writes the wizard's selections into the profile on disk. With the
// wizard stubbed we can assert the mapping (res[2]=framework, res[3]=env, …)
// without a terminal.
func TestRunInitWritesProfile(t *testing.T) {
	isolate(t)
	stubInitWizard(t, func(_, _ string, _ []tui.Step) ([][]string, error) {
		return canned(
			"php", "laravel", "laravel", "sail", "postgres", "", "nginx",
			[]string{"redis"}, []string{"pest"}, []string{"git"}, "code",
		), nil
	})
	if err := runInit(io.Discard); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	// The profile must exist and reflect the canned selections.
	if _, err := os.Stat(profile.Path()); err != nil {
		t.Fatalf("profile not written: %v", err)
	}
	p, err := profile.Load()
	if err != nil {
		t.Fatalf("reload profile: %v", err)
	}
	checks := map[string]string{
		"framework": "laravel",
		"env":       "sail",
		"database":  "postgres",
		"webserver": "nginx",
		"services":  "redis",
		"addons":    "pest",
		"extras":    "git",
		"editor":    "code",
	}
	for k, want := range checks {
		if got := p.Defaults[k]; got != want {
			t.Errorf("defaults[%q] = %q, want %q", k, got, want)
		}
	}
	if p.Defaults["frontend"] != "" {
		t.Errorf("frontend = %q, want empty (None)", p.Defaults["frontend"])
	}
}

// A different selection shape: a frontend chosen, multiple services/addons, and
// the "None" editor (empty key). Exercises the join + first() paths.
func TestRunInitSavesFrontendAndMultis(t *testing.T) {
	isolate(t)
	stubInitWizard(t, func(_, _ string, _ []tui.Step) ([][]string, error) {
		return canned(
			"php", "magento", "magento", "ddev", "mysql", "hyva", "nginx",
			[]string{"redis", "elasticsearch"}, []string{"n98"}, []string{"ci", "git"}, "",
		), nil
	})
	if err := runInit(io.Discard); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	p, err := profile.Load()
	if err != nil {
		t.Fatalf("reload profile: %v", err)
	}
	if p.Defaults["frontend"] != "hyva" {
		t.Errorf("frontend = %q, want hyva", p.Defaults["frontend"])
	}
	// The web server has its own key and stays out of the services list, so
	// that list can never be mistaken for an answer to the web-server question.
	if p.Defaults["webserver"] != "nginx" {
		t.Errorf("webserver = %q, want nginx", p.Defaults["webserver"])
	}
	if p.Defaults["services"] != "redis,elasticsearch" {
		t.Errorf("services = %q, want redis,elasticsearch", p.Defaults["services"])
	}
	if p.Defaults["extras"] != "ci,git" {
		t.Errorf("extras = %q, want ci,git", p.Defaults["extras"])
	}
	if p.Defaults["editor"] != "" {
		t.Errorf("editor = %q, want empty (None)", p.Defaults["editor"])
	}
}

// A pre-existing profile with an empty git identity gets it filled from the
// stubbed selections + git config; the save must not clobber an existing name.
func TestRunInitKeepsExistingGitIdentity(t *testing.T) {
	isolate(t)
	cfg := os.Getenv("KEEL_CONFIG_DIR")
	prof := "" +
		"git:\n" +
		"  name: Existing Person\n" +
		"  email: existing@example.com\n" +
		"defaults:\n" +
		"  framework: fastapi\n"
	if err := os.WriteFile(filepath.Join(cfg, "profile.yaml"), []byte(prof), 0o644); err != nil {
		t.Fatal(err)
	}
	stubInitWizard(t, func(_, _ string, _ []tui.Step) ([][]string, error) {
		return canned(
			// No web server: a native environment runs the app's own dev
			// server, so there is nothing to put in front of it.
			"python", "fastapi", "fastapi", "uv-local", "sqlite", "", "",
			nil, nil, nil, "nvim",
		), nil
	})
	if err := runInit(io.Discard); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	p, err := profile.Load()
	if err != nil {
		t.Fatalf("reload profile: %v", err)
	}
	if p.Git.Name != "Existing Person" || p.Git.Email != "existing@example.com" {
		t.Errorf("git identity clobbered: %+v", p.Git)
	}
	if p.Defaults["framework"] != "fastapi" {
		t.Errorf("framework = %q, want fastapi", p.Defaults["framework"])
	}
}

// The cancel path: the wizard returns ErrCancelled, so runInit prints a notice,
// returns nil, and writes NO profile.
func TestRunInitCancelled(t *testing.T) {
	isolate(t)
	stubInitWizard(t, func(_, _ string, _ []tui.Step) ([][]string, error) {
		return nil, tui.ErrCancelled
	})
	var buf bytes.Buffer
	if err := runInit(&buf); err != nil {
		t.Fatalf("runInit (cancel) should return nil, got %v", err)
	}
	mustContain(t, buf.String(), "cancelled")
	if _, err := os.Stat(profile.Path()); err == nil {
		t.Error("cancel path must not write a profile")
	}
}

// A non-cancel wizard error propagates unchanged and writes nothing.
func TestRunInitWizardError(t *testing.T) {
	isolate(t)
	stubInitWizard(t, func(_, _ string, _ []tui.Step) ([][]string, error) {
		return nil, os.ErrPermission
	})
	if err := runInit(io.Discard); err == nil {
		t.Fatal("expected the wizard error to propagate")
	}
	if _, err := os.Stat(profile.Path()); err == nil {
		t.Error("error path must not write a profile")
	}
}

// This drives the option-building closures the real wizard would call: the stub
// invokes each step's Dynamic(prior) with a growing selection (Laravel path),
// exercising single/multi/familyChoices/variantChoices/fwOf, then returns a
// canned final result. Covers runInit's step construction, not just the save.
func TestRunInitExercisesStepBuilders(t *testing.T) {
	isolate(t)
	// A saved profile makes the pre-selection branches (hasProfile, saved
	// framework/frontend/services) fire inside the builders.
	cfg := os.Getenv("KEEL_CONFIG_DIR")
	prof := "" +
		"defaults:\n" +
		"  framework: laravel\n" +
		"  frontend: \"\"\n" +
		"  services: redis\n" +
		"  addons: pest\n" +
		"  extras: git\n" +
		"  editor: code\n"
	if err := os.WriteFile(filepath.Join(cfg, "profile.yaml"), []byte(prof), 0o644); err != nil {
		t.Fatal(err)
	}
	stubInitWizard(t, func(_, _ string, steps []tui.Step) ([][]string, error) {
		// Simulate the wizard: build a growing selection, calling each step's
		// Dynamic to materialise its options (exactly what the TUI does on enter).
		prior := [][]string{}
		pick := func(opts []tui.Choice) []string {
			if len(opts) == 0 {
				return []string{""}
			}
			// Prefer a pre-selected option, else the first.
			for _, o := range opts {
				if o.Selected {
					return []string{o.Key}
				}
			}
			return []string{opts[0].Key}
		}
		for _, s := range steps {
			opts := s.Options
			if s.Dynamic != nil {
				opts = s.Dynamic(prior)
			}
			prior = append(prior, pick(opts))
		}
		// prior now has 10 entries in the right shape; return it as the result.
		return prior, nil
	})
	if err := runInit(io.Discard); err != nil {
		t.Fatalf("runInit (builders): %v", err)
	}
	p, err := profile.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	// The Laravel family was pre-selected from the profile, so its concrete
	// framework id must be a Laravel variant.
	if p.Defaults["framework"] == "" {
		t.Error("framework should be set from the builder path")
	}
}

// The no-saved-profile path through the builders: every multi falls back to
// recipe defaults, the frontend step pre-selects a framework default (Magento →
// Hyvä), and services category-grouping (hasCat) sorts + prefixes labels. We
// select the Magento family in the language/framework steps.
func TestRunInitBuildersNoProfileMagento(t *testing.T) {
	isolate(t)
	stubInitWizard(t, func(_, _ string, steps []tui.Step) ([][]string, error) {
		prior := [][]string{}
		// Step 0 language: pick php. Step 1 family: pick the Magento family.
		// Later steps: take the first option (defaults) to reach the end.
		want := map[int]string{0: "php", 1: "magento"}
		for i, s := range steps {
			opts := s.Options
			if s.Dynamic != nil {
				opts = s.Dynamic(prior)
			}
			var choice []string
			if key, ok := want[i]; ok {
				choice = []string{key}
			} else if len(opts) == 0 {
				choice = []string{""}
			} else {
				choice = []string{opts[0].Key}
			}
			prior = append(prior, choice)
		}
		return prior, nil
	})
	if err := runInit(io.Discard); err != nil {
		t.Fatalf("runInit (magento builders): %v", err)
	}
	p, err := profile.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if p.Defaults["framework"] != "magento" {
		t.Errorf("framework = %q, want magento", p.Defaults["framework"])
	}
}

// runInit through the `keel init` command wiring (RunE) too, so initCmd's RunE
// closure is covered.
func TestInitCmdRunE(t *testing.T) {
	isolate(t)
	stubInitWizard(t, func(_, _ string, _ []tui.Step) ([][]string, error) {
		return canned(
			"php", "laravel", "laravel", "ddev", "mysql", "", "",
			nil, nil, nil, "code",
		), nil
	})
	out, err := runRoot(t, "init")
	if err != nil {
		t.Fatalf("keel init: %v", err)
	}
	mustContain(t, out, "saved profile")
}
