package gen

import (
	"strings"
	"testing"
)

// renderComp renders a single Magento component through RenderPlan (the real
// studio/CLI entrypoint, which threads Params) and returns the produced files
// keyed by path, plus a fatal on error. It proves the whole path from a
// PlanComponent's collected params to rendered files, not just RenderMagento.
func renderComp(t *testing.T, typ, name string, params map[string]any, fields ...Field) map[string]string {
	t.Helper()
	if params == nil {
		params = map[string]any{}
	}
	if name != "" {
		params["name"] = name
	}
	p := ModulePlan{
		Vendor: "Acme", Module: "Blog", Framework: "magento", Target: TargetAppCode,
		Components: []PlanComponent{{Type: typ, Params: params, Fields: fields}},
	}
	files, err := RenderPlan(p)
	if err != nil {
		t.Fatalf("RenderPlan(%s): %v", typ, err)
	}
	out := make(map[string]string, len(files))
	for _, f := range files {
		out[f.Path] = f.Content
	}
	return out
}

// wantFile asserts a file exists at path and contains needle.
func wantFile(t *testing.T, files map[string]string, path, needle string) {
	t.Helper()
	c, ok := files[path]
	if !ok {
		var have []string
		for p := range files {
			have = append(have, p)
		}
		t.Fatalf("expected file %q not produced; got %v", path, have)
	}
	if needle != "" && !strings.Contains(c, needle) {
		t.Fatalf("%s missing %q:\n%s", path, needle, c)
	}
}

