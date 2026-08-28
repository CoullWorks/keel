# Contributing to keel

keel is designed to be **enhanceable by anyone**: most contributions are a new
*recipe*, which is a small YAML file — no Go required.

## Add a recipe

A recipe is one node in the decision tree (a framework, add-on, env, database,
service, config overlay, extra, or generator). The built-in catalogue is the
top-level `recipes/` directory, laid out by what a thing IS rather than by which
framework happens to use it:

```
recipes/
  infra/                     shared infrastructure - one of each, for any framework
    db/                      postgres, mysql, mariadb, mongo, supabase
    search/                  opensearch, elasticsearch, meilisearch, solr
    data/                    redis, memcached, rabbitmq, minio, mongodb
    web/                     nginx, apache, varnish
  frameworks/<language>/<framework>/
    framework.yaml           the framework itself
    addons/                  packages and tooling for this framework
    envs/                    how this framework runs (ddev, sail, compose, local)
    extras/                  AI config, CI, and other project-level additions
    frontends/               UI kits and front-end pairings
    infra/                   THIS framework's configuration of shared infrastructure
```

The split is the point. `infra/web/varnish.yaml` says how Varnish comes up, and
applies to anything; `frameworks/php/magento/infra/varnish.yaml` says how Magento
is told to use it. Neither knows about the other. That is why there is one NGINX
instead of six identical copies, and why a Django project can put Varnish in
front of itself without anyone adding a Django-shaped Varnish recipe.

Add yours in the matching directory, or drop it in `~/.config/keel/recipes/` to
use it locally with no recompile.

Infrastructure is shared: there is one Postgres and one NGINX, each describing
how it comes up per environment, not one copy per framework. Your framework's
own configuration of them goes in its `infra/` directory as a `kind: config`
recipe — see `docs/SHARED-RECIPES.md`.

```yaml
# recipes/frameworks/php/laravel/addons/horizon.yaml
id: horizon                 # unique id (a later recipe with the same id overrides it)
kind: addon                 # framework | starter | addon | env | db | service | extra | generator
label: Horizon (queues dashboard)
appliesTo: [laravel]        # which framework(s); omit or use "*" for any
requires: [redis]           # capabilities that must be present (from another recipe's `provides`)
conflicts: []               # capabilities that must be absent
provides: [queue-dashboard] # capabilities this contributes
default: false              # pre-ticked in the picker / seeded into the profile
install:                    # ordered shell steps — shell out to official installers
  - "{{env}} composer require laravel/horizon"
  - "{{env}} exec php artisan horizon:install"
smoke:                      # how CI proves this recipe's combos still boot
  - "{{env}} exec php artisan horizon:status"
```

Guidelines:
- **Orchestrate, don't reimplement.** Call `laravel new` / `composer` / `ddev` /
  `shopify` / `wp`. keel owns the *join* (env + `.env`/config wiring via `patch`),
  not framework templates.
- **Model compatibility with capabilities.** Use `provides` / `requires` /
  `conflicts` so the resolver can validate combos and the TUI can grey out
  invalid ones. `{{env}}` is templated to the chosen env driver.
- **Every recipe ships a `smoke` step.** That's what keeps the catalogue from
  rotting — CI generates recipe combos, boots them, and runs the smoke steps.

Kinds resolve in this order: `framework → starter → addon → env → db → service →
extra → generator`. See [`docs/ROUTES.md`](docs/ROUTES.md) for the full catalogue.

## Code changes

```sh
make test     # unit (TDD) + BDD scenarios — must be green
make lint     # go vet
```

- Unit tests live beside the code (`*_test.go`).
- Behaviour lives in [`features/`](features/) as Gherkin, run via godog. If you
  change a route or a rule, add or update a scenario.
- Keep packages small and single-purpose (`recipe`, `resolver`, `profile`,
  `catalog`, `engine`, `driver`, `tui`, `cli`).

## Ground rules

- MIT-licensed; by contributing you agree your work is too.
- Be decent — see [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
- Small PRs, one concern each. A new recipe + its smoke test is the ideal first PR.
