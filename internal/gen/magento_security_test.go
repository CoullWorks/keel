package gen

import (
	"strings"
	"testing"
)

// TestMagentoParamInjectionRejected is the security gate proof: every param that
// lands in a file path or a PHP/XML identifier must be refused by plan validation
// when it carries traversal or shell/markup metacharacters, so a malicious value
// never reaches a template or the disk. Each row is a component + one poisoned
// param; the plan must fail Validate.
func TestMagentoParamInjectionRejected(t *testing.T) {
	tests := []struct {
		name   string
		typ    string
		cname  string
		params map[string]any
	}{
		{"controller area traversal", "controller", "View", map[string]any{"area": "../x", "path": "Post"}},
		{"controller path traversal", "controller", "View", map[string]any{"area": "frontend", "path": "../../etc"}},
		{"controller action injection", "controller", "View", map[string]any{"area": "frontend", "action": "Foo;rm"}},
		{"plugin target injection", "plugin", "P", map[string]any{"target": "Foo;rm -rf", "method": "get"}},
		{"plugin method injection", "plugin", "P", map[string]any{"target": "Acme\\Blog\\Model\\Foo", "method": "get name"}},
		{"plugin bad type", "plugin", "P", map[string]any{"target": "Acme\\Blog\\Model\\Foo", "method": "get", "plugin_type": "sideways"}},
		{"preference for injection", "preference", "", map[string]any{"for": "A;B", "prefer": "Acme\\Blog\\Model\\Foo"}},
		{"preference prefer injection", "preference", "", map[string]any{"for": "Acme\\Blog\\Api\\I", "prefer": "../../evil"}},
		{"cron schedule injection", "cron", "Sync", map[string]any{"schedule": "0 3 * * * <script>"}},
		{"system_config section injection", "system_config", "", map[string]any{"section": "sec;drop", "group": "g", "field": "f"}},
		{"system_config bad field_type", "system_config", "", map[string]any{"section": "s", "group": "g", "field": "f", "field_type": "exec"}},
		{"system_config source_model injection", "system_config", "", map[string]any{"section": "s", "group": "g", "field": "f", "source_model": "Foo;rm"}},
		{"acl resource injection", "acl", "", map[string]any{"resource_id": "Acme;rm::config"}},
		{"acl parent traversal", "acl", "", map[string]any{"resource_id": "Acme_Blog::config", "parent": "../x"}},
		{"setup_patch_data name traversal", "setup_patch_data", "", map[string]any{"name": "../../etc/passwd"}},
		{"ui_listing id injection", "ui_component_listing", "Foo", map[string]any{"id": "grid;rm", "model": "Acme\\Blog\\Model\\Foo"}},
		{"ui_listing model injection", "ui_component_listing", "Foo", map[string]any{"id": "grid", "model": "Foo;rm"}},
		{"menu id injection", "menu", "", map[string]any{"id": "Acme;rm::menu"}},
		{"menu action traversal", "menu", "", map[string]any{"id": "Acme_Blog::menu", "action": "../../etc"}},
		{"view handle injection", "view", "Extra", map[string]any{"handle": "cat;rm", "block": "Acme\\Blog\\Block\\Extra"}},
		{"view template traversal", "view", "Extra", map[string]any{"handle": "cat", "template": "../../../etc/passwd"}},
		{"product_attribute code injection", "product_attribute", "AddA", map[string]any{"code": "my code"}},
		{"product_attribute source injection", "product_attribute", "AddA", map[string]any{"code": "code", "source": "Foo;rm"}},
		{"webapi service injection", "webapi", "Item", map[string]any{"route": "items", "service": "Foo;rm"}},
		{"webapi route traversal", "webapi", "Item", map[string]any{"route": "../../etc", "service": "Acme\\Blog\\Api\\I"}},
		{"graphql type injection", "graphql", "T", map[string]any{"type": "my type"}},
		{"cron_group group injection", "cron_group", "", map[string]any{"group": "grp;rm"}},
		{"cache_type id injection", "cache_type", "C", map[string]any{"id": "cache;rm"}},
		{"indexer id injection", "indexer", "Foo", map[string]any{"id": "idx;rm"}},
		{"message_queue topic injection", "message_queue", "H", map[string]any{"topic": "t;rm", "consumer": "c", "queue": "q"}},
		{"unit_test class injection", "unit_test", "Foo", map[string]any{"class": "Foo;rm"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			params := tc.params
			if params == nil {
				params = map[string]any{}
			}
			if tc.cname != "" {
				params["name"] = tc.cname
			}
			p := ModulePlan{
				Vendor: "Acme", Module: "Blog", Framework: "magento",
				Components: []PlanComponent{{Type: tc.typ, Params: params}},
			}
			if err := p.Validate(); err == nil {
				t.Fatalf("expected %s to be rejected by validation, but it passed", tc.name)
			}
			// And rendering the same plan must also fail (RenderPlan validates first).
			if _, err := RenderPlan(p); err == nil {
				t.Fatalf("expected RenderPlan to reject %s", tc.name)
			}
		})
	}
}

