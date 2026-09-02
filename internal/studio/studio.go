// Package studio serves keel studio: a local web GUI over keel's core. It binds
// loopback only and exposes a JSON API (list recipes, resolve a plan, list/add
// projects) plus a live-streaming build endpoint (SSE) and an embedded UI.
// See docs/STUDIO.md.
package studio

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/creds"
	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/envfile"
	"github.com/coullworks/keel/internal/mcp"
	"github.com/coullworks/keel/internal/pack"
	"github.com/coullworks/keel/internal/platform"
	"github.com/coullworks/keel/internal/profile"
	"github.com/coullworks/keel/internal/project"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
	"github.com/coullworks/keel/internal/scaffold"
	"github.com/coullworks/keel/internal/selfupdate"
	"github.com/coullworks/keel/plugin"
)

//go:embed index.html
var indexHTML string

//go:embed assets/anchor.png assets/crab.png assets/favicon.png
var assetsFS embed.FS

type recipeDTO struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Label     string   `json:"label"`
	Lang      string   `json:"lang,omitempty"`
	Source    string   `json:"source"`
	AppliesTo []string `json:"appliesTo,omitempty"`
	Default   bool     `json:"default"`
	// DefaultFor overrides Default per framework: one shared database recipe is
	// the default for some frameworks and not others.
	DefaultFor []string `json:"defaultFor,omitempty"`
	// Provides carries the capabilities the recipe satisfies, so the page can
	// group by what a recipe IS rather than by a hardcoded list of ids. The
	// web-server question uses it: anything providing "webserver" is an
	// alternative to the others, never an addition.
	Provides []string `json:"provides,omitempty"`
	// Conflicts are the capabilities this recipe cannot coexist with, so the
	// build wizard can prevent a conflicting multi-select (two search services)
	// up front instead of only failing at resolve.
	Conflicts []string `json:"conflicts,omitempty"`
	Family    string   `json:"family,omitempty"`
	Variant   string   `json:"variant,omitempty"`
	Category  string   `json:"category,omitempty"`
}

func toDTO(r recipe.Recipe) recipeDTO {
	src := r.Source
	if src == "" {
		src = "builtin"
	}
	return recipeDTO{ID: r.ID, Kind: string(r.Kind), Label: r.Label, Lang: r.Lang, Source: src, AppliesTo: r.AppliesTo, Default: r.Default, DefaultFor: r.DefaultFor, Provides: r.Provides, Conflicts: r.Conflicts, Family: r.Family, Variant: r.Variant, Category: r.Category}
}

// loopbackOnly rejects requests whose Host isn't localhost (defence in depth
// against a malicious page driving the local server — the Storybook CVE lesson).
func loopbackOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if i := strings.LastIndex(host, ":"); i >= 0 {
			host = host[:i]
		}
		if host != "127.0.0.1" && host != "localhost" && host != "[::1]" {
			http.Error(w, "keel studio is loopback-only", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// badRequest answers a malformed or incomplete request with a 400 and a JSON
// error body. It is the one shape for "the client sent something keel cannot
// use": a decode failure or a missing required field. Some mutating routes used
// to reply 200 with an {error} body for these, which read as success to anything
// checking the status; keeping the JSON body means the SPA still shows the
// message (its apiJSON throws on any non-2xx and surfaces the text), while the
// status now says plainly that nothing was done.
func badRequest(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func handleRecipes(w http.ResponseWriter, r *http.Request) {
	reg, err := catalog.Registry()
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	out := make([]recipeDTO, 0, reg.Len())
	for _, rc := range reg.All() {
		out = append(out, toDTO(rc))
	}
	writeJSON(w, map[string]any{"recipes": out, "languages": reg.Languages()})
}

func handleResolve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		badRequest(w, "bad request")
		return
	}
	reg, err := catalog.Registry()
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	plan, err := resolver.Resolve(reg, body.IDs)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()}) // live validation feedback
		return
	}
	recs := make([]recipeDTO, 0, len(plan.Recipes))
	for _, rc := range plan.Recipes {
		recs = append(recs, toDTO(rc))
	}
	writeJSON(w, map[string]any{
		"framework":   plan.Framework,
		"recipes":     recs,
		"steps":       engine.Steps(plan),
		"credentials": credentialsFor(plan),
		"envNames":    creds.Suggestions(plan),
	})
}

// credentialDTO describes a credential the plan needs. It carries whether one is
// already remembered, never the value: this crosses an HTTP boundary and lands
// in a browser, and a secret that is only displayed is still a secret leaked
// into devtools, autofill and screenshots.
type credentialDTO struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	Help     string `json:"help"`
	Required bool   `json:"required"`
	Auth     string `json:"auth,omitempty"`
	Saved    bool   `json:"saved"`
}

func credentialsFor(plan *resolver.Plan) []credentialDTO {
	want := creds.Required(plan)
	if len(want) == 0 {
		return []credentialDTO{}
	}
	store, err := creds.Load()
	if err != nil {
		store = &creds.Store{}
	}
	out := make([]credentialDTO, 0, len(want))
	for _, c := range want {
		saved := false
		if v, ok := store.Get(c.ID); ok && v.Filled() {
			saved = true
		}
		out = append(out, credentialDTO{
			ID: c.ID, Kind: c.Kind, Label: c.Label, Help: c.Help,
			Required: c.Required, Auth: c.Auth, Saved: saved,
		})
	}
	return out
}

