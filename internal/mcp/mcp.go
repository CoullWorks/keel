// Package mcp exposes keel to AI coding agents over the Model
// Context Protocol on stdio. Zero external deps: a small JSON-RPC 2.0 loop.
//
// Philosophy — "AI chooses, keel guarantees": the tools let an agent *understand*
// and *select* tested recipes and inspect projects; keel still runs the real,
// deterministic installers. Read-only by default; write tools (scaffold/adopt/
// db/deploy) only register when Options.Write is set, mirroring GitHub/Supabase
// MCP safety practice.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/gen"
	"github.com/coullworks/keel/internal/optimize"
	"github.com/coullworks/keel/internal/project"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

const protocolVersion = "2024-11-05"

// Options configures the server.
type Options struct {
	Version string
	Write   bool // register write tools (scaffold/adopt/db/deploy)
	// RunKeel executes a keel subcommand in dir for write tools (injected so the
	// mcp package doesn't import cli). Streams combined output to the returned
	// string. Nil disables write execution.
	RunKeel func(ctx context.Context, dir string, args []string) (string, error)
}

// --- JSON-RPC envelopes ---

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// tool is one MCP tool.
type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	handler     func(ctx context.Context, args map[string]any) (any, error)
}

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}
func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func strArr(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

// fieldArraySchema is the JSON Schema for keel_generate's typed `fields` list —
// the mage2gen primitive as a structured input so an agent describes columns as
// objects (not a stringly-typed grammar). The `type` enum is sourced from
// gen.FieldTypeStrings so the schema and the validator never drift.
func fieldArraySchema() map[string]any {
	types := make([]any, len(gen.FieldTypeStrings()))
	for i, t := range gen.FieldTypeStrings() {
		types[i] = t
	}
	return map[string]any{
		"type":        "array",
		"description": "typed columns for a model-shaped component; each becomes real DDL",
		"items": obj(map[string]any{
			"name":     str("column name (plain identifier)"),
			"type":     map[string]any{"type": "string", "enum": types, "description": "column type"},
			"nullable": map[string]any{"type": "boolean"},
			"unique":   map[string]any{"type": "boolean"},
			"index":    map[string]any{"type": "boolean"},
			"default":  str("default value, rendered literally"),
			"length":   map[string]any{"type": "integer", "description": "column length (0 = the type's default)"},
		}, "name", "type"),
	}
}

// server is the transport-agnostic MCP dispatcher: it holds the tool set (built
// once from Options) and turns one JSON-RPC request into one response. Both the
// stdio loop (Serve) and the HTTP transport (ServeHTTP) drive the same server,
// so the initialize/tools/list/tools/call semantics are defined in exactly one
// place regardless of how the bytes arrive.
type server struct {
	opts   Options
	tools  []tool
	byName map[string]tool
}

// newServer builds the tool set for opts once. Reused by every request so tool
// construction (which touches the recipe catalogue for schemas) is not repeated
// per call.
func newServer(opts Options) *server {
	tools := buildTools(opts)
	byName := make(map[string]tool, len(tools))
	for _, t := range tools {
		byName[t.Name] = t
	}
	return &server{opts: opts, tools: tools, byName: byName}
}

// handle dispatches one JSON-RPC request to a response. A notification (no id)
// yields the zero rpcResponse and reply=false, so the caller can suppress the
// write; every id-bearing request produces a response (a tool error surfaces as
// an isError result, never a transport error, matching MCP practice). This is
// the single dispatch both transports share.
func (s *server) handle(ctx context.Context, req rpcRequest) (resp rpcResponse, reply bool) {
	if len(req.ID) == 0 { // notification (e.g. notifications/initialized) — no reply
		return rpcResponse{}, false
	}
	switch req.Method {
	case "initialize":
		return rpcResponse{ID: req.ID, Result: map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "keel", "version": s.opts.Version},
		}}, true
	case "ping":
		return rpcResponse{ID: req.ID, Result: map[string]any{}}, true
	case "tools/list":
		list := make([]tool, len(s.tools))
		copy(list, s.tools)
		return rpcResponse{ID: req.ID, Result: map[string]any{"tools": list}}, true
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		t, ok := s.byName[p.Name]
		if !ok {
			return rpcResponse{ID: req.ID, Result: toolError("unknown tool: " + p.Name)}, true
		}
		res, err := t.handler(ctx, p.Arguments)
		if err != nil {
			return rpcResponse{ID: req.ID, Result: toolError(err.Error())}, true
		}
		return rpcResponse{ID: req.ID, Result: toolText(res)}, true
	default:
		return rpcResponse{ID: req.ID, Error: &rpcError{Code: -32601, Message: "method not found: " + req.Method}}, true
	}
}

