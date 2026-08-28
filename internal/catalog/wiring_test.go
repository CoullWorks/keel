package catalog

import (
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/resolver"
)

// TestChosenDatabaseReachesTheApp is the regression test for a whole class of
// silent misbuild: keel provisions the database the user asked for, and the
// application carries on using whatever connection string its own scaffold
// shipped with. The server is up, connected to nothing, and nothing says so.
func TestChosenDatabaseReachesTheApp(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ fw, env, db, want string }{
		{"fastapi", "fastapi-ddev", "postgres", "postgresql"},
		{"fastapi", "fastapi-local", "postgres", "postgresql"},
		{"nextjs", "nextjs-local", "postgres", "postgresql"},
		{"nextjs", "nextjs-docker", "postgres", "postgresql"},
		{"laravel", "ddev", "postgres", "pgsql"},
		{"laravel", "sail", "postgres", "pgsql"},
	} {
		t.Run(tc.fw+"/"+tc.env+"/"+tc.db, func(t *testing.T) {
			plan, err := resolver.Resolve(reg, []string{tc.fw, tc.env, tc.db})
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			eff := engine.Effective(plan, "app")
			if !strings.Contains(eff, tc.want) {
				t.Errorf("the app is never told about the chosen database (expected %q in the patches):\n%s", tc.want, eff)
			}
			// And it must not still be pointing at the scaffold's default.
			if strings.Contains(eff, "sqlite:///./app.db") {
				t.Errorf("%s still writes the SQLite default while %s was provisioned", tc.fw, tc.db)
			}
			if strings.Contains(eff, "app:secret@localhost:5432") {
				t.Errorf("%s still writes its hardcoded compose connection string", tc.fw)
			}
		})
	}
}
