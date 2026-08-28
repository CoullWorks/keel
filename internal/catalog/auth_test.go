package catalog

import (
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

// authScaffolds pins the per-stack auth scaffolds: each must resolve against its
// framework, provide the `auth` capability, drop its key files and render a
// token-free smoke step that proves the sign-in surface exists.
//
// This is the guard the task asked for: an auth scaffold that stops resolving,
// stops providing `auth`, or loses its route handler is a silent regression -
// the build still succeeds and the feature is simply gone (the exact failure
// mode TestDjangoAddonsRegisterThemselves was written for).
func TestAuthScaffoldsResolveAndProvideAuth(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		// framework the scaffold belongs to.
		framework string
		// recipe under test.
		recipe string
		// extras needed to satisfy the recipe's requires (e.g. a db).
		extras []string
		// files that must be rendered (the route handler / login endpoint).
		wantFiles []string
		// a substring every plan of this scaffold's smoke steps must contain,
		// proving the sign-in surface is exercised.
		wantSmoke string
	}{
		{
			name:      "Next.js Auth.js v5",
			framework: "nextjs",
			recipe:    "nextjs-authjs",
			wantFiles: []string{
				"app/api/auth/[...nextauth]/route.ts", // the Auth.js route handler
				"auth.ts",                             // the auth config
				"app/signin/page.tsx",                 // sign-in page
				"app/register/page.tsx",               // register page
				"app/reset-password/page.tsx",         // password reset page
				"middleware.ts",                       // middleware-protected route gate
				"app/dashboard/page.tsx",              // the protected route
			},
			wantSmoke: "npm run build",
		},
		{
			name:      "Django DRF session login",
			framework: "django",
			recipe:    "django-next",
			wantFiles: []string{
				"accounts/views.py",                // login/register/reset views
				"accounts/urls.py",                 // the wired URLs
				"config/urls.d/50-accounts.py",     // mounts them under /api/auth/
				"config/settings.d/50-accounts.py", // registers the app
			},
			wantSmoke: "check",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, ok := reg.Get(tc.recipe)
			if !ok {
				t.Fatalf("recipe %s is not in the catalogue", tc.recipe)
			}
			// The scaffold must advertise the auth capability, so `--with` shows it
			// as auth and a second auth recipe conflicts rather than double-wiring.
			if !provides(r, "auth") {
				t.Errorf("%s must provide the `auth` capability", tc.recipe)
			}

			ids := []string{tc.framework, defaultEnv(reg, tc.framework)}
			if db := defaultDB(reg, tc.framework, defaultEnv(reg, tc.framework)); db != "" {
				ids = append(ids, db)
			}
			ids = append(ids, tc.extras...)
			ids = append(ids, tc.recipe)

			plan, err := resolver.Resolve(reg, ids)
			if err != nil {
				t.Fatalf("resolve %v: %v", ids, err)
			}

			files := engine.RenderedFiles(plan, "proj")
			for _, want := range tc.wantFiles {
				body, ok := files[want]
				if !ok {
					t.Errorf("%s: expected file %q was not rendered", tc.recipe, want)
					continue
				}
				if strings.TrimSpace(body) == "" {
					t.Errorf("%s: expected file %q is empty", tc.recipe, want)
				}
				// A leaked keel command token in a file body renders to a broken
				// path/import. JSX/TS legitimately contains `{{ … }}` (inline
				// styles) and `${…}`, so only the real env-command tokens are
				// checked, not any brace pair.
				for _, tok := range []string{"{{env}}", "{{exec}}", "{{project}}", "{{create}}", "{{start}}"} {
					if strings.Contains(body, tok) {
						t.Errorf("%s: file %q leaks the unrendered token %s", tc.recipe, want, tok)
					}
				}
			}

			smoke := engine.SmokeSteps(plan, "proj")
			var found bool
			for _, s := range smoke {
				if strings.Contains(s, "{{") {
					t.Errorf("%s: smoke step leaks a token: %q", tc.recipe, s)
				}
				if strings.Contains(s, tc.wantSmoke) {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: no smoke step contains %q, so the sign-in surface is unproven: %v",
					tc.recipe, tc.wantSmoke, smoke)
			}
		})
	}
}

func provides(r recipe.Recipe, cap string) bool {
	for _, p := range r.Provides {
		if p == cap {
			return true
		}
	}
	return false
}