// Serve runs the MCP server loop over in/out until in is closed. It is the stdio
// transport: each newline-delimited JSON-RPC request is dispatched through the
// shared server.handle and its response written back as one JSON line.
func Serve(ctx context.Context, in io.Reader, out io.Writer, opts Options) error {
	srv := newServer(opts)
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		resp, reply := srv.handle(ctx, req)
		if !reply {
			continue
		}
		resp.JSONRPC = "2.0"
		_ = enc.Encode(resp)
	}
	return sc.Err()
}

// toolText wraps a value as an MCP tool result (pretty JSON in a text block).
func toolText(v any) map[string]any {
	b, _ := json.MarshalIndent(v, "", "  ")
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}}
}
func toolError(msg string) map[string]any {
	return map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": msg}}}
}

// --- tools ---

func buildTools(opts Options) []tool {
	ts := []tool{
		{
			Name:        "keel_list_frameworks",
			Description: "List the frameworks keel can scaffold, each with its available environments and databases. Use this first to understand what stacks are possible.",
			InputSchema: obj(nil),
			handler:     handleListFrameworks,
		},
		{
			Name:        "keel_list_recipes",
			Description: "List keel recipes (framework/env/db/service/addon/frontend/extra), optionally filtered by kind and/or framework. Recipes are the tested building blocks a stack is composed from.",
			InputSchema: obj(map[string]any{"kind": str("filter by kind: framework|env|db|service|addon|frontend|extra"), "framework": str("filter to recipes applicable to this framework id")}),
			handler:     handleListRecipes,
		},
		{
			Name:        "keel_resolve_plan",
			Description: "Resolve a set of recipe ids into an ordered, validated build plan and preview the exact shell steps that would run (a dry run, nothing is executed). Use this to check a stack composes before scaffolding.",
			InputSchema: obj(map[string]any{"ids": strArr("recipe ids, e.g. [\"laravel\",\"ddev\",\"postgres\",\"filament\"]")}, "ids"),
			handler:     handleResolve,
		},
		{
			Name:        "keel_list_projects",
			Description: "List the projects keel is tracking on this machine (path, detected stack, whether keel-managed, and monorepo members).",
			InputSchema: obj(nil),
			handler:     handleListProjects,
		},
		{
			Name:        "keel_optimize",
			Description: "Run keel's read-only optimization scan over a project and return structured findings (security: committed secrets/.env, hardcoded keys; performance: Next.js next/image & next/link; hygiene). Never modifies the project.",
			InputSchema: obj(map[string]any{"path": str("project directory to scan (default: current dir)")}),
			handler:     handleOptimize,
		},
		{
			Name:        "keel_list_generatables",
			Description: "List what keel can generate into a project (models, controllers, modules, the auth stack, …), each with its level (code-block|module|package|stack) and its typed inputs (each input's name/type/required/choices). Framework comes from the project manifest unless overridden. Use this to discover what you can generate here and what inputs each component takes before calling keel_generate.",
			InputSchema: obj(map[string]any{"path": str("project directory (default: current dir)"), "framework": str("override the framework id detected from the project manifest")}),
			handler:     handleListGeneratables,
		},
	}
	if opts.Write && opts.RunKeel != nil {
		ts = append(ts,
			tool{
				Name:        "keel_scaffold",
				Description: "Scaffold a new project by running keel new for the given framework and add-ons. WRITE: runs the real installers. Resolve the plan first.",
				InputSchema: obj(map[string]any{"framework": str("framework id"), "name": str("project directory name"), "with": strArr("extra recipe ids (env/db/services/addons)")}, "framework"),
				// dirAny: scaffolding creates a NEW directory and always runs from cwd.
				handler: writeHandler(opts, dirAny, scaffoldArgs),
			},
			tool{
				Name:        "keel_adopt",
				Description: "Adopt an existing project so keel can manage it (writes .keel/manifest.yaml). WRITE.",
				InputSchema: obj(map[string]any{"path": str("project directory")}, "path"),
				// dirExists: adopt's job is to bring an untracked-but-existing dir under
				// management, so it requires the dir to exist, not to be tracked yet.
				handler: writeHandler(opts, dirExists, func(a map[string]any) (string, []string) { return s(a, "path"), []string{"adopt"} }),
			},
			tool{
				Name:        "keel_run",
				Description: "Run a project task by its canonical name (dev|test|lint|typecheck|build|…) in a keel project. Same vocabulary across every stack. WRITE. Requires a tracked keel project (adopt it first).",
				InputSchema: obj(map[string]any{"path": str("project directory"), "task": str("task name, e.g. test|lint|typecheck")}, "path", "task"),
				handler:     writeHandler(opts, dirTracked, func(a map[string]any) (string, []string) { return s(a, "path"), []string{"run", s(a, "task")} }),
			},
			tool{
				Name:        "keel_commerce_ready",
				Description: "Make a store AI-agent-ready: scaffold agentic-commerce artifacts (JSON-LD product markup, product feed, agents.json) and run the configured readiness tool. WRITE. Requires a tracked keel project.",
				InputSchema: obj(map[string]any{"path": str("store project directory")}, "path"),
				handler:     writeHandler(opts, dirTracked, func(a map[string]any) (string, []string) { return s(a, "path"), []string{"commerce", "ready"} }),
			},
			tool{
				Name:        "keel_db",
				Description: "Run a database task (migrate|seed|reset|status) in a keel-managed project. WRITE. Requires a tracked keel project.",
				InputSchema: obj(map[string]any{"path": str("project directory"), "action": str("migrate|seed|reset|status")}, "path", "action"),
				handler:     writeHandler(opts, dirTracked, func(a map[string]any) (string, []string) { return s(a, "path"), []string{"db", s(a, "action")} }),
			},
			tool{
				Name:        "keel_deploy",
				Description: "Generate production deploy artifacts (compose|fly|vps|render|railway|vercel) for a project. WRITE (writes files only; no cloud calls). Requires a tracked keel project.",
				InputSchema: obj(map[string]any{"path": str("project directory"), "target": str("compose|fly|vps|render|railway|vercel")}, "path"),
				handler: writeHandler(opts, dirTracked, func(a map[string]any) (string, []string) {
					args := []string{"deploy"}
					if t := s(a, "target"); t != "" {
						args = append(args, t)
					}
					return s(a, "path"), args
				}),
			},
			tool{
				Name:        "keel_generate",
				Description: "Generate a code component into a project: a model/controller/module (code-block/module) via `keel gen`, or a whole capability like auth (level:stack) via `keel add`. Models take a typed `fields` list that renders into real columns (migration/db_schema/model). Call keel_list_generatables first to learn the component keys and their inputs. WRITE: runs the real generator. dryRun defaults to true. Preview the plan, then re-call with dryRun:false to write.",
				InputSchema: obj(map[string]any{
					"path":      str("project directory"),
					"component": str("component key from keel_list_generatables (e.g. model, controller, module, auth)"),
					"name":      str("class/identifier name for the component (plain identifier; slashes for namespaces)"),
					"module":    str("Magento module as Vendor/Module (for components that live in a module)"),
					"fields":    fieldArraySchema(),
					"inputs":    map[string]any{"type": "object", "description": "extra typed inputs for the component (see keel_list_generatables inputs)"},
					"dryRun":    map[string]any{"type": "boolean", "description": "preview only, write nothing (default: true)"},
				}, "path", "component", "name"),
				handler: handleGenerate(opts),
			},
		)
	}
	sort.SliceStable(ts, func(i, j int) bool { return ts[i].Name < ts[j].Name })
	return ts
}