// handleProfile returns the engineer profile + whether onboarding is needed
// (GET), or saves it from the onboarding wizard (POST).
func handleProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body struct {
			Name, ProjectsDir, Hosting                    string
			Framework, Env, Database, Editor              string
			Frontend, Webserver, Services, Addons, Extras string
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			badRequest(w, "bad request")
			return
		}
		p, _ := profile.Load()
		if p.Defaults == nil {
			p.Defaults = map[string]string{}
		}
		p.Git.Name = body.Name
		set := func(k, v string) { p.Defaults[k] = v } // overwrite (blank clears a default)
		set("projects_dir", body.ProjectsDir)
		set("hosting", body.Hosting)
		set("framework", body.Framework)
		set("env", body.Env)
		set("database", body.Database)
		set("editor", body.Editor)
		// Full default stack (blank = fall back to the recipe's own default).
		set("frontend", body.Frontend)
		// The web server is its own key, kept out of the services list: a list
		// with no web server in it cannot say whether one was declined or never
		// asked for, and guessing wrong builds a stack nothing can reach. The
		// page always sends an answer, profile.NoWebServer included.
		set("webserver", body.Webserver)
		set("services", body.Services)
		set("addons", body.Addons)
		set("extras", body.Extras)
		if err := p.Save(); err != nil {
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"saved": true})
		return
	}
	p, _ := profile.Load()
	writeJSON(w, map[string]any{
		"exists":       profile.Exists(),
		"name":         p.Git.Name,
		"projects_dir": p.Defaults["projects_dir"],
		"hosting":      p.Defaults["hosting"],
		"framework":    p.Defaults["framework"],
		"env":          p.Defaults["env"],
		"database":     p.Defaults["database"],
		"editor":       p.Defaults["editor"],
		"frontend":     p.Defaults["frontend"],
		"webserver":    p.Defaults["webserver"],
		"services":     p.Defaults["services"],
		"addons":       p.Defaults["addons"],
		"extras":       p.Defaults["extras"],
		// The same once-a-day cached upgrade check the CLI runs. Served with the
		// profile because the page already fetches that on load, so noticing a
		// release costs no extra request.
		"upgrade": selfupdate.Notice(Repo, Version),
		"hostingOptions": func() []map[string]string {
			out := make([]map[string]string, 0, len(profile.HostingOptions))
			for _, h := range profile.HostingOptions {
				out = append(out, map[string]string{"key": h.Key, "label": h.Label})
			}
			return out
		}(),
	})
}

// packDTO is one recipe pack as the studio lists it: its identity, trust, the
// recipes it ships, and whether it is the built-in example pack keel bundles.
type packDTO struct {
	Name        string      `json:"name"`
	Version     string      `json:"version"`
	Description string      `json:"description,omitempty"`
	Trusted     bool        `json:"trusted"`
	Builtin     bool        `json:"builtin,omitempty"`
	Enabled     bool        `json:"enabled"`
	Recipes     []recipeDTO `json:"recipes"`
}

// handlePacks lists recipe packs and the recipes each contributes, so the studio
// surfaces extensions (packs "ship" into the UI: their recipes already appear in
// the Build tree via /api/recipes; this panel makes their provenance and trust
// visible). The shipped example pack always leads the list as a browsable
// built-in, the pack twin of the compiled-in example plugin, so the six recipe
// kinds it demonstrates are visible on a fresh install with nothing added yet.
func handlePacks(w http.ResponseWriter, r *http.Request) {
	// POST toggles a pack on or off — the pack twin of the plugin enable/disable.
	// A disabled pack keeps its files but drops out of the catalog, so its recipes
	// leave the Build tree until it is turned back on.
	if r.Method == http.MethodPost {
		var body struct {
			Action string `json:"action"` // enable | disable | remove
			Name   string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
			badRequest(w, "a pack name is required")
			return
		}
		var err error
		switch body.Action {
		case "enable":
			err = pack.SetEnabled(body.Name, true)
		case "disable":
			err = pack.SetEnabled(body.Name, false)
		case "remove":
			// Uninstall: delete the pack's files and its packs.yaml entry. Already-
			// generated projects are untouched. It is the pack twin of removing a
			// plugin.
			var ok bool
			if ok, err = pack.Uninstall(body.Name); err == nil && !ok {
				badRequest(w, "pack not installed: "+body.Name)
				return
			}
		default:
			badRequest(w, "unknown action: "+body.Action)
			return
		}
		if err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"ok": true})
		return
	}

	reg, err := pack.Load()
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	cat, _ := catalog.Registry()
	disabled := pack.DisabledSet()
	out := []packDTO{}
	seen := map[string]bool{}
	emit := func(name, version string, trusted, enabled bool) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		d := packDTO{Name: name, Version: version, Trusted: trusted, Enabled: enabled}
		if cat != nil {
			for _, rc := range cat.All() {
				if rc.Pack == name {
					d.Recipes = append(d.Recipes, toDTO(rc))
				}
			}
		}
		out = append(out, d)
	}
	// Installed packs (packs.yaml) first, so an installed record wins over a loose
	// home checkout of the same pack.
	for _, p := range reg.Packs {
		emit(p.Name, p.Version, p.Trusted, !p.Disabled)
	}
	// Packs discovered anywhere under home — the pack twin of a home-discovered
	// plugin: present with zero config, on by default, toggled via packs.yaml.
	for _, dp := range pack.Discover() {
		if dp.Installed {
			continue // already emitted from packs.yaml above
		}
		v := ""
		if dp.Manifest != nil {
			v = dp.Manifest.Version
		}
		emit(dp.Name, v, false, !disabled[dp.Name])
	}
	writeJSON(w, map[string]any{"packs": out})
}

// handleProjects lists tracked projects (GET), adds one by path (POST {path}),
// or untracks one (DELETE {path}) without touching its files.
func handleProjects(w http.ResponseWriter, r *http.Request) {
	reg, err := project.Load()
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	if r.Method == http.MethodPost || r.Method == http.MethodDelete {
		var body struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Path) == "" {
			badRequest(w, "a project path is required")
			return
		}
		if r.Method == http.MethodDelete {
			// Fire project.deleted BEFORE untracking, while the project is still
			// resolvable, so a listener can clean up its own state. It never fails
			// the untrack: a plugin's teardown is best-effort, and the files on disk
			// are never touched regardless.
			emitProjectEvent(r.Context(), io.Discard, pluginsFor(), plugin.EventProjectDeleted, projectFor(body.Path))
			// Untrack only — the project's files on disk are never touched.
			reg.Remove(body.Path)
			if err := reg.Save(); err != nil {
				writeJSON(w, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, map[string]any{"removed": body.Path})
			return
		}
		p, err := reg.Add(body.Path) // handles ~, validates the dir, detects monorepo members
		if err == nil {
			err = reg.Save()
		}
		if err != nil {
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"added": p})
		return
	}
	reg.Refresh() // re-detect stack/managed/members live (e.g. after adopt)
	_ = reg.Save()
	writeJSON(w, map[string]any{"projects": withEnv(reg.Projects)})
}

// projectRow is a tracked project plus its env, so the dashboard can label a
// Start/Stop control (docker vs ddev vs local) without a round trip per row. The
// env is read from the manifest of a managed project; unmanaged projects have no
// env and simply get no start/stop control.
type projectRow struct {
	project.Project
	Env string `json:"env,omitempty"`
}

// withEnv enriches each tracked project with its manifest env for the dashboard.
func withEnv(ps []project.Project) []projectRow {
	out := make([]projectRow, 0, len(ps))
	for _, p := range ps {
		row := projectRow{Project: p}
		if m, err := engine.ReadManifest(p.Path); err == nil {
			row.Env = m.Env
		}
		out = append(out, row)
	}
	return out
}

