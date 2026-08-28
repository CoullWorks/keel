package cli

import "testing"

func TestDbCommand(t *testing.T) {
	c := map[string]string{
		"artisan": "ddev exec php artisan",
		"manage":  "ddev exec python manage.py",
		"magento": "ddev magento",
		"console": "ddev exec php bin/console",
		"exec":    "docker compose exec -T app",
	}
	// native has an empty exec (no container to shell into), so join must not
	// leave a leading space on the Node/FastAPI commands.
	native := map[string]string{"exec": "", "console": "php bin/console"}
	prisma := []string{"node-prisma"}
	drizzle := []string{"node-drizzle"}

	cases := []struct {
		name    string
		fw      string
		env     map[string]string
		recipes []string
		action  string
		want    string
	}{
		// Laravel: every action.
		{"laravel migrate", "laravel", c, nil, "migrate", "ddev exec php artisan migrate"},
		{"laravel seed", "laravel", c, nil, "seed", "ddev exec php artisan db:seed"},
		{"laravel reset", "laravel", c, nil, "reset", "ddev exec php artisan migrate:fresh --seed"},
		{"laravel status", "laravel", c, nil, "status", "ddev exec php artisan migrate:status"},
		// Django: every action (seed now runs the seed management command).
		{"django migrate", "django", c, nil, "migrate", "ddev exec python manage.py migrate"},
		{"django seed", "django", c, nil, "seed", "ddev exec python manage.py seed"},
		{"django reset", "django", c, nil, "reset", "ddev exec python manage.py flush --no-input"},
		{"django status", "django", c, nil, "status", "ddev exec python manage.py showmigrations"},
		// Magento: every action (migrate/reset alias to setup:upgrade).
		{"magento migrate", "magento", c, nil, "migrate", "ddev magento setup:upgrade"},
		{"magento reset", "magento", c, nil, "reset", "ddev magento setup:upgrade"},
		{"magento status", "magento", c, nil, "status", "ddev magento setup:db:status"},
		{"magento seed", "magento", c, nil, "seed", ""}, // magento has no seed
		// Symfony: Doctrine Migrations + Fixtures.
		{"symfony migrate", "symfony", c, nil, "migrate", "ddev exec php bin/console doctrine:migrations:migrate --no-interaction"},
		{"symfony seed", "symfony", c, nil, "seed", "ddev exec php bin/console doctrine:fixtures:load --no-interaction"},
		{"symfony reset", "symfony", c, nil, "reset", "ddev exec php bin/console doctrine:database:drop --force --if-exists && ddev exec php bin/console doctrine:database:create && ddev exec php bin/console doctrine:migrations:migrate --no-interaction && ddev exec php bin/console doctrine:fixtures:load --no-interaction"},
		{"symfony status", "symfony", c, nil, "status", "ddev exec php bin/console doctrine:migrations:status"},
		// FastAPI: Alembic. No seed (scaffold ships no models).
		{"fastapi migrate", "fastapi", c, nil, "migrate", "docker compose exec -T app alembic upgrade head"},
		{"fastapi reset", "fastapi", c, nil, "reset", "docker compose exec -T app alembic downgrade base && docker compose exec -T app alembic upgrade head"},
		{"fastapi status", "fastapi", c, nil, "status", "docker compose exec -T app alembic current"},
		{"fastapi seed", "fastapi", c, nil, "seed", ""}, // deferred: no models to seed
		{"fastapi native migrate", "fastapi", native, nil, "migrate", "alembic upgrade head"},
		// Node + Prisma.
		{"prisma migrate", "nextjs", c, prisma, "migrate", "docker compose exec -T app npx prisma migrate deploy"},
		{"prisma seed", "nextjs", c, prisma, "seed", "docker compose exec -T app npx prisma db seed"},
		{"prisma reset", "nextjs", c, prisma, "reset", "docker compose exec -T app npx prisma migrate reset --force"},
		{"prisma status", "express", c, prisma, "status", "docker compose exec -T app npx prisma migrate status"},
		{"prisma native migrate", "nextjs", native, prisma, "migrate", "npx prisma migrate deploy"},
		// Node + Drizzle.
		{"drizzle migrate", "express", c, drizzle, "migrate", "docker compose exec -T app npm run db:migrate"},
		{"drizzle seed", "express", c, drizzle, "seed", "docker compose exec -T app npm run db:seed"},
		{"drizzle status", "fastify", c, drizzle, "status", "docker compose exec -T app npm run db:generate"},
		{"drizzle reset", "fastify", c, drizzle, "reset", ""}, // drizzle has no reset
		// Node without any ORM add-on: no db tasks.
		{"node no orm", "nextjs", c, nil, "migrate", ""},
		// Unknowns.
		{"laravel bogus action", "laravel", c, nil, "bogus", ""},
		{"unknown framework", "unknown", c, nil, "migrate", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dbCommand(tc.fw, tc.env, tc.recipes, tc.action); got != tc.want {
				t.Errorf("dbCommand(%q,%q) = %q, want %q", tc.fw, tc.action, got, tc.want)
			}
		})
	}
}
