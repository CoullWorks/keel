// Package recipes holds keel's built-in recipe catalogue and embeds it into the
// binary.
//
// It lives at the top level of the repository on purpose. Recipes are the part
// of keel other people are meant to read, copy and extend, and burying them
// under internal/ said the opposite. Everything here is data: adding a stack
// means adding YAML, not Go.
//
// The layout separates what a thing IS from which framework happens to use it:
//
//	infra/db      databases (postgres, mysql, mariadb, mongo, supabase)
//	infra/search  search engines (opensearch, elasticsearch, meilisearch, solr)
//	infra/data    caches, queues and object storage (redis, rabbitmq, minio…)
//	infra/web     web servers and edge caches (nginx, apache, varnish)
//
//	frameworks/<language>/<framework>/
//	  framework.yaml  addons/  envs/  extras/  frontends/  infra/
//
//	shared/       cross-cutting recipes that belong to no framework and no
//	              infrastructure: git, and anything else every project gets
//	              regardless of stack. A recipe here applies to "*".
//
// Infrastructure is shared: one Postgres, one Redis, one NGINX, each describing
// how it comes up per environment family and applying to any framework. A
// framework's own configuration of that infrastructure lives in its infra/
// directory as a `kind: config` recipe, matched by a `when:` block — because a
// Magento MySQL is tuned differently from a Laravel one, and that difference
// belongs with Magento rather than in a second copy of MySQL.
// See docs/SHARED-RECIPES.md.
package recipes

import "embed"

// FS is the built-in catalogue. The embed keeps keel a single static binary.
//
//go:embed all:infra all:frameworks all:shared
var FS embed.FS