// sseWriter turns each newline-terminated write into a Server-Sent Event frame
// and flushes immediately, so build output streams to the browser line by line.
type sseWriter struct {
	w   http.ResponseWriter
	f   http.Flusher
	buf []byte
}

func (s *sseWriter) emit(line []byte) {
	fmt.Fprintf(s.w, "data: %s\n\n", bytes.ReplaceAll(line, []byte("\r"), nil))
	s.f.Flush()
}

func (s *sseWriter) Write(p []byte) (int, error) {
	s.buf = append(s.buf, p...)
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			break
		}
		s.emit(s.buf[:i])
		s.buf = s.buf[i+1:]
	}
	return len(p), nil
}

func (s *sseWriter) flushRemainder() {
	if len(s.buf) > 0 {
		s.emit(s.buf)
		s.buf = nil
	}
}

// emitDone writes the terminating SSE frame carrying the command's result, so
// the browser learns whether the command succeeded without scraping the streamed
// lines for a ✓/✗. The data is a small JSON object — {"ok":true,"code":0} on
// success, {"ok":false,"code":N,"err":"…"} on failure — the frontend parses in
// stream() to raise a success/failure toast. code is the process exit code when
// known (from an ExitError), else 0 on success and 1 on a non-exec failure. It
// replaces the old bare `data: end`: every streaming handler ends with exactly
// one of these, so a collapsed console still gets an accurate result signal.
func emitDone(w io.Writer, f http.Flusher, ok bool, code int, errMsg string) {
	payload := map[string]any{"ok": ok, "code": code}
	if errMsg != "" {
		payload["err"] = errMsg
	}
	b, err := json.Marshal(payload)
	if err != nil {
		b = []byte(`{"ok":false,"code":1}`)
	}
	fmt.Fprintf(w, "event: done\ndata: %s\n\n", b)
	f.Flush()
}

// exitCode extracts a process exit code from an error, so a done frame can carry
// the real code a command failed with (e.g. "exit N") rather than a flat 1. A
// nil error is a clean 0; a non-ExitError failure (a keel-level error, not a
// child process) is reported as 1.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if c := ee.ExitCode(); c > 0 {
			return c
		}
	}
	return 1
}

