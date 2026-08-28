package gen

import (
	"slices"
	"sort"

	"github.com/coullworks/keel/internal/recipe"
)

// Generatable is one thing keel can generate into a project of a given framework:
// a code-block (a Laravel model, a Magento block), a module, a package or a whole
// stack (auth). It is the framework-scoped, level-tagged catalogue entry the
// studio's generate menu is built from, so a Next project is offered its routes
// and its auth stack, never Laravel's nouns.
//
// It unifies two sources behind one shape: the built-in code-block generators
// keel ships in Go (Laravel's artisan make:*, Magento's mage2gen templates) and
// the recipe-driven `kind: generator` catalogue (auth and future stacks defined
// as data). The studio does not care which source an entry came from.
type Generatable struct {
	Key       string            // stable id (e.g. "model", "auth")
	Label     string            // human label for the menu
	Level     recipe.GenLevel   // code-block | module | package | stack
	Framework string            // the framework family this is offered under
	Provides  []string          // capabilities it contributes (e.g. "auth")
	Inputs    []recipe.GenInput // typed inputs to collect before running
	// Recipe is the id of the recipe-driven generator this came from, empty for a
	// built-in code-block generator. A stack generatable carries it so the caller
	// can resolve it to the addon(s) it applies via ResolveStack.
	Recipe string
}

// nameInput is the class/identifier every named code-block collects. It is the
// single reused "what do you call it" widget, so a component form declares its
// name field as data like every other input rather than the CLI special-casing it.
var nameInput = recipe.GenInput{Name: "name", Type: recipe.InClass, Label: "Name", Required: true}

// fieldsInput is the standard typed-field-list input a model-shaped code-block
// generator collects: the mage2gen primitive, surfaced so the studio renders a
// fields table for models (and Magento's schema-bearing components).
var fieldsInput = recipe.GenInput{Name: "fields", Type: recipe.InFields, Label: "Fields", Required: false}

