package gen

import (
	"encoding/xml"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/coullworks/keel/internal/recipe"
)

// init registers Magento's generation stance: template-driven (Magento ships no
// generator, so keel writes files modelled on mage2gen) and — uniquely among the
// PHP frameworks — with a REAL module concept (Vendor_Module), so a UI may show
// its Module header/name box. The module code-block is level:module; everything
// else is a code-block. Render dispatches to RenderMagento via a component
// lookup, so the whole-plan RenderPlan path and this per-component path agree.
func init() {
	comps := make([]Generatable, 0, len(MagentoComponents))
	for _, c := range MagentoComponents {
		level := recipe.LevelCodeBlock
		if c.Key == "module" {
			level = recipe.LevelModule
		}
		comps = append(comps, componentGeneratable("magento", c.Key, c.Label, level))
	}
	Register(&FrameworkGen{
		Family:     "magento",
		HasModule:  true,
		Components: comps,
		Render: func(key, name string, fields []Field, params map[string]any) ([]OutFile, bool, error) {
			comp, ok := MagentoByKey(key)
			if !ok {
				return nil, false, nil
			}
			v := MagentoVars{Name: name, Fields: fields, Params: params}
			if params != nil {
				if s, ok := params["vendor"].(string); ok {
					v.Vendor = s
				}
				if s, ok := params["module"].(string); ok {
					v.Module = s
				}
			}
			files, err := RenderMagento(comp, v)
			return files, true, err
		},
	})
}

// MagentoVars are the template inputs for a Magento component. Fields is the
// typed column list a model renders into db_schema.xml; it is empty for
// components that have no schema (a block, a helper). Base is the directory the
// module's files are written under: empty means the default app/code/<Vendor>/
// <Module>, and a Target (app/code vs composer package) sets it so a module can
// be generated into vendor/<vendor>/<module> instead.
//
// Params is the component's collected settings, keyed by its GenInput names (the
// same bag the studio/CLI/MCP fill in from each component's form). A template
// reads a setting with the {{p .}} / {{pdefault .}} helpers rather than a named
// struct field, so a new component's form threads through without changing this
// type. Any param that lands in a file path or a PHP/XML identifier is validated
// in ModulePlan.Validate before it ever reaches a template (see paramValidators).
type MagentoVars struct {
	Vendor, Module, Name string
	Fields               []Field
	Base                 string
	Params               map[string]any
}

// OutFile is a rendered file to write, relative to the project root.
type OutFile struct{ Path, Content string }

type mFile struct{ path, content string }

// MagentoComponent is a generatable Magento artifact (mage2gen-modelled). Unlike
// Laravel (which has artisan make), Magento has no generator, so each component
// emits templated files.
type MagentoComponent struct {
	Key   string
	Label string
	files []mFile
}

// magentoColumnsTmpl adapts MagentoColumnsXML for use inside a template: it
// returns the pre-rendered <column>/<constraint> block, trusting the field names
// already validated by ValidateFields, so the db_schema template can splat the
// typed columns in with {{columns .Fields}}.
func magentoColumnsTmpl(fields []Field) (string, error) { return MagentoColumnsXML(fields) }

// paramString returns the string value of a param on v, or "" when it is unset
// or not a string. It is nil-safe (a component with no Params reads as all
// empty), so a template can splat {{p . "area"}} without guarding.
func paramString(v MagentoVars, key string) string {
	if v.Params == nil {
		return ""
	}
	if s, ok := v.Params[key].(string); ok {
		return s
	}
	return ""
}

// paramDefault is paramString with a fallback used when the value is empty, so a
// template writes {{pdefault . "area" "frontend"}} instead of an if/else.
func paramDefault(v MagentoVars, key, def string) string {
	if s := paramString(v, key); s != "" {
		return s
	}
	return def
}

// paramBool reads a param as a boolean, tolerating both a real bool (the studio
// checkbox) and the "true"/"1"/"yes"/"on" strings a CLI/MCP caller may send.
func paramBool(v MagentoVars, key string) bool {
	if v.Params == nil {
		return false
	}
	switch x := v.Params[key].(type) {
	case bool:
		return x
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true", "1", "yes", "on":
			return true
		}
	}
	return false
}

