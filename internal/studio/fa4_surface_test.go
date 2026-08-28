package studio

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/coullworks/keel/plugin"
)

// These pin the frontend the same coarse way the other *_surface tests do: by
// reading what index.html declares, paired with TestThePageParses proving the
// inline script actually runs. They cover the three frontend tasks the studio
// wave finished — command-execution toasts (FA4), the deploy-target picker, and
// the per-capability grant UI — plus the read-DTO that feeds the last one.

// FA4: the command-execution notifications exist and stream() consumes the done
// status rather than discarding it. The bug this guards against is the original
// `if(f.startsWith("event: done"))continue;`, which threw away the {ok,code,err}
// payload so a collapsed console got no pass/fail signal.
func TestPageHasCommandToasts(t *testing.T) {
	for _, want := range []string{
		`id="toasts"`,     // the container the toasts mount into
		`aria-live`,       // announced to a screen reader
		"function toast(", // the raise/replace helper
		"function dismissToast(",
		"function streamLabel(", // human label for the running/result toast
		`kind:"running"`,        // a sticky running toast while the command streams
		`kind:"ok"`,             // replaced by ok…
		`kind:"bad"`,            // …or bad on the result
		"JSON.parse(m[1])",      // the done frame's data: JSON is parsed
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("the command-toast surface is missing %q", want)
		}
	}
	// The done frame must be PARSED for its status, not skipped. The old code
	// unconditionally `continue`d on an event: done line; the fix reads the
	// {ok,code,err} it carries.
	if strings.Contains(indexHTML, `if(f.startsWith("event: done"))continue;`) {
		t.Error("stream() still discards the event: done status frame instead of parsing it")
	}
	// A failed command reveals the console so the toast's detail is readable.
	if !strings.Contains(indexHTML, "revealConsole(") {
		t.Error("a failed stream must be able to reveal the console drawer")
	}
	// The collapsed-console activity badge exists and is wired.
	for _, want := range []string{`id="termact"`, "function termCollapsed(", "noteTermActivity("} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("the collapsed-console activity signal is missing %q", want)
		}
	}
}

// The Deploy tab reads the backend target listing rather than hardcoding two
// buttons, renders a picker, and generates via stream() so it gets the FA4
// toasts.
func TestDeployTabUsesTargetsEndpoint(t *testing.T) {
	for _, want := range []string{
		"/api/deploy/targets",        // the source of truth (shells keel deploy --json)
		"function renderDeploy(",     // still the tab's renderer
		"function pickDeployTarget(", // selecting a target re-renders
		"rlistHTML(",                 // the picker reuses the existing radio list
		`args:["deploy",`,            // generate runs keel deploy <target> via exec
		`"--dry-run"`,                // preview is the dry run
		"PROFILE.hosting",            // defaults to the profile hosting default when offered
		`stream("/api/exec"`,         // via stream() so it gets the toasts
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("the Deploy tab is missing %q", want)
		}
	}
	// The old two-button deploy surface is gone — it used projExec, not a picker.
	if strings.Contains(indexHTML, `projExec(${d},["deploy"])`) {
		t.Error("the old hardcoded two-button deploy surface is still present")
	}
}