// componentInputs is the per-component typed form for a code-block, keyed by
// component key. This is the wiring the fitness review asks for: each component
// declares its OWN form (not one shared name-only box), UNIFORMLY across
// frameworks — the CLI, the studio and the MCP surface all read these Inputs, so
// only the catalogue differs per framework, never the form machinery. A key with
// no entry falls back to just the name input.
var componentInputs = map[string][]recipe.GenInput{
	// Magento
	"module": {
		{Name: "vendor", Type: recipe.InClass, Label: "Vendor", Required: true},
		{Name: "module", Type: recipe.InClass, Label: "Module", Required: true},
		{Name: "target", Type: recipe.InOptions, Label: "Target", Required: false,
			Choices: []string{string(TargetAppCode), string(TargetPackage)}, Default: string(TargetAppCode),
			Help: "app-code drops it in app/code; package makes a Composer package under vendor/"},
	},
	"model":    {nameInput, fieldsInput},
	"observer": {nameInput, {Name: "event", Type: recipe.InRef, Label: "Event to observe", Required: false, Help: "the Magento event name this observer listens for"}},
	"block":    {nameInput},
	"helper":   {nameInput},
	"console":  {nameInput, {Name: "command", Type: recipe.InText, Label: "CLI command", Required: false, Help: "e.g. vendor:do-thing"}},
	// Magento — backend / wiring. (controller lives in magentoOverrides: its key
	// collides with Laravel's controller; every other Magento key below is unique.)
	"plugin": {
		{Name: "target", Type: recipe.InClass, Label: "Target class", Required: true, Help: "the class to intercept"},
		{Name: "plugin_type", Type: recipe.InOptions, Label: "Plugin type", Choices: []string{"before", "after", "around"}, Default: "after"},
		{Name: "method", Type: recipe.InText, Label: "Target method", Required: true},
		{Name: "area", Type: recipe.InOptions, Label: "Area", Choices: []string{"global", "frontend", "adminhtml"}, Default: "global"},
		{Name: "sort_order", Type: recipe.InInt, Label: "Sort order", Default: "10"},
	},
	"preference": {
		{Name: "for", Type: recipe.InClass, Label: "For (interface/class)", Required: true},
		{Name: "prefer", Type: recipe.InClass, Label: "Prefer (implementation)", Required: true},
		{Name: "area", Type: recipe.InOptions, Label: "Area", Choices: []string{"global", "frontend", "adminhtml"}, Default: "global"},
	},
	"cron": {nameInput,
		{Name: "schedule", Type: recipe.InText, Label: "Schedule (cron expr)", Default: "* * * * *"},
		{Name: "group", Type: recipe.InText, Label: "Group", Default: "default"},
	},
	"acl": {
		{Name: "resource_id", Type: recipe.InText, Label: "Resource id", Required: true, Help: "e.g. Vendor_Module::config"},
		{Name: "title", Type: recipe.InText, Label: "Title"},
		{Name: "parent", Type: recipe.InText, Label: "Parent resource", Default: "Magento_Backend::admin"},
	},
	"menu": {
		{Name: "id", Type: recipe.InText, Label: "Menu id", Required: true, Help: "e.g. Vendor_Module::menu"},
		{Name: "title", Type: recipe.InText, Label: "Title"},
		{Name: "parent", Type: recipe.InText, Label: "Parent menu id"},
		{Name: "action", Type: recipe.InText, Label: "Action (admin path)"},
		{Name: "resource", Type: recipe.InText, Label: "ACL resource"},
		{Name: "sort_order", Type: recipe.InInt, Label: "Sort order", Default: "10"},
	},
	"setup_patch_data": {
		{Name: "name", Type: recipe.InClass, Label: "Patch class", Required: true},
		{Name: "dependencies", Type: recipe.InClass, Label: "Depends on patch", Help: "an existing patch class this runs after"},
	},
	"setup_patch_schema": {
		{Name: "name", Type: recipe.InClass, Label: "Patch class", Required: true},
	},
	"cron_group": {
		{Name: "group", Type: recipe.InText, Label: "Group id", Required: true},
		{Name: "schedule_generate_every", Type: recipe.InInt, Label: "Generate every", Default: "15"},
		{Name: "schedule_ahead_for", Type: recipe.InInt, Label: "Schedule ahead for", Default: "20"},
		{Name: "schedule_lifetime", Type: recipe.InInt, Label: "Schedule lifetime", Default: "15"},
		{Name: "history_cleanup_every", Type: recipe.InInt, Label: "History cleanup every", Default: "10"},
		{Name: "history_success_lifetime", Type: recipe.InInt, Label: "History success lifetime", Default: "60"},
		{Name: "history_failure_lifetime", Type: recipe.InInt, Label: "History failure lifetime", Default: "600"},
		{Name: "use_separate_process", Type: recipe.InBool, Label: "Use separate process"},
	},
	"cache_type": {nameInput,
		{Name: "id", Type: recipe.InText, Label: "Cache type id", Required: true},
		{Name: "tag", Type: recipe.InText, Label: "Cache tag"},
	},
	"indexer": {nameInput,
		{Name: "id", Type: recipe.InText, Label: "Indexer id", Required: true},
		{Name: "title", Type: recipe.InText, Label: "Title"},
		{Name: "view_id", Type: recipe.InText, Label: "Mview view id"},
	},
	"message_queue": {nameInput,
		{Name: "topic", Type: recipe.InText, Label: "Topic", Required: true},
		{Name: "consumer", Type: recipe.InText, Label: "Consumer name", Required: true},
		{Name: "queue", Type: recipe.InText, Label: "Queue name", Required: true},
		{Name: "handler", Type: recipe.InClass, Label: "Handler class"},
		{Name: "connection", Type: recipe.InOptions, Label: "Connection", Choices: []string{"amqp", "db"}, Default: "db"},
	},
	// Magento — admin config / API
	"system_config": {
		{Name: "tab", Type: recipe.InText, Label: "Tab"},
		{Name: "section", Type: recipe.InText, Label: "Section", Required: true},
		{Name: "group", Type: recipe.InText, Label: "Group", Required: true},
		{Name: "field", Type: recipe.InText, Label: "Field", Required: true},
		{Name: "field_type", Type: recipe.InOptions, Label: "Field type", Choices: []string{"text", "textarea", "select", "multiselect", "password", "yesno"}, Default: "text"},
		{Name: "source_model", Type: recipe.InClass, Label: "Source model", Help: "only for select/multiselect"},
		{Name: "label", Type: recipe.InText, Label: "Label"},
		{Name: "comment", Type: recipe.InText, Label: "Comment"},
		{Name: "scope", Type: recipe.InOptions, Label: "Scope", Choices: []string{"default", "website", "store"}, Default: "default"},
	},
	"email_template": {nameInput,
		{Name: "id", Type: recipe.InText, Label: "Template id", Required: true},
		{Name: "label", Type: recipe.InText, Label: "Label"},
		{Name: "area", Type: recipe.InOptions, Label: "Area", Choices: []string{"frontend", "adminhtml"}, Default: "frontend"},
		{Name: "subject", Type: recipe.InText, Label: "Subject"},
	},
	"webapi": {nameInput,
		{Name: "route", Type: recipe.InText, Label: "Route", Required: true, Help: "e.g. mymodule/items"},
		{Name: "method", Type: recipe.InOptions, Label: "HTTP method", Choices: []string{"GET", "POST", "PUT", "DELETE"}, Default: "GET"},
		{Name: "service", Type: recipe.InClass, Label: "Service (Api interface)", Required: true},
		{Name: "resource", Type: recipe.InText, Label: "ACL resource"},
	},
	"graphql": {nameInput,
		{Name: "type", Type: recipe.InText, Label: "Type / query field", Required: true},
		{Name: "resolver", Type: recipe.InClass, Label: "Resolver class"},
		{Name: "operation", Type: recipe.InOptions, Label: "Operation", Choices: []string{"query", "mutation"}, Default: "query"},
	},
	// Magento — UI / EAV
	"ui_component_listing": {nameInput,
		{Name: "id", Type: recipe.InText, Label: "UI component id", Required: true},
		{Name: "model", Type: recipe.InClass, Label: "Entity resource model"},
		{Name: "acl", Type: recipe.InText, Label: "ACL resource"},
		{Name: "add_button", Type: recipe.InBool, Label: "Add button"},
	},
	"ui_component_form": {nameInput,
		{Name: "id", Type: recipe.InText, Label: "UI component id", Required: true},
		{Name: "model", Type: recipe.InClass, Label: "Entity model"},
		fieldsInput,
	},
	"view": {nameInput,
		{Name: "area", Type: recipe.InOptions, Label: "Area", Choices: []string{"frontend", "adminhtml"}, Default: "frontend"},
		{Name: "handle", Type: recipe.InText, Label: "Layout handle", Required: true},
		{Name: "block", Type: recipe.InClass, Label: "Block class"},
		{Name: "template", Type: recipe.InText, Label: "Template path"},
	},
	"widget": {nameInput,
		{Name: "id", Type: recipe.InText, Label: "Widget id", Required: true},
		{Name: "label", Type: recipe.InText, Label: "Label"},
		{Name: "description", Type: recipe.InText, Label: "Description"},
	},
	"product_attribute": {nameInput,
		{Name: "code", Type: recipe.InText, Label: "Attribute code", Required: true},
		{Name: "label", Type: recipe.InText, Label: "Label"},
		{Name: "input", Type: recipe.InOptions, Label: "Input", Choices: []string{"text", "textarea", "select", "boolean", "date", "price", "multiselect"}, Default: "text"},
		{Name: "type", Type: recipe.InOptions, Label: "Backend type", Choices: []string{"varchar", "text", "int", "decimal", "datetime"}, Default: "varchar"},
		{Name: "source", Type: recipe.InClass, Label: "Source model", Help: "for select/multiselect"},
		{Name: "required", Type: recipe.InBool, Label: "Required"},
		{Name: "scope", Type: recipe.InOptions, Label: "Scope", Choices: []string{"global", "website", "store"}, Default: "global"},
		{Name: "group", Type: recipe.InText, Label: "Attribute group", Default: "General"},
		{Name: "sort_order", Type: recipe.InInt, Label: "Sort order", Default: "10"},
		{Name: "used_in_grid", Type: recipe.InBool, Label: "Used in grid"},
		{Name: "visible_on_front", Type: recipe.InBool, Label: "Visible on front"},
	},
	"customer_attribute": {nameInput,
		{Name: "code", Type: recipe.InText, Label: "Attribute code", Required: true},
		{Name: "label", Type: recipe.InText, Label: "Label"},
		{Name: "input", Type: recipe.InOptions, Label: "Input", Choices: []string{"text", "textarea", "select", "boolean", "date", "multiselect"}, Default: "text"},
		{Name: "type", Type: recipe.InOptions, Label: "Backend type", Choices: []string{"varchar", "text", "int", "decimal", "datetime"}, Default: "varchar"},
		{Name: "source", Type: recipe.InClass, Label: "Source model"},
		{Name: "required", Type: recipe.InBool, Label: "Required"},
	},
	"unit_test": {nameInput,
		{Name: "class", Type: recipe.InClass, Label: "Class under test", Required: true},
	},
	// Laravel — the package generatable offers the app-vs-package Target, so a
	// Laravel project can scaffold a distributable Composer package instead of
	// adding to the app tree. It is the Laravel twin of Magento's module target.
	"package": {nameInput,
		{Name: "vendor", Type: recipe.InClass, Label: "Vendor", Required: false, Help: "Composer vendor for the package namespace"},
		{Name: "target", Type: recipe.InOptions, Label: "Target", Required: false,
			Choices: []string{string(TargetPackage), string(TargetAppCode)}, Default: string(TargetPackage),
			Help: "package scaffolds a distributable Composer package; app-code drops it in the app tree"},
	},
	// Laravel
	"controller": {nameInput, {Name: "resource", Type: recipe.InBool, Label: "Resource controller", Required: false}},
	"migration":  {nameInput, fieldsInput},
	"request":    {nameInput},
	"resource":   {nameInput},
	"event":      {nameInput},
	"listener":   {nameInput, {Name: "event", Type: recipe.InRef, Label: "Event", Required: false}},
	"job":        {nameInput},
	"command":    {nameInput, {Name: "signature", Type: recipe.InText, Label: "Signature", Required: false}},
	"policy":     {nameInput, {Name: "model", Type: recipe.InClass, Label: "Model", Required: false}},
	"middleware": {nameInput},
	"test":       {nameInput},
}

