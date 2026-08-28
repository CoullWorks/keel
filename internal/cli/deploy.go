package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/coullworks/keel/internal/engine"
	"github.com/spf13/cobra"
)

// runtime is a framework's production shape: the container image, the port it
// listens on, and the command that runs it. Kept in code (not recipes) because
// deploy artifacts are generated joins, not part of the project graph.
type runtime struct {
	Port       string
	Dockerfile string
}

func runtimeFor(framework string) (runtime, bool) {
	switch framework {
	case "laravel":
		return runtime{Port: "8080", Dockerfile: `# keel-generated production image (serversideup: php-fpm + nginx, prod-tuned)
FROM serversideup/php:8.3-fpm-nginx
WORKDIR /var/www/html
COPY . .
RUN composer install --no-dev --optimize-autoloader --no-interaction \
 && php artisan config:cache && php artisan route:cache
# serversideup serves the app on :8080 and exposes /up for health checks.
`}, true
	case "django":
		return runtime{Port: "8000", Dockerfile: `# keel-generated production image
FROM python:3.12-slim
RUN pip install --no-cache-dir uv
WORKDIR /app
COPY . .
RUN uv sync --no-dev && KEEL_DB=postgres uv run python manage.py collectstatic --noinput
ENV KEEL_DB=postgres
CMD ["sh", "-c", "uv run python manage.py migrate --noinput && uv run gunicorn config.wsgi -b 0.0.0.0:8000"]
`}, true
	case "fastapi":
		return runtime{Port: "8000", Dockerfile: `# keel-generated production image
FROM python:3.12-slim
RUN pip install --no-cache-dir uv
WORKDIR /app
COPY . .
RUN uv sync --no-dev
CMD ["uv", "run", "uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"]
`}, true
	case "nextjs":
		return runtime{Port: "3000", Dockerfile: `# keel-generated production image
FROM node:22-slim
WORKDIR /app
COPY . .
RUN npm install && npm run build
EXPOSE 3000
CMD ["npm", "start"]
`}, true
	}
	return runtime{}, false
}

var deployTargets = []string{"compose", "fly", "vps", "render", "railway", "vercel"}

// deployTargetDesc is the one-line description of each target, so `keel deploy
// --json` (and the studio's Deploy tab, which reads it) can describe a target
// without the frontend hardcoding a list that would drift from deployTargets.
var deployTargetDesc = map[string]string{
	"compose": "Docker Compose on a single host (app + Postgres + Caddy TLS)",
	"fly":     "Fly.io machines with a generated fly.toml",
	"vps":     "A VPS over SSH: rsync + compose (adds deploy.sh)",
	"render":  "Render blueprint (render.yaml) from the Dockerfile + a free Postgres",
	"railway": "Railway build from the Dockerfile (railway.json)",
	"vercel":  "Vercel build config (vercel.json), best for Next.js / Node / static",
}

// deployFiles returns the artifacts to write for a framework + target. Pure so
// it can be table-tested without touching the filesystem.
func deployFiles(framework, target string) (map[string]string, error) {
	// Vercel is build-config, not a container — handled before the runtime gate.
	if target == "vercel" {
		return map[string]string{
			"vercel.json": vercelJSON(framework),
			"DEPLOY.md":   vercelDoc(framework),
		}, nil
	}

	rt, ok := runtimeFor(framework)
	if !ok {
		return map[string]string{
			"DEPLOY.md": "# Deploy\n\nkeel does not generate a production deploy for **" + framework +
				"** yet (its production topology is involved). Deploy it with your platform's" +
				" official guide, or open an issue for a recipe.\n",
		}, nil
	}

	files := map[string]string{"Dockerfile": rt.Dockerfile}
	switch target {
	case "compose", "vps":
		files["docker-compose.prod.yml"] = composeProd(rt)
		files["Caddyfile"] = caddyfile(rt)
		files[".env.prod.example"] = "DOMAIN=example.com\n"
		if target == "vps" {
			files["deploy.sh"] = deployScript()
		}
		files["DEPLOY.md"] = deployDoc(framework, target, rt)
	case "fly":
		files["fly.toml"] = flyToml(rt)
		files["DEPLOY.md"] = deployDoc(framework, target, rt)
	case "render":
		files["render.yaml"] = renderYAML(rt)
		files["DEPLOY.md"] = deployDoc(framework, target, rt)
	case "railway":
		files["railway.json"] = railwayJSON()
		files["DEPLOY.md"] = deployDoc(framework, target, rt)
	default:
		return nil, fmt.Errorf("unknown target %q (want: %s)", target, strings.Join(deployTargets, ", "))
	}
	return files, nil
}

