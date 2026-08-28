package studio

import (
	"strings"
	"testing"
)

// The Env & Secrets tab was action-only (Sync .env / Generate a key) and never
// listed the project's real variables, so on a Next.js app, a monorepo member or
// Magento you saw nothing. This surface test (reads what index.html declares,
// paired with TestThePageParses proving the block runs) pins that renderSecrets
// now draws an Environment variables section — fed by /api/env — ABOVE the
// existing actions, with the public/secret classification the reader returns.
func TestRenderSecretsListsEnvVars(t *testing.T) {
	for _, want := range []string{
		`function renderSecrets(`,   // the tab renderer
		`id="envvars"`,              // the env section host
		`function loadEnvVars(`,     // the loader
		`fetchJSON("/api/env?dir="`, // fetches the resolved env
		`function envVarRow(`,       // renders one variable
		`v.public`,                  // the public/secret classification
		`••• present`,               // secrets are masked, never the value
		`Environment variables`,     // the section heading
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("renderSecrets is missing the env-listing surface: %q", want)
		}
	}

	// The env section must be rendered ABOVE the Sync .env action, not below it:
	// the developer's real variables are the point of the tab, the actions are
	// secondary.
	envIdx := strings.Index(indexHTML, `+`+"`"+`<div id="envvars"></div>`+"`")
	syncIdx := strings.Index(indexHTML, `<h3>Sync .env</h3>`)
	if envIdx < 0 || syncIdx < 0 {
		t.Fatalf("could not locate the env section (%d) or the Sync .env action (%d)", envIdx, syncIdx)
	}
	if envIdx > syncIdx {
		t.Errorf("the Environment variables section must render above the Sync/Generate actions")
	}
}