// handleBuild runs a build and streams its output live over SSE. Default is a
// dry-run (safe preview); {"run":true} executes for real. Loopback-only.
func handleBuild(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	var body struct {
		IDs  []string `json:"ids"`
		Name string   `json:"name"`
		Run  bool     `json:"run"` // false -> dry-run preview
		// Consent is a grant this server issued for this exact plan. There is
		// deliberately no "trust" boolean: see consentStore.
		Consent string `json:"consent"`
		// Credentials the plan asked for. These are secrets: they are written to
		// disk and never echoed into the build stream below.
		Credentials []creds.Value `json:"credentials"`
		// Options are the user's answers to the applicable plugins' wizard steps,
		// keyed by step ID (the same IDs /api/plugin-options reports). A step the
		// user left unanswered falls back to that step's defaults; each value is
		// re-validated against the step's real options before it reaches Apply.
		Options map[string][]string `json:"options"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := safeProjectName(strings.TrimSpace(body.Name)); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	sw := &sseWriter{w: w, f: flusher}

	reg, err := catalog.Registry()
	if err != nil {
		sw.emit([]byte("✗ " + err.Error()))
		emitDone(w, flusher, false, 1, err.Error())
		return
	}
	plan, err := resolver.Resolve(reg, body.IDs)
	if err != nil {
		sw.emit([]byte("✗ " + err.Error()))
		emitDone(w, flusher, false, 1, err.Error())
		return
	}

	// Consent gate for untrusted (pack) recipes, mirroring the CLI: show the
	// exact hooks the build would run, hand back a single-use grant for this
	// plan, and let the user come back with it. A dry run executes nothing, so
	// it needs no grant.
	consented := false
	if body.Run && !engine.PlanTrusted(plan) {
		fp := planFingerprint(plan)
		if consents.redeem(body.Consent, fp) {
			consented = true
		} else {
			emitConsentRequest(w, sw, flusher, plan, consents.issue(fp))
			return
		}
	}

	// Credentials before anything runs, so a missing required key stops a build
	// that has changed nothing rather than one stuck inside composer.
	if body.Run {
		if missing := creds.MissingRequired(plan, body.Credentials); len(missing) > 0 {
			for _, c := range missing {
				label := c.Label
				if label == "" {
					label = c.ID
				}
				sw.emit([]byte("✗ " + plan.Framework + " needs " + label + " (" + c.ID + ")"))
			}
			emitDone(w, flusher, false, 1, "missing required credentials")
			return
		}
	}

	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = plan.Framework + "-app"
	}
	// Create under the onboarded projects folder if set, else the cwd.
	base, _ := os.Getwd()
	if prof, err := profile.Load(); err == nil {
		if pd := project.Expand(prof.Get("", "projects_dir")); pd != "" {
			base = pd
		}
	}
	dir := filepath.Join(base, name)

	sw.emit([]byte(fmt.Sprintf("keel %s → ./%s", modeLabel(body.Run), name)))
	if body.Run {
		if err := writeCredentials(plan, dir, body.Credentials, sw); err != nil {
			sw.emit([]byte("✗ " + err.Error()))
			emitDone(w, flusher, false, 1, err.Error())
			return
		}
	}
	err = engine.Build(r.Context(), plan, engine.Options{
		Dir:     dir,
		DryRun:  !body.Run,
		Out:     sw,
		Runner:  engine.ExecRunner{Out: sw},
		Trusted: consented,
	})
	sw.flushRemainder()
	if err != nil {
		sw.emit([]byte("✗ " + err.Error()))
	} else {
		sw.emit([]byte("✓ done"))
		if body.Run {
			// Run the project's plugin steps (the studio's headless half), then the
			// shared post-build sequence via scaffold.Finish — the SAME tail
			// `keel new` runs: project.created + project.built, the plan's open
			// hooks, and tracking. The studio used to hand-roll this (and skip the
			// open hooks), which is exactly the drift scaffold.Finish removes.
			proj := plugin.Project{
				Dir:       dir,
				Name:      filepath.Base(dir),
				Framework: plan.Framework,
				Env:       planEnvID(plan),
			}
			runProjectSteps(r.Context(), sw, pluginsFor(), proj, body.Options)
			url := scaffold.Finish(r.Context(), scaffold.Options{
				Plan: plan, Dir: dir, Proj: proj,
				Emitter: pluginsFor(), PluginIO: newStreamIO(sw), Out: sw, Track: true,
			})
			// The one fact a person wants after a build finishes: where to open it,
			// the same line the CLI's summary ends with. site_url carries the real
			// port rather than an assumed 8080.
			if url != "" {
				sw.emit([]byte("open: " + url))
			}
		} else {
			// A dry run runs nothing, but still shows where the project would live.
			if url := engine.SiteURL(plan, filepath.Base(dir)); url != "" {
				sw.emit([]byte("open: " + url))
			}
		}
	}
	if err != nil {
		emitDone(w, flusher, false, exitCode(err), err.Error())
	} else {
		emitDone(w, flusher, true, 0, "")
	}
}

// planEnvID is the plan's env recipe id, which a plugin.Project carries so a
// listener knows how the project runs. It mirrors the CLI's planEnv rather than
// importing it, keeping studio free of a cli dependency.
func planEnvID(plan *resolver.Plan) string {
	for _, r := range plan.Recipes {
		if r.Kind == recipe.Env {
			return r.ID
		}
	}
	return ""
}

// emitConsentRequest ends the stream with the commands an untrusted plan would
// run plus the grant that authorises them. The UI shows both and only sends the
// grant back if the user agrees, so consent is always the answer to something
// the server said, never a flag the caller invented.
func emitConsentRequest(w http.ResponseWriter, sw *sseWriter, flusher http.Flusher, plan *resolver.Plan, grant string) {
	sw.emit([]byte("⚠ This plan includes recipes from an untrusted pack."))
	sw.emit([]byte("  Building it would run these hooks:"))
	steps := engine.HookSteps(plan)
	for _, s := range steps {
		sw.emit([]byte("    → " + s))
	}
	payload, err := json.Marshal(map[string]any{"grant": grant, "steps": steps})
	if err != nil {
		sw.emit([]byte("✗ " + err.Error()))
		emitDone(w, flusher, false, 1, err.Error())
		return
	}
	fmt.Fprintf(w, "event: consent\ndata: %s\n\n", payload)
	// The build did not run — it is paused awaiting consent. ok:false carries that,
	// but the frontend sees the consent frame first and shows the consent flow
	// rather than a "failed" toast.
	emitDone(w, flusher, false, 0, "consent required")
}

// writeCredentials puts the supplied values where this environment reads them
// and remembers the ones the user ticked. It reports paths, never values: the
// build stream is displayed in a browser pane and gets screenshotted.
func writeCredentials(plan *resolver.Plan, dir string, values []creds.Value, sw *sseWriter) error {
	if len(values) == 0 {
		return nil
	}
	safeBase, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("refusing path outside %q", dir)
	}
	if err := os.MkdirAll(safeBase, 0o755); err != nil {
		return err
	}
	path, err := creds.WriteComposerAuth(plan.EnvFamily(), dir, values)
	if err != nil {
		return err
	}
	if path != "" {
		sw.emit([]byte("✓ wrote Composer credentials to " + path))
	}
	if env := creds.EnvValues(values); len(env) > 0 {
		envPath, err := filepath.Abs(filepath.Join(dir, ".env"))
		if err != nil || !strings.HasPrefix(envPath, safeBase) {
			return fmt.Errorf("refusing path outside %q", dir)
		}
		f, err := envfile.Load(envPath)
		if err != nil {
			return err
		}
		for k, v := range env {
			f.Set(k, v) // upsert: Merge only adds keys that are missing
		}
		if err := os.WriteFile(envPath, []byte(f.Render()), 0o600); err != nil {
			return err
		}
		sw.emit([]byte(fmt.Sprintf("✓ wrote %d key(s) to .env", len(env))))
	}
	store, err := creds.Load()
	if err != nil {
		return err
	}
	store.Remember(values...)
	return store.Save()
}

func modeLabel(run bool) string {
	if run {
		return "build"
	}
	return "preview"
}

// execAllowed is the whitelist of keel subcommands the studio may run in a
// project directory (Data / Deploy / Secrets panels). Anything else is refused.
var execAllowed = map[string]bool{"db": true, "deploy": true, "secrets": true, "update": true, "doctor": true, "adopt": true, "optimize": true, "gen": true, "add": true}

// execNoProject are whitelisted commands that don't require an existing keel
// manifest (doctor runs anywhere; adopt creates the manifest; optimize is a
// read-only scan that works on any directory).
var execNoProject = map[string]bool{"doctor": true, "adopt": true, "optimize": true}

// execArgv runs a whitelisted keel subcommand as argv (no shell), streaming to
// out. It is a package var — a test seam — so a test can prove handleExec passes
// browser-supplied args through as data (no sh -c) without re-executing the keel
// binary (which, under `go test`, is the test binary and would recurse). The real
// body is the one-line RunArgv call handleRun and handleGenerate also use.
var execArgv = func(ctx context.Context, out io.Writer, dir string, argv []string) error {
	return engine.ExecRunner{Out: out}.RunArgv(ctx, dir, argv)
}

// handleExec re-runs the keel binary (a whitelisted subcommand) inside a tracked
// project and streams its output live. This reuses every command's real logic
// without a studio↔cli import cycle. Loopback-only.
func handleExec(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	var body struct {
		Dir  string   `json:"dir"`
		Args []string `json:"args"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Args) == 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	sw := &sseWriter{w: w, f: flusher}
	if !execAllowed[body.Args[0]] {
		sw.emit([]byte("✗ command not allowed: " + body.Args[0]))
		emitDone(w, flusher, false, 1, "command not allowed: "+body.Args[0])
		return
	}
	dir, err := projectDir(body.Dir)
	if err != nil {
		sw.emit([]byte("✗ " + err.Error()))
		emitDone(w, flusher, false, 1, err.Error())
		return
	}
	body.Dir = dir
	// doctor/adopt don't need an existing manifest; everything else does.
	if !execNoProject[body.Args[0]] {
		safeBase, absErr := filepath.Abs(body.Dir)
		manifest, joinErr := filepath.Abs(filepath.Join(body.Dir, ".keel", "manifest.yaml"))
		if absErr != nil || joinErr != nil || !strings.HasPrefix(manifest, safeBase) {
			sw.emit([]byte("✗ not a keel project: " + body.Dir))
			emitDone(w, flusher, false, 1, "not a keel project: "+body.Dir)
			return
		}
		if _, err := os.Stat(manifest); err != nil {
			sw.emit([]byte("✗ not a keel project: " + body.Dir))
			emitDone(w, flusher, false, 1, "not a keel project: "+body.Dir)
			return
		}
	}
	exe, err := os.Executable()
	if err != nil {
		exe = "keel"
	}
	// RunArgv, not Run: body.Args reaches us straight from the browser, so it is
	// passed as argv with no shell in between — mirroring handleRun (console.go)
	// and handleGenerate (generate_plan.go). A previous shellJoin→sh -c hop meant
	// an arg containing ; | & etc. was a shell fragment, not data.
	sw.emit([]byte("→ keel " + strings.Join(body.Args, " ")))
	runErr := execArgv(r.Context(), sw, body.Dir, append([]string{exe}, body.Args...))
	sw.flushRemainder()
	if runErr != nil {
		sw.emit([]byte("✗ " + runErr.Error()))
		emitDone(w, flusher, false, exitCode(runErr), runErr.Error())
	} else {
		sw.emit([]byte("✓ done"))
		emitDone(w, flusher, true, 0, "")
	}
}