// magentoOverrides holds the forms for Magento component keys that collide with a
// Laravel key (only "controller" today: Laravel's is a resource-controller flag,
// Magento's is area/route/path/action). It is consulted first when the family is
// magento, so each framework gets its own form for the shared key while the rest
// of the catalogue stays in the single componentInputs map.
var magentoOverrides = map[string][]recipe.GenInput{
	"controller": {
		{Name: "area", Type: recipe.InOptions, Label: "Area", Choices: []string{"frontend", "adminhtml"}, Default: "frontend"},
		{Name: "route", Type: recipe.InText, Label: "Route (frontName)", Help: "the URL front name, e.g. blog"},
		{Name: "path", Type: recipe.InText, Label: "Path", Help: "controller sub-path under the area, e.g. Post"},
		{Name: "action", Type: recipe.InClass, Label: "Action", Default: "Index"},
	},
}

// inputsFor returns the typed form for a component key within a framework family:
// its declared per-component inputs, or the lone name input if none are declared.
// A magento family consults magentoOverrides first so a key shared with Laravel
// (controller) still gets the Magento form. It is the single accessor so every
// surface gets the identical form for a component.
func inputsFor(family, key string) []recipe.GenInput {
	if family == "magento" {
		if in, ok := magentoOverrides[key]; ok {
			return in
		}
	}
	if in, ok := componentInputs[key]; ok {
		return in
	}
	return []recipe.GenInput{nameInput}
}

