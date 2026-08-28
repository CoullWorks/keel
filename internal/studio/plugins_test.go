package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/plugins"
	"github.com/coullworks/keel/internal/pluginstore"
	"github.com/coullworks/keel/internal/plugintest"
	"github.com/coullworks/keel/plugin"
)

func TestPluginScreensAreListed(t *testing.T) {
	isolateConfig(t)
	f := plugintest.Install(t, "demo")
	reloadRegistry()

	mux := newMux("tok", nil)
	req := httptest.NewRequest("GET", "/api/plugins/screens", nil)
	req.Host = "127.0.0.1:7777"
	req.Header.Set("X-Keel-Token", "tok")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		Screens []struct {
			ID, Title string
		} `json:"screens"`
		Problems []string `json:"problems"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, rec.Body)
	}
	// keel ships zero built-ins, so a discovered plugin's screens are what the
	// listing must carry — both the static and the live one it declares. Listing a
	// screen never runs the plugin, so no trust is needed here.
	ids := map[string]bool{}
	for _, s := range got.Screens {
		ids[s.ID] = true
	}
	for _, want := range []string{f.StaticScreen(), f.LiveScreen()} {
		if !ids[want] {
			t.Errorf("screen %q not listed, got %+v", want, got.Screens)
		}
	}
	// A plugin that failed to register is exactly what someone needs told about.
	if len(got.Problems) > 0 {
		t.Errorf("a plugin failed to register: %v", got.Problems)
	}
}

// GET /api/plugins must return {plugins:[…]}, the shape every reader on the page
// unwraps. A Round-2 regression returned a bare JSON array, so the page saw no
// .plugins field and drew "Nothing installed yet". This asserts the wrapper AND
// that a discovered plugin is in it (keel ships zero built-ins, so a discovered
// one is what the list must carry).
func TestPluginsGETReturnsWrappedList(t *testing.T) {
	isolateConfig(t)
	f := plugintest.Install(t, "demo")
	reloadRegistry()
	w := muxGet(testMux(), "/api/plugins")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	// A bare array would fail to decode into a struct with a plugins field, which is
	// exactly the shape the page assumes — so decode the way the page reads it.
	var got struct {
		Plugins []struct {
			Name    string `json:"name"`
			BuiltIn bool   `json:"builtIn"`
			Where   string `json:"where"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("GET /api/plugins is not {plugins:[…]}: %v\n%s", err, w.Body.String())
	}
	if len(got.Plugins) == 0 {
		t.Fatalf("the list is empty; the page would say \"nothing installed\": %s", w.Body.String())
	}
	// The discovered plugin is present, and shown as an installed plugin — not a
	// built-in, because keel bundles none.
	var found bool
	for _, p := range got.Plugins {
		if p.Name == f.Name {
			found = true
			if p.BuiltIn {
				t.Errorf("a discovered plugin must not be marked built-in: %+v", p)
			}
			if p.Where != "installed" {
				t.Errorf("a discovered plugin's WHERE = %q, want installed", p.Where)
			}
		}
	}
	if !found {
		t.Errorf("the discovered plugin %q is missing from the list: %+v", f.Name, got.Plugins)
	}
}