// dirPolicy says what a write tool requires of the directory it acts inside.
// The MCP write surface runs real installers/tasks against a path an agent
// supplies, so — exactly like the studio's /api handlers (internal/studio's
// projectDir) — a tool that mutates an existing project must refuse a path that
// is not one keel already tracks. Scaffolding a NEW project is the deliberate
// exception, and adopting an untracked dir is what brings it under management.
type dirPolicy int

const (
	// dirTracked: the dir must already be a tracked keel project (or a member of
	// one). This is the invariant for every tool that runs a task, touches the
	// database, deploys, or generates into an existing project.
	dirTracked dirPolicy = iota
	// dirExists: the dir need not be tracked yet but must exist — the adopt tool,
	// whose whole purpose is to bring an existing directory under management.
	dirExists
	// dirAny: no project constraint — scaffolding a NEW project (keel_scaffold),
	// which creates the directory and always runs from the current working dir.
	dirAny
)

// resolveWriteDir applies a dirPolicy to a caller-supplied path, returning the
// absolute directory a write tool may act inside or a clear error. It mirrors
// internal/studio's projectDir: an empty path means the current working dir
// (which is not attacker-chosen and always addressable), a leading ~ is
// expanded, and dirTracked rejects anything not in the project registry. This is
// the single choke point that stops a token-holding agent from running
// `keel db reset` / `keel run <task>` / generating in an arbitrary directory.
func resolveWriteDir(dir string, policy dirPolicy) (string, error) {
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	abs, err := filepath.Abs(project.Expand(dir))
	if err != nil {
		return "", fmt.Errorf("bad directory: %w", err)
	}
	abs = filepath.Clean(abs)
	switch policy {
	case dirAny:
		return abs, nil
	case dirExists:
		if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
			return "", fmt.Errorf("no such directory: %s", abs)
		}
		return abs, nil
	}
	// dirTracked: the working directory is always addressable (a CLI-less agent
	// starts there and it is not chosen from the request), everything else must
	// be a tracked project or a member of one.
	if cwd, err := os.Getwd(); err == nil && filepath.Clean(cwd) == abs {
		return abs, nil
	}
	reg, err := project.Load()
	if err != nil {
		return "", fmt.Errorf("could not read the project list: %w", err)
	}
	for _, p := range reg.Projects {
		if filepath.Clean(p.Path) == abs {
			return abs, nil
		}
		for _, m := range project.Members(p.Path) {
			if filepath.Clean(m.Path) == abs {
				return abs, nil
			}
		}
	}
	return "", fmt.Errorf("not a tracked keel project: %s (adopt it first with keel_adopt)", abs)
}

