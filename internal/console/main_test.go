package console

import (
	"os"
	"testing"

	"github.com/coullworks/keel/internal/selfupdate"
)

// TestMain disables the self-update check for the whole package. New() calls
// selfupdate.Notice, which returns an "upgrade available" line by reading a cache
// a background refresh populates. Once a keel release exists, a test binary
// (version "dev") sees that release as an available upgrade, and the footer shows
// the upgrade line INSTEAD of the "Support keel" sponsor (they are mutually
// exclusive by design). Because the refresh is asynchronous, whether the cache is
// populated depends on test order and timing, so the render tests failed
// intermittently once v1.0.0 was published. Turning the check off here makes the
// footer deterministic. A test that wants the upgrade path can re-enable it with
// t.Setenv(selfupdate.DisableEnv, "").
func TestMain(m *testing.M) {
	os.Setenv(selfupdate.DisableEnv, "1")
	os.Exit(m.Run())
}
