Feature: Scaffolding a stack produces a real, working project
  keel new must resolve every stack through the real binary, and — when a frontend
  is chosen — boot a site whose landing page proves the environment works. API-only
  stacks prove themselves with a curl to /health.

  # Offline + fast: drives the real `keel` binary end-to-end (no network).
  Scenario Outline: <name> resolves through the keel binary
    Given keel is built
    When I run keel new for "<framework>" with kit "<kit>" on env "<env>"
    Then the build plan includes "<expect>"

    Examples:
      | name                 | framework | kit                                   | env          | expect      |
      | express              | express   |                                       | express-local   | npm install |
      | fastify              | fastify   |                                       | fastify-local   | npm install |
      | flask                | flask     |                                       | flask-local  | uv sync     |
      | fastapi              | fastapi   |                                       | fastapi-local| uv          |
      | django               | django    |                                       | django-local | migrate     |
      | next + tailwind      | nextjs    | nextjs-tailwind                       | nextjs-local   | tailwindcss |
      | next + daisyui       | nextjs    | nextjs-tailwind,nextjs-daisyui        | nextjs-local   | daisyui     |
      | next + shadcn        | nextjs    | nextjs-tailwind,nextjs-shadcn         | nextjs-local   | shadcn      |
      | next + mui           | nextjs    | nextjs-mui                            | nextjs-local   | @mui        |
      | nuxt + vuetify       | nuxt      | nuxt-vuetify                          | nuxt-local   | vuetify     |
      | astro + daisyui      | astro     | astro-tailwind,astro-daisyui          | astro-local   | daisyui     |
      | sveltekit + daisyui  | sveltekit | sveltekit-tailwind,sveltekit-daisyui  | sveltekit-local   | daisyui     |

  # Visible: scaffold for real, boot it, load the homepage in a browser, and save a
  # Records the session: a video, a screenshot and a trace, and fails if the page
  # reported an error. A still screenshot alone proves only that something
  # rendered, and a stack trace screenshots perfectly well.
  # Run with `make e2e-visible` (needs node + a browser, see `make e2e-browser`).
  # Skipped by default so `make bdd` stays offline.
  @visible
  Scenario Outline: <name> boots and its landing page proves it works
    Given keel is built
    When I scaffold and boot "<framework>" with kit "<kit>" on env "<env>"
    Then the homepage contains "app is running"
    And the session is recorded as "<name>"

    Examples:
      | name             | framework | kit                                  | env          |
      | next-tailwind    | nextjs    | nextjs-tailwind                      | nextjs-local |
      | next-daisyui     | nextjs    | nextjs-tailwind,nextjs-daisyui       | nextjs-local |
      | astro-tailwind   | astro     | astro-tailwind                       | astro-local   |
      | sveltekit-daisy  | sveltekit | sveltekit-tailwind,sveltekit-daisyui | sveltekit-local   |

  # API-only stacks have no page — their proof is a healthy JSON response.
  @visible @api
  Scenario Outline: <name> answers a health check
    Given keel is built
    When I scaffold and boot "<framework>" with kit "" on env "<env>"
    Then GET "/health" returns "healthy"

    Examples:
      | name    | framework | env           |
      | express | express   | express-local    |
      | fastapi | fastapi   | fastapi-local |

  # Full: the container environments. These bring up DDEV/Sail/compose stacks for
  # real and tear them down (volumes included), so they need Docker and time.
  # Run with `make e2e-full`. Magento and WooCommerce lead the table: they are the
  # ecommerce frameworks in an ecommerce-positioned tool and had no end-to-end
  # coverage at all.
  #
  # The Magento rows build Mage-OS, not Adobe's distribution. Adobe's needs
  # Marketplace keys, which are bought per account and cannot be put in a public
  # repository - so a table naming it could never pass here or in CI, and a suite
  # that cannot run is not coverage. Mage-OS is the same codebase from a
  # repository that needs no keys, and every recipe these rows touch (both envs,
  # the shared install sequence, Hyvä) lists both distributions in appliesTo, so
  # this exercises the same files. Adobe's remains a one-line swap for anyone
  # holding keys: change the framework column to `magento`.
  @full
  Scenario Outline: <name> comes up in a container environment and is torn down
    Given keel is built
    When I scaffold "<framework>" with kit "<kit>" on env "<env>"
    Then the environment reports itself running
    # Containers being up is not the same as the project working. Without this
    # line, Django and four Node skeletons shipped answering 404 at the very URL
    # keel prints, and Reflex answered 403 to everything, with the suite green.
    And it serves a page over HTTP
    And tearing it down leaves no containers or volumes behind

    Examples:
      | name                 | framework          | kit             | env                      |
      | magento ddev         | magento-mageos     |                 | magento-ddev             |
      | magento docker       | magento-mageos     |                 | magento-docker           |
      | magento hyva         | magento-mageos     | magento-hyva    | magento-ddev             |
      | woocommerce ddev     | woocommerce        |                 | woocommerce-ddev         |
      | woocommerce docker   | woocommerce        |                 | woocommerce-docker       |
      | woocommerce bedrock  | woocommerce-bedrock|                 | woocommerce-bedrock-ddev |
      | laravel ddev         | laravel            |                 | ddev                     |
      | laravel sail         | laravel            |                 | sail                     |
      | laravel docker       | laravel            |                 | laravel-docker           |
      | laravel livewire     | laravel            | laravel-livewire| ddev                     |
      | django ddev          | django             |                 | django-ddev              |
      | django docker        | django             |                 | django-docker            |
      | symfony ddev         | symfony            |                 | symfony-ddev             |
      | fastapi ddev         | fastapi            |                 | fastapi-ddev             |
      | nextjs docker        | nextjs             |                 | nextjs-docker            |
      | node docker          | express            |                 | express-docker              |
