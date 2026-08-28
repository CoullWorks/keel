package gen

// magento_backend.go holds the backend/wiring components: controllers, DI
// interception (plugin, preference), scheduled work (cron, cron_group), access
// control (acl, menu), setup patches, and the platform plumbing (cache type,
// indexer, message queue). Each is modelled on mage2gen's snippet output and
// emits real Magento 2 XML (correct urn:magento xsd) and PSR-4 PHP. The
// identifier/path settings each consumes are validated in magento_validate.go
// before any template runs.

// magentoBackend is concatenated into MagentoComponents (see magento.go).
var magentoBackend = []MagentoComponent{
	{Key: "controller", Label: "Controller (frontend/adminhtml + routes.xml)", files: []mFile{
		{"Controller/{{ucfirst (pdefault . \"area\" \"frontend\")}}/{{p . \"path\"}}/{{pdefault . \"action\" \"Index\"}}.php", tController},
		{"etc/{{pdefault . \"area\" \"frontend\"}}/routes.xml", tControllerRoutes},
	}},
	{Key: "plugin", Label: "Plugin (interceptor + di.xml)", files: []mFile{
		{"Plugin/{{.Name}}.php", tPlugin},
		{"{{if eq (pdefault . \"area\" \"global\") \"global\"}}etc/di.xml{{else}}etc/{{p . \"area\"}}/di.xml{{end}}", tPluginDi},
	}},
	{Key: "preference", Label: "Preference (di.xml)", files: []mFile{
		{"{{if eq (pdefault . \"area\" \"global\") \"global\"}}etc/di.xml{{else}}etc/{{p . \"area\"}}/di.xml{{end}}", tPreferenceDi},
	}},
	{Key: "cron", Label: "Cron job (+ crontab.xml)", files: []mFile{
		{"Cron/{{.Name}}.php", tCron},
		{"etc/crontab.xml", tCrontab},
	}},
	{Key: "acl", Label: "ACL resource (acl.xml)", files: []mFile{
		{"etc/acl.xml", tAcl},
	}},
	{Key: "menu", Label: "Admin menu (menu.xml)", files: []mFile{
		{"etc/adminhtml/menu.xml", tMenu},
	}},
	{Key: "setup_patch_data", Label: "Data patch (Setup/Patch/Data)", files: []mFile{
		{"Setup/Patch/Data/{{.Name}}.php", tDataPatch},
	}},
	{Key: "setup_patch_schema", Label: "Schema patch (Setup/Patch/Schema)", files: []mFile{
		{"Setup/Patch/Schema/{{.Name}}.php", tSchemaPatch},
	}},
	{Key: "cron_group", Label: "Cron group (cron_groups.xml)", files: []mFile{
		{"etc/cron_groups.xml", tCronGroups},
	}},
	{Key: "cache_type", Label: "Cache type (cache.xml + Model)", files: []mFile{
		{"etc/cache.xml", tCacheXML},
		{"Model/Cache/Type/{{.Name}}.php", tCacheType},
	}},
	{Key: "indexer", Label: "Indexer (indexer.xml + mview.xml + Model)", files: []mFile{
		{"etc/indexer.xml", tIndexerXML},
		{"etc/mview.xml", tMviewXML},
		{"Model/Indexer/{{.Name}}.php", tIndexerModel},
	}},
	{Key: "message_queue", Label: "Message queue consumer (communication/queue xml + handler)", files: []mFile{
		{"etc/communication.xml", tCommunicationXML},
		{"etc/queue.xml", tQueueXML},
		{"etc/queue_consumer.xml", tQueueConsumerXML},
		{"etc/queue_topology.xml", tQueueTopologyXML},
		{"etc/queue_publisher.xml", tQueuePublisherXML},
		{"Model/{{.Name}}.php", tQueueHandler},
	}},
}

// --- controller ---

const tController = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Controller\{{ucfirst (pdefault . "area" "frontend")}}{{if p . "path"}}\{{p . "path"}}{{end}};

use Magento\Framework\App\Action\HttpGetActionInterface;
use Magento\Framework\Controller\Result\Page;
use Magento\Framework\Controller\ResultFactory;

class {{pdefault . "action" "Index"}} implements HttpGetActionInterface
{
    public function __construct(
        private readonly ResultFactory $resultFactory
    ) {
    }

    public function execute(): Page
    {
        /** @var Page $result */
        $result = $this->resultFactory->create(ResultFactory::TYPE_PAGE);
        return $result;
    }
}
`

const tControllerRoutes = `<?xml version="1.0"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:framework:App/etc/routes.xsd">
    <router id="{{if eq (pdefault . "area" "frontend") "adminhtml"}}admin{{else}}standard{{end}}">
        <route id="{{pdefault . "route" (lower .Module)}}" frontName="{{pdefault . "route" (lower .Module)}}">
            <module name="{{.Vendor}}_{{.Module}}"/>
        </route>
    </router>
