package gen

import (
	"fmt"
	"regexp"
)

// This file is the security gate for the Magento generator's per-component
// settings. Every param that a template interpolates into a FILE PATH or a
// PHP/XML identifier (a class, namespace, route, config node id, ACL resource,
// attribute code, cron group, ui-component id) is validated HERE, against a
// pattern narrow enough that a shell, a filesystem or an XML parser cannot
// reinterpret it — before RenderPlan hands the value to a template. A value like
// area="../../etc" or class="Foo;rm -rf" is refused at plan validation, so it
// never reaches disk. Free-text values (labels, comments, descriptions) are NOT
// validated here — they are XML-escaped in the template with the xesc helper.

// classPattern matches a PHP class or interface reference: one-or-more
// PascalCase/identifier segments joined by backslashes, with an optional leading
// backslash for a fully-qualified name (\Magento\Framework\App\Action\Action or
// Vendor\Module\Model\Foo). No path traversal, no shell metacharacters can pass.
var classPattern = regexp.MustCompile(`^\\?[A-Za-z_][A-Za-z0-9_]*(\\[A-Za-z_][A-Za-z0-9_]*)*$`)

// identPattern matches a single plain identifier: a route id, a config
// section/group/field, an attribute code, a layout handle segment, a ui-component
// id, a cron group. Letters, digits and underscores only.
var identPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// aclPattern matches a Magento ACL/menu resource id: Vendor_Module::resource, or
// a bare identifier. The two identifier halves are joined by "::"; each half may
// itself carry underscores (Magento_Backend::admin).
var aclPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(::[A-Za-z_][A-Za-z0-9_]*)?$`)

// pathSegPattern matches a relative-path param that becomes one or more directory
// segments (a controller Path, a template subpath). Slash-separated identifiers,
// no leading/trailing slash, no "." or ".." — so it cannot climb out of the
// module tree. A trailing ".phtml"/".html" is allowed on template paths and
// handled by the template's own suffixing, so only the segment body is validated.
var pathSegPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(/[A-Za-z_][A-Za-z0-9_]*)*$`)

// dottedPattern matches a dot-separated identifier: a message-queue topic or
// queue name (async.operations.all). Identifier segments joined by dots; no
// traversal, no shell/markup metacharacters.
var dottedPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// cronExprPattern matches a crontab schedule expression: the five whitespace-
// separated fields, each a run of the crontab vocabulary (digits, * , - /). It
// keeps a schedule from smuggling markup into crontab.xml while staying liberal
// about the field grammar itself (Magento validates the semantics).
var cronExprPattern = regexp.MustCompile(`^[\d*/,\- ]+$`)

// paramKind is how a param value is checked. Each kind is a pattern narrow enough
// that the value is safe to interpolate into the place the template puts it.
type paramKind int

const (
	kindClass    paramKind = iota // a PHP class/interface reference
	kindIdent                     // a single plain identifier
	kindACL                       // a Vendor_Module::resource id
	kindPathSeg                   // slash-separated path segments (no traversal)
	kindDotted                    // a dot-separated identifier (queue topic/name)
	kindCronExpr                  // a crontab schedule expression
	kindOption                    // one of a fixed choice set
)

// paramRule binds a param name to how it must validate, and (for kindOption) the
// allowed values. required marks a param that must be present and non-empty.
type paramRule struct {
	name     string
	kind     paramKind
	required bool
	choices  []string // for kindOption
}