// TestMagentoRequiredParams proves required identifier params are enforced at
// plan validation — a plugin with no target, a system_config with no section, an
// acl with no resource_id are all rejected.
func TestMagentoRequiredParams(t *testing.T) {
	tests := []struct {
		name   string
		typ    string
		params map[string]any
	}{
		{"plugin needs target", "plugin", map[string]any{"name": "P", "method": "get"}},
		{"plugin needs method", "plugin", map[string]any{"name": "P", "target": "Acme\\Blog\\Model\\Foo"}},
		{"preference needs for", "preference", map[string]any{"prefer": "Acme\\Blog\\Model\\Foo"}},
		{"system_config needs section", "system_config", map[string]any{"group": "g", "field": "f"}},
		{"acl needs resource_id", "acl", map[string]any{"title": "t"}},
		{"webapi needs service", "webapi", map[string]any{"name": "Item", "route": "items"}},
		{"ui_listing needs id", "ui_component_listing", map[string]any{"name": "Foo"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := ModulePlan{
				Vendor: "Acme", Module: "Blog", Framework: "magento",
				Components: []PlanComponent{{Type: tc.typ, Params: tc.params}},
			}
			if err := p.Validate(); err == nil {
				t.Fatalf("expected %s to fail (missing required param)", tc.name)
			}
		})
	}
}

// TestMagentoNamelessComponentsValidate proves a component named by its own param
// (system_config, acl, cron_group) is NOT rejected for having an empty shared
// name — the fix to ModulePlan.Validate so it validates the identifying param
// instead of demanding a name.
func TestMagentoNamelessComponentsValidate(t *testing.T) {
	tests := []struct {
		typ    string
		params map[string]any
	}{
		{"system_config", map[string]any{"section": "sec", "group": "grp", "field": "fld"}},
		{"acl", map[string]any{"resource_id": "Acme_Blog::config"}},
		{"cron_group", map[string]any{"group": "mygroup"}},
		{"preference", map[string]any{"for": "Acme\\Blog\\Api\\I", "prefer": "Acme\\Blog\\Model\\Foo"}},
	}
	for _, tc := range tests {
		t.Run(tc.typ, func(t *testing.T) {
			p := ModulePlan{
				Vendor: "Acme", Module: "Blog", Framework: "magento",
				Components: []PlanComponent{{Type: tc.typ, Params: tc.params}},
			}
			if err := p.Validate(); err != nil {
				t.Fatalf("nameless component %s should validate, got: %v", tc.typ, err)
			}
			if _, err := RenderPlan(p); err != nil {
				t.Fatalf("nameless component %s should render, got: %v", tc.typ, err)
			}
		})
	}
}

// TestMagentoNamedComponentsRequireName proves a named code-block (block, model)
// with no name is rejected, so the name-required rule still holds for the
// components that carry one.
func TestMagentoNamedComponentsRequireName(t *testing.T) {
	for _, typ := range []string{"block", "model", "helper", "observer", "cron"} {
		t.Run(typ, func(t *testing.T) {
			p := ModulePlan{
				Vendor: "Acme", Module: "Blog", Framework: "magento",
				Components: []PlanComponent{{Type: typ}},
			}
			err := p.Validate()
			if err == nil {
				t.Fatalf("%s with no name should be rejected", typ)
			}
			if !strings.Contains(err.Error(), "name is required") {
				t.Fatalf("%s: expected a name-required error, got: %v", typ, err)
			}
		})
	}
}

// TestGeneratablesMagentoControllerForm proves the family-aware inputsFor gives a
// magento controller the area/route/path/action form (magentoOverrides), not
// Laravel's resource-flag form — the collision fix.
func TestGeneratablesMagentoControllerForm(t *testing.T) {
	// Laravel controller keeps its resource flag.
	lar := keys(Generatables(testRegistry(t), "laravel"))["controller"]
	if !hasInput(lar.Inputs, "resource", "bool") {
		t.Fatalf("laravel controller should keep its resource flag, got %+v", lar.Inputs)
	}
	// Magento controller gets area/route/path/action.
	gs := builtinGeneratables("magento")
	var mag Generatable
	for _, g := range gs {
		if g.Key == "controller" {
			mag = g
		}
	}
	for _, want := range []string{"area", "route", "path", "action"} {
		found := false
		for _, in := range mag.Inputs {
			if in.Name == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("magento controller form missing %q input: %+v", want, mag.Inputs)
		}
	}
	if hasInput(mag.Inputs, "resource", "bool") {
		t.Fatalf("magento controller must not have Laravel's resource flag")
	}
}