func composeProd(rt runtime) string {
	return `# keel-generated. Bring up with: docker compose -f docker-compose.prod.yml up -d --build
services:
  app:
    build: .
    restart: unless-stopped
    env_file: [.env]
    environment:
      DATABASE_URL: postgresql://app:${DB_PASSWORD:-secret}@db:5432/app
      KEEL_DB: postgres
    depends_on: [db]
  db:
    image: postgres:16
    restart: unless-stopped
    environment:
      POSTGRES_DB: app
      POSTGRES_USER: app
      POSTGRES_PASSWORD: ${DB_PASSWORD:-secret}
    volumes: [db_data:/var/lib/postgresql/data]
  caddy:
    image: caddy:2
    restart: unless-stopped
    ports: ["80:80", "443:443"]
    environment:
      DOMAIN: ${DOMAIN:-localhost}
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile
      - caddy_data:/data
    depends_on: [app]
volumes:
  db_data:
  caddy_data:
`
}

func caddyfile(rt runtime) string {
	return `# keel-generated reverse proxy with automatic HTTPS.
{$DOMAIN} {
	reverse_proxy app:` + rt.Port + `
}
`
}

func flyToml(rt runtime) string {
	return `# keel-generated. Deploy: fly launch --no-deploy (once), then fly deploy.
app = "app"
primary_region = "lhr"

[build]

[http_service]
  internal_port = ` + rt.Port + `
  force_https = true
  auto_stop_machines = "stop"
  auto_start_machines = true
  min_machines_running = 0

[[vm]]
  memory = "512mb"
  cpu_kind = "shared"
  cpus = 1
`
}

func vercelJSON(framework string) string {
	if framework == "nextjs" {
		// Vercel auto-detects Next.js; a minimal file documents the framework.
		return "{\n  \"$schema\": \"https://openapi.vercel.sh/vercel.json\",\n  \"framework\": \"nextjs\"\n}\n"
	}
	return "{\n  \"$schema\": \"https://openapi.vercel.sh/vercel.json\"\n}\n"
}

func vercelDoc(framework string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Deploy: %s (Vercel)\n\n", framework)
	if framework != "nextjs" {
		b.WriteString("> Vercel is best suited to Next.js / Node / static frontends. For **" +
			framework + "** a container target (compose, fly, render, railway) is usually a better fit.\n\n")
	}
	b.WriteString("## Vercel\n\n" +
		"1. `npm i -g vercel` (or use the dashboard).\n" +
		"2. `vercel link` in this project.\n" +
		"3. Set env vars: `vercel env add DATABASE_URL` (and any others).\n" +
		"4. `vercel --prod` to deploy. Pushes to the linked git branch auto-deploy.\n\n" +
		"> Generated by keel. Secrets never leave your machine.\n")
	return b.String()
}

func renderYAML(rt runtime) string {
	return `# keel-generated Render blueprint. Create from the dashboard: New > Blueprint.
services:
  - type: web
    name: app
    runtime: docker
    dockerfilePath: ./Dockerfile
    healthCheckPath: /
    envVars:
      - key: DATABASE_URL
        fromDatabase:
          name: app-db
          property: connectionString
databases:
  - name: app-db
    plan: free
    postgresMajorVersion: "16"
`
}

func railwayJSON() string {
	return `{
  "$schema": "https://railway.app/railway.schema.json",
  "build": { "builder": "DOCKERFILE", "dockerfilePath": "Dockerfile" },
  "deploy": { "restartPolicyType": "ON_FAILURE", "restartPolicyMaxRetries": 3 }
}
`
}