func s(a map[string]any, k string) string {
	if v, ok := a[k].(string); ok {
		return v
	}
	return ""
}
func strList(a map[string]any, k string) []string {
	var out []string
	if arr, ok := a[k].([]any); ok {
		for _, v := range arr {
			if str, ok := v.(string); ok {
				out = append(out, str)
			}
		}
	}
	return out
}

type recipeDTO struct {
	ID, Kind, Label, Lang string
	AppliesTo             []string `json:",omitempty"`
	Default               bool
	// DefaultFor overrides Default per framework: one shared database recipe is
	// the default for some frameworks and not others.
	DefaultFor []string `json:",omitempty"`
}

func dto(r recipe.Recipe) recipeDTO {
	return recipeDTO{ID: r.ID, Kind: string(r.Kind), Label: r.Label, Lang: r.Lang, AppliesTo: r.AppliesTo, Default: r.Default, DefaultFor: r.DefaultFor}
}

func handleListFrameworks(_ context.Context, _ map[string]any) (any, error) {
	reg, err := catalog.Registry()
	if err != nil {
		return nil, err
	}
	type fwOut struct {
		ID, Label, Lang string
		Envs, Databases []string
	}
	var out []fwOut
	for _, fw := range reg.OfKind(recipe.Framework) {
		f := fwOut{ID: fw.ID, Label: fw.Label, Lang: fw.Lang}
		for _, e := range reg.ForFramework(fw.ID, recipe.Env) {
			f.Envs = append(f.Envs, e.ID)
		}
		for _, d := range reg.ForFramework(fw.ID, recipe.DB) {
			f.Databases = append(f.Databases, d.ID)
		}
		out = append(out, f)
	}
	return out, nil
}

func handleListRecipes(_ context.Context, a map[string]any) (any, error) {
	reg, err := catalog.Registry()
	if err != nil {
		return nil, err
	}
	kind, fw := s(a, "kind"), s(a, "framework")
	var out []recipeDTO
	for _, r := range reg.All() {
		if kind != "" && string(r.Kind) != kind {
			continue
		}
		if fw != "" && !r.AppliesToFramework(fw) {
			continue
		}
		out = append(out, dto(r))
	}
	return out, nil
}

func handleResolve(_ context.Context, a map[string]any) (any, error) {
	reg, err := catalog.Registry()
	if err != nil {
		return nil, err
	}
	plan, err := resolver.Resolve(reg, strList(a, "ids"))
	if err != nil {
		return nil, err
	}
	recs := make([]recipeDTO, 0, len(plan.Recipes))
	for _, r := range plan.Recipes {
		recs = append(recs, dto(r))
	}
	return map[string]any{"framework": plan.Framework, "recipes": recs, "steps": engine.Steps(plan)}, nil
}

func handleListProjects(_ context.Context, _ map[string]any) (any, error) {
	reg, err := project.Load()
	if err != nil {
		return nil, err
	}
	reg.Refresh()
	return reg.Projects, nil
}