</config>
`

// --- plugin ---

const tPlugin = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Plugin;

class {{.Name}}
{
{{if eq (pdefault . "plugin_type" "after") "before"}}    public function before{{ucfirst (p . "method")}}(
        \{{p . "target"}} $subject
    ): array {
        return [];
    }
{{else if eq (p . "plugin_type") "around"}}    public function around{{ucfirst (p . "method")}}(
        \{{p . "target"}} $subject,
        callable $proceed
    ) {
        return $proceed();
    }
{{else}}    public function after{{ucfirst (p . "method")}}(
        \{{p . "target"}} $subject,
        $result
    ) {
        return $result;
    }
{{end}}}
`

const tPluginDi = `<?xml version="1.0"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:framework:ObjectManager/etc/config.xsd">
    <type name="{{p . "target"}}">
        <plugin name="{{lower .Vendor}}_{{lower .Module}}_{{lower .Name}}" type="{{.Vendor}}\{{.Module}}\Plugin\{{.Name}}" sortOrder="{{pdefault . "sort_order" "10"}}"/>
    </type>
</config>
`

// --- preference ---

const tPreferenceDi = `<?xml version="1.0"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:framework:ObjectManager/etc/config.xsd">
    <preference for="{{p . "for"}}" type="{{p . "prefer"}}"/>
</config>
`

// --- cron ---

const tCron = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Cron;

use Psr\Log\LoggerInterface;

class {{.Name}}
{
    public function __construct(
        private readonly LoggerInterface $logger
    ) {
    }

    public function execute(): void
    {
        $this->logger->info('{{.Vendor}}\{{.Module}}\Cron\{{.Name}} ran');
    }
}
`

const tCrontab = `<?xml version="1.0"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:framework:Cron/etc/crontab.xsd">
    <group id="{{pdefault . "group" "default"}}">
        <job name="{{lower .Vendor}}_{{lower .Module}}_{{lower .Name}}" instance="{{.Vendor}}\{{.Module}}\Cron\{{.Name}}" method="execute">
            <schedule>{{pdefault . "schedule" "* * * * *"}}</schedule>
        </job>
    </group>
</config>
`

// --- acl ---

const tAcl = `<?xml version="1.0"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:framework:Acl/etc/acl.xsd">
    <acl>
        <resources>
            <resource id="{{pdefault . "parent" "Magento_Backend::admin"}}">
                <resource id="{{p . "resource_id"}}" title="{{xesc (p . "title")}}"/>
            </resource>
        </resources>
    </acl>
</config>
`

// --- menu ---

const tMenu = `<?xml version="1.0"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:module:Magento_Backend:etc/menu.xsd">
    <menu>
        <add id="{{p . "id"}}"
             title="{{xesc (p . "title")}}"
             translate="title"
             module="{{.Vendor}}_{{.Module}}"
             sortOrder="{{pdefault . "sort_order" "10"}}"
             {{if p . "parent"}}parent="{{p . "parent"}}"
             {{end}}action="{{p . "action"}}"
             resource="{{pdefault . "resource" "Magento_Backend::admin"}}"/>
    </menu>
</config>
`

// --- setup: data patch ---

const tDataPatch = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Setup\Patch\Data;

use Magento\Framework\Setup\ModuleDataSetupInterface;
use Magento\Framework\Setup\Patch\DataPatchInterface;

class {{.Name}} implements DataPatchInterface
{
    public function __construct(
        private readonly ModuleDataSetupInterface $moduleDataSetup
    ) {
    }

    public function apply(): void
    {
        $this->moduleDataSetup->getConnection()->startSetup();
        // TODO: apply the data change
        $this->moduleDataSetup->getConnection()->endSetup();
    }

    public static function getDependencies(): array
    {
        return [{{if p . "dependencies"}}\{{p . "dependencies"}}::class{{end}}];
    }

    public function getAliases(): array
    {
        return [];
    }
}
`

// --- setup: schema patch ---

const tSchemaPatch = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Setup\Patch\Schema;

use Magento\Framework\Setup\SchemaSetupInterface;
use Magento\Framework\Setup\Patch\SchemaPatchInterface;

class {{.Name}} implements SchemaPatchInterface
{
    public function __construct(
        private readonly SchemaSetupInterface $schemaSetup
    ) {
    }

