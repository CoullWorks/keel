package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/gen"
	"github.com/spf13/cobra"
)

// makeSubcommands are the object-first verbs of `keel make`: stage a plan of
// components in a module, then generate it as a whole. They are attached to the
// existing gen command (which carries the `make` alias), so `keel make add`,
// `keel make list`, etc. resolve without touching root.go. This is the mage2gen
// interaction model — build the object over several steps, then generate — made
// uniform across frameworks: the same add/list/info/remove/generate loop over the
// same ModulePlan, differing only in the catalogue each framework offers.
func makeSubcommands() []*cobra.Command {
	return []*cobra.Command{
		makeAddCmd(),
		makeListCmd(),
		makeInfoCmd(),
		makeRemoveCmd(),
		makeGenerateCmd(),
	}
}

// modulePlanPath is where a module's staged plan lives: .keel/modules/<module>.yaml,
// next to the manifest the plan will generate against.
func modulePlanPath(dir, module string) string {
	return filepath.Join(dir, ".keel", "modules", module+".yaml")
}

// loadOrNewPlan reads the staged plan for a module, or returns a fresh one seeded
// with the vendor/module/target/framework so the first `add` starts a coherent
// plan without a separate init step.
func loadOrNewPlan(dir, vendor, module string, target gen.Target, framework string) (gen.ModulePlan, error) {
	b, err := os.ReadFile(modulePlanPath(dir, module))
	if err == nil {
		p, uerr := gen.UnmarshalPlan(b)
		if uerr != nil {
			return gen.ModulePlan{}, fmt.Errorf("reading staged plan for %s: %w", module, uerr)
		}
		return p, nil
	}
	if !os.IsNotExist(err) {
		return gen.ModulePlan{}, err
	}
	return gen.ModulePlan{Vendor: vendor, Module: module, Target: target, Framework: framework}, nil
}

// savePlan writes a plan back to disk, creating .keel/modules as needed.
func savePlan(dir string, p gen.ModulePlan) error {
	if err := p.Validate(); err != nil {
		return err
	}
	b, err := gen.MarshalPlan(p)
	if err != nil {
		return err
	}
	path := modulePlanPath(dir, p.Module)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// makeAddCmd stages one component into a module's plan.
func makeAddCmd() *cobra.Command {
	var module, vendor, target, framework string
	var fieldFlags []string
	c := &cobra.Command{
		Use:   "add <type> [name]",
		Short: "Stage a component into a module's plan (mage2gen-style add loop)",
		Long: "Adds one component to the module's staged plan (.keel/modules/<module>.yaml)\n" +
			"without generating anything yet. Add as many as you like, then run\n" +
			"`keel make generate` to render the whole plan at once. Use `keel make info\n" +
			"<type>` to see a component's inputs, and --field for a model's typed columns\n" +
			"(name:type[,nullable][,unique][,index][,default=..][,len=..]).",
		Example: "  keel make add module Acme/Blog\n" +
			"  keel make add model Post -m Acme/Blog --field title:string --field body:text,nullable\n" +
			"  keel make add observer OrderPlaced -m Acme/Blog\n",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMakeAdd(cmd.OutOrStdout(), args, module, vendor, target, framework, fieldFlags)
		},
	}
	c.Flags().StringVarP(&module, "module", "m", "", "module as Vendor/Module (or just Module)")
	c.Flags().StringVar(&vendor, "vendor", "", "vendor (when not given as Vendor/Module)")
	c.Flags().StringVar(&target, "target", "", "where files land: app-code (default) or package")
	c.Flags().StringVarP(&framework, "framework", "f", "", "force framework")
	c.Flags().StringArrayVar(&fieldFlags, "field", nil, "model field name:type[,attr]... (repeatable)")
	return c
}