// paramValidators is the authoritative per-component list of settings that carry
// an identifier or a path and therefore MUST be validated before rendering. A
// component absent from this map has no path/identifier params beyond the shared
// name (validated by ModulePlan.Validate via ValidateName). Free-text params are
// deliberately not listed — they are escaped in the template with xesc.
var paramValidators = map[string][]paramRule{
	"controller": {
		{name: "area", kind: kindOption, choices: []string{"frontend", "adminhtml"}},
		{name: "route", kind: kindIdent},
		{name: "path", kind: kindPathSeg},
		{name: "action", kind: kindIdent},
	},
	"plugin": {
		{name: "target", kind: kindClass, required: true},
		{name: "plugin_type", kind: kindOption, choices: []string{"before", "after", "around"}},
		{name: "method", kind: kindIdent, required: true},
		{name: "area", kind: kindOption, choices: []string{"global", "frontend", "adminhtml"}},
	},
	"preference": {
		{name: "for", kind: kindClass, required: true},
		{name: "prefer", kind: kindClass, required: true},
		{name: "area", kind: kindOption, choices: []string{"global", "frontend", "adminhtml"}},
	},
	"cron": {
		{name: "schedule", kind: kindCronExpr},
		{name: "group", kind: kindIdent},
	},
	"system_config": {
		{name: "tab", kind: kindIdent},
		{name: "section", kind: kindIdent, required: true},
		{name: "group", kind: kindIdent, required: true},
		{name: "field", kind: kindIdent, required: true},
		{name: "field_type", kind: kindOption, choices: []string{"text", "textarea", "select", "multiselect", "password", "yesno"}},
		{name: "source_model", kind: kindClass},
		{name: "scope", kind: kindOption, choices: []string{"default", "website", "store"}},
	},
	"acl": {
		{name: "resource_id", kind: kindACL, required: true},
		{name: "parent", kind: kindACL},
	},
	"setup_patch_data": {
		{name: "name", kind: kindIdent, required: true},
	},
	"setup_patch_schema": {
		{name: "name", kind: kindIdent, required: true},
	},
	"ui_component_listing": {
		{name: "id", kind: kindIdent, required: true},
		{name: "model", kind: kindClass},
		{name: "acl", kind: kindACL},
	},
	"ui_component_form": {
		{name: "id", kind: kindIdent, required: true},
		{name: "model", kind: kindClass},
	},
	"menu": {
		{name: "id", kind: kindACL, required: true},
		{name: "parent", kind: kindACL},
		{name: "action", kind: kindPathSeg},
		{name: "resource", kind: kindACL},
	},
	"email_template": {
		{name: "id", kind: kindIdent, required: true},
		{name: "area", kind: kindOption, choices: []string{"frontend", "adminhtml"}},
	},
	"widget": {
		{name: "id", kind: kindIdent, required: true},
	},
	"view": {
		{name: "area", kind: kindOption, choices: []string{"frontend", "adminhtml"}},
		{name: "handle", kind: kindIdent, required: true},
		{name: "block", kind: kindClass},
		{name: "template", kind: kindPathSeg},
	},
	"product_attribute": {
		{name: "code", kind: kindIdent, required: true},
		{name: "input", kind: kindOption, choices: []string{"text", "textarea", "select", "boolean", "date", "price", "multiselect"}},
		{name: "type", kind: kindOption, choices: []string{"varchar", "text", "int", "decimal", "datetime"}},
		{name: "source", kind: kindClass},
		{name: "scope", kind: kindOption, choices: []string{"global", "website", "store"}},
		{name: "group", kind: kindIdent},
	},
	"customer_attribute": {
		{name: "code", kind: kindIdent, required: true},
		{name: "input", kind: kindOption, choices: []string{"text", "textarea", "select", "boolean", "date", "multiselect"}},
		{name: "type", kind: kindOption, choices: []string{"varchar", "text", "int", "decimal", "datetime"}},
		{name: "source", kind: kindClass},
	},
	"webapi": {
		{name: "route", kind: kindPathSeg},
		{name: "method", kind: kindOption, choices: []string{"GET", "POST", "PUT", "DELETE"}},
		{name: "service", kind: kindClass, required: true},
		{name: "resource", kind: kindACL},
	},
	"graphql": {
		{name: "type", kind: kindIdent, required: true},
		{name: "resolver", kind: kindClass},
		{name: "operation", kind: kindOption, choices: []string{"query", "mutation"}},
	},
	"cron_group": {
		{name: "group", kind: kindIdent, required: true},
	},
	"cache_type": {
		{name: "id", kind: kindIdent, required: true},
		{name: "tag", kind: kindIdent},
	},
	"indexer": {
		{name: "id", kind: kindIdent, required: true},
		{name: "view_id", kind: kindIdent},
	},
	"message_queue": {
		{name: "topic", kind: kindDotted, required: true},
		{name: "consumer", kind: kindIdent, required: true},
		{name: "queue", kind: kindDotted, required: true},
		{name: "handler", kind: kindClass},
		{name: "connection", kind: kindOption, choices: []string{"amqp", "db"}},
	},
	"unit_test": {
		{name: "class", kind: kindClass, required: true},
	},
}

