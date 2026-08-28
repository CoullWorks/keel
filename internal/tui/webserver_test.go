package tui

import (
	"testing"

	"github.com/coullworks/keel/internal/profile"
	"github.com/coullworks/keel/internal/recipe"
)

// SavedWebServer has to tell three states apart, and both wizards pre-select
// from it. Confusing "never asked" with "declined" is what shipped stacks with
// nothing listening on them, so each state gets a case of its own.
func TestSavedWebServerTellsUnansweredFromDeclined(t *testing.T) {
	services := []recipe.Recipe{
		{ID: "nginx", Kind: recipe.Service, Provides: []string{recipe.WebServer}},
		{ID: "apache", Kind: recipe.Service, Provides: []string{recipe.WebServer}},
		{ID: "redis", Kind: recipe.Service},
	}

	for _, tc := range []struct {
		name         string
		defaults     map[string]string
		wantID       string
		wantAnswered bool
	}{{
		name:     "a profile written before the question existed has not answered it",
		defaults: map[string]string{"services": "redis"},
	}, {
		name:         "an explicit choice is the answer",
		defaults:     map[string]string{"webserver": "apache", "services": "redis"},
		wantID:       "apache",
		wantAnswered: true,
	}, {
		name:         "declining is an answer, not a silence",
		defaults:     map[string]string{"webserver": profile.NoWebServer},
		wantID:       profile.NoWebServer,
		wantAnswered: true,
	}, {
		// The layout `keel init` wrote before the key existed.
		name:         "a web server left in the services list is read as the answer",
		defaults:     map[string]string{"services": "nginx,redis"},
		wantID:       "nginx",
		wantAnswered: true,
	}, {
		name:     "an empty profile has not answered it",
		defaults: map[string]string{},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			id, answered := SavedWebServer(services, &profile.Profile{Defaults: tc.defaults})
			if id != tc.wantID || answered != tc.wantAnswered {
				t.Errorf("SavedWebServer = (%q, %v), want (%q, %v)",
					id, answered, tc.wantID, tc.wantAnswered)
			}
		})
	}
}

// DeclinedWebServer separates "the user chose None" (honour it) from "no web
// server ended up in the plan for any other reason" (re-add the default). Only
// the first is a real decline; runNewInteractive's safety net keys off this.
//
// The decline sentinel is profile.NoWebServer, unified with `keel init` and
// SavedWebServer. The empty-string case is called out on its own: it used to be
// the decline sentinel, and a bare "" at this step now means "no answer" — NOT a
// decline — so the two must not be confused again.
func TestDeclinedWebServer(t *testing.T) {
	// res index 4 is the web server step (see webServerStep).
	full := func(ws string) [][]string {
		return [][]string{{"php"}, {"laravel"}, {"laravel"}, {"sail"}, {ws}, {"mysql"}}
	}
	for _, tc := range []struct {
		name string
		res  [][]string
		want bool
	}{
		{"explicit None decline", full(profile.NoWebServer), true},
		// The previously-masked case: "" was the old decline key. With the opt-out
		// unified on profile.NoWebServer, an empty answer is no longer a decline,
		// so the safety net re-adds the default rather than shipping no ingress.
		{"empty key is not a decline (the pre-unification sentinel)", full(""), false},
		{"chose nginx", full("nginx"), false},
		{"nil result (flag path / never asked)", nil, false},
		{"result too short to reach the step", [][]string{{"php"}, {"laravel"}}, false},
		{"multi-answer at the step is not a single decline", [][]string{{"a"}, {"b"}, {"c"}, {"d"}, {profile.NoWebServer, "x"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeclinedWebServer(tc.res); got != tc.want {
				t.Errorf("DeclinedWebServer = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestBuildStepsWebServerNoneKeyIsNoWebServer pins the unification end to end:
// the "None" option BuildSteps renders must carry the profile.NoWebServer key
// (not the empty string), so a "None" chosen in `keel new` is the same decline
// value `keel init` records. It also proves that decline does not leak into the
// recipe ids: IDsFromResult drops the sentinel exactly as it drops the empty
// "no frontend" key, so the resolver never sees a bogus "none" recipe.
func TestBuildStepsWebServerNoneKeyIsNoWebServer(t *testing.T) {
	reg := recipe.NewRegistry()
	for _, r := range []recipe.Recipe{
		{ID: "laravel", Kind: recipe.Framework, Lang: "php", Label: "Laravel"},
		{ID: "nginx", Kind: recipe.Service, Label: "NGINX", Provides: []string{recipe.WebServer}, Default: true},
	} {
		if err := reg.Add(r); err != nil {
			t.Fatal(err)
		}
	}
	steps := BuildSteps(reg, &profile.Profile{Defaults: map[string]string{}})
	// Drive the web server step's Dynamic to see the rendered "None" option.
	// A prefix answer of [php],[laravel],[laravel],[sail] lets fwOf resolve.
	choices := steps[webServerStep].Dynamic([][]string{{"php"}, {"laravel"}, {"laravel"}, {"sail"}})
	var none *Choice
	for i := range choices {
		if choices[i].Label == "None (I'll front it myself)" {
			none = &choices[i]
		}
	}
	if none == nil {
		t.Fatal("the web server step must offer a None option")
	}
	if none.Key != profile.NoWebServer {
		t.Errorf("None key = %q, want %q (unified with keel init)", none.Key, profile.NoWebServer)
	}

	// A result that chose None must be read as a decline AND drop the sentinel.
	res := [][]string{{"php"}, {"laravel"}, {"laravel"}, {"sail"}, {profile.NoWebServer}, {"mysql"}}
	if !DeclinedWebServer(res) {
		t.Error("choosing None must read as a decline")
	}
	for _, id := range IDsFromResult(res) {
		if id == profile.NoWebServer {
			t.Errorf("the %q decline sentinel leaked into the recipe ids", profile.NoWebServer)
		}
	}
}

// The webServerStep constant hardcodes the step's position, so a reorder of
// BuildSteps must move it too. This pins the position to the real step titled
// "Web server" so the two cannot silently drift.
func TestWebServerStepIndexMatchesBuildSteps(t *testing.T) {
	reg := recipe.NewRegistry()
	// A minimal framework + the two web servers, enough for BuildSteps to render.
	for _, r := range []recipe.Recipe{
		{ID: "laravel", Kind: recipe.Framework, Lang: "php", Label: "Laravel"},
		{ID: "nginx", Kind: recipe.Service, Label: "NGINX", Provides: []string{recipe.WebServer}, Default: true},
	} {
		if err := reg.Add(r); err != nil {
			t.Fatal(err)
		}
	}
	steps := BuildSteps(reg, &profile.Profile{Defaults: map[string]string{}})
	if webServerStep >= len(steps) || steps[webServerStep].Title != "Web server" {
		got := "out of range"
		if webServerStep < len(steps) {
			got = steps[webServerStep].Title
		}
		t.Errorf("webServerStep points at %q, want the %q step", got, "Web server")
	}
}