// Each plugin's screen must be keyed by its own id and resolve to ITS OWN screen —
// one plugin's tab must never render another's screen. The registry is the single
// source that maps an id to a screen, so this asserts screens do not collide:
// distinct ids, distinct titles, each resolving to itself.
func TestEachPluginScreenMapsToItsOwnScreen(t *testing.T) {
	isolateConfig(t)
	f := plugintest.Install(t, "demo")
	reloadRegistry()
	reg := registry()

	// Every registered screen resolves back to itself by id: no id shadows another.
	seen := map[string]string{}
	for _, s := range reg.Screens() {
		got, ok := reg.Screen(s.ID)
		if !ok {
			t.Errorf("screen %q is listed but does not resolve by id", s.ID)
			continue
		}
		if got.Title != s.Title {
			t.Errorf("screen id %q resolved to a different screen: got title %q, want %q", s.ID, got.Title, s.Title)
		}
		if prev, dup := seen[s.ID]; dup {
			t.Errorf("two plugins claim screen id %q (%q and %q): a routing collision", s.ID, prev, s.Title)
		}
		seen[s.ID] = s.Title
	}

	// The plugin's two screens own distinct, non-colliding ids and titles.
	stat, ok := reg.Screen(f.StaticScreen())
	if !ok || stat.Title != plugintest.StaticTitle {
		t.Errorf("static screen wrong: ok=%v title=%q, want %q", ok, stat.Title, plugintest.StaticTitle)
	}
	live, ok := reg.Screen(f.LiveScreen())
	if !ok || live.Title != plugintest.LiveTitle {
		t.Errorf("live screen wrong: ok=%v title=%q, want %q", ok, live.Title, plugintest.LiveTitle)
	}
}

// A screen endpoint that took any path from the browser would let a plugin read
// anywhere on disk. With a real screen present, the untracked path is what must
// be refused — before the plugin is ever asked to render.
func TestScreenRefusesAnUntrackedPath(t *testing.T) {
	isolateConfig(t)
	f := plugintest.Install(t, "demo")
	reloadRegistry()
	mux := newMux("tok", nil)
	req := httptest.NewRequest("POST", "/api/plugins/screen",
		strings.NewReader(`{"id":"`+f.StaticScreen()+`","dir":"/etc"}`))
	req.Host = "127.0.0.1:7777"
	req.Header.Set("X-Keel-Token", "tok")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), "error") {
		t.Errorf("an untracked path should be refused, got: %s", rec.Body)
	}
}

func TestScreenEndpointNeedsTheToken(t *testing.T) {
	mux := newMux("tok", nil)
	req := httptest.NewRequest("GET", "/api/plugins/screens", nil)
	req.Host = "127.0.0.1:7777"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Errorf("a request with no token should be refused, got %d", rec.Code)
	}
}

// muxPost serves a loopback POST with a valid session token, the way the served
// page does.
func muxPost(mux *http.ServeMux, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "http://127.0.0.1"+path, strings.NewReader(body))
	req.Host = "127.0.0.1"
	req.Header.Set(tokenHeader, testTok)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// TestStudioPluginsInstallAndToggle: the studio has to be able to do everything
