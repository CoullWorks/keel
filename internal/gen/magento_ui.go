package gen

// magento_ui.go holds the presentation + EAV components: admin ui_component grids
// and forms (with their DataProviders + di.xml virtual types), storefront view
// (layout + template + block), widgets, EAV product/customer attributes (added
// via a data patch using EavSetup), and a PHPUnit unit test. Identifier/path
// settings are validated in magento_validate.go; labels/comments are xesc-escaped.

// magentoUI is concatenated into MagentoComponents (see magento.go).
var magentoUI = []MagentoComponent{
	{Key: "ui_component_listing", Label: "Admin grid (ui_component listing + DataProvider + di.xml)", files: []mFile{
		{"view/adminhtml/ui_component/{{p . \"id\"}}.xml", tUiListing},
		{"Ui/Component/Listing/{{.Name}}DataProvider.php", tUiListingDataProvider},
		{"etc/di.xml", tUiListingDi},
	}},
	{Key: "ui_component_form", Label: "Admin form (ui_component form + DataProvider)", files: []mFile{
		{"view/adminhtml/ui_component/{{p . \"id\"}}.xml", tUiForm},
		{"Ui/Component/Form/{{.Name}}DataProvider.php", tUiFormDataProvider},
	}},
	{Key: "view", Label: "View (layout + template + block)", files: []mFile{
		{"view/{{pdefault . \"area\" \"frontend\"}}/layout/{{p . \"handle\"}}.xml", tViewLayout},
		{"view/{{pdefault . \"area\" \"frontend\"}}/templates/{{pdefault . \"template\" (lower .Name)}}.phtml", tViewTemplate},
		{"Block/{{.Name}}.php", tViewBlock},
	}},
	{Key: "widget", Label: "Widget (widget.xml + Block + template)", files: []mFile{
		{"etc/widget.xml", tWidgetXML},
		{"Block/Widget/{{.Name}}.php", tWidgetBlock},
		{"view/frontend/templates/widget/{{lower .Name}}.phtml", tWidgetTemplate},
	}},
	{Key: "product_attribute", Label: "Product attribute (EAV data patch)", files: []mFile{
		{"Setup/Patch/Data/{{.Name}}.php", tProductAttribute},
	}},
	{Key: "customer_attribute", Label: "Customer attribute (EAV data patch)", files: []mFile{
		{"Setup/Patch/Data/{{.Name}}.php", tCustomerAttribute},
	}},
	{Key: "unit_test", Label: "Unit test (PHPUnit)", files: []mFile{
		{"Test/Unit/{{.Name}}Test.php", tUnitTest},
	}},
}

// --- ui_component listing (admin grid) ---

const tUiListing = `<?xml version="1.0"?>
<listing xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:module:Magento_Ui:etc/ui_configuration.xsd">
    <argument name="data" xsi:type="array">
        <item name="js_config" xsi:type="array">
            <item name="provider" xsi:type="string">{{p . "id"}}.{{p . "id"}}_data_source</item>
        </item>
        <item name="spinner" xsi:type="string">{{p . "id"}}_columns</item>
{{if pbool . "add_button"}}        <item name="buttons" xsi:type="array">
            <item name="add" xsi:type="array">
                <item name="name" xsi:type="string">add</item>
                <item name="label" xsi:type="string" translate="true">Add New</item>
                <item name="class" xsi:type="string">primary</item>
            </item>
        </item>
{{end}}    </argument>
    <dataSource name="{{p . "id"}}_data_source" component="Magento_Ui/js/grid/provider">
        <settings>
            <storageConfig>
                <param name="indexField" xsi:type="string">entity_id</param>
            </storageConfig>
            <updateUrl path="mui/index/render"/>
        </settings>
        <aclResource>{{pdefault . "acl" (print .Vendor "_" .Module "::config")}}</aclResource>
        <dataProvider class="{{.Vendor}}\{{.Module}}\Ui\Component\Listing\{{.Name}}DataProvider" name="{{p . "id"}}_data_source">
            <settings>
                <requestFieldName>entity_id</requestFieldName>
                <primaryFieldName>entity_id</primaryFieldName>
            </settings>
        </dataProvider>
    </dataSource>
    <columns name="{{p . "id"}}_columns">
        <selectionsColumn name="ids">
            <settings>
                <indexField>entity_id</indexField>
            </settings>
        </selectionsColumn>
        <column name="entity_id">
            <settings>
                <label translate="true">ID</label>
            </settings>
        </column>
    </columns>
</listing>
`

const tUiListingDataProvider = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Ui\Component\Listing;

use Magento\Framework\View\Element\UiComponent\DataProvider\DataProvider;