func deployScript() string {
	return `#!/usr/bin/env bash
# keel-generated: rsync the project to a VPS and (re)start the prod stack.
set -euo pipefail
: "${DEPLOY_HOST:?set DEPLOY_HOST=user@your-server}"
DEST="${DEPLOY_PATH:-/srv/app}"

rsync -az --delete \
  --exclude '.git' --exclude 'node_modules' --exclude '.venv' \
  --exclude 'vendor' --exclude '.next' \
  ./ "$DEPLOY_HOST:$DEST/"

ssh "$DEPLOY_HOST" "cd $DEST && docker compose -f docker-compose.prod.yml up -d --build"
echo "deployed to $DEPLOY_HOST:$DEST"
`
}

func deployDoc(framework, target string, rt runtime) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Deploy: %s (%s)\n\n", framework, target)
	fmt.Fprintf(&b, "keel generated a production image listening on port **%s**.\n\n", rt.Port)
	switch target {
	case "compose":
		b.WriteString("## Docker Compose (single host)\n\n" +
			"1. `cp .env.prod.example .env` and set `DOMAIN` (+ `DB_PASSWORD`).\n" +
			"2. Point your domain's DNS at this host.\n" +
			"3. `docker compose -f docker-compose.prod.yml up -d --build`\n\n" +
			"Caddy terminates TLS automatically and reverse-proxies to the app.\n")
	case "vps":
		b.WriteString("## VPS (rsync + compose over SSH)\n\n" +
			"1. Install Docker on the server; point your domain at it.\n" +
			"2. `cp .env.prod.example .env` and set `DOMAIN` (+ `DB_PASSWORD`).\n" +
			"3. `DEPLOY_HOST=user@your-server ./deploy.sh`\n\n" +
			"`deploy.sh` rsyncs the project and runs the compose stack (app + Postgres + Caddy).\n")
	case "fly":
		b.WriteString("## Fly.io\n\n" +
			"1. `fly launch --no-deploy` (accept the generated `fly.toml`).\n" +
			"2. Add Postgres: `fly postgres create` then `fly postgres attach <db>`.\n" +
			"3. Set secrets: `fly secrets set $(grep -v '^#' .env | xargs)`.\n" +
			"4. `fly deploy`.\n")
	case "render":
		b.WriteString("## Render\n\n" +
			"1. Push this repo to GitHub.\n" +
			"2. Render dashboard > **New > Blueprint**, pick this repo. It reads `render.yaml`.\n" +
			"3. Render provisions the web service (from the Dockerfile) + a free Postgres and\n" +
			"   injects `DATABASE_URL`. Add any other env vars in the dashboard.\n" +
			"4. Every push to the branch redeploys.\n")
	case "railway":
		b.WriteString("## Railway\n\n" +
			"1. `railway init` then `railway up` (builds from the Dockerfile via `railway.json`).\n" +
			"2. Add a database: `railway add` > PostgreSQL. It injects `DATABASE_URL`.\n" +
			"3. Set env vars: `railway variables set KEY=value`.\n" +
			"4. `railway up` to deploy (or connect the repo for auto-deploys).\n")
	}
	b.WriteString("\n> Generated by keel. Review before shipping. Secrets never leave your machine.\n")
	return b.String()
}

