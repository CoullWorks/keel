package cli

import (
	"fmt"
	"strings"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/engine"
	"github.com/spf13/cobra"
)

// nodeFrameworks are the framework ids whose db tasks run through a JavaScript
// ORM (Prisma or Drizzle) rather than a framework CLI. The ORM is an add-on, so
// which one a project has is read from the manifest's recipe list, not the
// framework id.
var nodeFrameworks = map[string]bool{
	"express": true, "fastify": true, "hono": true, "nestjs": true,
	"nuxt": true, "astro": true, "sveltekit": true, "adonisjs": true,
	"nextjs": true,
}

func dbCmd() *cobra.Command {
	var dryRun bool
	c := &cobra.Command{
		Use: "db <migrate|seed|reset|status>",
		Long: "Runs the database task the way this project's stack does it, through its\n" +
			"environment. One command whatever the framework: keel reads .keel/manifest.yaml\n" +
			"to know whether that means artisan, manage.py or something else.\n",
		Example: "  keel db migrate\n" +
			"  keel db status\n",
		Short:     "Database tasks for the project in the current directory",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"migrate", "seed", "reset", "status"},
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			m, err := engine.ReadManifest(".")
			if err != nil {
				return manifestErr(err)
			}
			reg, err := catalog.Registry()
			if err != nil {
				return err
			}
			env, _ := reg.Get(m.Env)
			full := dbCommand(m.Framework, env.Commands, m.Recipes, args[0])
			if full == "" {
				return fmt.Errorf("no %q task for %s", args[0], m.Framework)
			}
			fmt.Fprintln(out, "→", full)
			if dryRun {
				return nil
			}
			return engine.ExecRunner{Out: out}.Run(cmd.Context(), ".", full)
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "print the command without running it")
	return c
}

// dbCommand maps a db action to the stack's command, using the env's command
// vocabulary (so it works under DDEV, Sail, Docker or Local). recipes is the
// manifest's chosen recipe ids, needed only to tell a Node project's ORM apart
// (Prisma vs Drizzle are add-ons, not part of the framework id).
func dbCommand(framework string, c map[string]string, recipes []string, action string) string {
	artisan, manage, magento := c["artisan"], c["manage"], c["magento"]
	// console is Symfony's bin/console; exec fronts npx (Node) and uv (FastAPI).
	console, exec := c["console"], c["exec"]
	switch framework {
	case "laravel":
		switch action {
		case "migrate":
			return artisan + " migrate"
		case "seed":
			return artisan + " db:seed"
		case "reset":
			return artisan + " migrate:fresh --seed"
		case "status":
			return artisan + " migrate:status"
		}
	case "django":
		switch action {
		case "migrate":
			return manage + " migrate"
		case "seed":
			// The django-seed add-on ships a `seed` management command backed by
			// factory_boy + Faker; loaddata only replays a static fixture file.
			return manage + " seed"
		case "reset":
			return manage + " flush --no-input"
		case "status":
			return manage + " showmigrations"
		}
	case "magento":
		switch action {
		case "migrate", "reset":
			return magento + " setup:upgrade"
		case "status":
			return magento + " setup:db:status"
		}
	case "symfony":
		// Doctrine Migrations (doctrine:migrations:migrate) and Fixtures
		// (doctrine:fixtures:load), the standard Symfony database toolchain.
		switch action {
		case "migrate":
			return console + " doctrine:migrations:migrate --no-interaction"
		case "seed":
			return console + " doctrine:fixtures:load --no-interaction"
		case "reset":
			// Drop, recreate, migrate, then load fixtures: a clean rebuild.
			return console + " doctrine:database:drop --force --if-exists && " +
				console + " doctrine:database:create && " +
				console + " doctrine:migrations:migrate --no-interaction && " +
				console + " doctrine:fixtures:load --no-interaction"
		case "status":
			return console + " doctrine:migrations:status"
		}
	case "fastapi":
		// Alembic is FastAPI's usual migration tool. There is no seed here: the
		// scaffold ships no models, so a realistic seeder would have nothing to
		// populate (see recipes note; tracked as a follow-up).
		switch action {
		case "migrate":
			return join(exec, "alembic upgrade head")
		case "reset":
			return join(exec, "alembic downgrade base") + " && " + join(exec, "alembic upgrade head")
		case "status":
			return join(exec, "alembic current")
		}
	}
	if nodeFrameworks[framework] {
		return nodeDBCommand(exec, recipes, action)
	}
	return ""
}

// nodeDBCommand maps a db action for a Node project, dispatching on whichever
// ORM add-on the manifest recorded. Prisma and Drizzle spell every task
// differently, so there is no single Node command.
func nodeDBCommand(exec string, recipes []string, action string) string {
	switch {
	case has(recipes, "node-prisma"):
		switch action {
		case "migrate":
			return join(exec, "npx prisma migrate deploy")
		case "seed":
			return join(exec, "npx prisma db seed")
		case "reset":
			// --force skips the interactive confirm; keel builds without a TTY.
			// migrate reset also re-runs the seed script, so this reseeds too.
			return join(exec, "npx prisma migrate reset --force")
		case "status":
			return join(exec, "npx prisma migrate status")
		}
	case has(recipes, "node-drizzle"):
		// The drizzle add-on wires these npm scripts (db:migrate/db:generate).
		switch action {
		case "migrate":
			return join(exec, "npm run db:migrate")
		case "seed":
			// The node-drizzle-seed add-on adds this script; without it Drizzle
			// has no seed step of its own.
			return join(exec, "npm run db:seed")
		case "status":
			// Drizzle has no status command; generate is a no-op when the schema
			// already matches, so it doubles as a drift check.
			return join(exec, "npm run db:generate")
		}
	}
	return ""
}

// join prefixes a command with the env's exec wrapper, collapsing the leading
// space a native env (empty exec) would otherwise produce.
func join(exec, cmd string) string {
	return strings.TrimSpace(exec + " " + cmd)
}

// has reports whether id is in the manifest's recipe list.
func has(recipes []string, id string) bool {
	for _, r := range recipes {
		if r == id {
			return true
		}
	}
	return false
}