class {{.Name}}DataProvider extends DataProvider
{
}
`

const tUiListingDi = `<?xml version="1.0"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:framework:ObjectManager/etc/config.xsd">
    <virtualType name="{{.Vendor}}{{.Module}}{{.Name}}GridCollection" type="Magento\Framework\View\Element\UiComponent\DataProvider\SearchResult">
        <arguments>
            <argument name="mainTable" xsi:type="string">{{lower .Vendor}}_{{lower .Module}}_{{lower .Name}}</argument>
            <argument name="resourceModel" xsi:type="string">{{p . "model"}}</argument>
        </arguments>
    </virtualType>
    <type name="Magento\Framework\View\Element\UiComponent\DataProvider\CollectionFactory">
        <arguments>
            <argument name="collections" xsi:type="array">
                <item name="{{p . "id"}}_data_source" xsi:type="string">{{.Vendor}}{{.Module}}{{.Name}}GridCollection</item>
            </argument>
        </arguments>
    </type>
</config>
`

// --- ui_component form (admin form) ---

const tUiForm = `<?xml version="1.0"?>
<form xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:module:Magento_Ui:etc/ui_configuration.xsd">
    <argument name="data" xsi:type="array">
        <item name="js_config" xsi:type="array">
            <item name="provider" xsi:type="string">{{p . "id"}}.{{p . "id"}}_data_source</item>
        </item>
        <item name="label" xsi:type="string" translate="true">{{xesc .Name}}</item>
    </argument>
    <dataSource name="{{p . "id"}}_data_source">
        <argument name="data" xsi:type="array">
            <item name="js_config" xsi:type="array">
                <item name="component" xsi:type="string">Magento_Ui/js/form/provider</item>
            </item>
        </argument>
        <dataProvider class="{{.Vendor}}\{{.Module}}\Ui\Component\Form\{{.Name}}DataProvider" name="{{p . "id"}}_data_source">
            <settings>
                <requestFieldName>entity_id</requestFieldName>
                <primaryFieldName>entity_id</primaryFieldName>
            </settings>
        </dataProvider>
    </dataSource>
    <fieldset name="general">
        <settings>
            <label translate="true">General</label>
        </settings>
{{range .Fields}}        <field name="{{.Name}}" formElement="input">
            <settings>
                <dataType>text</dataType>
                <label translate="true">{{.Name}}</label>
            </settings>
        </field>
{{end}}    </fieldset>
</form>
`

const tUiFormDataProvider = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Ui\Component\Form;

use Magento\Ui\DataProvider\AbstractDataProvider;

class {{.Name}}DataProvider extends AbstractDataProvider
{
    public function getData(): array
    {
        return [];
    }
}
`

// --- view (layout + template + block) ---

const tViewLayout = `<?xml version="1.0"?>
<page xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" layout="1column" xsi:noNamespaceSchemaLocation="urn:magento:framework:View/Layout/etc/page_configuration.xsd">
    <body>
        <referenceContainer name="content">
            <block class="{{pdefault . "block" (print .Vendor "\\" .Module "\\Block\\" .Name)}}" name="{{lower .Vendor}}_{{lower .Module}}_{{lower .Name}}" template="{{.Vendor}}_{{.Module}}::{{pdefault . "template" (lower .Name)}}.phtml"/>
        </referenceContainer>
    </body>
</page>
`

const tViewTemplate = `<?php
/** @var \{{pdefault . "block" (print .Vendor "\\" .Module "\\Block\\" .Name)}} $block */
?>
<div class="{{lower .Vendor}}-{{lower .Module}}-{{lower .Name}}">
    <!-- TODO: template markup -->
</div>
`

const tViewBlock = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Block;

use Magento\Framework\View\Element\Template;

class {{.Name}} extends Template
{
}
`

// --- widget ---

const tWidgetXML = `<?xml version="1.0"?>
<widgets xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:module:Magento_Widget:etc/widget.xsd">
    <widget id="{{pdefault . "id" (lower .Name)}}" class="{{.Vendor}}\{{.Module}}\Block\Widget\{{.Name}}">
        <label translate="true">{{xesc (pdefault . "label" .Name)}}</label>
        <description translate="true">{{xesc (pdefault . "description" .Name)}}</description>
        <parameters/>
    </widget>
</widgets>
`

const tWidgetBlock = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Block\Widget;

use Magento\Framework\View\Element\Template;
use Magento\Widget\Block\BlockInterface;

class {{.Name}} extends Template implements BlockInterface
{
    protected $_template = '{{.Vendor}}_{{.Module}}::widget/{{lower .Name}}.phtml';
}
`

const tWidgetTemplate = `<?php
/** @var \{{.Vendor}}\{{.Module}}\Block\Widget\{{.Name}} $block */
?>
<div class="widget {{lower .Vendor}}-{{lower .Name}}-widget">
    <!-- TODO: widget markup -->
</div>
`