func runMakeAdd(out io.Writer, args []string, module, vendor, target, framework string, fieldFlags []string) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	fw, err := makeFramework(framework)
	if err != nil {
		return err
	}
	compType := args[0]
	// The `module` component itself carries Vendor/Module as its argument.
	if compType == "module" && len(args) == 2 {
		module = args[1]
	}
	v, mod, err := resolveVendorModule(module, vendor)
	if err != nil {
		return err
	}
	tgt := gen.Target(target)
	if tgt == "" {
		tgt = gen.TargetAppCode
	}
	if !gen.Targets[tgt] {
		return fmt.Errorf("unknown target %q: use %s or %s", target, gen.TargetAppCode, gen.TargetPackage)
	}
	if err := validateComponentType(fw, compType); err != nil {
		return err
	}
	plan, err := loadOrNewPlan(dir, v, mod, tgt, fw)
	if err != nil {
		return err
	}
	pc := gen.PlanComponent{Type: compType, Params: map[string]any{}}
	if len(args) == 2 && compType != "module" {
		if err := gen.ValidateName(args[1]); err != nil {
			return err
		}
		pc.Params["name"] = args[1]
	}
	if len(fieldFlags) > 0 {
		if compType != "model" {
			return fmt.Errorf("--field only applies to a model, not %s", compType)
		}
		fields, err := gen.ParseFields(fieldFlags)
		if err != nil {
			return err
		}
		pc.Fields = fields
	}
	plan.AddComponent(pc)
	if err := savePlan(dir, plan); err != nil {
		return err
	}
	fmt.Fprintf(out, "staged %s%s in %s (%d component(s) in plan)\n", compType, nameSuffix(pc), plan.Module, len(plan.Components))
	fmt.Fprintf(out, "run `keel make generate -m %s` to render the plan.\n", plan.Module)
	return nil
}

// makeListCmd prints a module's staged plan.
func makeListCmd() *cobra.Command {
	var module string
	c := &cobra.Command{
		Use:   "list",
		Short: "Show a module's staged plan",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMakeList(cmd.OutOrStdout(), module)
		},
	}
	c.Flags().StringVarP(&module, "module", "m", "", "module (Vendor/Module or Module)")
	return c
}

func runMakeList(out io.Writer, module string) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	_, mod, err := resolveVendorModule(module, "")
	if err != nil {
		return err
	}
	b, err := os.ReadFile(modulePlanPath(dir, mod))
	if err != nil {
		return fmt.Errorf("no staged plan for %s (add a component first with `keel make add`)", mod)
	}
	plan, err := gen.UnmarshalPlan(b)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "module %s (target %s, %s)\n", planLabel(plan), planTarget(plan), plan.Framework)
	if len(plan.Components) == 0 {
		fmt.Fprintln(out, "  (empty)")
		return nil
	}
	for i, c := range plan.Components {
		fmt.Fprintf(out, "  [%d] %s%s\n", i, c.Type, nameSuffix(c))
		for _, f := range c.Fields {
			fmt.Fprintf(out, "        - %s %s\n", f.Name, f.Type)
		}
	}
	return nil
}

// makeInfoCmd prints a component type's typed inputs — the same GenInput form the
// studio renders — so the CLI user (or an agent reading --help) sees a component's
// form before staging it.
func makeInfoCmd() *cobra.Command {
	var framework string
	c := &cobra.Command{
		Use:   "info <type>",
		Short: "Show a component type's inputs (its typed form)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMakeInfo(cmd.OutOrStdout(), args[0], framework)
		},
	}
	c.Flags().StringVarP(&framework, "framework", "f", "", "force framework")
	return c
}

func runMakeInfo(out io.Writer, compType, framework string) error {
	fw, err := makeFramework(framework)
	if err != nil {
		return err
	}
	g, ok := generatableFor(fw, compType)
	if !ok {
		return fmt.Errorf("unknown component %q for %s", compType, fw)
	}
	fmt.Fprintf(out, "%s: %s (level %s)\n", g.Key, g.Label, g.Level)
	if len(g.Inputs) == 0 {
		fmt.Fprintln(out, "  (no inputs)")
		return nil
	}
	for _, in := range g.Inputs {
		req := ""
		if in.Required {
			req = " (required)"
		}
		fmt.Fprintf(out, "  %s: %s%s\n", in.Name, in.Type, req)
		if len(in.Choices) > 0 {
			fmt.Fprintf(out, "      choices: %s\n", strings.Join(in.Choices, ", "))
		}
		if in.Help != "" {
			fmt.Fprintf(out, "      %s\n", in.Help)
		}
	}
	return nil
}

// makeRemoveCmd drops a staged component by its list index.
func makeRemoveCmd() *cobra.Command {
	var module string
	c := &cobra.Command{
		Use:     "remove <index>",
		Aliases: []string{"rm"},
		Short:   "Remove a staged component from a module's plan (by index from `list`)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMakeRemove(cmd.OutOrStdout(), args[0], module)
		},
	}
	c.Flags().StringVarP(&module, "module", "m", "", "module (Vendor/Module or Module)")
	return c
}