// TestMagentoComponentsRender walks every new component: it asserts (a) the
// expected files land at the right paths and (b) a representative param actually
// reaches the output — which only works because Step 1 threads c.Params into the
// template. A component whose param does NOT surface would fail here.
func TestMagentoComponentsRender(t *testing.T) {
	base := "app/code/Acme/Blog/"
	tests := []struct {
		name   string
		typ    string
		cname  string
		params map[string]any
		// file -> substring the param must produce
		want map[string]string
	}{
		{
			name: "controller", typ: "controller", cname: "View",
			params: map[string]any{"area": "frontend", "route": "blog", "path": "Post", "action": "View"},
			want: map[string]string{
				base + "Controller/Frontend/Post/View.php": `namespace Acme\Blog\Controller\Frontend\Post;`,
				base + "etc/frontend/routes.xml":           `frontName="blog"`,
			},
		},
		{
			name: "plugin", typ: "plugin", cname: "PriceLogger",
			params: map[string]any{"target": "Magento\\Catalog\\Model\\Product", "plugin_type": "before", "method": "getName"},
			want: map[string]string{
				base + "Plugin/PriceLogger.php": "beforeGetName",
				base + "etc/di.xml":             `name="Magento\Catalog\Model\Product"`,
			},
		},
		{
			name: "preference", typ: "preference", cname: "",
			params: map[string]any{"for": "Acme\\Blog\\Api\\FooInterface", "prefer": "Acme\\Blog\\Model\\Foo"},
			want: map[string]string{
				base + "etc/di.xml": `for="Acme\Blog\Api\FooInterface" type="Acme\Blog\Model\Foo"`,
			},
		},
		{
			name: "cron", typ: "cron", cname: "Sync",
			params: map[string]any{"schedule": "0 3 * * *", "group": "default"},
			want: map[string]string{
				base + "Cron/Sync.php":   "class Sync",
				base + "etc/crontab.xml": "0 3 * * *",
			},
		},
		{
			name: "system_config", typ: "system_config", cname: "",
			params: map[string]any{"section": "sec", "group": "grp", "field": "fld", "field_type": "select", "source_model": "Acme\\Blog\\Model\\Source\\Opts", "label": "My Field"},
			want: map[string]string{
				base + "etc/adminhtml/system.xml": `<field id="fld"`,
				base + "etc/config.xml":           "<sec>",
				base + "etc/acl.xml":              "Acme_Blog::config",
			},
		},
		{
			name: "acl", typ: "acl", cname: "",
			params: map[string]any{"resource_id": "Acme_Blog::config", "title": "Config", "parent": "Magento_Backend::admin"},
			want: map[string]string{
				base + "etc/acl.xml": `id="Acme_Blog::config"`,
			},
		},
		{
			name: "setup_patch_data", typ: "setup_patch_data", cname: "",
			params: map[string]any{"name": "AddThing"},
			want: map[string]string{
				base + "Setup/Patch/Data/AddThing.php": "implements DataPatchInterface",
			},
		},
		{
			name: "setup_patch_schema", typ: "setup_patch_schema", cname: "",
			params: map[string]any{"name": "AddCol"},
			want: map[string]string{
				base + "Setup/Patch/Schema/AddCol.php": "implements SchemaPatchInterface",
			},
		},
		{
			name: "ui_component_listing", typ: "ui_component_listing", cname: "Foo",
			params: map[string]any{"id": "acme_grid", "model": "Acme\\Blog\\Model\\ResourceModel\\Foo", "acl": "Acme_Blog::config", "add_button": true},
			want: map[string]string{
				base + "view/adminhtml/ui_component/acme_grid.xml": `spinner" xsi:type="string">acme_grid_columns`,
				base + "Ui/Component/Listing/FooDataProvider.php":  "class FooDataProvider",
				base + "etc/di.xml": "AcmeBlogFooGridCollection",
			},
		},
		{
			name: "ui_component_form", typ: "ui_component_form", cname: "Foo",
			params: map[string]any{"id": "acme_form", "model": "Acme\\Blog\\Model\\Foo"},
			want: map[string]string{
				base + "view/adminhtml/ui_component/acme_form.xml": `provider" xsi:type="string">acme_form.acme_form_data_source`,
				base + "Ui/Component/Form/FooDataProvider.php":     "class FooDataProvider",
			},
		},
		{
			name: "menu", typ: "menu", cname: "",
			params: map[string]any{"id": "Acme_Blog::menu", "title": "Blog", "action": "blog/index/index", "resource": "Acme_Blog::config"},
			want: map[string]string{
				base + "etc/adminhtml/menu.xml": `id="Acme_Blog::menu"`,
			},
		},
		{
			name: "email_template", typ: "email_template", cname: "Hello",
			params: map[string]any{"id": "acme_hello", "label": "Hi", "area": "frontend", "subject": "Welcome"},
			want: map[string]string{
				base + "etc/email_templates.xml":             `id="acme_hello"`,
				base + "view/frontend/email/acme_hello.html": "Welcome",
			},
		},
		{
			name: "widget", typ: "widget", cname: "Promo",
			params: map[string]any{"id": "acme_promo", "label": "Promo", "description": "d"},
			want: map[string]string{
				base + "etc/widget.xml":                             `id="acme_promo"`,
				base + "Block/Widget/Promo.php":                     "class Promo",
				base + "view/frontend/templates/widget/promo.phtml": "widget",
			},
		},
		{
			name: "view", typ: "view", cname: "Extra",
			params: map[string]any{"area": "frontend", "handle": "catalog_product_view", "block": "Acme\\Blog\\Block\\Extra", "template": "product/extra"},
			want: map[string]string{
				base + "view/frontend/layout/catalog_product_view.xml": `class="Acme\Blog\Block\Extra"`,
				base + "view/frontend/templates/product/extra.phtml":   "Acme",
				base + "Block/Extra.php":                               "class Extra",
			},
		},
		{
			name: "product_attribute", typ: "product_attribute", cname: "AddMyAttr",
			params: map[string]any{"code": "my_attr", "label": "My Attr", "input": "select", "type": "int", "required": true},
			want: map[string]string{
				base + "Setup/Patch/Data/AddMyAttr.php": "'my_attr'",
			},
		},
		{
			name: "customer_attribute", typ: "customer_attribute", cname: "AddLoyalty",
			params: map[string]any{"code": "loyalty", "label": "Loyalty", "input": "text", "type": "varchar"},
			want: map[string]string{
				base + "Setup/Patch/Data/AddLoyalty.php": "'loyalty'",
			},
		},
		{
			name: "webapi", typ: "webapi", cname: "Item",
			params: map[string]any{"route": "items", "method": "POST", "service": "Acme\\Blog\\Api\\ItemInterface", "resource": "Acme_Blog::config"},
			want: map[string]string{
				base + "etc/webapi.xml":        `url="/V1/items" method="POST"`,
				base + "Api/ItemInterface.php": "interface ItemInterface",
				base + "Model/Item.php":        "class Item implements ItemInterface",
				base + "etc/di.xml":            `Acme\Blog\Api\ItemInterface`,
			},
		},
		{
			name: "graphql", typ: "graphql", cname: "Thing",
			params: map[string]any{"type": "myThing", "operation": "query"},
			want: map[string]string{
				base + "etc/schema.graphqls":      "myThing:",
				base + "Model/Resolver/Thing.php": "implements ResolverInterface",
			},
		},
		{
			name: "cron_group", typ: "cron_group", cname: "",
			params: map[string]any{"group": "mygroup", "use_separate_process": true},
			want: map[string]string{
				base + "etc/cron_groups.xml": `id="mygroup"`,
			},
		},
		{
			name: "cache_type", typ: "cache_type", cname: "Custom",
			params: map[string]any{"id": "acme_cache", "tag": "ACME"},
			want: map[string]string{
				base + "etc/cache.xml":               `name="acme_cache"`,
				base + "Model/Cache/Type/Custom.php": "ACME",
			},
		},
		{
			name: "indexer", typ: "indexer", cname: "Foo",
			params: map[string]any{"id": "acme_idx", "title": "Idx", "view_id": "acme_idx"},
			want: map[string]string{
				base + "etc/indexer.xml":       `id="acme_idx"`,
				base + "etc/mview.xml":         `id="acme_idx"`,
				base + "Model/Indexer/Foo.php": "class Foo",
			},
		},
		{
			name: "message_queue", typ: "message_queue", cname: "Handler",
			params: map[string]any{"topic": "acme.topic", "consumer": "acmeConsumer", "queue": "acme.queue", "handler": "Acme\\Blog\\Model\\Handler", "connection": "amqp"},
			want: map[string]string{
				base + "etc/communication.xml":   `name="acme.topic"`,
				base + "etc/queue.xml":           `name="acme.queue"`,
				base + "etc/queue_consumer.xml":  `name="acmeConsumer"`,
				base + "etc/queue_topology.xml":  "acme.topic",
				base + "etc/queue_publisher.xml": "acme.topic",
				base + "Model/Handler.php":       "class Handler",
			},
		},
		{
			name: "unit_test", typ: "unit_test", cname: "Foo",
			params: map[string]any{"class": "Acme\\Blog\\Model\\Foo"},
			want: map[string]string{
				base + "Test/Unit/FooTest.php": "class FooTest extends TestCase",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := renderComp(t, tc.typ, tc.cname, tc.params)
			for path, needle := range tc.want {
				wantFile(t, files, path, needle)
			}
		})
	}
}