// projectDB resolves the DSN for a tracked project and opens a real connection
// to its database via a Go SQL driver. It is the shared front door for every
// structured DB endpoint: it validates the dir, reads the manifest, derives the
// DSN from the running env (ddev describe / compose+.env), and hands back an
// open *sql.DB plus the engine kind. The caller owns closing the DB.
//
// It is a seam (package var) so a test can hand the grid/commit handlers a fake
// *sql.DB and exercise their full read/write path without a live database.
var projectDB = func(ctx context.Context, dir string) (*sql.DB, dbEngine, error) {
	dir, err := projectDir(dir)
	if err != nil {
		return nil, "", err
	}
	m, err := engine.ReadManifest(dir)
	if err != nil {
		return nil, "", fmt.Errorf("not a keel project: %s", dir)
	}
	reg, _ := catalog.Registry()
	env, _ := reg.Get(m.Env)
	// The project's env files are where a real deployment's own DB creds live;
	// they override keel's dev defaults where present. Node/Next projects keep
	// their secrets in .env.local, which wins over .env by that ecosystem's own
	// convention, so a hosted-Supabase Next project resolves to its real database.
	envGet := projectEnvGetter(dir)
	d, err := deriveDSN(ctx, dir, env.Commands["exec"], m, envGet)
	if err != nil {
		return nil, "", err
	}
	db, err := openDB(ctx, d)
	if err != nil {
		return nil, "", fmt.Errorf("could not connect to the project database (is it running?): %w", err)
	}
	return db, d.Engine, nil
}

