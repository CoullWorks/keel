package catalog

import (
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

// pureLibraries need no registration: importing them is the whole integration.
// Everything else has to appear somewhere in the project's configuration or it
// is installed and inert.
var pureLibraries = map[string]bool{
	"django-pillow":       true, // an image library other code imports
	"django-factory-boy":  true, // used from tests directly
	"django-model-bakery": true, // used from tests directly
	"django-environ":      true, // the settings file already reads it
	"django-anymail":      true, // configured per backend when you pick one
	"django-simplejwt":    true, // ships its own files and settings hook
	"django-pytest":       true, // a test runner; configured via pyproject [tool.pytest], not Django settings
}

// TestDjangoAddonsRegisterThemselves is the guard for the worst class of defect
// in this catalogue: an add-on that installs a package and enables nothing.
//
// `keel new django --with django-debug-toolbar` used to install the package and
// stop. The toolbar never appeared, because it does nothing until it is in
// INSTALLED_APPS, in MIDDLEWARE, in INTERNAL_IPS and mounted on a URL. The build
// succeeded, the tests passed and the feature simply was not there. Two of these
// were default:true, so every Django project shipped with them.
//
// Nothing keel could do about it either: file patching only understood dotenv,
// so no recipe could edit settings.py. Hence config/settings.d and config/urls.d.
func TestDjangoAddonsRegisterThemselves(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range reg.ForFramework("django", recipe.Addon) {
		if pureLibraries[a.ID] {
			continue
		}
		var registers bool
		for _, f := range a.Files {
			if strings.HasPrefix(f.Path, "config/settings.d/") ||
				strings.HasPrefix(f.Path, "config/urls.d/") ||
				strings.HasPrefix(f.Path, "config/") {
				registers = true
			}
		}
		if !registers {
			t.Errorf("%s installs a package but registers it nowhere: it will be inert. "+
				"Add a config/settings.d fragment, or list it in pureLibraries with a reason.", a.ID)
		}
	}
}

// TestDjangoLoadsItsFragments: the fragments are only worth writing if the
// framework reads them. Removing either loader would leave every add-on inert
// again, silently.
func TestDjangoLoadsItsFragments(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := resolver.Resolve(reg, []string{"django", "django-docker", "postgres"})
	if err != nil {
		t.Fatal(err)
	}
	files := engine.RenderedFiles(plan, "proj")
	for path, want := range map[string]string{
		"config/settings.py": "settings.d",
		"config/urls.py":     "urls.d",
	} {
		if !strings.Contains(files[path], want) {
			t.Errorf("%s does not load %s, so add-on fragments would never run", path, want)
		}
	}
}