    public function apply(): void
    {
        $this->schemaSetup->startSetup();
        // TODO: apply the schema change
        $this->schemaSetup->endSetup();
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

// --- cron group ---

const tCronGroups = `<?xml version="1.0"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:module:Magento_Cron:etc/cron_groups.xsd">
    <group id="{{p . "group"}}">
        <schedule_generate_every>{{pdefault . "schedule_generate_every" "15"}}</schedule_generate_every>
        <schedule_ahead_for>{{pdefault . "schedule_ahead_for" "20"}}</schedule_ahead_for>
        <schedule_lifetime>{{pdefault . "schedule_lifetime" "15"}}</schedule_lifetime>
        <history_cleanup_every>{{pdefault . "history_cleanup_every" "10"}}</history_cleanup_every>
        <history_success_lifetime>{{pdefault . "history_success_lifetime" "60"}}</history_success_lifetime>
        <history_failure_lifetime>{{pdefault . "history_failure_lifetime" "600"}}</history_failure_lifetime>
        <use_separate_process>{{if pbool . "use_separate_process"}}1{{else}}0{{end}}</use_separate_process>
    </group>
</config>
`

// --- cache type ---

const tCacheXML = `<?xml version="1.0"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:framework:Cache/etc/cache.xsd">
    <type name="{{p . "id"}}" translate="label,description" instance="{{.Vendor}}\{{.Module}}\Model\Cache\Type\{{.Name}}">
        <label>{{xesc (pdefault . "id" .Name)}}</label>
        <description>{{xesc .Name}} cache</description>
    </type>
</config>
`

const tCacheType = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Model\Cache\Type;

use Magento\Framework\App\Cache\Type\FrontendPool;
use Magento\Framework\Cache\Frontend\Decorator\TagScope;

class {{.Name}} extends TagScope
{
    public const TYPE_IDENTIFIER = '{{p . "id"}}';
    public const CACHE_TAG = '{{pdefault . "tag" (lower .Name)}}';

    public function __construct(FrontendPool $cacheFrontendPool)
    {
        parent::__construct($cacheFrontendPool->get(self::TYPE_IDENTIFIER), self::CACHE_TAG);
    }
}
`

// --- indexer ---

const tIndexerXML = `<?xml version="1.0"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:framework:Indexer/etc/indexer.xsd">
    <indexer id="{{p . "id"}}" view_id="{{pdefault . "view_id" (p . "id")}}" class="{{.Vendor}}\{{.Module}}\Model\Indexer\{{.Name}}">
        <title translate="true">{{xesc (pdefault . "title" .Name)}}</title>
        <description translate="true">{{xesc (pdefault . "title" .Name)}}</description>
    </indexer>
</config>
`

const tMviewXML = `<?xml version="1.0"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:framework:Mview/etc/mview.xsd">
    <view id="{{pdefault . "view_id" (p . "id")}}" class="{{.Vendor}}\{{.Module}}\Model\Indexer\{{.Name}}" group="indexer">
        <subscriptions>
            <table name="{{lower .Vendor}}_{{lower .Module}}_{{lower .Name}}" entity_column="entity_id"/>
        </subscriptions>
    </view>
</config>
`

const tIndexerModel = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Model\Indexer;

use Magento\Framework\Indexer\ActionInterface;
use Magento\Framework\Mview\ActionInterface as MviewActionInterface;

class {{.Name}} implements ActionInterface, MviewActionInterface
{
    public function executeFull(): void
    {
        // TODO: reindex everything
    }

    public function executeList(array $ids): void
    {
        $this->execute($ids);
    }

    public function executeRow($id): void
    {
        $this->execute([$id]);
    }

    public function execute($ids): void
    {
        // TODO: reindex the given ids
    }
}
`

// --- message queue consumer ---

const tCommunicationXML = `<?xml version="1.0"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:framework-message-queue:etc/communication.xsd">
    <topic name="{{p . "topic"}}" request="string">
        <handler name="{{p . "consumer"}}" type="{{.Vendor}}\{{.Module}}\Model\{{.Name}}" method="process"/>
    </topic>
</config>
`

const tQueueXML = `<?xml version="1.0"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:framework-message-queue:etc/queue.xsd">
    <broker topic="{{p . "topic"}}" exchange="magento-{{pdefault . "connection" "db"}}" type="{{pdefault . "connection" "db"}}">
        <queue name="{{p . "queue"}}" consumer="{{p . "consumer"}}" handler="{{.Vendor}}\{{.Module}}\Model\{{.Name}}::process"/>
    </broker>
</config>
`

const tQueueConsumerXML = `<?xml version="1.0"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:framework-message-queue:etc/consumer.xsd">
    <consumer name="{{p . "consumer"}}" queue="{{p . "queue"}}" connection="{{pdefault . "connection" "db"}}" handler="{{.Vendor}}\{{.Module}}\Model\{{.Name}}::process"/>
</config>
`

const tQueueTopologyXML = `<?xml version="1.0"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:framework-message-queue:etc/topology.xsd">
    <exchange name="magento-{{pdefault . "connection" "db"}}" type="topic" connection="{{pdefault . "connection" "db"}}">
        <binding id="{{lower .Vendor}}{{lower .Module}}Binding" topic="{{p . "topic"}}" destinationType="queue" destination="{{p . "queue"}}"/>
    </exchange>
</config>
`

const tQueuePublisherXML = `<?xml version="1.0"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="urn:magento:framework-message-queue:etc/publisher.xsd">
    <publisher topic="{{p . "topic"}}">
        <connection name="{{pdefault . "connection" "db"}}" exchange="magento-{{pdefault . "connection" "db"}}"/>
    </publisher>
</config>
`

const tQueueHandler = `<?php
declare(strict_types=1);

namespace {{.Vendor}}\{{.Module}}\Model;

class {{.Name}}
{
    public function process(string $message): void
    {
        // TODO: handle the queued message
    }
}
`