// projectEnvGetter reads a project's env with Node/Next precedence: .env.local
// overrides .env. It never fails — a missing file yields no keys — so a project
// with neither still resolves to keel's dev defaults. Reading .env.local is what
// makes a hosted-Supabase project's own credentials visible to the studio, which
// keyed only off .env before.
func projectEnvGetter(dir string) func(string) string {
	base := map[string]string{}
	for _, name := range []string{".env", ".env.local"} { // later file wins
		f, err := envfile.Load(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		for _, k := range f.Keys() {
			base[k] = f.Get(k)
		}
	}
	return func(k string) string { return base[k] }
}

// handleDBQuery runs a free-form SQL statement against a project's database
// through its env DB client, streaming the result. This is the raw SQL escape
// hatch that sits beside the structured grid: it shells to the same client you'd
// use by hand (ddev psql / docker compose exec) so an arbitrary statement (DDL,
// multi-row, EXPLAIN) still works. Loopback-only.
func handleDBQuery(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	var body struct {
		Dir string `json:"dir"`
		SQL string `json:"sql"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.SQL) == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	sw := &sseWriter{w: w, f: flusher}

	dir, err := projectDir(body.Dir)
	if err != nil {
		sw.emit([]byte("✗ " + err.Error()))
		emitDone(w, flusher, false, 1, err.Error())
		return
	}
	body.Dir = dir

	m, err := engine.ReadManifest(body.Dir)
	if err != nil {
		sw.emit([]byte("✗ not a keel project: " + body.Dir))
		emitDone(w, flusher, false, 1, "not a keel project: "+body.Dir)
		return
	}
	reg, _ := catalog.Registry()
	env, _ := reg.Get(m.Env)
	cmd := dbClientCmd(env.Commands["exec"], m, body.SQL)
	if cmd == "" {
		sw.emit([]byte("✗ could not determine a DB client for this project"))
		emitDone(w, flusher, false, 1, "could not determine a DB client for this project")
		return
	}
	runErr := (engine.ExecRunner{Out: sw}).Run(r.Context(), body.Dir, cmd)
	sw.flushRemainder()
	if runErr != nil {
		sw.emit([]byte("✗ " + runErr.Error()))
		emitDone(w, flusher, false, exitCode(runErr), runErr.Error())
		return
	}
	emitDone(w, flusher, true, 0, "")
}

// dbClientCmd builds a one-shot SQL command for the project's database, using
// the env's exec prefix (ddev has first-class psql/mysql helpers; otherwise we
// invoke the client with the default keel dev creds db/db). Used by the raw SQL
// runner only; the structured grid uses a real driver.
func dbClientCmd(exec string, m *engine.Manifest, sql string) string {
	isPG := false
	for _, id := range m.Recipes {
		if strings.Contains(id, "postgres") {
			isPG = true
		}
	}
	q := "'" + strings.ReplaceAll(sql, "'", "'\\''") + "'"
	if strings.HasPrefix(strings.TrimSpace(exec), "ddev") {
		if isPG {
			return "ddev psql -c " + q
		}
		return "ddev mysql -e " + q
	}
	if isPG {
		return strings.TrimSpace(exec + " psql -U db -d db -c " + q)
	}
	return strings.TrimSpace(exec + " mysql -u db -pdb db -e " + q)
}

// handleDBPing is the Data panel's pre-flight: it derives the project's DSN and
// opens a connection (openDB pings), returning {reachable, reason?} so the UI can
// gate the grid on a live database rather than opening it and letting the driver
// throw a raw "connection refused" into the page. The reason is a calm,
// actionable sentence, never the driver's own error string.
//
// It is a read that names a project by ?dir=, so it is a guarded GET. A
// reachable=false is a normal answer (the database is simply not up), so the
// HTTP status stays 200 — the caller reads the boolean, it does not catch an
// error.
func handleDBPing(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	// A short leash: a pre-flight that hangs is worse than one that says "not
	// reachable", because the UI is waiting on it before it can offer the grid.
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	db, _, err := projectDB(ctx, dir)
	if err != nil {
		writeJSON(w, map[string]any{"reachable": false, "reason": pingReason(err)})
		return
	}
	_ = db.Close()
	writeJSON(w, map[string]any{"reachable": true})
}

// pingReason turns a connection failure into a calm, actionable sentence and
// classifies it so the UI can offer the right next step: a configuration problem
// (not a keel project, no DSN) is "configure connection"; anything else is a
// database that is simply not running yet.
func pingReason(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not a keel project"),
		strings.Contains(msg, "not a tracked"),
		strings.Contains(msg, "bad directory"):
		return "Configure connection - this project has no reachable database configured."
	default:
		return "Database not reachable - start the environment, then reopen Data."
	}
}

// handleDBTables lists the project database's tables (public schema) as JSON via
// a real driver, so the grid offers a click-a-table browser.
func handleDBTables(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Dir string `json:"dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ctx, cancel := dbCtx(r.Context())
	defer cancel()
	db, eng, err := projectDB(ctx, body.Dir)
	if err != nil {
		writeJSON(w, map[string]any{"tables": []string{}, "error": err.Error()})
		return
	}
	defer db.Close()
	tables, err := listTables(ctx, db, eng)
	if err != nil {
		writeJSON(w, map[string]any{"tables": []string{}, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"tables": tables})
}

// listTables returns the public/default-schema table names, ordered, straight
// from information_schema so it matches what the grid can open.
func listTables(ctx context.Context, db *sql.DB, e dbEngine) ([]string, error) {
	var q string
	if e == enginePostgres {
		q = `SELECT table_name FROM information_schema.tables
		     WHERE table_schema='public' AND table_type='BASE TABLE' ORDER BY table_name`
	} else {
		q = `SELECT table_name FROM information_schema.tables
		     WHERE table_schema=DATABASE() AND table_type='BASE TABLE' ORDER BY table_name`
	}
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tables := []string{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tables = append(tables, t)
	}
	return tables, rows.Err()
}

// handleDBGrid returns one page of a table as structured JSON: typed columns,
// string-encoded rows, the primary key (so the UI knows which cells are
// editable), and the paging envelope. Filtering binds a real parameter and
// sort/filter columns are validated against the live schema — no request text
// is ever concatenated into SQL. Loopback-only, POST-only, token-guarded.
func handleDBGrid(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Dir       string `json:"dir"`
		Table     string `json:"table"`
		Page      int    `json:"page"`
		PageSize  int    `json:"pageSize"`
		Sort      string `json:"sort"`
		Desc      bool   `json:"desc"`
		FilterCol string `json:"filterCol"`
		Filter    string `json:"filter"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Table) == "" {
		badRequest(w, "bad request")
		return
	}
	ctx, cancel := dbCtx(r.Context())
	defer cancel()
	db, eng, err := projectDB(ctx, body.Dir)
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	defer db.Close()

	ok, err := tableExists(ctx, db, eng, body.Table)
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, map[string]any{"error": "no such table: " + body.Table})
		return
	}
	cols, err := listColumns(ctx, db, eng, body.Table)
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	// Mark FK columns so the UI can link a cell to its referenced row. Non-fatal
	// if it fails — the grid still renders, just without FK links.
	cols = attachFKs(ctx, db, eng, body.Table, cols)
	pk, err := primaryKey(ctx, db, eng, body.Table)
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}

	q := gridQuery{Table: body.Table, Page: body.Page, PageSize: body.PageSize, SortDesc: body.Desc}
	q.normalise()
	// Sort/filter columns must be real columns of this table, or they're dropped
	// — a caller can't sort or filter by an expression it invented.
	if validCol(cols, body.Sort) {
		q.SortCol = body.Sort
	}
	if validCol(cols, body.FilterCol) && strings.TrimSpace(body.Filter) != "" {
		q.FilterCol = body.FilterCol
		q.Filter = body.Filter
	}

	countSQL, countArgs := buildCount(eng, q)
	total := 0
	if err := db.QueryRowContext(ctx, countSQL, countArgs...).Scan(&total); err != nil {
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	selectSQL, selectArgs := buildSelect(eng, q, colNames(cols))
	rows, err := db.QueryContext(ctx, selectSQL, selectArgs...)
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	defer rows.Close()
	data, err := scanRows(rows)
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, gridResult{
		Table: body.Table, Columns: cols, Rows: data, PK: pk,
		Page: q.Page, PageSize: q.PageSize, Total: total,
	})
}

// handleDBCommit applies a batch of staged inline cell edits in ONE transaction:
// all succeed or none do. Every UPDATE is parameterised and keyed on the table's
// verified primary key, so an edit touches exactly one row; a table with no
// primary key is refused. Loopback-only, POST-only, token-guarded.
func handleDBCommit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Dir   string     `json:"dir"`
		Table string     `json:"table"`
		Edits []cellEdit `json:"edits"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Table) == "" || len(body.Edits) == 0 {
		badRequest(w, "bad request")
		return
	}
	ctx, cancel := dbCtx(r.Context())
	defer cancel()
	db, eng, err := projectDB(ctx, body.Dir)
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	defer db.Close()

	if ok, err := tableExists(ctx, db, eng, body.Table); err != nil || !ok {
		writeJSON(w, map[string]any{"error": "no such table: " + body.Table})
		return
	}
	cols, err := listColumns(ctx, db, eng, body.Table)
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	pk, err := primaryKey(ctx, db, eng, body.Table)
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	if len(pk) == 0 {
		writeJSON(w, map[string]any{"error": "table " + body.Table + " has no primary key: editing is disabled"})
		return
	}
	// Validate every edit's column against the live schema BEFORE opening the
	// transaction, so a bad column can't leave a half-applied batch.
	for _, e := range body.Edits {
		if !validCol(cols, e.Column) {
			writeJSON(w, map[string]any{"error": "no such column: " + e.Column})
			return
		}
	}

	updated, err := applyEdits(ctx, db, eng, body.Table, body.Edits, pk)
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"committed": true, "updated": updated})
}

// applyEdits runs every staged edit inside one transaction and rolls back on the
// first failure, so the browser's "Commit" is atomic. It returns the number of
// rows changed. Split out of the handler so the transaction path is unit-tested
// against a fake DB with no network.
func applyEdits(ctx context.Context, db *sql.DB, eng dbEngine, table string, edits []cellEdit, pk []string) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	updated := 0
	for _, e := range edits {
		q, args, err := buildUpdate(eng, table, e.Column, e.Value, e.Key, pk)
		if err != nil {
			if rb := tx.Rollback(); rb != nil {
				err = errors.Join(err, fmt.Errorf("rollback: %w", rb))
			}
			return 0, err
		}
		res, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			if rb := tx.Rollback(); rb != nil {
				err = errors.Join(err, fmt.Errorf("rollback: %w", rb))
			}
			return 0, err
		}
		if n, err := res.RowsAffected(); err == nil {
			updated += int(n)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return updated, nil
}

