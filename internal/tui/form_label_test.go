package tui

import (
	"testing"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/profile"
)

// TestEveryLanguageHasALabel: the language step is the first thing anyone sees,
// and an unlabelled id sits there in lower case next to "PHP" and "Python"
// looking like a bug. Adding a language to the catalogue must not silently
// produce one.
func TestEveryLanguageHasALabel(t *testing.T) {
	reg, err := catalog.Registry()
	if err != nil {
		t.Skip("no catalogue")
	}
	steps := BuildSteps(reg, profile.Default())
	if len(steps) == 0 {
		t.Fatal("no steps")
	}
	for _, o := range steps[0].Options {
		if o.Label == o.Key {
			t.Errorf("language %q renders as its raw id — add it to langLabel", o.Key)
		}
	}
}

// TestTheWizardAsksForAWebServer.
//
// The web server is the only ingress a built stack has: no environment recipe
// publishes a host port, so a plan without one produces healthy containers and
// a site_url that refuses the connection. It also has to be one question rather
// than two tick-boxes, because NGINX and Apache conflict - offering them in the
// Services list let you tick both (a resolve error at the end of the wizard) or
// neither (a stack you cannot open).
//
// The console renders these same steps, so asserting here covers both front
// doors; the studio's copy is guarded in internal/studio.
func TestTheWizardAsksForAWebServer(t *testing.T) {
	reg, err := catalog.Registry()
	if err != nil {
		t.Fatal(err)
	}
	steps := BuildSteps(reg, &profile.Profile{Defaults: map[string]string{}})

	var web *Step
	for i := range steps {
		if steps[i].Title == "Web server" {
			web = &steps[i]
		}
		if steps[i].Multi && steps[i].Title == "Services" {
			// Services must not offer them a second time.
			for _, c := range steps[i].Dynamic([][]string{{"php"}, {"laravel"}, {"laravel"}}) {
				if c.Key == "nginx" || c.Key == "apache" {
					t.Errorf("the Services step still offers %q, which the Web server step owns", c.Key)
				}
			}
		}
	}
	if web == nil {
		t.Fatal("no Web server step: a built stack has no other way in")
	}
	if web.Multi {
		t.Error("the Web server step is multi-select, so both web servers can be chosen and they conflict")
	}
	opts := web.Dynamic([][]string{{"php"}, {"laravel"}, {"laravel"}})
	var selected, none int
	for _, o := range opts {
		if o.Selected {
			selected++
		}
		// The opt-out is keyed profile.NoWebServer (unified with keel init), not
		// the empty string it used to carry.
		if o.Key == profile.NoWebServer {
			none++
		}
	}
	if selected != 1 {
		t.Errorf("want exactly one web server pre-selected, got %d", selected)
	}
	if none != 1 {
		t.Error("want an explicit opt-out option, so choosing no web server is a decision rather than an oversight")
	}
}