// Generatables returns the valid generatables for a framework family, merging
// the built-in code-block generators keel ships for that framework with the
// recipe-driven `kind: generator` catalogue scoped to it. It reuses
// Registry.ForFramework for the recipe side, so a `kind: generator` recipe with
// `appliesTo: [nextjs]` shows up for Next and nowhere else.
//
// This is the accessor the studio calls to build a framework-aware generate
// menu, replacing the hardcoded Laravel noun list. reg may be nil (no recipe
// catalogue loaded), in which case only the built-in code-block generators are
// returned — so gen degrades to exactly its old behaviour rather than failing.
func Generatables(reg *recipe.Registry, framework string) []Generatable {
	family := framework
	if reg != nil {
		if r, ok := reg.Get(framework); ok && r.Family != "" {
			family = r.Family
		}
	}
	out := builtinGeneratables(family)
	out = append(out, recipeGeneratables(reg, framework)...)
	return out
}

// builtinGeneratables lists the code-block generators keel implements in Go for a
// framework family, read from the per-framework registry. A model-shaped
// component carries the fields input, so the typed field list is offered exactly
// where it renders (Laravel models, Magento schema-bearing components).
//
// This was a hardcoded switch over laravel+magento; every other framework fell to
// a nil default. It now derives from the registry each framework file registers
// into, so a framework with no built-in generators still returns nil (and gets
// only its recipe-driven stacks), and a new framework arrives as a Register call,
// not an edit here.
func builtinGeneratables(family string) []Generatable {
	fg := lookup(family)
	if fg == nil {
		return nil
	}
	return fg.Components
}

