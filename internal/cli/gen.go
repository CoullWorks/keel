package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/gen"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/tui"
	"github.com/spf13/cobra"
)

// genWizard is the wizard entrypoint behind a package var: a test seam so
// pickComponents' result-mapping and cancel handling can be covered with canned
// selections instead of a real terminal.
var genWizard = tui.Wizard

// pickComponents runs the mouse+keyboard wizard to multiselect component keys.
// The wizard's intro line names the target, so the picker says which project and
// stack it is about to generate into rather than just "generate code components".
func pickComponents(intro, title, help string, opts []tui.Choice) ([]string, error) {
	res, err := genWizard("keel gen", intro, []tui.Step{{Title: title, Help: help, Multi: true, Options: opts}})
	if err != nil {
		if errors.Is(err, tui.ErrCancelled) {
			return nil, nil
		}
		return nil, err
	}
	return res[0], nil
}

func genCmd() *cobra.Command {
	var dryRun bool
	var module, framework, vendor, target string
	var fieldFlags []string
	c := &cobra.Command{
		Use:     "gen [component] [name...]",
		Aliases: []string{"make"},
		Example: "  keel gen model Order                            # Laravel: artisan make:model\n" +
			"  keel gen model Order --field title:string --field total:decimal\n" +
			"  keel gen entity Product                         # Symfony: bin/console make:entity\n" +
			"  keel gen component Button                       # Next.js: React component file\n" +
			"  keel gen startapp billing                       # Django: manage.py startapp\n" +
			"  keel gen package Blog --vendor Acme             # Laravel: distributable package\n" +
			"  keel gen module Acme/Blog -f magento\n" +
			"  keel gen auth                                   # add the framework's auth stack\n" +
			"  keel gen tests                                  # add the framework's test suite\n" +
			"  keel make                                       # object-first guided flow\n",
		Args:  cobra.ArbitraryArgs,
		Short: "Generate code components (framework-aware; drives each framework's own generators, templates where it has none)",
		Long: "keel gen generates into the keel project in the current directory, and\n" +
			"names that project, its framework and its environment in the output.\n\n" +
			"It is framework-aware: it offers only what the project's framework can\n" +
			"generate. Where a framework ships its own generators keel drives them\n" +
			"(Laravel's artisan make:*, Symfony's bin/console make, NestJS's nest\n" +
			"generate, Adonis's ace make, Django's manage.py); where it has none keel\n" +
			"writes idiomatic files from templates (Magento, the Node meta-frameworks,\n" +
			"the Python micro-frameworks). Models take a typed --field list\n" +
			"(name:type[:nullable]) that renders into real columns (Magento\n" +
			"db_schema.xml, Laravel migration + $fillable). Auth is a level:stack\n" +
			"generator: `keel gen auth` names the framework's auth recipe(s) to add.\n\n" +
			"Field types: " + strings.Join(gen.FieldTypeStrings(), ", ") + ".\n" +
			"Frameworks with built-in generators: " + strings.Join(gen.Frameworks, ", ") + ".\n\n" +
			"The framework comes from the project's .keel manifest; override it with -f.\n" +
			"Run `keel gen` (or `keel make`) with no component for a guided flow.\n" +
			"Names must be plain identifiers (slashes for namespaces): they end up in\n" +
			"a command's arguments and in file paths.",
		ValidArgsFunction: func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			// Only the first argument is a component; the rest are names, which
			// keel cannot know.
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			tgt, err := genTargetFor(framework)
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return genCompletions(tgt), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			// A stack generator (auth, tests) is framework-neutral in the CLI and
			// needs no code-block generator, so it resolves the framework from the
			// manifest without the Supports gate: `keel gen auth` / `keel gen tests`
			// work on Django and Next too, where there is no artisan/mage2gen path.
			if len(args) > 0 && isStackRequest(args[0]) {
				fw, err := stackTargetFramework(framework)
				if err != nil {
					return err
				}
				return runStackGen(out, fw, args[0])
			}
			// `keel make` (the alias) with no component is the object-first guided
			// flow: pick a module/object, then loop adding models with fields.
			guided := cmd.CalledAs() == "make" && len(args) == 0
			tgt, err := genTargetFor(framework)
			if err != nil {
				return err
			}
			fields, err := gen.ParseFields(fieldFlags)
			if err != nil {
				return err
			}
			params := genParams(vendor, target)
			switch tgt.Framework {
			case "magento":
				return runMagentoGen(out, tgt, args, module, fields, dryRun)
			case "laravel":
				return runLaravelGen(cmd.Context(), out, tgt, args, fields, params, guided, dryRun)
			default:
				return runRegistryGen(cmd.Context(), out, tgt, args, fields, params, dryRun)
			}
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be generated, write nothing")
	c.Flags().StringVar(&module, "module", "", "Magento module as Vendor/Module")
	c.Flags().StringVarP(&framework, "framework", "f", "", "force framework (defaults to the project's; e.g. laravel, symfony, django, nextjs)")
	c.Flags().StringVar(&vendor, "vendor", "", "package vendor namespace (Laravel package)")
	c.Flags().StringVar(&target, "target", "", "where to generate: app-code or package (where the framework supports both)")
	c.Flags().StringArrayVar(&fieldFlags, "field", nil, "model field as name:type[,attr]... (repeatable); types: "+strings.Join(gen.FieldTypeStrings(), ", "))
	// The object-first `make` verbs (add/list/info/remove/generate) are subcommands
	// here so `keel make add ...` resolves through the `make` alias without touching
	// root.go. A bare `keel gen`/`keel make` keeps its one-shot guided flow above.
	c.AddCommand(makeSubcommands()...)
	return c
}

// genCompletions lists the component keys shell completion should offer for a
// target: the framework's built-in code-blocks plus its stack generators (auth).
func genCompletions(tgt genTarget) []string {
	out := gen.ComponentKeys(tgt.Framework)
	if reg, err := catalog.Registry(); err == nil {
		for _, verb := range []string{"auth", "tests"} {
			if _, ok := gen.StackFor(reg, tgt.Framework, stackVerbs[verb]); ok {
				out = append(out, verb)
			}
		}
	}
	return out
}

// stackVerbs are the friendly capability aliases `keel gen <verb>` resolves to a
// level:stack generator: "add the framework's <verb> stack". Each maps to the
// capability a stack generatable must provide.
var stackVerbs = map[string]string{"auth": "auth", "tests": "tests"}

// isStackRequest reports whether the requested component is a stack verb (auth,
// tests) rather than a code-block. A stack verb adds the framework's stack via
// `keel add`, so it takes the framework-neutral path, not the code-block gate.
func isStackRequest(component string) bool {
	_, ok := stackVerbs[component]
	return ok
}

// stackTargetFramework resolves the project's framework (family-mapped) for a
// stack request, without the code-block Supports gate or the env requirement:
// adding the auth stack only needs to know which framework the project is.
func stackTargetFramework(forced string) (string, error) {
	m, err := engine.ReadManifest(".")
	if err != nil {
		return "", manifestErr(err)
	}
	fw := m.Framework
	if forced != "" {
		fw = forced
	}
	return genFamily(fw), nil
}

// runStackGen resolves the framework's auth stack to the addon recipe(s) it
// applies and points the user at `keel add` — the lifecycle command that installs
// them. gen does not install the addon itself: that is `keel add`'s job, and a
// stack generator is only a framework-scoped pointer at recipes that already
// exist.
func runStackGen(out io.Writer, framework, verb string) error {
	reg, err := catalog.Registry()
	if err != nil {
		return err
	}
	capability := stackVerbs[verb]
	g, ok := gen.StackFor(reg, framework, capability)
	if !ok {
		return fmt.Errorf("no %s stack is defined for %s yet", verb, framework)
	}
	ids, _ := gen.ResolveStack(reg, g.Recipe)
	fmt.Fprintf(out, "%s\n", g.Label)
	fmt.Fprintf(out, "%s for %s is the %s recipe. Add it with:\n\n", verb, framework, strings.Join(ids, " + "))
	fmt.Fprintf(out, "    keel add %s\n", strings.Join(ids, " "))
	return nil
}

func containsGenStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// genTarget is the project a `keel gen` run acts on: which directory, whose
// generators, and the command prefix that reaches it. It is resolved once, up
// front, and printed above whatever gen is about to do, so "which project did
// that just write into" is never a question answered by inspection afterwards.
type genTarget struct {
	Dir       string
	Framework string // the family: a magento-mageos project generates as magento
	Env       string
}

// runTitle heads the Laravel panel. The commands shown are run through the env,
// so the env is part of the answer to "what is this about to do".
func (t genTarget) runTitle() string {
	return "keel gen · " + t.Framework + " on " + t.Env + " · " + t.Dir
}

// writeTitle heads the Magento panel. No env here: those files are written
// straight to disk, and naming the env would imply a container round-trip that
// does not happen.
func (t genTarget) writeTitle() string {
	return "keel gen · " + t.Framework + " · " + t.Dir
}

// intro is the one-line target summary the interactive picker shows.
func (t genTarget) intro() string {
	return "generating into " + t.Dir + " (" + t.Framework + ")"
}

// genTargetFor resolves the project in the current directory, or explains why it
// cannot. keel gen acts on a keel project, exactly as `keel db` and `keel run`
// do.
//
// It used to fall back to laravel-on-ddev whenever there was no manifest, so
// `keel gen model Order` in an empty directory printed a plan to run artisan
// there; and it treated every non-Magento framework as Laravel, so a Django or
// Next.js project got `php artisan make:model` too. Both are resolved here, once,
// rather than at each use.
func genTargetFor(forced string) (genTarget, error) {
	dir, err := os.Getwd()
	if err != nil {
		return genTarget{}, err
	}
	m, err := engine.ReadManifest(".")
	if err != nil {
		return genTarget{}, manifestErr(err)
	}
	fw := m.Framework
	if forced != "" {
		fw = forced
	}
	fw = genFamily(fw)
	if !gen.Supports(fw) {
		// Even without a built-in code-block generator, the framework may still have
		// a stack generator (its auth). Point that out rather than a flat refusal,
		// while keeping the "no generators for <fw>" phrasing callers key on.
		if hint := stackHint(fw); hint != "" {
			return genTarget{}, fmt.Errorf("keel gen has no code-block generators for %s: it generates code for %s. %s",
				fw, strings.Join(gen.Frameworks, ", "), hint)
		}
		return genTarget{}, fmt.Errorf("keel gen has no generators for %s: it generates for %s. Use %s's own tooling for scaffolding",
			fw, strings.Join(gen.Frameworks, ", "), fw)
	}
	// A CLI-driven framework shells out (artisan, bin/console, nest, ace,
	// manage.py) and needs to know how to reach the project. An env-less manifest
	// is hand-edited or damaged; guessing one produces an argv with an empty
	// command in front of it. Template-driven frameworks write files directly and
	// need no env.
	if gen.CLIDriven(fw) && m.Env == "" {
		return genTarget{}, fmt.Errorf("project manifest has no env, so keel cannot tell how to reach %s's generator (re-run keel adopt)", fw)
	}
	return genTarget{Dir: dir, Framework: fw, Env: m.Env}, nil
}

// stackHint returns a one-line "but `keel gen auth` works" nudge for a framework
// that has no code-block generator but does have an auth stack, so the refusal is
// actionable rather than a dead end.
func stackHint(fw string) string {
	reg, err := catalog.Registry()
	if err != nil {
		return ""
	}
	var hints []string
	for _, verb := range []string{"auth", "tests"} {
		if _, ok := gen.StackFor(reg, fw, stackVerbs[verb]); ok {
			hints = append(hints, "`keel gen "+verb+"` adds its "+verb+" stack.")
		}
	}
	return strings.Join(hints, " ")
}

// genFamily maps a framework id onto the family whose generators apply, so a
// magento-mageos project generates Magento components instead of falling through
// to Laravel's artisan. An id keel does not know is returned unchanged, and
// fails the Supports check with its own name in the message.
func genFamily(id string) string {
	reg, err := catalog.Registry()
	if err != nil {
		return id
	}
	if r, ok := reg.Get(id); ok && r.Family != "" {
		return r.Family
	}
	return id
}

// ---- Laravel (artisan make + field-aware model files) ----

func runLaravelGen(ctx context.Context, out io.Writer, tgt genTarget, args []string, fields []gen.Field, params map[string]any, guided, dryRun bool) error {
	if len(args) == 0 {
		if guided {
			return runLaravelGuided(out, tgt, dryRun)
		}
		return runLaravelInteractive(ctx, out, tgt, dryRun)
	}
	// A Laravel package is rendered (not artisan), and carries the app-vs-package
	// Target the fitness review asks for. It is the one Laravel key that writes
	// files, so it routes through the registry render path rather than ByKey.
	if args[0] == "package" {
		return runRenderComponent(out, tgt, "package", args[1:], fields, params, dryRun)
	}
	comp, ok := gen.ByKey(args[0])
	if !ok {
		return fmt.Errorf("unknown laravel component %q: keel gen generates %s (or run `keel gen` to pick from a list)",
			args[0], strings.Join(append(gen.Keys(), "package"), ", "))
	}
	if len(fields) > 0 && comp.Key != "model" {
		return fmt.Errorf("--field only applies to a model, not %s", comp.Key)
	}
	names := args[1:]
	if len(names) == 0 {
		return fmt.Errorf("give at least one name, e.g. keel gen %s MyThing", comp.Key)
	}
	// A model WITH fields is written as files (model + migration) so the columns
	// actually land; artisan make:model leaves the migration empty. A model with
	// no fields keeps the old artisan path so nothing regresses.
	if comp.Key == "model" && len(fields) > 0 {
		var files []gen.OutFile
		for _, n := range names {
			if err := gen.ValidateName(n); err != nil {
				return err
			}
			rendered, err := gen.RenderLaravelModel(gen.LaravelModelVars{Name: n, Fields: fields})
			if err != nil {
				return err
			}
			files = append(files, rendered...)
		}
		return writeFiles(out, tgt, files, dryRun)
	}
	var cmds [][]string
	for _, n := range names {
		if err := gen.ValidateName(n); err != nil {
			return err
		}
		cmds = append(cmds, gen.Command(tgt.Env, comp, n))
	}
	return runCmds(ctx, out, tgt, cmds, dryRun)
}

func runLaravelInteractive(ctx context.Context, out io.Writer, tgt genTarget, dryRun bool) error {
	opts := make([]tui.Choice, 0, len(gen.LaravelComponents))
	for _, c := range gen.LaravelComponents {
		opts = append(opts, tui.Choice{Key: c.Key, Label: c.Label})
	}
	keys, err := pickComponents(tgt.intro(), "What do you need?", "Pick components; you'll name each next.", opts)
	if err != nil {
		return err
	}
	var cmds [][]string
	for _, k := range keys {
		comp, _ := gen.ByKey(k)
		names := ""
		if err := huh.NewInput().Title(comp.Label + " name(s)").Placeholder("comma-separated").Value(&names).Run(); err != nil {
			return err
		}
		for _, n := range splitCSV(names) {
			if err := gen.ValidateName(n); err != nil {
				return err
			}
			cmds = append(cmds, gen.Command(tgt.Env, comp, n))
		}
	}
	return runCmds(ctx, out, tgt, cmds, dryRun)
}

// runLaravelGuided is the object-first `keel make` flow for Laravel: name a
// model, add typed fields in a loop, then write the model + migration. It is the
// mage2gen-style "build the object, then generate" wizard the fitness review asks
// for, applied to Laravel.
func runLaravelGuided(out io.Writer, tgt genTarget, dryRun bool) error {
	name := ""
	if err := huh.NewInput().Title("Model name").Placeholder("Order").Value(&name).Run(); err != nil {
		return err
	}
	if err := gen.ValidateName(name); err != nil {
		return err
	}
	fields, err := collectFields()
	if err != nil {
		return err
	}
	files, err := gen.RenderLaravelModel(gen.LaravelModelVars{Name: name, Fields: fields})
	if err != nil {
		return err
	}
	return writeFiles(out, tgt, files, dryRun)
}

// runCmds runs each generator command. Commands are argv, not shell strings, so
// a component name can never be read as command syntax; the joined form is for
// display only.
func runCmds(ctx context.Context, out io.Writer, tgt genTarget, cmds [][]string, dryRun bool) error {
	if len(cmds) == 0 {
		fmt.Fprintln(out, "nothing to generate.")
		return nil
	}
	shown := make([]string, len(cmds))
	for i, c := range cmds {
		shown[i] = strings.Join(c, " ")
	}
	fmt.Fprint(out, tui.RenderSteps(tgt.runTitle(), shown))
	if dryRun {
		return nil
	}
	if !DockerRunning() {
		return fmt.Errorf("Docker doesn't appear to be running - start it and try again (keel doctor)")
	}
	r := engine.ExecRunner{Out: out}
	for i, c := range cmds {
		fmt.Fprintf(out, "→ %s\n", shown[i])
		if err := r.RunArgv(ctx, ".", c); err != nil {
			return fmt.Errorf("gen failed (%s): %w", shown[i], err)
		}
	}
	return nil
}

// ---- Registry-driven (Symfony, NestJS, Adonis, Django + the template frameworks) ----

// genParams collects the flag-supplied component inputs into the params map the
// registry render path reads (vendor/target for a Laravel package today). Empty
// flags are omitted so a renderer falls back to its own defaults.
func genParams(vendor, target string) map[string]any {
	params := map[string]any{}
	if vendor != "" {
		params["vendor"] = vendor
	}
	if target != "" {
		params["target"] = target
	}
	return params
}

// runRegistryGen generates for any framework driven by the per-family registry:
// every framework except the two (Laravel, Magento) that keep a bespoke path for
// their interactive/field-aware behaviour. It dispatches per component key — a
// CLI-driven key builds argv and runs it through the env exactly as the artisan
// path does, a template key renders files — so Symfony/NestJS/Adonis/Django and
// the Node/Python template frameworks all generate without a bespoke runner each.
func runRegistryGen(ctx context.Context, out io.Writer, tgt genTarget, args []string, fields []gen.Field, params map[string]any, dryRun bool) error {
	if len(args) == 0 {
		return runRegistryInteractive(ctx, out, tgt, fields, params, dryRun)
	}
	key := args[0]
	switch gen.ComponentMode(tgt.Framework, key) {
	case "command":
		return runCommandComponent(ctx, out, tgt, key, args[1:], fields, dryRun)
	case "render":
		return runRenderComponent(out, tgt, key, args[1:], fields, params, dryRun)
	default:
		return fmt.Errorf("unknown %s component %q: keel gen generates %s (or run `keel gen` to pick from a list)",
			tgt.Framework, key, strings.Join(gen.ComponentKeys(tgt.Framework), ", "))
	}
}

// runCommandComponent drives a framework's own generator CLI for a component:
// it builds one argv per name and runs them through the env. A no-name generator
// (Symfony make:migration, Django makemigrations) is valid with no argument; a
// name-taking one requires at least one name rather than silently dropping into
// the tool's interactive prompt.
func runCommandComponent(ctx context.Context, out io.Writer, tgt genTarget, key string, names []string, fields []gen.Field, dryRun bool) error {
	if len(fields) > 0 {
		return fmt.Errorf("--field applies to Laravel and Magento models, not %s %s", tgt.Framework, key)
	}
	var cmds [][]string
	if len(names) == 0 {
		if commandTakesName(tgt.Framework, tgt.Env, key) {
			return fmt.Errorf("give at least one name, e.g. keel gen %s MyThing", key)
		}
		argv, _ := gen.FrameworkCommand(tgt.Framework, tgt.Env, key, "")
		cmds = append(cmds, argv)
	} else {
		for _, n := range names {
			if err := gen.ValidateName(n); err != nil {
				return err
			}
			argv, _ := gen.FrameworkCommand(tgt.Framework, tgt.Env, key, n)
			cmds = append(cmds, argv)
		}
	}
	return runCmds(ctx, out, tgt, cmds, dryRun)
}

// runRenderComponent renders a template-driven component to files, one set per
// name. It is the framework-neutral twin of the Magento file path, used for the
// Node/Python template frameworks, Django's templated half and the Laravel
// package.
func runRenderComponent(out io.Writer, tgt genTarget, key string, names []string, fields []gen.Field, params map[string]any, dryRun bool) error {
	if len(names) == 0 {
		return fmt.Errorf("give at least one name, e.g. keel gen %s MyThing", key)
	}
	var files []gen.OutFile
	for _, n := range names {
		if err := gen.ValidateName(n); err != nil {
			return err
		}
		rendered, ok, err := gen.FrameworkRender(tgt.Framework, key, n, fields, params)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%s cannot render %q", tgt.Framework, key)
		}
		files = append(files, rendered...)
	}
	return writeFiles(out, tgt, files, dryRun)
}