func handleOptimize(_ context.Context, a map[string]any) (any, error) {
	dir := s(a, "path")
	if dir == "" {
		dir = "."
	}
	dir = project.Expand(dir)
	fw := ""
	if m, err := engine.ReadManifest(dir); err == nil {
		fw = m.Framework
	} else {
		fw = project.Detect(dir)
	}
	rep := optimize.Run(dir, fw)
	return map[string]any{
		"findings": rep.Findings,
		"summary": map[string]int{
			"error": rep.Count(optimize.SevError), "warn": rep.Count(optimize.SevWarn), "info": rep.Count(optimize.SevInfo),
		},
	}, nil
}

// resolveFramework returns the framework id for a project dir: the manifest's
// framework, falling back to detection, with an explicit override winning. It is
// the one place the generate tools resolve "which framework is this project",
// mirroring handleOptimize so the answer is consistent across the MCP surface.
func resolveFramework(dir, override string) string {
	if override != "" {
		return override
	}
	if m, err := engine.ReadManifest(dir); err == nil && m.Framework != "" {
		return m.Framework
	}
	return project.Detect(dir)
}

// generatableDTO is the JSON shape of a Generatable for keel_list_generatables:
// enough for an agent to pick a component and know what inputs to send, without
// leaking Go types across the wire.
type generatableDTO struct {
	Key       string            `json:"key"`
	Label     string            `json:"label"`
	Level     string            `json:"level"`
	Framework string            `json:"framework"`
	Provides  []string          `json:"provides,omitempty"`
	Inputs    []recipe.GenInput `json:"inputs,omitempty"`
}

func handleListGeneratables(_ context.Context, a map[string]any) (any, error) {
	dir := s(a, "path")
	if dir == "" {
		dir = "."
	}
	dir = project.Expand(dir)
	fw := resolveFramework(dir, s(a, "framework"))
	// reg may fail to load; Generatables tolerates a nil registry (built-ins only),
	// so a catalogue error degrades to the built-in generators rather than failing.
	reg, _ := catalog.Registry()
	gens := gen.Generatables(reg, fw)
	out := make([]generatableDTO, 0, len(gens))
	for _, g := range gens {
		out = append(out, generatableDTO{
			Key: g.Key, Label: g.Label, Level: string(g.Level),
			Framework: g.Framework, Provides: g.Provides, Inputs: g.Inputs,
		})
	}
	return map[string]any{"framework": fw, "generatables": out}, nil
}

// fieldsFromArgs decodes keel_generate's structured `fields` list into typed
// gen.Field values. It reads the same attribute set the CLI --field grammar and
// the studio's fields table produce, so one structured column description renders
// identically however it arrived.
func fieldsFromArgs(a map[string]any) []gen.Field {
	raw, ok := a["fields"].([]any)
	if !ok {
		return nil
	}
	out := make([]gen.Field, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		f := gen.Field{
			Name:     s(m, "name"),
			Type:     gen.FieldType(s(m, "type")),
			Nullable: boolArg(m, "nullable"),
			Default:  s(m, "default"),
			Unique:   boolArg(m, "unique"),
			Index:    boolArg(m, "index"),
			Length:   intArg(m, "length"),
		}
		out = append(out, f)
	}
	return out
}

func boolArg(a map[string]any, k string) bool {
	b, _ := a[k].(bool)
	return b
}