// xmlEscape escapes a free-text string for safe interpolation into XML attribute
// text or a PHP string literal (labels, comments, descriptions). Identifier and
// path params are validated up front instead; this guards the values that are
// legitimately free text but must not break the surrounding markup.
func xmlEscape(s string) string {
	var sb strings.Builder
	// EscapeText only errors on an unwritable writer; a strings.Builder never
	// fails, so the error is intentionally discarded.
	_ = xml.EscapeText(&sb, []byte(s))
	return sb.String()
}

// magentoFuncs are the template helpers every Magento template may use. lower and
// columns are the originals; p/pdefault/pbool read the collected params; xesc
// escapes free text going into XML/PHP string literals; title upper-cases the
// first letter (route/menu id niceties). ucfirst is title's alias.
var magentoFuncs = template.FuncMap{
	"lower":    strings.ToLower,
	"columns":  magentoColumnsTmpl,
	"p":        paramString,
	"pdefault": paramDefault,
	"pbool":    paramBool,
	"xesc":     xmlEscape,
	"ucfirst":  ucfirst,
}

// ucfirst upper-cases the first rune of s, leaving the rest untouched — used for
// deriving a class-ish token from a lower-case identifier in a template.
func ucfirst(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func render(tmpl string, v MagentoVars) (string, error) {
	t, err := template.New("m").Funcs(magentoFuncs).Parse(tmpl)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	if err := t.Execute(&sb, v); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// RenderMagento renders a component's files under the module's base directory:
// v.Base when set (from a Target), else the default app/code/<Vendor>/<Module>/.
func RenderMagento(comp MagentoComponent, v MagentoVars) ([]OutFile, error) {
	base := v.Base
	if base == "" {
		base = filepath.Join("app", "code", v.Vendor, v.Module)
	}
	out := make([]OutFile, 0, len(comp.files))
	for _, f := range comp.files {
		p, err := render(f.path, v)
		if err != nil {
			return nil, err
		}
		c, err := render(f.content, v)
		if err != nil {
			return nil, err
		}
		out = append(out, OutFile{Path: filepath.Join(base, p), Content: c})
	}
	return out, nil
}

// MagentoByKey returns a component by key.
func MagentoByKey(key string) (MagentoComponent, bool) {
	for _, c := range MagentoComponents {
		if c.Key == key {
			return c, true
		}
	}
	return MagentoComponent{}, false
}

// MagentoComponents is the catalogue offered in a Magento project — the full
// mage2gen-modelled set. The core code-blocks live here; the wider set is grouped
// into cohesive files (magento_backend.go, magento_config.go, magento_ui.go) and
// concatenated below, so this one slice stays the single place the whole
// catalogue is assembled (see docs/GEN.md for the settings each component takes).
var MagentoComponents = func() []MagentoComponent {
	out := make([]MagentoComponent, 0, len(magentoCore)+len(magentoBackend)+len(magentoConfig)+len(magentoUI))
	out = append(out, magentoCore...)
	out = append(out, magentoBackend...)
	out = append(out, magentoConfig...)
	out = append(out, magentoUI...)
	return out
}()

// magentoCore is the original core code-block catalogue: the module bootstrap and
// the plain PHP artifacts every module builds from.
var magentoCore = []MagentoComponent{
	{Key: "module", Label: "Module (registration + module.xml)", files: []mFile{
		{"registration.php", tRegistration},
		{"etc/module.xml", tModuleXML},
		{"composer.json", tComposer},
	}},
	{Key: "model", Label: "Model (+ resource model, collection, db_schema)", files: []mFile{
		{"Model/{{.Name}}.php", tModel},
		{"Model/ResourceModel/{{.Name}}.php", tResourceModel},
		{"Model/ResourceModel/{{.Name}}/Collection.php", tCollection},
		{"etc/db_schema.xml", tDbSchema},
	}},
	{Key: "observer", Label: "Observer (+ events.xml)", files: []mFile{
		{"Observer/{{.Name}}.php", tObserver},
		{"etc/events.xml", tEventsXML},
	}},
	{Key: "block", Label: "Block", files: []mFile{
		{"Block/{{.Name}}.php", tBlock},
	}},
	{Key: "helper", Label: "Helper", files: []mFile{
		{"Helper/{{.Name}}.php", tHelper},
	}},
	{Key: "console", Label: "Console command (+ di.xml)", files: []mFile{
		{"Console/Command/{{.Name}}.php", tConsole},
		{"etc/di.xml", tConsoleDi},
	}},
}

const tRegistration = `<?php
declare(strict_types=1);

use Magento\Framework\Component\ComponentRegistrar;

ComponentRegistrar::register(
    ComponentRegistrar::MODULE,
    '{{.Vendor}}_{{.Module}}',
    __DIR__
);
`

const tModuleXML = `<?xml version="1.0"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:framework:Module/etc/module.xsd">
    <module name="{{.Vendor}}_{{.Module}}"/>
</config>
`

const tComposer = `{
    "name": "{{lower .Vendor}}/module-{{lower .Module}}",
    "description": "{{.Vendor}} {{.Module}} module",
    "type": "magento2-module",
    "require": {
        "php": ">=8.1"
    },
    "autoload": {
        "files": ["registration.php"],
        "psr-4": {
            "{{.Vendor}}\\{{.Module}}\\": ""
        }
    }
}
`

const tModel = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Model;

use Magento\Framework\Model\AbstractModel;

class {{.Name}} extends AbstractModel
{
    protected function _construct(): void
    {
        $this->_init(\{{.Vendor}}\{{.Module}}\Model\ResourceModel\{{.Name}}::class);
    }
}
`

const tResourceModel = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Model\ResourceModel;

use Magento\Framework\Model\ResourceModel\Db\AbstractDb;

class {{.Name}} extends AbstractDb
{
    protected function _construct(): void
    {
        $this->_init('{{lower .Vendor}}_{{lower .Module}}_{{lower .Name}}', 'entity_id');
    }
}
`

const tCollection = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Model\ResourceModel\{{.Name}};

use Magento\Framework\Model\ResourceModel\Db\Collection\AbstractCollection;

class Collection extends AbstractCollection
{
    protected function _construct(): void
    {
        $this->_init(
            \{{.Vendor}}\{{.Module}}\Model\{{.Name}}::class,
            \{{.Vendor}}\{{.Module}}\Model\ResourceModel\{{.Name}}::class
        );
    }
}
`

const tDbSchema = `<?xml version="1.0"?>
<schema xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:framework:Setup/Declaration/Schema/etc/schema.xsd">
    <table name="{{lower .Vendor}}_{{lower .Module}}_{{lower .Name}}" resource="default" engine="innodb" comment="{{.Name}}">
        <column xsi:type="int" name="entity_id" unsigned="true" nullable="false" identity="true" comment="Entity ID"/>
{{columns .Fields}}        <constraint xsi:type="primary" referenceId="PRIMARY">
            <column name="entity_id"/>
        </constraint>
    </table>
</schema>
`

const tObserver = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Observer;

use Magento\Framework\Event\Observer;
use Magento\Framework\Event\ObserverInterface;

class {{.Name}} implements ObserverInterface
{
    public function execute(Observer $observer): void
    {
        // TODO: implement
    }
}
`

const tEventsXML = `<?xml version="1.0"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:framework:Event/etc/events.xsd">
    <event name="example_event">
        <observer name="{{lower .Vendor}}_{{lower .Module}}_{{lower .Name}}" instance="{{.Vendor}}\{{.Module}}\Observer\{{.Name}}"/>
    </event>
</config>
`

const tBlock = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Block;

use Magento\Framework\View\Element\Template;

class {{.Name}} extends Template
{
}
`

const tHelper = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Helper;

use Magento\Framework\App\Helper\AbstractHelper;

class {{.Name}} extends AbstractHelper
{
}
`

const tConsole = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Console\Command;

use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;

class {{.Name}} extends Command
{
    protected function configure(): void
    {
        $this->setName('{{lower .Vendor}}:{{lower .Name}}')
            ->setDescription('{{.Name}} command');
        parent::configure();
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $output->writeln('<info>{{.Name}} ran</info>');
        return Command::SUCCESS;
    }
}
`

const tConsoleDi = `<?xml version="1.0"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:framework:ObjectManager/etc/config.xsd">
    <type name="Magento\Framework\Console\CommandListInterface">
        <arguments>
            <argument name="commands" xsi:type="array">
                <item name="{{lower .Vendor}}_{{lower .Name}}" xsi:type="object">{{.Vendor}}\{{.Module}}\Console\Command\{{.Name}}</item>
            </argument>
        </arguments>
    </type>
</config>
`
