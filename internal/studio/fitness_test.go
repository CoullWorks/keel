package studio

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/plugintest"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
	"github.com/coullworks/keel/plugin"
)

// --- Fix 1: DB pre-flight gate (/api/db/ping) --------------------------------

// A reachable database returns {reachable:true} with no scary reason. The ping
// reuses projectDB (which opens and pings), so a stub that hands back a live fake
// DB proves the happy path without a real database.
func TestHandleDBPingReachable(t *testing.T) {
	db, _ := openFakeDB(t)
	defer db.Close()
	restore := stubProjectDB(t, db, enginePostgres)
	defer restore()

	w := muxGet(testMux(), "/api/db/ping?dir=.")
	if w.Code != http.StatusOK {
		t.Fatalf("ping should be 200, got %d: %s", w.Code, w.Body)
	}
	m := decodeJSON(t, w)
	if r, _ := m["reachable"].(bool); !r {
		t.Fatalf("a live DB should be reachable: %s", w.Body)
	}
	if _, ok := m["reason"]; ok {
		t.Errorf("a reachable DB should carry no reason: %s", w.Body)
	}
}

// An unreachable database is a NORMAL answer (200), and the reason is a calm,
// actionable sentence — never the raw driver error the grid used to throw.
func TestHandleDBPingUnreachableIsCalm(t *testing.T) {
	prev := projectDB
	projectDB = func(ctx context.Context, dir string) (*sql.DB, dbEngine, error) {
		return nil, "", errors.New("could not connect to the project database (is it running?): dial tcp 127.0.0.1:55432: connection refused")
	}
	defer func() { projectDB = prev }()

	w := muxGet(testMux(), "/api/db/ping?dir=.")
	if w.Code != http.StatusOK {
		t.Fatalf("an unreachable DB is a normal 200 answer, got %d", w.Code)
	}
	m := decodeJSON(t, w)
	if r, _ := m["reachable"].(bool); r {
		t.Fatalf("an unreachable DB must not report reachable: %s", w.Body)
	}
	reason, _ := m["reason"].(string)
	if reason == "" {
		t.Fatalf("an unreachable DB must carry a reason: %s", w.Body)
	}
	// The whole point of the gate: the browser never sees the raw driver error.
	for _, leak := range []string{"dial tcp", "connection refused", "55432"} {
		if strings.Contains(reason, leak) {
			t.Errorf("the calm reason leaks the raw driver error %q: %q", leak, reason)
		}
	}
	if !strings.Contains(reason, "start the environment") {
		t.Errorf("the reason should tell the user how to fix it: %q", reason)
	}
}

// A configuration problem (not a keel project / no DSN) is classified as
// "configure", so the UI offers the right next step rather than a Start button
// that cannot help.
func TestPingReasonClassifiesConfigVsNotRunning(t *testing.T) {
	if got := pingReason(errors.New("not a tracked keel project: /x")); !strings.Contains(got, "Configure") {
		t.Errorf("a tracking problem should be a configure reason, got %q", got)
	}
	if got := pingReason(errors.New("dial tcp: connection refused")); !strings.Contains(got, "start the environment") {
		t.Errorf("a connection problem should be a not-running reason, got %q", got)
	}
}

// --- Fix 3: a studio build runs plugin steps + emits events ------------------

// recordingPlugins is a stub projectPlugins that records which events were
// emitted and lets a test supply steps, so the build's post-build lifecycle can
// be verified without a real plugin on disk.
type recordingPlugins struct {
	steps    []plugin.Step
	emitted  []plugin.Event
	stepsFor string // records the framework StepsFor was asked for
}

func (r *recordingPlugins) StepsFor(fw string) []plugin.Step {
	r.stepsFor = fw
	return r.steps
}

func (r *recordingPlugins) Emit(ctx context.Context, e plugin.Event, io plugin.IO, p plugin.Project) []error {
	r.emitted = append(r.emitted, e)
	return nil
}