// handleBrand and the brand endpoints live in brand.go.

// Version and Repo are set by the CLI at start-up, so the studio can report an
// available upgrade without owning keel's version number.
var (
	Version = "dev"
	Repo    = "coullworks/keel"
)

// newMux wires the studio's routes. mcpOpts, when non-nil, auto-starts keel's
// MCP server on POST /mcp behind the same guardAPI (loopback + same-origin +
// session token) as every /api route — so `keel studio` gives an agent a live,
// authenticated MCP endpoint in the studio's own process. nil leaves /mcp
// unmounted (the --no-mcp opt-out).
func newMux(tok string, mcpOpts *mcp.Options) *http.ServeMux {
	get := []string{http.MethodGet}
	post := []string{http.MethodPost}
	mux := http.NewServeMux()
	if mcpOpts != nil {
		mountMCP(mux, tok, *mcpOpts)
	}
	mux.HandleFunc("/api/recipes", guardAPI(tok, get, handleRecipes))
	mux.HandleFunc("/api/resolve", guardAPI(tok, post, handleResolve))
	mux.HandleFunc("/api/projects", guardAPI(tok, []string{http.MethodGet, http.MethodPost, http.MethodDelete}, handleProjects))
	// Per-project dashboard actions (open in editor, start/stop/restart the dev
	// env) and the Run & Logs live task tail. All POST-only, projectDir-validated.
	mux.HandleFunc("/api/project/action", guardAPI(tok, post, handleProjectAction))
	mux.HandleFunc("/api/run", guardAPI(tok, post, handleRun))
	mux.HandleFunc("/api/run/tasks", guardAPI(tok, post, handleRunTasks))
	mux.HandleFunc("/api/build", guardAPI(tok, post, handleBuild))
	mux.HandleFunc("/api/exec", guardAPI(tok, post, handleExec))
	// Framework-aware Generate menu + the Add-service picker: both are reads
	// keyed by ?dir=, resolving the project's own framework from its manifest so
	// each shows only what that stack supports (never the Laravel list universally).
	mux.HandleFunc("/api/generators", guardAPI(tok, get, handleGenerators))
	// The uniform Generate surface: one ModulePlan (module + staged components +
	// typed fields) is submitted here and rendered identically for every framework
	// — the whole plan at once where keel has a renderer (Magento), else per
	// component via `keel gen`. POST-only, projectDir-validated, token-guarded.
	mux.HandleFunc("/api/generate", guardAPI(tok, post, handleGenerate))
	mux.HandleFunc("/api/addable", guardAPI(tok, get, handleAddable))
	// The Deploy tab's target picker + per-target artifact listing, read from the
	// keel binary's own `keel deploy --json` so deploy.go stays the single source
	// of truth (no hardcoded target list in the frontend). A read keyed by ?dir=.
	mux.HandleFunc("/api/deploy/targets", guardAPI(tok, get, handleDeployTargets))
	// A monorepo member's effective (inherited) shared backend, so a member's
	// detail view resolves its one shared database from the monorepo root rather
	// than re-deriving a wrong local DSN. A read keyed by ?dir=, guarded as a GET.
	mux.HandleFunc("/api/member/backend", guardAPI(tok, get, handleMemberBackend))
	// The Overview's running-services dashboard: a GET lists the project env's
	// services + each one's up/down state (?dir=), and a POST starts/stops/restarts
	// one named service and streams the result. Both guarded like the DB routes.
	mux.HandleFunc("/api/services", guardAPI(tok, get, handleServices))
	mux.HandleFunc("/api/service/action", guardAPI(tok, post, handleServiceAction))
	// The Overview's data-driven, per-framework fact feed (?dir=, cached), and the
	// Manage-services tab's list of recipes this project already has (?dir=), so
	// each can be shown with a Remove. Both guarded GETs, projectDir-validated.
	mux.HandleFunc("/api/overview/facts", guardAPI(tok, get, handleOverviewFacts))
	// The Overview DB tile loads on its OWN async path so the dashboard never
	// blocks on the database ping (which can take ~5s when the DB is down).
	mux.HandleFunc("/api/overview/db", guardAPI(tok, get, handleOverviewDB))
	mux.HandleFunc("/api/installed", guardAPI(tok, get, handleInstalled))
	mux.HandleFunc("/api/db/ping", guardAPI(tok, get, handleDBPing))
	mux.HandleFunc("/api/db/query", guardAPI(tok, post, handleDBQuery))
	mux.HandleFunc("/api/db/tables", guardAPI(tok, post, handleDBTables))
	mux.HandleFunc("/api/db/grid", guardAPI(tok, post, handleDBGrid))
	mux.HandleFunc("/api/db/commit", guardAPI(tok, post, handleDBCommit))
	// Brand: the legacy two-colour per-project apply (back-compat), the GLOBAL
	// default editor (Settings → Brand, over ~/.config/keel/brand.yaml), and the
	// per-project override (the project view's Brand tab, over the manifest brand
	// block). All three behind the same guardAPI. The /global and /project GETs
	// are reads; their POSTs and the legacy /api/brand are writes.
	mux.HandleFunc("/api/brand", guardAPI(tok, post, handleBrand))
	mux.HandleFunc("/api/brand/global", guardAPI(tok, []string{http.MethodGet, http.MethodPost}, handleBrandGlobal))
	mux.HandleFunc("/api/brand/project", guardAPI(tok, []string{http.MethodGet, http.MethodPost}, handleBrandProject))
	// Magento config for the Env & Secrets tab: reads app/etc/env.xml (+ .env) and
	// surfaces the keys with every secret VALUE masked. A read, GET-only.
	mux.HandleFunc("/api/env", guardAPI(tok, get, handleEnv))
	mux.HandleFunc("/api/magento/env", guardAPI(tok, get, handleMagentoEnv))
	mux.HandleFunc("/api/packs", guardAPI(tok, []string{http.MethodGet, http.MethodPost}, handlePacks))
	mux.HandleFunc("/api/profile", guardAPI(tok, []string{http.MethodGet, http.MethodPost}, handleProfile))
	// Plugin screens. Listing is a read; rendering takes a project and so is a
	// post, guarded like every other mutating route even though it only reads.
	mux.HandleFunc("/api/plugins", guardAPI(tok, []string{http.MethodGet, http.MethodPost}, handlePlugins))
	mux.HandleFunc("/api/plugins/screens", guardAPI(tok, get, handleScreens))
	mux.HandleFunc("/api/plugins/screen", guardAPI(tok, post, handleScreen))
	// The wizard options the applicable plugins would ask during a build,
	// described as data so the Build flow can render a real form (e.g. ai-core's
	// skills/agents) instead of running each plugin with its defaults. A read
	// keyed by ?framework= (new stack) or ?dir= (rebuild).
	mux.HandleFunc("/api/plugin-options", guardAPI(tok, get, handlePluginOptions))
	// The interactive per-project option form (ai-core's skills + rules): what a
	// project currently has (so the form pre-checks from installed state, not just
	// the recommended defaults) is a read keyed by ?dir=; applying the edited
	// selection is a reconciling write that adds and removes on the fly.
	mux.HandleFunc("/api/plugin-state", guardAPI(tok, get, handlePluginState))
	mux.HandleFunc("/api/plugin/apply", guardAPI(tok, post, handlePluginApply))
	// The additive render hooks the studio consumes: overview sections and
	// per-project actions (both take a project, so POST), and global plugin pages.
	// An action that runs is capability-gated in the handler.
	mux.HandleFunc("/api/plugin-overview", guardAPI(tok, post, handleOverview))
	mux.HandleFunc("/api/plugin-actions", guardAPI(tok, post, handlePluginActions))
	mux.HandleFunc("/api/plugin/action", guardAPI(tok, post, handlePluginAction))
	mux.HandleFunc("/api/plugin-pages", guardAPI(tok, get, handlePluginPages))
	mux.HandleFunc("/api/plugin/page", guardAPI(tok, post, handlePluginPage))
	mux.HandleFunc("/api/plugin/call", guardAPI(tok, post, handlePluginCall))
	assetSrv := http.FileServer(http.FS(assetsFS))
	mux.HandleFunc("/assets/", loopbackOnly(func(w http.ResponseWriter, r *http.Request) { assetSrv.ServeHTTP(w, r) }))
	// A trusted plugin's built UI bundle, mounted by the React studio (no iframe).
	mux.HandleFunc("/plugin-assets/", loopbackOnly(handlePluginAsset))
	// Serve the anchor as the favicon so browsers' automatic /favicon.ico request
	// gets a 200 instead of a 404 (which shows as a console "failed to fetch").
	mux.HandleFunc("/favicon.ico", loopbackOnly(func(w http.ResponseWriter, r *http.Request) {
		b, err := assetsFS.ReadFile("assets/favicon.png")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(b)
	}))
	// The React studio's hashed JS/CSS live under /app (Vite assetsDir), served
	// only when the build is present. Hashed names are immutable, so cache hard.
	if dist, ok := reactDist(); ok {
		appSrv := http.FileServer(http.FS(dist))
		mux.HandleFunc("/app/", loopbackOnly(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			appSrv.ServeHTTP(w, r)
		}))
	}
	mux.HandleFunc("/", loopbackOnly(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// Never cache the UI: the HTML is embedded and changes with every keel
		// build, so a cached copy would silently hide new features after upgrade.
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		// The page carries this session's token so it, and only it, can call the
		// API. Never cached, so the token never outlives the process that issued it.
		html := indexHTML
		if dist, ok := reactDist(); ok && reactEnabled() {
			if b, err := fs.ReadFile(dist, "index.html"); err == nil {
				html = string(b)
			}
		}
		_, _ = io.WriteString(w, strings.Replace(html, tokenPlaceholder, tok, 1))
	}))
	return mux
}