func runMakeRemove(out io.Writer, idxArg, module string) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	_, mod, err := resolveVendorModule(module, "")
	if err != nil {
		return err
	}
	i, err := strconv.Atoi(idxArg)
	if err != nil {
		return fmt.Errorf("index must be a number (see `keel make list`): %q", idxArg)
	}
	b, err := os.ReadFile(modulePlanPath(dir, mod))
	if err != nil {
		return fmt.Errorf("no staged plan for %s", mod)
	}
	plan, err := gen.UnmarshalPlan(b)
	if err != nil {
		return err
	}
	if !plan.RemoveComponent(i) {
		return fmt.Errorf("no component at index %d (plan has %d)", i, len(plan.Components))
	}
	if err := savePlan(dir, plan); err != nil {
		return err
	}
	fmt.Fprintf(out, "removed component %d (%d left)\n", i, len(plan.Components))
	return nil
}

// makeGenerateCmd renders a module's whole staged plan to disk.
func makeGenerateCmd() *cobra.Command {
	var module string
	var dryRun bool
	c := &cobra.Command{
		Use:   "generate",
		Short: "Render a module's staged plan as a whole",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMakeGenerate(cmd.OutOrStdout(), module, dryRun)
		},
	}
	c.Flags().StringVarP(&module, "module", "m", "", "module (Vendor/Module or Module)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be written, write nothing")
	return c
}

func runMakeGenerate(out io.Writer, module string, dryRun bool) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	_, mod, err := resolveVendorModule(module, "")
	if err != nil {
		return err
	}
	b, err := os.ReadFile(modulePlanPath(dir, mod))
	if err != nil {
		return fmt.Errorf("no staged plan for %s (add components first)", mod)
	}
	plan, err := gen.UnmarshalPlan(b)
	if err != nil {
		return err
	}
	files, err := gen.RenderPlan(plan)
	if err != nil {
		return err
	}
	tgt := genTarget{Dir: dir, Framework: firstNonEmpty(plan.Framework, "magento")}
	return writeFiles(out, tgt, files, dryRun)
}

// ---- helpers ----

// makeFramework resolves the framework a plan generates for: the forced value,
// else the project's manifest framework (family-mapped). It does not require the
// artisan-reachable env that `keel gen` does, because staging a plan writes no
// files until generate.
func makeFramework(forced string) (string, error) {
	if forced != "" {
		return genFamily(forced), nil
	}
	m, err := engine.ReadManifest(".")
	if err != nil {
		return "", manifestErr(err)
	}
	return genFamily(m.Framework), nil
}

// validateComponentType rejects a component type the framework's catalogue does
// not offer, so a typo is caught at `add`, not at `generate`.
func validateComponentType(fw, compType string) error {
	if _, ok := generatableFor(fw, compType); ok {
		return nil
	}
	return fmt.Errorf("unknown component %q for %s (see `keel make info` or `keel gen --help`)", compType, fw)
}

// generatableFor looks up a component in a framework's uniform catalogue
// (Generatables), the single source both the CLI and studio read.
func generatableFor(fw, key string) (gen.Generatable, bool) {
	reg, _ := catalog.Registry()
	for _, g := range gen.Generatables(reg, fw) {
		if g.Key == key {
			return g, true
		}
	}
	return gen.Generatable{}, false
}

// resolveVendorModule parses a module reference that may be "Vendor/Module",
// "Module" (with a separate --vendor) or just "Module". The module name is the
// plan's id, so it must always be present and a plain identifier.
func resolveVendorModule(module, vendor string) (string, string, error) {
	module = strings.TrimSpace(module)
	if module == "" {
		return "", "", fmt.Errorf("give a module with -m Vendor/Module (or -m Module)")
	}
	if strings.Contains(module, "/") {
		return splitVendorModule(module)
	}
	if err := gen.ValidateName(module); err != nil {
		return "", "", fmt.Errorf("module: %w", err)
	}
	if vendor != "" {
		if err := gen.ValidateName(vendor); err != nil {
			return "", "", fmt.Errorf("vendor: %w", err)
		}
	}
	return vendor, module, nil
}

func nameSuffix(c gen.PlanComponent) string {
	if n := c.Name(); n != "" {
		return " " + n
	}
	return ""
}

func planLabel(p gen.ModulePlan) string {
	if p.Vendor != "" {
		return p.Vendor + "/" + p.Module
	}
	return p.Module
}

func planTarget(p gen.ModulePlan) gen.Target {
	if p.Target == "" {
		return gen.TargetAppCode
	}
	return p.Target
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