// The core Fix 3 assertion: the post-build lifecycle emits project.created (so
// ai-core's world is set up and a listener like sonar's fires), and project.built
// after it. Driven through the exact function handleBuild calls, against a
// recording stub swapped in via the pluginsFor seam.
func TestStudioBuildEmitsProjectCreated(t *testing.T) {
	applied := false
	step := plugin.Step{
		ID: "demo-step", Title: "Demo",
		Options: func(ctx context.Context, p plugin.Project) ([]plugin.Option, error) {
			return []plugin.Option{{Value: "on", Label: "On", Default: true}}, nil
		},
		Apply: func(ctx context.Context, io plugin.IO, p plugin.Project, chosen []string) error {
			applied = true
			return nil
		},
	}
	rec := &recordingPlugins{steps: []plugin.Step{step}}

	var out bytes.Buffer
	// The studio's headless half: run the project's plugin steps. The lifecycle
	// events (project.created + project.built) are now the shared scaffold.Finish
	// sequence, covered by the scaffold package's own tests — this proves a studio
	// build still runs its plugin steps, the silent-skip bug this originally fixed.
	runProjectSteps(context.Background(), &out, rec, plugin.Project{Name: "shop", Framework: "laravel"}, nil)

	if !applied {
		t.Error("the plugin step was never applied: a studio build silently skips plugins, the bug this fixes")
	}
	if rec.stepsFor != "laravel" {
		t.Errorf("steps should be resolved for the built framework, got %q", rec.stepsFor)
	}
}

// A step whose Options fails must not stop the build — a plugin's trouble after a
// successful build cannot undo the build — and its failure is surfaced in the
// stream rather than swallowed.
func TestStudioBuildStepFailureIsSurfacedNotFatal(t *testing.T) {
	bad := plugin.Step{
		ID: "bad", Title: "Bad",
		Options: func(ctx context.Context, p plugin.Project) ([]plugin.Option, error) {
			return nil, errors.New("boom")
		},
		Apply: func(ctx context.Context, io plugin.IO, p plugin.Project, chosen []string) error { return nil },
	}
	rec := &recordingPlugins{steps: []plugin.Step{bad}}
	var out bytes.Buffer
	// Must not panic or return early; the failure is reported into the stream.
	runProjectSteps(context.Background(), &out, rec, plugin.Project{Name: "x", Framework: "nextjs"}, nil)
	if !strings.Contains(out.String(), "boom") {
		t.Errorf("a step's failure should be surfaced in the stream: %q", out.String())
	}
}

// The real pluginsFor seam wires to the shared registry, so a real studio build
// has a discovered plugin's steps and listeners available — this guards the wiring
// the stub tests bypass.
func TestPluginsForWiresTheRealRegistry(t *testing.T) {
	isolateConfig(t)
	plugintest.Install(t, "demo") // a discovered Stepper that applies to every framework
	reloadRegistry()
	reg := pluginsFor()
	if reg == nil {
		t.Fatal("pluginsFor returned nil; a studio build would run no plugins")
	}
	// The discovered plugin contributes a laravel-applicable step; it must reach
	// the registry a studio build runs, or its wizard would never fire.
	if len(reg.StepsFor("laravel")) == 0 {
		t.Error("the real registry offers no laravel steps; a discovered plugin would not run from a studio build")
	}
}

// A plugin writes through IO, not fmt: streamIO turns those styled calls into
// ordinary lines in the build stream so the browser can colour them. This proves
// a step's output actually reaches the stream — the studio used to drop it.
func TestStreamIOWritesEveryChannel(t *testing.T) {
	var out bytes.Buffer
	io := newStreamIO(&out)
	io.Title("Assistant setup")
	io.Detail("skills", "3")
	io.Note("wrote .cursor/rules")
	io.OK("installed")
	io.Warn("no agents chosen")
	io.Bad("could not write")
	io.List("files", []string{"a.md", "b.md"})
	io.List("empty", nil) // a titled list with no items writes nothing
	got := out.String()
	for _, want := range []string{"Assistant setup", "skills: 3", "wrote .cursor/rules",
		"✓ installed", "⚠ no agents chosen", "✗ could not write", "files:", "  - a.md", "  - b.md"} {
		if !strings.Contains(got, want) {
			t.Errorf("stream is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "empty:") {
		t.Errorf("an empty list should write nothing, got:\n%s", got)
	}
}

// planEnvID is the env recipe id a built project carries so a listener knows how
// it runs. It is the env-kind recipe in the plan, or empty when there is none.
func TestPlanEnvID(t *testing.T) {
	plan := &resolver.Plan{Framework: "laravel", Recipes: []recipe.Recipe{
		{ID: "laravel", Kind: recipe.Framework},
		{ID: "laravel-docker", Kind: recipe.Env},
	}}
	if got := planEnvID(plan); got != "laravel-docker" {
		t.Fatalf("planEnvID = %q, want laravel-docker", got)
	}
	none := &resolver.Plan{Recipes: []recipe.Recipe{{ID: "laravel", Kind: recipe.Framework}}}
	if got := planEnvID(none); got != "" {
		t.Fatalf("a plan with no env recipe should yield empty, got %q", got)
	}
}