// --- product attribute (EAV via data patch) ---

const tProductAttribute = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Setup\Patch\Data;

use Magento\Catalog\Model\Product;
use Magento\Eav\Setup\EavSetup;
use Magento\Eav\Setup\EavSetupFactory;
use Magento\Framework\Setup\ModuleDataSetupInterface;
use Magento\Framework\Setup\Patch\DataPatchInterface;

class {{.Name}} implements DataPatchInterface
{
    public function __construct(
        private readonly ModuleDataSetupInterface $moduleDataSetup,
        private readonly EavSetupFactory $eavSetupFactory
    ) {
    }

    public function apply(): void
    {
        /** @var EavSetup $eavSetup */
        $eavSetup = $this->eavSetupFactory->create(['setup' => $this->moduleDataSetup]);
        $eavSetup->addAttribute(
            Product::ENTITY,
            '{{p . "code"}}',
            [
                'type' => '{{pdefault . "type" "varchar"}}',
                'label' => '{{xesc (pdefault . "label" (p . "code"))}}',
                'input' => '{{pdefault . "input" "text"}}',
                {{if p . "source"}}'source' => \{{p . "source"}}::class,
                {{end}}'required' => {{if pbool . "required"}}true{{else}}false{{end}},
                'global' => \Magento\Eav\Model\Entity\Attribute\ScopedAttributeInterface::SCOPE_{{if eq (pdefault . "scope" "global") "website"}}WEBSITE{{else if eq (p . "scope") "store"}}STORE{{else}}GLOBAL{{end}},
                'group' => '{{xesc (pdefault . "group" "General")}}',
                'sort_order' => {{pdefault . "sort_order" "10"}},
                'used_in_product_listing' => {{if pbool . "used_in_grid"}}true{{else}}false{{end}},
                'visible_on_front' => {{if pbool . "visible_on_front"}}true{{else}}false{{end}},
                'user_defined' => true,
            ]
        );
    }

    public static function getDependencies(): array
    {
        return [];
    }

    public function getAliases(): array
    {
        return [];
    }
}
`

// --- customer attribute (EAV via data patch) ---

const tCustomerAttribute = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Setup\Patch\Data;

use Magento\Customer\Model\Customer;
use Magento\Customer\Setup\CustomerSetupFactory;
use Magento\Eav\Model\Entity\Attribute\Set as AttributeSet;
use Magento\Eav\Model\Entity\Attribute\SetFactory as AttributeSetFactory;
use Magento\Framework\Setup\ModuleDataSetupInterface;
use Magento\Framework\Setup\Patch\DataPatchInterface;

class {{.Name}} implements DataPatchInterface
{
    public function __construct(
        private readonly ModuleDataSetupInterface $moduleDataSetup,
        private readonly CustomerSetupFactory $customerSetupFactory,
        private readonly AttributeSetFactory $attributeSetFactory
    ) {
    }

    public function apply(): void
    {
        $customerSetup = $this->customerSetupFactory->create(['setup' => $this->moduleDataSetup]);
        $customerEntity = $customerSetup->getEavConfig()->getEntityType(Customer::ENTITY);
        $attributeSetId = $customerEntity->getDefaultAttributeSetId();
        /** @var AttributeSet $attributeSet */
        $attributeSet = $this->attributeSetFactory->create();
        $attributeGroupId = $attributeSet->getDefaultGroupId($attributeSetId);

        $customerSetup->addAttribute(
            Customer::ENTITY,
            '{{p . "code"}}',
            [
                'type' => '{{pdefault . "type" "varchar"}}',
                'label' => '{{xesc (pdefault . "label" (p . "code"))}}',
                'input' => '{{pdefault . "input" "text"}}',
                {{if p . "source"}}'source' => \{{p . "source"}}::class,
                {{end}}'required' => {{if pbool . "required"}}true{{else}}false{{end}},
                'visible' => true,
                'user_defined' => true,
                'system' => false,
            ]
        );

        $attribute = $customerSetup->getEavConfig()->getAttribute(Customer::ENTITY, '{{p . "code"}}');
        $attribute->addData([
            'attribute_set_id' => $attributeSetId,
            'attribute_group_id' => $attributeGroupId,
            'used_in_forms' => ['adminhtml_customer'],
        ]);
        $attribute->save();
    }

    public static function getDependencies(): array
    {
        return [];
    }

    public function getAliases(): array
    {
        return [];
    }
}
`

// --- unit test ---

const tUnitTest = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Test\Unit;

use PHPUnit\Framework\TestCase;

class {{.Name}}Test extends TestCase
{
    public function testIsInstantiable(): void
    {
        $this->assertTrue(class_exists(\{{p . "class"}}::class));
    }
}
`
