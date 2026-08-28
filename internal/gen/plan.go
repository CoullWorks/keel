package gen

import (
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Target is where a module's generated files land. It is the app/code-vs-composer
// choice from the fitness review, framework-neutral: every framework has a "drop
// it in the app tree" location and a "make it a distributable package" location,
// so the renderer takes its base path from the target instead of hardcoding
// app/code. A framework whose only location is the app tree simply never offers
// the package target.
type Target string

const (
	// TargetAppCode writes into the application's own tree (Magento app/code, a
	// Laravel app/, a Django project package). This is the default.
	TargetAppCode Target = "app-code"
	// TargetPackage writes into a distributable package tree (a Composer package
	// under vendor/, an npm package, a Symfony bundle).
	TargetPackage Target = "package"
)

// Targets is the set of valid targets, for validation and menus.
var Targets = map[Target]bool{TargetAppCode: true, TargetPackage: true}

// BasePath returns the directory a module's files are written under for a target,
// given the module's vendor and module names. It is the single place the app/code
// vs package location is decided, so the renderer never hardcodes a base again.
//
// The vendor/module segments are validated identifiers by the time a plan is
// rendered (Component/plan validation calls ValidateName), so joining them here
// cannot escape the project.
func (t Target) BasePath(vendor, module string) string {
	switch t {
	case TargetPackage:
		return filepath.Join("vendor", strings.ToLower(vendor), "module-"+strings.ToLower(module))
	default: // TargetAppCode and the zero value
		return filepath.Join("app", "code", vendor, module)
	}
}

// PlanComponent is one staged component in a ModulePlan: a component type (e.g.
// "model", "observer", "block") and its collected params. Params is a flat,
// framework-neutral bag keyed by the component's GenInput names, plus the special
// "name" key for the component's identifier and "fields" for a typed column list.
// Staging the params (rather than pre-rendered files) is what lets a whole plan be
// rendered at once — the precondition for XML merge on re-gen in a later wave.
type PlanComponent struct {
	Type   string         `yaml:"type"`
	Params map[string]any `yaml:"params,omitempty"`
	Fields []Field        `yaml:"fields,omitempty"`
}

// Name returns the component instance's name from its params, or "" if unset.
func (c PlanComponent) Name() string {
	if c.Params == nil {
		return ""
	}
	if s, ok := c.Params["name"].(string); ok {
		return s
	}
	return ""
}

// ModulePlan is the add-many staging document: the whole set of components a user
// is building up before generating them together. It is the shape the studio
// keeps in memory and the CLI persists on disk (.keel/modules/<module>.yaml), and
// the same shape the MCP surface will accept — one uniform plan for EVERY
// framework, differing only in which component Types its catalogue offers.
//
// Rendering the plan as a whole (RenderPlan) rather than one component at a time
// is deliberate: it is what will let a later wave merge shared XML (etc/*.xml)
// across the plan's components instead of overwriting on each add.
type ModulePlan struct {
	// Vendor and Module name the module. Vendor is optional for frameworks that
	// have no vendor concept (a plain Laravel app); Module is the plan's id.
	Vendor string `yaml:"vendor,omitempty"`
	Module string `yaml:"module,omitempty"`
	// Framework the plan generates for (its catalogue). Empty resolves from the
	// project manifest at render time.
	Framework string `yaml:"framework,omitempty"`
	// Target is where the module's files land (app/code vs package).
	Target Target `yaml:"target,omitempty"`
	// Components are the staged instances, in add order.
	Components []PlanComponent `yaml:"components"`
}

// AddComponent appends a staged component to the plan.
func (p *ModulePlan) AddComponent(c PlanComponent) { p.Components = append(p.Components, c) }

// RemoveComponent drops the component at index i (0-based), reporting whether it
// existed. It is the inverse of AddComponent for the CLI's `remove` verb.
func (p *ModulePlan) RemoveComponent(i int) bool {
	if i < 0 || i >= len(p.Components) {
		return false
	}
	p.Components = append(p.Components[:i], p.Components[i+1:]...)
	return true
}

// Validate checks the plan is coherent before it is rendered or persisted: a
// module name where the framework has a module concept, a known target, and every
// component naming a real type for the plan's framework with a valid instance name
// and fields.
//
// The module name is required only for a framework that HAS a module concept
// (Magento's Vendor_Module, NestJS's @Module) — this is the studio's "Module"
// leak, fixed at the source: a Next/Laravel/Django/Symfony/FastAPI plan generates
// components directly and has no module to name, so demanding one here (and the UI
// showing a "Module" box for it) was wrong. An empty framework means Magento, as
// it does in RenderPlan, so it keeps the requirement.
func (p ModulePlan) Validate() error {
	hasModule := p.Framework == "" || HasModuleConcept(p.Framework)
	if hasModule && strings.TrimSpace(p.Module) == "" {
		return fmt.Errorf("plan has no module name")
	}
	if p.Vendor != "" {
		if err := ValidateName(p.Vendor); err != nil {
			return fmt.Errorf("plan vendor: %w", err)
		}
	}
	if p.Module != "" {
		if err := ValidateName(p.Module); err != nil {
			return fmt.Errorf("plan module: %w", err)
		}
	}
	if p.Target != "" && !Targets[p.Target] {
		return fmt.Errorf("plan has unknown target %q (want %s or %s)", p.Target, TargetAppCode, TargetPackage)
	}
	// The per-component param rules and the "which components are named by their
	// own param" set are Magento-specific (a Laravel plan drives artisan and shares
	// only the name/fields). Gate them on the plan's framework so a Laravel
	// controller is not measured against Magento's controller rules. An empty
	// framework means Magento here, matching RenderPlan.
	magento := p.Framework == "" || p.Framework == "magento"
	for i, c := range p.Components {
		if strings.TrimSpace(c.Type) == "" {
			return fmt.Errorf("component %d has no type", i)
		}
		n := c.Name()
		if n != "" {
			if err := ValidateName(n); err != nil {
				return fmt.Errorf("component %d (%s): %w", i, c.Type, err)
			}
		} else if magento && componentNeedsName(c.Type) {
			// A Magento component that takes a name must have one; the ones named by
			// a dedicated param (system_config, acl, cron_group, …) are exempt and
			// validate that param instead, below.
			return fmt.Errorf("component %d (%s): name is required", i, c.Type)
		}
		if err := ValidateFields(c.Fields); err != nil {
			return fmt.Errorf("component %d (%s): %w", i, c.Type, err)
		}
		if magento {
			if err := validateComponentParams(c); err != nil {
				return fmt.Errorf("component %d: %w", i, err)
			}
		}
	}
	return nil
}

// RenderPlan renders every component in a Magento plan into OutFiles, in add
// order, honouring the plan's target for the module base path. It is the
// whole-plan renderer: because it sees all components at once it is where a later
// wave will merge the shared XML (module.xml, di.xml, db_schema.xml) that each
// component contributes, instead of the current one-file-per-component overwrite.
//
// Only Magento is wired here today (it is the framework with no native
// generator); Laravel drives artisan and renders a field-aware model directly.
// The plan shape itself is framework-neutral, so a later wave adds a RenderPlan
// arm per framework without changing the staging model.
func RenderPlan(p ModulePlan) ([]OutFile, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if p.Framework != "" && p.Framework != "magento" {
		return nil, fmt.Errorf("rendering a plan for %s is not wired yet (magento only)", p.Framework)
	}
	base := p.Target.BasePath(p.Vendor, p.Module)
	var out []OutFile
	for _, c := range p.Components {
		comp, ok := MagentoByKey(c.Type)
		if !ok {
			return nil, fmt.Errorf("unknown magento component %q in plan", c.Type)
		}
		v := MagentoVars{Vendor: p.Vendor, Module: p.Module, Name: c.Name(), Fields: c.Fields, Base: base, Params: c.Params}
		files, err := RenderMagento(comp, v)
		if err != nil {
			return nil, err
		}
		out = append(out, files...)
	}
	return out, nil
}

// MarshalPlan encodes a plan as YAML for on-disk persistence under .keel/modules.
func MarshalPlan(p ModulePlan) ([]byte, error) { return yaml.Marshal(p) }

// UnmarshalPlan decodes a persisted plan, validating it so a hand-edited or stale
// file is rejected on load rather than at render.
func UnmarshalPlan(b []byte) (ModulePlan, error) {
	var p ModulePlan
	if err := yaml.Unmarshal(b, &p); err != nil {
		return ModulePlan{}, err
	}
	if err := p.Validate(); err != nil {
		return ModulePlan{}, err
	}
	return p, nil
}