// commandTakesName reports whether a CLI-driven component key takes a name
// argument, by comparing the argv the builder produces with and without one. It
// lets the runner require a name for name-taking generators (make:controller)
// while allowing the no-name ones (make:migration, makemigrations) to run bare,
// without the CLI hardcoding each framework's list.
func commandTakesName(family, env, key string) bool {
	with, _ := gen.FrameworkCommand(family, env, key, "Probe")
	without, _ := gen.FrameworkCommand(family, env, key, "")
	return len(with) != len(without)
}

// runRegistryInteractive is the no-argument guided flow for a registry-driven
// framework: pick components from the framework's own catalogue, name each, then
// dispatch every pick through the same per-key command/render path. It mirrors
// runLaravelInteractive for the frameworks that arrived with the registry.
func runRegistryInteractive(ctx context.Context, out io.Writer, tgt genTarget, fields []gen.Field, params map[string]any, dryRun bool) error {
	reg, _ := catalog.Registry()
	var opts []tui.Choice
	for _, g := range gen.Generatables(reg, tgt.Framework) {
		if g.Level == recipe.LevelStack {
			continue // auth stacks are `keel gen auth`, not a code-block pick
		}
		opts = append(opts, tui.Choice{Key: g.Key, Label: g.Label})
	}
	if len(opts) == 0 {
		return fmt.Errorf("keel gen has no components to offer for %s", tgt.Framework)
	}
	keys, err := pickComponents(tgt.intro(), "What do you need?", "Pick components; you'll name each next.", opts)
	if err != nil {
		return err
	}
	for _, k := range keys {
		names := ""
		if err := huh.NewInput().Title(k + " name(s)").Placeholder("comma-separated").Value(&names).Run(); err != nil {
			return err
		}
		if err := runRegistryGen(ctx, out, tgt, append([]string{k}, splitCSV(names)...), fields, params, dryRun); err != nil {
			return err
		}
	}
	return nil
}

