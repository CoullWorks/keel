package gen

// magento_config.go holds the admin-configuration and API-surface components:
// the admin System Config field (system.xml + config.xml default + acl.xml),
// transactional email templates, the REST webapi surface, and GraphQL. Every
// identifier setting is validated in magento_validate.go; free-text (labels,
// comments) is XML-escaped with the xesc template helper.

// magentoConfig is concatenated into MagentoComponents (see magento.go).
var magentoConfig = []MagentoComponent{
	{Key: "system_config", Label: "Admin config field (system.xml + config.xml + acl.xml)", files: []mFile{
		{"etc/adminhtml/system.xml", tSystemXML},
		{"etc/config.xml", tConfigXML},
		{"etc/acl.xml", tSystemAcl},
	}},
	{Key: "email_template", Label: "Email template (email_templates.xml + html)", files: []mFile{
		{"etc/email_templates.xml", tEmailTemplatesXML},
		{"view/{{pdefault . \"area\" \"frontend\"}}/email/{{lower (p . \"id\")}}.html", tEmailHTML},
	}},
	{Key: "webapi", Label: "Web API (webapi.xml + Api interface + Model + di.xml)", files: []mFile{
		{"etc/webapi.xml", tWebapiXML},
		{"Api/{{.Name}}Interface.php", tWebapiInterface},
		{"Model/{{.Name}}.php", tWebapiModel},
		{"etc/di.xml", tWebapiDi},
	}},
	{Key: "graphql", Label: "GraphQL (schema.graphqls + resolver)", files: []mFile{
		{"etc/schema.graphqls", tGraphqlSchema},
		{"Model/Resolver/{{.Name}}.php", tGraphqlResolver},
	}},
}

// --- system config ---

const tSystemXML = `<?xml version="1.0"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:module:Magento_Config:etc/system_file.xsd">
    <system>
        <tab id="{{pdefault . "tab" (lower .Vendor)}}" translate="label" sortOrder="100">
            <label>{{xesc (pdefault . "tab" .Vendor)}}</label>
        </tab>
        <section id="{{p . "section"}}" translate="label" type="text" sortOrder="10" showInDefault="1" showInWebsite="1" showInStore="1">
            <label>{{xesc (pdefault . "label" (p . "section"))}}</label>
            <tab>{{pdefault . "tab" (lower .Vendor)}}</tab>
            <resource>{{.Vendor}}_{{.Module}}::config</resource>
            <group id="{{p . "group"}}" translate="label" type="text" sortOrder="10" showInDefault="1" showInWebsite="1" showInStore="1">
                <label>{{xesc (pdefault . "group" (p . "group"))}}</label>
                <field id="{{p . "field"}}" translate="label comment" type="{{pdefault . "field_type" "text"}}" sortOrder="10" showInDefault="1"{{if eq (pdefault . "scope" "default") "store"}} showInWebsite="1" showInStore="1"{{else if eq (p . "scope") "website"}} showInWebsite="1"{{end}}>
                    <label>{{xesc (pdefault . "label" (p . "field"))}}</label>
                    <comment>{{xesc (p . "comment")}}</comment>
{{if p . "source_model"}}                    <source_model>{{p . "source_model"}}</source_model>
{{end}}                </field>
            </group>
        </section>
    </system>
</config>
`

const tConfigXML = `<?xml version="1.0"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:module:Magento_Store:etc/config.xsd">
    <default>
        <{{p . "section"}}>
            <{{p . "group"}}>
                <{{p . "field"}}></{{p . "field"}}>
            </{{p . "group"}}>
        </{{p . "section"}}>
    </default>
</config>
`

const tSystemAcl = `<?xml version="1.0"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:framework:Acl/etc/acl.xsd">
    <acl>
        <resources>
            <resource id="Magento_Backend::admin">
                <resource id="Magento_Backend::stores">
                    <resource id="Magento_Config::config">
                        <resource id="{{.Vendor}}_{{.Module}}::config" title="{{xesc (pdefault . "label" .Module)}} Config"/>
                    </resource>
                </resource>
            </resource>
        </resources>
    </acl>
</config>
`

// --- email template ---

const tEmailTemplatesXML = `<?xml version="1.0"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:module:Magento_Email:etc/email_templates.xsd">
    <template id="{{p . "id"}}" label="{{xesc (pdefault . "label" (p . "id"))}}" file="{{lower (p . "id")}}.html" type="html" module="{{.Vendor}}_{{.Module}}" area="{{pdefault . "area" "frontend"}}"/>
</config>
`

const tEmailHTML = `<!--@subject {{xesc (pdefault . "subject" (p . "id"))}} @-->
<!--@vars {
} @-->
{{"{{"}}template config_path="design/email/header_template"{{"}}"}}
<p>{{xesc (pdefault . "subject" (p . "id"))}}</p>
{{"{{"}}template config_path="design/email/footer_template"{{"}}"}}
`

// --- webapi ---

const tWebapiXML = `<?xml version="1.0"?>
<routes xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:module:Magento_Webapi:etc/webapi.xsd">
    <route url="/V1/{{p . "route"}}" method="{{pdefault . "method" "GET"}}">
        <service class="{{.Vendor}}\{{.Module}}\Api\{{.Name}}Interface" method="execute"/>
        <resources>
            <resource ref="{{pdefault . "resource" "anonymous"}}"/>
        </resources>
    </route>
</routes>
`

const tWebapiInterface = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Api;

interface {{.Name}}Interface
{
    /**
     * @return string
     */
    public function execute(): string;
}
`

const tWebapiModel = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Model;

use {{.Vendor}}\{{.Module}}\Api\{{.Name}}Interface;

class {{.Name}} implements {{.Name}}Interface
{
    public function execute(): string
    {
        return '';
    }
}
`

const tWebapiDi = `<?xml version="1.0"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:framework:ObjectManager/etc/config.xsd">
    <preference for="{{.Vendor}}\{{.Module}}\Api\{{.Name}}Interface" type="{{.Vendor}}\{{.Module}}\Model\{{.Name}}"/>
</config>
`

// --- graphql ---

const tGraphqlSchema = `type Query {
    {{p . "type"}}: {{ucfirst (p . "type")}} @resolver(class: "{{.Vendor}}\\{{.Module}}\\Model\\Resolver\\{{.Name}}") @doc(description: "{{ucfirst (p . "type")}} query")
}

type {{ucfirst (p . "type")}} @doc(description: "{{ucfirst (p . "type")}}") {
    id: Int @doc(description: "Entity id")
}
`

const tGraphqlResolver = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Model\Resolver;

use Magento\Framework\GraphQl\Config\Element\Field;
use Magento\Framework\GraphQl\Query\ResolverInterface;
use Magento\Framework\GraphQl\Schema\Type\ResolveInfo;

class {{.Name}} implements ResolverInterface
{
    public function resolve(
        Field $field,
        $context,
        ResolveInfo $info,
        ?array $value = null,
        ?array $args = null
    ) {
        return [];
    }
}
`