// handleDeployTargets passes the binary's `keel deploy --json` output straight
// through — {framework,targets:[{key,desc,artifacts}]} — the exact shape the
// Deploy tab's picker renders. Driven through the runCapture seam so no real
// subprocess runs; this is the backend contract the deploy picker depends on.
func TestHandleDeployTargetsPassesThroughTheListing(t *testing.T) {
	isolateConfig(t)
	restore := runCapture
	runCapture = func(_ context.Context, _ string, _ ...string) (string, error) {
		return `{"framework":"laravel","targets":[{"key":"compose","desc":"Docker Compose","artifacts":["Dockerfile","compose.yaml"]},{"key":"fly","desc":"Fly.io"}]}`, nil
	}
	defer func() { runCapture = restore }()

	w := muxGet(testMux(), "/api/deploy/targets")
	if w.Code != 200 {
		t.Fatalf("deploy targets: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{`"key":"compose"`, `"desc":"Docker Compose"`, `"Dockerfile"`, `"key":"fly"`, `"framework":"laravel"`} {
		if !strings.Contains(body, want) {
			t.Errorf("deploy targets response missing %q: %s", want, body)
		}
	}
}

// The plugin detail draws each declared capability with a grant/revoke toggle
// that calls the /api/plugins grant|ungrant action with the fields the endpoint
// expects, and makes the trust-first requirement visible.
func TestPluginCardHasCapabilityGrantUI(t *testing.T) {
	for _, want := range []string{
		"function pluginCapsHTML(",              // the per-capability rows
		"data-cap-act",                          // the grant/ungrant toggle
		"data-cap",                              // carries which capability
		`action:b.getAttribute("data-cap-act")`, // wired to pluginAction
		"cap:b.getAttribute",                    // sends the cap field the backend reads
		"No capabilities requested",             // the honest empty state (built-ins)
		"Trust",                                 // caps require trust first — surfaced
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("the capability grant UI is missing %q", want)
		}
	}
	// An untrusted plugin's grant buttons are disabled-with-reason, not silently
	// dead — the button is disabled and titled with why.
	if !strings.Contains(indexHTML, "disabled title=") {
		t.Error("an untrusted plugin's capability toggles must be disabled-with-reason")
	}
}

// The listPlugins read-DTO exposes an installed plugin's declared capabilities
// and the granted subset, so the UI can draw the per-capability grant toggle.
// This is the backend half the capability grant UI depends on. It is driven the
// same real way the other studio plugin tests are: a source on disk installed
// through the API, then trusted (which grants the declared set).
func TestListPluginsExposesCapabilities(t *testing.T) {
	isolateConfig(t)
	mux := testMux()

	// A plugin source declaring one capability (net) in its manifest.
	src := filepath.Join(t.TempDir(), "capdemo")
	if err := os.MkdirAll(filepath.Join(src, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema: 1\nname: capdemo\nversion: 1.0.0\ndescription: caps\nauthor: t\nlicense: MIT\ncapabilities:\n  - net\n"
	if err := os.WriteFile(filepath.Join(src, "config", "register.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := muxPost(mux, "/api/plugins", `{"action":"add","source":`+strconv.Quote(src)+`}`); w.Code != 200 {
		t.Fatalf("install: %d %s", w.Code, w.Body.String())
	}

	// Before trust: the capability is DECLARED but not granted.
	row := findDTO(t, listPlugins(), "capdemo")
	if !hasCap(row.Capabilities, string(plugin.CapNet)) {
		t.Errorf("declared capabilities = %v, want it to include %q", row.Capabilities, plugin.CapNet)
	}
	if hasCap(row.Granted, string(plugin.CapNet)) {
		t.Errorf("granted = %v before trust, want empty", row.Granted)
	}

	// Trusting grants the declared set, so the DTO now reports it granted.
	if w := muxPost(mux, "/api/plugins", `{"action":"trust","name":"capdemo"}`); w.Code != 200 {
		t.Fatalf("trust: %d %s", w.Code, w.Body.String())
	}
	row = findDTO(t, listPlugins(), "capdemo")
	if !hasCap(row.Granted, string(plugin.CapNet)) {
		t.Errorf("granted capabilities = %v after trust, want it to include %q", row.Granted, plugin.CapNet)
	}

	// Ungranting the one capability drops it from the granted set but leaves it
	// declared — the visible, actionable gap the grant toggle draws.
	if w := muxPost(mux, "/api/plugins", `{"action":"ungrant","name":"capdemo","cap":"net"}`); w.Code != 200 {
		t.Fatalf("ungrant: %d %s", w.Code, w.Body.String())
	}
	row = findDTO(t, listPlugins(), "capdemo")
	if hasCap(row.Granted, string(plugin.CapNet)) {
		t.Errorf("granted = %v after ungrant, want empty", row.Granted)
	}
	if !hasCap(row.Capabilities, string(plugin.CapNet)) {
		t.Errorf("declared = %v after ungrant, want it still to include %q", row.Capabilities, plugin.CapNet)
	}
}

// findDTO returns the plugin row by name, failing the test if it is absent.
func findDTO(t *testing.T, rows []pluginDTO, name string) pluginDTO {
	t.Helper()
	for _, r := range rows {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("the DTO did not include the installed plugin %q", name)
	return pluginDTO{}
}

func hasCap(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