// Serve starts the studio on addr (loopback), optionally opening the browser.
// Progress goes to w so the caller owns the stream (nil means os.Stdout).
//
// Unless noMCP is set, the studio also auto-starts keel's MCP server on POST /mcp
// behind its own guardAPI (same loopback + same-origin + session token as every
// /api route), so an agent gets a live, authenticated endpoint without a second
// process. The endpoint is printed next to the studio URL on startup; --no-mcp
// leaves it unmounted for anyone who wants the studio without the agent surface.
func Serve(w io.Writer, addr string, open, noMCP bool) error {
	if w == nil {
		w = os.Stdout
	}
	if addr == "" {
		addr = "127.0.0.1:7373"
	}
	if err := requireLoopbackAddr(addr); err != nil {
		return fmt.Errorf("keel studio: %w", err)
	}
	tok := newToken()
	var mcpOpts *mcp.Options
	if !noMCP {
		o := mcpOptions()
		mcpOpts = &o
	}
	mux := newMux(tok, mcpOpts)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// A busy port is the common case (a studio already running, or another
		// process on 7373): say so plainly instead of leaking a raw syscall error,
		// so the person reaches for the right fix rather than a stack trace.
		if errors.Is(err, syscall.EADDRINUSE) {
			return fmt.Errorf("keel studio: %s is already in use. Is a studio already running? (stop it, or pass --addr with a free port)", addr)
		}
		return fmt.Errorf("keel studio: %w", err)
	}
	url := "http://" + addr + "/"
	fmt.Fprintf(w, "keel studio → %s   (Ctrl-C to stop)\n", url)
	if !noMCP {
		// The live MCP endpoint an agent registers against. It is loopback + token
		// guarded like the rest of the studio, so the session token is required —
		// the Settings "Connect keel to your agent" card shows the full command.
		fmt.Fprintf(w, "keel MCP   → http://%s%s   (X-Keel-Token required, see Settings)\n", addr, mcpEndpointPath)
	}
	if open {
		_ = platform.OpenURL(url)
	}

	// Graceful shutdown on Ctrl+C / SIGTERM so we don't orphan the server.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serveOn(ctx, w, ln, mux)
}

// serveOn runs mux on ln until ctx is cancelled (graceful shutdown) or the
// server stops on its own. Split out of Serve so the serve/shutdown loop is
// testable with a loopback listener + a cancellable context — no OS signals and
// no reliance on a well-known port.
func serveOn(ctx context.Context, w io.Writer, ln net.Listener, mux http.Handler) error {
	if w == nil {
		w = os.Stdout
	}
	srv := &http.Server{
		Handler: mux,
		// Bound how long a client may take to send its headers, so a stalled
		// connection cannot hold a slot open indefinitely. There is deliberately
		// no write timeout: builds stream over SSE for as long as they take.
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	select {
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-ctx.Done():
		fmt.Fprintln(w, "\nkeel studio stopping…")
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	}
}