// ---- Magento (mage2gen-style templates, field-aware models) ----

func runMagentoGen(out io.Writer, tgt genTarget, args []string, module string, fields []gen.Field, dryRun bool) error {
	if len(args) == 0 {
		return runMagentoInteractive(out, tgt, dryRun)
	}
	comp, ok := gen.MagentoByKey(args[0])
	if !ok {
		return fmt.Errorf("unknown magento component %q: keel gen generates %s (or run `keel gen` to pick from a list)",
			args[0], strings.Join(gen.MagentoKeys(), ", "))
	}
	if len(fields) > 0 && comp.Key != "model" {
		return fmt.Errorf("--field only applies to a model, not %s", comp.Key)
	}
	var files []gen.OutFile
	if comp.Key == "module" {
		if len(args) < 2 {
			return fmt.Errorf("usage: keel gen module Vendor/Module")
		}
		v, m, err := splitVendorModule(args[1])
		if err != nil {
			return err
		}
		files, err = gen.RenderMagento(comp, gen.MagentoVars{Vendor: v, Module: m})
		if err != nil {
			return err
		}
	} else {
		if module == "" {
			return fmt.Errorf("magento components need --module Vendor/Module (or run `keel gen module Vendor/Module` first)")
		}
		v, m, err := splitVendorModule(module)
		if err != nil {
			return err
		}
		names := args[1:]
		if len(names) == 0 {
			return fmt.Errorf("give a name, e.g. keel gen %s MyThing --module %s", comp.Key, module)
		}
		for _, n := range names {
			if err := gen.ValidateName(n); err != nil {
				return err
			}
			rendered, err := gen.RenderMagento(comp, gen.MagentoVars{Vendor: v, Module: m, Name: n, Fields: fields})
			if err != nil {
				return err
			}
			files = append(files, rendered...)
		}
	}
	return writeFiles(out, tgt, files, dryRun)
}

