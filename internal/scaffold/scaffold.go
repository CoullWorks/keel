// Package scaffold owns the one post-build sequence keel runs after a project's
// files exist: fire the created + built lifecycle events, run the plan's open
// hooks, and track the project so every surface can find it. Both callers of a
// build — `keel new` (internal/cli) and keel studio's build endpoint
// (internal/studio) — went through this sequence independently and had already
// drifted: the CLI emitted only project.created and fired open hooks; the studio
// emitted project.created + project.built and skipped the open hooks. That split
// is exactly the kind of thing that rots, so it lives here once.
//
// What legitimately differs between the two — collecting a plugin step's answers
// (interactive huh prompts in the CLI, a headless options map in the studio) —
// stays with the caller: Finish is called after the caller has run its steps, so
// this package never needs to know how a step was answered.
package scaffold

import (
	"context"
	"io"
	"path/filepath"

	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/project"
	"github.com/coullworks/keel/internal/resolver"
	"github.com/coullworks/keel/plugin"
)

// Emitter is the slice of the plugin registry Finish needs: firing an event at
// every subscriber. Declared here (at the consumer) and kept to one method so a
// test can pass a recording stub, and neither internal/plugins nor its concrete
// registry is a build-time dependency of this package.
type Emitter interface {
	Emit(ctx context.Context, e plugin.Event, io plugin.IO, p plugin.Project) []error
}

// Options carry everything the post-build sequence needs that it can't derive.
type Options struct {
	// Plan is the resolved plan just built, used to fire its open hooks and to
	// derive the site URL. Trust must already have been consented to.
	Plan *resolver.Plan
	// Dir is the project directory that engine.Build wrote into.
	Dir string
	// Proj describes the finished project to a plugin (dir, name, framework, env).
	Proj plugin.Project
	// Emitter fires the lifecycle events. Required.
	Emitter Emitter
	// PluginIO is where a lifecycle listener's own output goes; also where a
	// warning about a listener's trouble is written. Required.
	PluginIO plugin.IO
	// Out is the plain writer for progress + open-hook output (nil -> os.Stdout is
	// used by the engine). Warnings go through PluginIO.
	Out io.Writer
	// Track, when true, records the project in the tracked-projects registry so
	// every surface (dashboard, plugin screens) can find it. Best effort: a
	// registry write failing never fails a build that already succeeded.
	Track bool
}

// Finish runs the shared post-build sequence: created + built events, open hooks,
// then (optionally) tracking. It is called after engine.Build has succeeded and
// after the caller has run its plugin steps, so the whole tail of a build has a
// single definition. It returns the project's site URL (may be "") so a caller
// can print its own "open:" / "done" line in its own voice.
//
// Nothing here fails the build: a lifecycle listener's opinion cannot undo a
// project that already exists on disk, and neither can a failed registry write,
// so listener trouble is reported as a warning and tracking is best effort.
func Finish(ctx context.Context, o Options) string {
	// project.created then project.built, in that order, so a listener that keys
	// off "the project now exists" and one that keys off "the build is complete"
	// both fire, and always the same way from either caller.
	emit(ctx, o, plugin.EventProjectCreated)
	emit(ctx, o, plugin.EventProjectBuilt)

	// Open hooks: the plan's post_open commands (the CLI's parity line). Its trust
	// was consented to before the build ran, so it is safe to run here.
	_ = engine.FireOpenHooks(ctx, o.Plan, o.Dir, o.Out)

	if o.Track {
		track(o)
	}
	// o.Dir is always a resolved (non-".") path here, so the project name is its
	// last element — the same value the CLI's projectName and the studio's own
	// filepath.Base derived before.
	return engine.SiteURL(o.Plan, filepath.Base(o.Dir))
}

// emit fires one lifecycle event and reports any listener trouble as a warning
// through the plugin IO, never as a build failure.
func emit(ctx context.Context, o Options, e plugin.Event) {
	for _, err := range o.Emitter.Emit(ctx, e, o.PluginIO, o.Proj) {
		o.PluginIO.Warn(err.Error())
	}
}

// track records the built project so the dashboard and plugin screens can find
// it. Best effort: it warns but never fails, because the project exists either
// way and the studio's Projects area being briefly empty is not worth losing a
// successful build over.
func track(o Options) {
	reg, err := project.Load()
	if err != nil {
		o.PluginIO.Warn("could not read the projects list: " + err.Error())
		return
	}
	if _, err := reg.Add(o.Dir); err != nil {
		o.PluginIO.Warn("could not track " + o.Dir + ": " + err.Error())
		return
	}
	if err := reg.Save(); err != nil {
		o.PluginIO.Warn("could not save the projects list: " + err.Error())
	}
}