// validateParamValue checks one param value against a kind, returning a
// caller-facing error naming the param. An empty value passes UNLESS the rule is
// required (checked separately) — an optional identifier left blank simply is not
// rendered.
func validateParamValue(name, value string, kind paramKind, choices []string) error {
	switch kind {
	case kindClass:
		if !classPattern.MatchString(value) {
			return fmt.Errorf("invalid %s %q: expected a PHP class reference", name, value)
		}
	case kindIdent:
		if !identPattern.MatchString(value) {
			return fmt.Errorf("invalid %s %q: use letters, digits and underscores", name, value)
		}
	case kindACL:
		if !aclPattern.MatchString(value) {
			return fmt.Errorf("invalid %s %q: expected Vendor_Module::resource", name, value)
		}
	case kindPathSeg:
		if !pathSegPattern.MatchString(value) {
			return fmt.Errorf("invalid %s %q: use slash-separated identifiers (no . or ..)", name, value)
		}
	case kindDotted:
		if !dottedPattern.MatchString(value) {
			return fmt.Errorf("invalid %s %q: use a dot-separated identifier", name, value)
		}
	case kindCronExpr:
		if !cronExprPattern.MatchString(value) {
			return fmt.Errorf("invalid %s %q: not a crontab expression", name, value)
		}
	case kindOption:
		for _, c := range choices {
			if value == c {
				return nil
			}
		}
		return fmt.Errorf("invalid %s %q: want one of %v", name, value, choices)
	}
	return nil
}

// validateComponentParams runs the security rules for one plan component. It
// enforces required params and validates every present identifier/path param. It
// is the single place ModulePlan.Validate calls so a malicious setting is
// rejected at plan time, never at render.
func validateComponentParams(c PlanComponent) error {
	rules, ok := paramValidators[c.Type]
	if !ok {
		return nil
	}
	for _, r := range rules {
		raw, present := "", false
		if c.Params != nil {
			if s, isStr := c.Params[r.name].(string); isStr {
				raw, present = s, s != ""
			}
		}
		if !present {
			if r.required {
				return fmt.Errorf("%s: %s is required", c.Type, r.name)
			}
			continue
		}
		if err := validateParamValue(r.name, raw, r.kind, r.choices); err != nil {
			return fmt.Errorf("%s: %w", c.Type, err)
		}
	}
	return nil
}

// componentNeedsName reports whether a component's identity comes from the shared
// "name" param (validated by ValidateName). A handful of components are named by
// their own param instead (system_config by section/group/field, acl by
// resource_id, cron_group by group, cache_type/indexer by id) and legitimately
// have no name — for those ModulePlan.Validate must not demand a name.
func componentNeedsName(componentType string) bool {
	return !nameless[componentType]
}

// nameless is the set of components whose identity is a dedicated param rather
// than the shared name, so an empty name is valid for them.
var nameless = map[string]bool{
	"module":               true, // named by vendor/module
	"system_config":        true, // section/group/field
	"acl":                  true, // resource_id
	"cron_group":           true, // group
	"cache_type":           true, // id
	"indexer":              true, // id
	"menu":                 true, // id
	"preference":           true, // for/prefer
	"ui_component_listing": true, // id
	"ui_component_form":    true, // id
	"email_template":       true, // id
	"widget":               true, // id
	"view":                 true, // handle
	"product_attribute":    true, // code
	"customer_attribute":   true, // code
	"webapi":               true, // route/service
	"graphql":              true, // type
	"message_queue":        true, // topic/consumer
}