// intArg reads an integer argument. JSON numbers decode to float64 through
// encoding/json into a map[string]any, so accept that too.
func intArg(a map[string]any, k string) int {
	switch v := a[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

// fieldFlag renders one typed field into the CLI `--field` grammar
// (name:type[,attr]...), so keel_generate hands `keel gen` exactly the value the
// CLI parses. Only set attributes are emitted, matching how a hand-typed flag
// reads.
func fieldFlag(f gen.Field) string {
	var b strings.Builder
	b.WriteString(f.Name)
	b.WriteByte(':')
	b.WriteString(string(f.Type))
	if f.Nullable {
		b.WriteString(",nullable")
	}
	if f.Unique {
		b.WriteString(",unique")
	}
	if f.Index {
		b.WriteString(",index")
	}
	if f.Length > 0 {
		fmt.Fprintf(&b, ",len=%d", f.Length)
	}
	if f.Default != "" {
		fmt.Fprintf(&b, ",default=%s", f.Default)
	}
	return b.String()
}

// handleGenerate is the write-gated generation tool. It validates the name and
// fields in-handler (a bad input is a clean tool error, never a re-exec), then
// maps the structured request onto the real CLI: a stack generatable (auth)
// resolves to `keel add <recipe...>` — so "generate auth" is explicit about
// installing recipes — and everything else runs `keel gen <component> <name>`
// with the typed fields as --field flags. Re-exec keeps this consistent with the
// other write tools.
func handleGenerate(opts Options) func(context.Context, map[string]any) (any, error) {
	return func(ctx context.Context, a map[string]any) (any, error) {
		// keel_generate writes into an EXISTING project, so it requires a tracked
		// one — the same invariant the studio's generate handler enforces.
		dir, err := resolveWriteDir(s(a, "path"), dirTracked)
		if err != nil {
			return nil, err
		}
		component := s(a, "component")
		name := s(a, "name")
		if component == "" {
			return nil, fmt.Errorf("component is required")
		}
		if err := gen.ValidateName(name); err != nil {
			return nil, err
		}
		fields := fieldsFromArgs(a)
		if err := gen.ValidateFields(fields); err != nil {
			return nil, err
		}

		fw := resolveFramework(dir, "")
		reg, _ := catalog.Registry()

		// A stack generatable (auth) installs recipes: resolve it to the addon ids
		// and run `keel add`, rather than pretending it is a code-block generator.
		if g, ok := generatableByKey(reg, fw, component); ok && g.Level == recipe.LevelStack {
			ids, ok := gen.ResolveStack(reg, g.Recipe)
			if !ok || len(ids) == 0 {
				return nil, fmt.Errorf("no recipes resolve for the %s stack on %s", component, fw)
			}
			args := append([]string{"add"}, ids...)
			args = append(args, "--yes")
			return runKeelArgs(ctx, opts, dir, args)
		}

		// dryRun defaults to true: preview unless explicitly turned off.
		dryRun := true
		if v, ok := a["dryRun"].(bool); ok {
			dryRun = v
		}
		args := []string{"gen", component, name}
		if module := s(a, "module"); module != "" {
			args = append(args, "--module", module)
		}
		for _, f := range fields {
			args = append(args, "--field", fieldFlag(f))
		}
		if dryRun {
			args = append(args, "--dry-run")
		}
		return runKeelArgs(ctx, opts, dir, args)
	}
}

// generatableByKey finds the generatable with the given key for a framework, so
// handleGenerate can tell a stack (auth → keel add) from a code-block (→ keel
// gen) using the same catalogue keel_list_generatables reports.
func generatableByKey(reg *recipe.Registry, fw, key string) (gen.Generatable, bool) {
	for _, g := range gen.Generatables(reg, fw) {
		if g.Key == key {
			return g, true
		}
	}
	return gen.Generatable{}, false
}

// runKeelArgs re-execs keel with args in dir and wraps the result like the other
// write tools, so a caller sees the argv that ran and its output.
func runKeelArgs(ctx context.Context, opts Options, dir string, args []string) (any, error) {
	out, err := opts.RunKeel(ctx, dir, args)
	if err != nil {
		return nil, fmt.Errorf("%s\n%w", out, err)
	}
	return map[string]any{"ran": append([]string{"keel"}, args...), "output": out}, nil
}

// writeHandler adapts an args→(dir,cmdargs) mapping to a gated write tool. policy
// decides what the dir must be (tracked project, existing dir, or unconstrained
// for scaffolding a new one); a dir that fails the policy is a clean tool error,
// never a re-exec, so a token-holding agent cannot run a task in an arbitrary
// directory.
func writeHandler(opts Options, policy dirPolicy, argsFn func(map[string]any) (string, []string)) func(context.Context, map[string]any) (any, error) {
	return func(ctx context.Context, a map[string]any) (any, error) {
		dir, cmdArgs := argsFn(a)
		safeDir, err := resolveWriteDir(dir, policy)
		if err != nil {
			return nil, err
		}
		out, err := opts.RunKeel(ctx, safeDir, cmdArgs)
		if err != nil {
			return nil, fmt.Errorf("%s\n%w", out, err)
		}
		return map[string]any{"ran": append([]string{"keel"}, cmdArgs...), "output": out}, nil
	}
}

func scaffoldArgs(a map[string]any) (string, []string) {
	args := []string{"new", s(a, "framework")}
	if name := s(a, "name"); name != "" {
		args = append(args, name)
	}
	if with := strList(a, "with"); len(with) > 0 {
		args = append(args, "--with", strings.Join(with, ","))
	}
	args = append(args, "--yes")
	return ".", args
}