// recipeGeneratables turns the `kind: generator` recipes valid under framework fw
// into Generatables, sorted by id for a stable menu. This is the data-driven path
// the fitness review asks for: new stacks (auth today, test-suite next) arrive as
// YAML, not Go.
func recipeGeneratables(reg *recipe.Registry, fw string) []Generatable {
	if reg == nil {
		return nil
	}
	gens := reg.ForFramework(fw, recipe.Generator)
	sort.Slice(gens, func(i, j int) bool { return gens[i].ID < gens[j].ID })
	out := make([]Generatable, 0, len(gens))
	for _, r := range gens {
		out = append(out, Generatable{
			Key:       r.ID,
			Label:     labelOr(r.Label, r.ID),
			Level:     r.Level,
			Framework: fw,
			Provides:  r.Provides,
			Inputs:    r.Inputs,
			Recipe:    r.ID,
		})
	}
	return out
}

func labelOr(label, id string) string {
	if label != "" {
		return label
	}
	return id
}

// ResolveStack returns the addon recipe ids a stack generator applies, in order.
// It is how "add the auth stack for this framework" becomes "install these
// existing recipes": a stack generator is a thin, framework-scoped pointer at
// addons that already exist (Breeze, Auth.js, allauth), so exposing auth as a
// generatable does not duplicate how any of them install.
func ResolveStack(reg *recipe.Registry, generatorID string) ([]string, bool) {
	if reg == nil {
		return nil, false
	}
	r, ok := reg.Get(generatorID)
	if !ok || r.Kind != recipe.Generator || r.Level != recipe.LevelStack {
		return nil, false
	}
	return r.Applies, true
}

// StackFor returns the level:stack generatable that provides a capability for a
// framework, if the catalogue defines one. It is the general form behind the CLI's
// stack verbs (`keel gen auth`, `keel gen tests`) and the studio's Stacks section:
// "add the <capability> stack for this framework" resolved to the recipe(s) it
// applies, instead of a hardcoded per-capability lookup.
func StackFor(reg *recipe.Registry, framework, capability string) (Generatable, bool) {
	for _, g := range Generatables(reg, framework) {
		if g.Level == recipe.LevelStack && slices.Contains(g.Provides, capability) {
			return g, true
		}
	}
	return Generatable{}, false
}

// AuthStackFor is StackFor for the auth capability: the direct answer to the
// studio's "Auth" button and `keel gen auth`.
func AuthStackFor(reg *recipe.Registry, framework string) (Generatable, bool) {
	return StackFor(reg, framework, "auth")
}