func deployCmd() *cobra.Command {
	var dryRun, force, asJSON bool
	c := &cobra.Command{
		Use: "deploy [target]",
		Example: "  keel deploy                                     # list the targets\n" +
			"  keel deploy fly\n" +
			"  keel deploy --json                              # machine-readable target + artifact listing\n" +
			"  keel deploy compose --force                     # write compose artifacts, overwriting existing\n",
		Short:     "Generate production deploy artifacts (compose | fly | vps | render | railway | vercel)",
		Long:      "Writes a production Dockerfile and target-specific config for the project's stack.\nWith no target it lists the targets; naming one writes its artifacts.\nkeel generates artifacts only - it never calls a cloud API or touches your secrets.",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: deployTargets,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if asJSON {
				return deployJSON(out, args)
			}
			// No target lists the targets rather than silently writing files: a
			// bare `keel deploy` is how a person asks "what can I deploy to?", so
			// answering with artifacts appearing on disk is a footgun. Naming a
			// target is the deliberate act that writes.
			if len(args) == 0 {
				return deployList(out)
			}
			target := args[0]
			m, err := engine.ReadManifest(".")
			if err != nil {
				return manifestErr(err)
			}
			files, err := deployFiles(m.Framework, target)
			if err != nil {
				return err
			}
			paths := make([]string, 0, len(files))
			for p := range files {
				paths = append(paths, p)
			}
			sort.Strings(paths)
			for _, p := range paths {
				if dryRun {
					fmt.Fprintf(out, "✎ would write %s\n", p)
					continue
				}
				if _, statErr := os.Stat(p); statErr == nil && !force {
					fmt.Fprintf(out, "• skip %s (exists, use --force to overwrite)\n", p)
					continue
				}
				if err := engine.WriteFile(".", p, files[p]); err != nil {
					return err
				}
				if p == "deploy.sh" {
					_ = os.Chmod(p, 0o755)
				}
				fmt.Fprintf(out, "✎ %s\n", p)
			}
			if !dryRun {
				fmt.Fprintln(out, "→ next: read DEPLOY.md")
			}
			return nil
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "list artifacts without writing them")
	c.Flags().BoolVar(&force, "force", false, "overwrite existing files")
	c.Flags().BoolVar(&asJSON, "json", false, "print the targets (and, in a keel project, each target's artifacts) as JSON")
	return c
}

// deployList prints the human-readable target menu a bare `keel deploy` shows.
// It reads the same deployTargets / deployTargetDesc the --json listing and the
// studio use, so the three can never disagree about what a target is or does.
func deployList(out io.Writer) error {
	fmt.Fprintln(out, "Choose a deploy target - `keel deploy <target>`:")
	for _, t := range deployTargets {
		fmt.Fprintf(out, "  %-9s %s\n", t, deployTargetDesc[t])
	}
	fmt.Fprintln(out, "\n`keel deploy --json` prints this as JSON (what keel studio's Deploy tab reads).")
	return nil
}

// deployTargetJSON is one target in the --json listing: its key, one-line
// description, and — when the command runs inside a keel project so the framework
// is known — the artifact filenames `keel deploy <target>` would write. Keeping
// this in deploy.go makes the command the single source of truth: the studio's
// Deploy tab reads it rather than hardcoding a list that would drift from
// deployTargets / deployFiles.
type deployTargetJSON struct {
	Key       string   `json:"key"`
	Desc      string   `json:"desc"`
	Artifacts []string `json:"artifacts,omitempty"`
}

// deployJSON prints the machine-readable target listing. With no positional
// target it lists every target; the framework (resolved from the project
// manifest when present) fills in each target's artifact filenames so the studio
// can show WHICH files a target generates before the user commits. Outside a keel
// project the artifacts are simply omitted — the target list still stands.
func deployJSON(out io.Writer, args []string) error {
	framework := ""
	if m, err := engine.ReadManifest("."); err == nil {
		framework = m.Framework
	}
	// A single positional narrows the listing to that one target (the studio asks
	// for the selected target's artifacts); no arg lists them all.
	targets := deployTargets
	if len(args) == 1 {
		if _, ok := deployTargetDesc[args[0]]; !ok {
			return fmt.Errorf("unknown target %q (want: %s)", args[0], strings.Join(deployTargets, ", "))
		}
		targets = []string{args[0]}
	}
	list := make([]deployTargetJSON, 0, len(targets))
	for _, t := range targets {
		row := deployTargetJSON{Key: t, Desc: deployTargetDesc[t]}
		if framework != "" {
			if files, err := deployFiles(framework, t); err == nil {
				names := make([]string, 0, len(files))
				for name := range files {
					names = append(names, name)
				}
				sort.Strings(names)
				row.Artifacts = names
			}
		}
		list = append(list, row)
	}
	return json.NewEncoder(out).Encode(map[string]any{
		"framework": framework,
		"targets":   list,
	})
}