// the console can. Managing plugins in one but not the other is exactly the
// split this work exists to remove.
func TestStudioPluginsInstallAndToggle(t *testing.T) {
	isolateConfig(t)
	mux := testMux()

	// A plugin source on disk, installed through the API.
	src := filepath.Join(t.TempDir(), "studiodemo")
	if err := os.MkdirAll(filepath.Join(src, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema: 1\nname: studiodemo\nversion: 1.0.0\ndescription: via studio\nauthor: t\nlicense: MIT\n"
	if err := os.WriteFile(filepath.Join(src, "config", "register.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	w := muxPost(mux, "/api/plugins", `{"action":"add","source":`+strconv.Quote(src)+`}`)
	if w.Code != 200 {
		t.Fatalf("install: %d %s", w.Code, w.Body.String())
	}
	var res struct {
		OK      bool `json:"ok"`
		Plugins []struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	// The response lists every plugin, built-ins included, so the new one is
	// looked for rather than assumed to be the only row.
	var installed bool
	for _, p := range res.Plugins {
		if p.Name == "studiodemo" {
			installed = p.Enabled
		}
	}
	if !res.OK || !installed {
		t.Fatalf("install did not take: %s", w.Body.String())
	}

	// Disabling through the studio must persist, exactly as it does in the CLI.
	if w := muxPost(mux, "/api/plugins", `{"action":"disable","name":"studiodemo"}`); w.Code != 200 {
		t.Fatalf("disable: %d %s", w.Code, w.Body.String())
	}
	if p, ok := pluginstore.Get("studiodemo"); !ok || p.Enabled {
		t.Error("disabling through the studio did not persist")
	}

	// And removing it leaves nothing.
	if w := muxPost(mux, "/api/plugins", `{"action":"remove","name":"studiodemo"}`); w.Code != 200 {
		t.Fatalf("remove: %d %s", w.Code, w.Body.String())
	}
	if _, ok := pluginstore.Get("studiodemo"); ok {
		t.Error("remove through the studio left the plugin behind")
	}
}

// TestStudioPluginsRejectsUnknownAction: the action is user input reaching a
// switch, so anything unrecognised must be refused rather than silently ignored.
func TestStudioPluginsRejectsUnknownAction(t *testing.T) {
	isolateConfig(t)
	if w := muxPost(testMux(), "/api/plugins", `{"action":"exec","name":"x"}`); w.Code != 400 {
		t.Fatalf("unknown action must be 400, got %d", w.Code)
	}
}

// TestStudioPluginsRejectsMissingArg: a request missing the field its action
// needs (a name to toggle, a source to add, a cap to grant) must be refused with
// a clear 400 BEFORE it reaches a store/git call — so the page shows an
// actionable message rather than a cryptic downstream error, and a built-in
// toggle can never write a garbage empty-named index record.
func TestStudioPluginsRejectsMissingArg(t *testing.T) {
	isolateConfig(t)
	cases := []struct {
		name string
		body string
		want string // a substring the 400 body must contain
	}{
		{"add without source", `{"action":"add"}`, "source"},
		{"enable without name", `{"action":"enable"}`, "name"},
		{"disable without name", `{"action":"disable"}`, "name"},
		{"trust without name", `{"action":"trust"}`, "name"},
		{"untrust without name", `{"action":"untrust"}`, "name"},
		{"remove without name", `{"action":"remove"}`, "name"},
		{"grant without name", `{"action":"grant","cap":"net"}`, "name"},
		{"grant without cap", `{"action":"grant","name":"x"}`, "capability"},
		{"ungrant without cap", `{"action":"ungrant","name":"x"}`, "capability"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := muxPost(testMux(), "/api/plugins", tc.body)
			if w.Code != 400 {
				t.Fatalf("want 400, got %d (%s)", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tc.want) {
				t.Errorf("400 body %q does not mention %q", strings.TrimSpace(w.Body.String()), tc.want)
			}
		})
	}
}

// TestMissingPluginArg unit-tests the contract table directly: a well-formed
// request for each action passes, a blank required field is named.
func TestMissingPluginArg(t *testing.T) {
	tests := []struct {
		action, name, source, cap string
		wantEmpty                 bool
	}{
		{"add", "", "./x", "", true},
		{"add", "", "", "", false},
		{"enable", "p", "", "", true},
		{"enable", "", "", "", false},
		{"grant", "p", "", "net", true},
		{"grant", "p", "", "", false},
		{"grant", "", "", "net", false},
		{"unknown-action", "", "", "", true}, // presence-only: the switch handles unknown
	}
	for _, tc := range tests {
		got := missingPluginArg(tc.action, tc.name, tc.source, tc.cap)
		if (got == "") != tc.wantEmpty {
			t.Errorf("missingPluginArg(%q,%q,%q,%q) = %q, wantEmpty=%v", tc.action, tc.name, tc.source, tc.cap, got, tc.wantEmpty)
		}
	}
}

// The studio's listing agrees, name for name, with the shared builder the CLI and
// the console also read: three surfaces disagreeing about what exists is the bug
// this replaced. A discovered plugin proves the list is not merely empty.
func TestStudioListingAgreesWithSharedBuilder(t *testing.T) {
	isolateConfig(t)
	plugintest.Install(t, "demo")
	reloadRegistry()
	got := listPlugins()

	rows := plugins.Rows(registry())
	if len(got) != len(rows) {
		t.Fatalf("studio lists %d plugins, the shared builder has %d", len(got), len(rows))
	}
	var sawDemo bool
	for i := range rows {
		if got[i].Name != rows[i].Name || got[i].Where != rows[i].Where {
			t.Errorf("row %d: studio has %s/%s, the builder has %s/%s",
				i, got[i].Name, got[i].Where, rows[i].Name, rows[i].Where)
		}
		if got[i].Name == "demo" {
			sawDemo = true
		}
	}
	if !sawDemo {
		t.Errorf("the discovered plugin is missing from the studio listing: %+v", got)
	}
}

// The studio must be able to disable and re-enable a discovered plugin, and the
// change must persist so the plugin stops registering: a disabled plugin's step
// disappears from the builder, and re-enabling brings it back.
func TestStudioTogglesInstalledPlugin(t *testing.T) {
	isolateConfig(t)
	f := plugintest.Install(t, "demo")
	reloadRegistry()
	mux := testMux()

	// Disable through the API.
	if w := muxPost(mux, "/api/plugins", `{"action":"disable","name":"`+f.Name+`"}`); w.Code != 200 {
		t.Fatalf("disable: %d %s", w.Code, w.Body.String())
	}
	if p, ok := pluginstore.Get(f.Name); !ok || p.Enabled {
		t.Fatal("disabling through the studio did not persist")
	}
	// The listing reflects it as disabled, and a disabled plugin registers
	// nothing, so its step disappears.
	var seenDisabled bool
	for _, p := range listPlugins() {
		if p.Name == f.Name {
			seenDisabled = !p.Enabled
		}
	}
	if !seenDisabled {
		t.Error("a disabled plugin should list as disabled")
	}
	if hasStep(registry().StepsFor("laravel"), f.Step()) {
		t.Error("a disabled plugin still offers its step; its wizard should disappear")
	}

	// Re-enable it and confirm its step comes back.
	if w := muxPost(mux, "/api/plugins", `{"action":"enable","name":"`+f.Name+`"}`); w.Code != 200 {
		t.Fatalf("enable: %d %s", w.Code, w.Body.String())
	}
	if p, ok := pluginstore.Get(f.Name); !ok || !p.Enabled {
		t.Error("re-enabling through the studio did not persist")
	}
	if !hasStep(registry().StepsFor("laravel"), f.Step()) {
		t.Error("a re-enabled plugin offers no step; its wizard did not come back")
	}
}

// hasStep reports whether a step with the given ID is present.
func hasStep(steps []plugin.Step, id string) bool {
	for _, s := range steps {
		if s.ID == id {
			return true
		}
	}
	return false
}

// The Build flow fetches the applicable plugins' wizard options as a schema so it
// can draw real controls instead of running defaults blind. The endpoint returns
// that schema for a framework, keyed the way the CLI wizard asks it. A discovered
// plugin's live options run its executable, so the plugin must be trusted.
func TestPluginOptionsReturnsSchema(t *testing.T) {
	isolateConfig(t)
	f := plugintest.Install(t, "demo")
	f.Trust()
	reloadRegistry()
	w := muxGet(testMux(), "/api/plugin-options?framework=laravel")
	if w.Code != 200 {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Schemas []struct {
			ID      string `json:"id"`
			Label   string `json:"label"`
			Type    string `json:"type"`
			Choices []struct {
				Value   string `json:"value"`
				Default bool   `json:"default"`
			} `json:"choices"`
		} `json:"schemas"`
		Problems []string `json:"problems"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, w.Body.String())
	}
	if len(got.Problems) > 0 {
		t.Errorf("a plugin failed to describe its options: %v", got.Problems)
	}
	var step *struct {
		ID      string `json:"id"`
		Label   string `json:"label"`
		Type    string `json:"type"`
		Choices []struct {
			Value   string `json:"value"`
			Default bool   `json:"default"`
		} `json:"choices"`
	}
	for i := range got.Schemas {
		if got.Schemas[i].ID == f.Step() {
			step = &got.Schemas[i]
		}
	}
	if step == nil {
		t.Fatalf("the plugin's step is missing from the schema: %+v", got.Schemas)
	}
	if step.Type != "multi" {
		t.Errorf("the plugin's step is multi-select; got type %q", step.Type)
	}
	if len(step.Choices) == 0 {
		t.Error("the plugin offered no choices for laravel; the form would be empty")
	}
}

// With no framework there is nothing to ask, and the endpoint says so cleanly
// rather than erroring: the Build form simply renders no plugin step yet.
func TestPluginOptionsWithoutFramework(t *testing.T) {
	isolateConfig(t)
	w := muxGet(testMux(), "/api/plugin-options")
	if w.Code != 200 {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Schemas []any `json:"schemas"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, w.Body.String())
	}
	if len(got.Schemas) != 0 {
		t.Errorf("no framework means no schema; got %+v", got.Schemas)
	}
}

// A disabled plugin exposes no options either: the Build form must not offer a
// step for a plugin the user switched off.
func TestPluginOptionsRespectsDisabledPlugin(t *testing.T) {
	isolateConfig(t)
	f := plugintest.Install(t, "demo")
	f.Trust()
	f.Enable(false)
	reloadRegistry()
	w := muxGet(testMux(), "/api/plugin-options?framework=laravel")
	if w.Code != 200 {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), f.Step()) {
		t.Error("a disabled plugin still offers its options; a disabled plugin's step must disappear")
	}
}

// chosenFor is the seam that makes the Build form's answers reach a plugin: it
// applies the user's picks (validated against the step's real options) and falls
// back to the step's defaults only when the user did not answer. A forged or
// stale value is dropped rather than passed to Apply.
func TestChosenForAppliesUserPicksAndValidates(t *testing.T) {
	opts := []plugin.Option{
		{Value: "a", Default: true},
		{Value: "b", Default: false},
		{Value: "c", Default: true},
	}
	step := plugin.Step{ID: "s1"}

	// No answer for the step -> defaults (a, c).
	if got := chosenFor(step, opts, nil); !equalSet(got, []string{"a", "c"}) {
		t.Errorf("unanswered step should take defaults, got %v", got)
	}
	// The user picked b only -> exactly b, defaults ignored.
	got := chosenFor(step, opts, map[string][]string{"s1": {"b"}})
	if !equalSet(got, []string{"b"}) {
		t.Errorf("a user's pick should replace the defaults, got %v", got)
	}
	// A pick that is not a real option is dropped; a valid one alongside survives.
	got = chosenFor(step, opts, map[string][]string{"s1": {"a", "../../etc/passwd", "z"}})
	if !equalSet(got, []string{"a"}) {
		t.Errorf("only valid options should reach Apply, got %v", got)
	}
	// An explicit empty answer means "none" and is honoured, not replaced.
	if got := chosenFor(step, opts, map[string][]string{"s1": {}}); len(got) != 0 {
		t.Errorf("an explicit empty choice should install nothing, got %v", got)
	}
}

func equalSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		m[x]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}

// The build applies the user's chosen options end-to-end: a recording step
// receives the picked values, not its defaults, when runProjectSteps is driven
// with a chosen map — the wiring handleBuild relies on.
func TestRunProjectStepsAppliesChosenOptions(t *testing.T) {
	var applied []string
	step := plugin.Step{
		ID: "pick", Multi: true,
		Options: func(ctx context.Context, p plugin.Project) ([]plugin.Option, error) {
			return []plugin.Option{
				{Value: "x", Default: true},
				{Value: "y", Default: false},
			}, nil
		},
		Apply: func(ctx context.Context, io plugin.IO, p plugin.Project, chosen []string) error {
			applied = chosen
			return nil
		},
	}
	rec := &recordingPlugins{steps: []plugin.Step{step}}
	var out bytes.Buffer
	runProjectSteps(context.Background(), &out, rec, plugin.Project{Framework: "laravel"},
		map[string][]string{"pick": {"y"}})
	if !equalSet(applied, []string{"y"}) {
		t.Errorf("the build should apply the user's choice (y), got %v", applied)
	}
}