func runMagentoInteractive(out io.Writer, tgt genTarget, dryRun bool) error {
	module := ""
	if err := huh.NewInput().Title("Module (Vendor/Module)").Placeholder("Acme/Blog").Value(&module).Run(); err != nil {
		return err
	}
	v, m, err := splitVendorModule(module)
	if err != nil {
		return err
	}
	opts := make([]tui.Choice, 0, len(gen.MagentoComponents))
	for _, c := range gen.MagentoComponents {
		opts = append(opts, tui.Choice{Key: c.Key, Label: c.Label})
	}
	keys, err := pickComponents(tgt.intro(), "Components", "Pick what to add to "+module+".", opts)
	if err != nil {
		return err
	}
	var files []gen.OutFile
	for _, k := range keys {
		comp, _ := gen.MagentoByKey(k)
		if k == "module" {
			rendered, _ := gen.RenderMagento(comp, gen.MagentoVars{Vendor: v, Module: m})
			files = append(files, rendered...)
			continue
		}
		// A model in the guided flow collects typed fields; other components just
		// take name(s).
		if k == "model" {
			names := ""
			if err := huh.NewInput().Title("Model name(s)").Placeholder("comma-separated").Value(&names).Run(); err != nil {
				return err
			}
			for _, n := range splitCSV(names) {
				if err := gen.ValidateName(n); err != nil {
					return err
				}
				fields, err := collectFields()
				if err != nil {
					return err
				}
				rendered, err := gen.RenderMagento(comp, gen.MagentoVars{Vendor: v, Module: m, Name: n, Fields: fields})
				if err != nil {
					return err
				}
				files = append(files, rendered...)
			}
			continue
		}
		names := ""
		if err := huh.NewInput().Title(comp.Label + " name(s)").Placeholder("comma-separated").Value(&names).Run(); err != nil {
			return err
		}
		for _, n := range splitCSV(names) {
			if err := gen.ValidateName(n); err != nil {
				return err
			}
			rendered, err := gen.RenderMagento(comp, gen.MagentoVars{Vendor: v, Module: m, Name: n})
			if err != nil {
				return err
			}
			files = append(files, rendered...)
		}
	}
	return writeFiles(out, tgt, files, dryRun)
}