// TestMagentoParamThreadingProvesStep1 is the direct proof that Step 1's Params
// threading works: the same component rendered with two different param values
// produces two different outputs. If RenderPlan dropped c.Params (the original
// bug) both would be identical.
func TestMagentoParamThreadingProvesStep1(t *testing.T) {
	a := renderComp(t, "cron", "Sync", map[string]any{"schedule": "0 1 * * *"})
	b := renderComp(t, "cron", "Sync", map[string]any{"schedule": "*/5 * * * *"})
	pa := a["app/code/Acme/Blog/etc/crontab.xml"]
	pb := b["app/code/Acme/Blog/etc/crontab.xml"]
	if !strings.Contains(pa, "0 1 * * *") {
		t.Fatalf("first crontab lost its schedule param:\n%s", pa)
	}
	if !strings.Contains(pb, "*/5 * * * *") {
		t.Fatalf("second crontab lost its schedule param:\n%s", pb)
	}
	if pa == pb {
		t.Fatal("two different schedule params produced identical output — Params not threaded")
	}
}

// TestMagentoXMLEscaping proves free-text labels/comments are XML-escaped rather
// than injected raw (the xesc helper), so a label with markup cannot break the
// surrounding XML.
func TestMagentoXMLEscaping(t *testing.T) {
	files := renderComp(t, "acl", "", map[string]any{"resource_id": "Acme_Blog::config", "title": "A & B <x>"})
	acl := files["app/code/Acme/Blog/etc/acl.xml"]
	if strings.Contains(acl, "A & B <x>") {
		t.Fatalf("acl title was injected raw (not escaped):\n%s", acl)
	}
	if !strings.Contains(acl, "A &amp; B &lt;x&gt;") {
		t.Fatalf("acl title not XML-escaped:\n%s", acl)
	}
}

// TestMagentoParamHelpers covers the nil-safe param/escape/casing helpers the
// templates rely on, including the branches the render tests do not hit (a
// missing Params map, a non-string value, the string spellings of a bool).
func TestMagentoParamHelpers(t *testing.T) {
	// nil Params: everything reads empty, no panic.
	nilv := MagentoVars{}
	if paramString(nilv, "x") != "" {
		t.Error("paramString on nil Params should be empty")
	}
	if paramBool(nilv, "x") {
		t.Error("paramBool on nil Params should be false")
	}
	if paramDefault(nilv, "x", "d") != "d" {
		t.Error("paramDefault should fall back when Params is nil")
	}

	// non-string value is treated as unset by paramString.
	v := MagentoVars{Params: map[string]any{"n": 7, "s": "hi", "b": true, "bs": "yes", "bn": "nope"}}
	if paramString(v, "n") != "" {
		t.Error("paramString of a non-string should be empty")
	}
	if paramString(v, "s") != "hi" {
		t.Error("paramString should read a string value")
	}
	if !paramBool(v, "b") || !paramBool(v, "bs") {
		t.Error("paramBool should be true for a real bool and a truthy string")
	}
	if paramBool(v, "bn") || paramBool(v, "n") {
		t.Error("paramBool should be false for a non-truthy string and a non-bool")
	}

	if ucfirst("") != "" {
		t.Error("ucfirst of empty should be empty")
	}
	if ucfirst("foo") != "Foo" {
		t.Error("ucfirst should upper-case the first rune")
	}
	if xmlEscape("a<b>&c") != "a&lt;b&gt;&amp;c" {
		t.Errorf("xmlEscape wrong: %q", xmlEscape("a<b>&c"))
	}
}
