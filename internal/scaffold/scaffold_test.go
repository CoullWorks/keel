package scaffold

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/project"
	"github.com/coullworks/keel/internal/resolver"
	"github.com/coullworks/keel/plugin"
)

// recordingEmitter records every event Finish fires and can be told to fail a
// given event, so a test can prove a listener's error is surfaced but not fatal.
type recordingEmitter struct {
	emitted []plugin.Event
	failOn  plugin.Event
}

func (r *recordingEmitter) Emit(ctx context.Context, e plugin.Event, io plugin.IO, p plugin.Project) []error {
	r.emitted = append(r.emitted, e)
	if e == r.failOn {
		return []error{errors.New("listener boom")}
	}
	return nil
}

// captureIO records the Warn lines Finish writes, so a test can assert a listener
// failure is reported rather than swallowed. The other methods are no-ops.
type captureIO struct{ warns []string }

func (c *captureIO) Title(string)          {}
func (c *captureIO) Detail(string, string) {}
func (c *captureIO) Note(string)           {}
func (c *captureIO) List(string, []string) {}
func (c *captureIO) OK(string)             {}
func (c *captureIO) Warn(t string)         { c.warns = append(c.warns, t) }
func (c *captureIO) Bad(string)            {}

func has(list []plugin.Event, e plugin.Event) bool {
	for _, x := range list {
		if x == e {
			return true
		}
	}
	return false
}

// Finish must fire BOTH lifecycle events, in created-then-built order — the single
// source of truth both `keel new` and the studio now share. The studio used to
// emit both while the CLI emitted only created; this is the behaviour that no
// longer depends on which caller you came through.
func TestFinishEmitsBothLifecycleEventsInOrder(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	rec := &recordingEmitter{}
	Finish(context.Background(), Options{
		Plan: &resolver.Plan{Framework: "laravel"}, Dir: dir,
		Proj:    plugin.Project{Dir: dir, Name: "shop", Framework: "laravel"},
		Emitter: rec, PluginIO: &captureIO{}, Track: true,
	})
	if !has(rec.emitted, plugin.EventProjectCreated) {
		t.Errorf("Finish must emit project.created; emitted %v", rec.emitted)
	}
	if !has(rec.emitted, plugin.EventProjectBuilt) {
		t.Errorf("Finish must emit project.built; emitted %v", rec.emitted)
	}
	if len(rec.emitted) < 2 || rec.emitted[0] != plugin.EventProjectCreated || rec.emitted[1] != plugin.EventProjectBuilt {
		t.Errorf("events must fire created-then-built; got %v", rec.emitted)
	}
}

// A listener returning an error must not stop the sequence: the project exists,
// so a listener's opinion cannot undo it. The error is surfaced as a warning
// through the plugin IO and project.built still fires afterwards.
func TestFinishSurvivesAFailingListener(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	rec := &recordingEmitter{failOn: plugin.EventProjectCreated}
	io := &captureIO{}
	Finish(context.Background(), Options{
		Plan: &resolver.Plan{Framework: "nextjs"}, Dir: dir,
		Proj:    plugin.Project{Dir: dir, Name: "x", Framework: "nextjs"},
		Emitter: rec, PluginIO: io, Track: false,
	})
	if !has(rec.emitted, plugin.EventProjectBuilt) {
		t.Errorf("a failing created listener must not stop project.built; emitted %v", rec.emitted)
	}
	if !strings.Contains(strings.Join(io.warns, "\n"), "boom") {
		t.Errorf("a listener's failure should be surfaced as a warning: %v", io.warns)
	}
}

// With Track set, the built project is registered so every surface can find it —
// the fix that stopped the CLI from building projects the studio's dashboard
// could not see.
func TestFinishTracksTheProject(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	Finish(context.Background(), Options{
		Plan: &resolver.Plan{Framework: "laravel"}, Dir: dir,
		Proj:    plugin.Project{Dir: dir, Name: "shop", Framework: "laravel"},
		Emitter: &recordingEmitter{}, PluginIO: &captureIO{}, Track: true,
	})
	reg, err := project.Load()
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	found := false
	for _, p := range reg.Projects {
		if strings.HasSuffix(p.Path, "/"+lastSeg(dir)) || p.Path == dir {
			found = true
		}
	}
	if !found {
		t.Errorf("Finish(Track:true) did not register the project; have %v", reg.Projects)
	}
}

func lastSeg(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