// collectFields is the interactive fields table: a loop that adds typed fields to
// a model until the user leaves the name blank. It is the CLI twin of the studio's
// fields table (name / type dropdown / nullable). The seam collectFieldRow lets a
// test feed rows without a terminal.
func collectFields() ([]gen.Field, error) {
	var fields []gen.Field
	for {
		f, more, err := collectFieldRow()
		if err != nil {
			return nil, err
		}
		if !more {
			break
		}
		if err := f.Validate(); err != nil {
			return nil, err
		}
		fields = append(fields, f)
	}
	return fields, gen.ValidateFields(fields)
}

// collectFieldRow prompts for one field row. It is a package var so tests can
// feed canned rows; the real body drives huh. Returning more=false ends the loop.
var collectFieldRow = func() (gen.Field, bool, error) {
	name := ""
	if err := huh.NewInput().Title("Field name (blank to finish)").Value(&name).Run(); err != nil {
		return gen.Field{}, false, err
	}
	if strings.TrimSpace(name) == "" {
		return gen.Field{}, false, nil
	}
	typ := string(gen.TypeString)
	sel := huh.NewSelect[string]().Title("Type for " + name).Options(fieldTypeOptions()...).Value(&typ)
	if err := sel.Run(); err != nil {
		return gen.Field{}, false, err
	}
	nullable := false
	if err := huh.NewConfirm().Title(name + " nullable?").Value(&nullable).Run(); err != nil {
		return gen.Field{}, false, err
	}
	return gen.Field{Name: strings.TrimSpace(name), Type: gen.FieldType(typ), Nullable: nullable}, true, nil
}

func fieldTypeOptions() []huh.Option[string] {
	opts := make([]huh.Option[string], 0, len(gen.FieldTypes))
	for _, t := range gen.FieldTypes {
		opts = append(opts, huh.NewOption(string(t), string(t)))
	}
	return opts
}

func writeFiles(out io.Writer, tgt genTarget, files []gen.OutFile, dryRun bool) error {
	if len(files) == 0 {
		fmt.Fprintln(out, "nothing to generate.")
		return nil
	}
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	fmt.Fprint(out, tui.RenderFiles(tgt.writeTitle(), paths))
	if dryRun {
		return nil
	}
	for _, f := range files {
		if err := engine.WriteFile(".", f.Path, f.Content); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "%s %d files written\n", "✓", len(files))
	return nil
}

// ---- helpers ----

// splitVendorModule parses "Vendor/Module". Both halves become directory names
// under the project, so each must be a plain identifier: "../.." is not a vendor.
func splitVendorModule(s string) (string, string, error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("expected Vendor/Module, got %q", s)
	}
	for _, p := range parts {
		if err := gen.ValidateName(p); err != nil {
			return "", "", fmt.Errorf("expected Vendor/Module: %w", err)
		}
	}
	return parts[0], parts[1], nil
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
