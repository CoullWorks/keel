package catalog

import (
	"path/filepath"
	"regexp"
	"strconv"

	"gopkg.in/yaml.v3"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

// TestNoTokenLeaks: no rendered step, for any framework × env × optional recipe,
// may contain an unsubstituted {{token}} — that means the recipe references a
// token the env does not define and nothing else supplies.
// TestSmokeStepsExist: every framework's default stack must render at least one
// smoke step (the proof-of-boot `keel recipes verify` runs). No leaks either.
func TestSmokeStepsExist(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, fw := range reg.OfKind(recipe.Framework) {
		ids := []string{fw.ID, defaultEnv(reg, fw.ID)}
		if db := defaultDB(reg, fw.ID, defaultEnv(reg, fw.ID)); db != "" {
			ids = append(ids, db)
		}
		plan, err := resolver.Resolve(reg, ids)
		if err != nil {
			continue
		}
		smoke := engine.SmokeSteps(plan, "app")
		if len(smoke) == 0 {
			t.Errorf("%s: no smoke steps — verify can't prove it boots", fw.ID)
		}
		for _, s := range smoke {
			if strings.Contains(s, "{{") {
				t.Errorf("%s: smoke step leaks a token: %q", fw.ID, s)
			}
		}
	}
}

func TestNoTokenLeaks(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	// Optional recipes are added ONE AT A TIME on top of the base stack. Adding
	// only the defaults (as this test once did) left every opt-in add-on
	// unrendered: laravel-breeze shipped a literal "{{stack}}" to artisan and
	// filament curled a literal "{{panelPath}}" for months.
	optionalKinds := []recipe.Kind{recipe.Addon, recipe.Service, recipe.Frontend, recipe.Extra}
	for _, fw := range reg.OfKind(recipe.Framework) {
		for _, env := range reg.ForFramework(fw.ID, recipe.Env) {
			base := []string{fw.ID, env.ID}
			if db := defaultDB(reg, fw.ID, env.ID); db != "" {
				base = append(base, db)
			}
			combos := [][]string{base}
			for _, kind := range optionalKinds {
				for _, opt := range reg.ForFramework(fw.ID, kind) {
					combos = append(combos, append(append([]string{}, base...), opt.ID))
				}
			}
			for _, ids := range combos {
				plan, err := resolver.Resolve(reg, ids)
				if err != nil {
					continue // composition is covered by the other tests
				}
				for _, step := range engine.Steps(plan) {
					if strings.Contains(step, "{{") || strings.Contains(step, "}}") {
						t.Errorf("token leak in %s: %q", strings.Join(ids, "+"), step)
					}
				}
				for _, step := range engine.SmokeSteps(plan, "app") {
					if strings.Contains(step, "{{") || strings.Contains(step, "}}") {
						t.Errorf("token leak in %s smoke step: %q", strings.Join(ids, "+"), step)
					}
				}
			}
		}
	}
}

// defaultEnv returns a framework's default env id (or first).
func defaultEnv(reg *recipe.Registry, fw string) string {
	envs := reg.ForFramework(fw, recipe.Env)
	for _, e := range envs {
		if e.Default {
			return e.ID
		}
	}
	if len(envs) > 0 {
		return envs[0].ID
	}
	return ""
}

// defaultDB mirrors what `keel new` picks: the default database that actually
// works with this framework and env, not one chosen in isolation.
func defaultDB(reg *recipe.Registry, fw, env string) string {
	return resolver.CompatibleDefault(reg, []string{fw, env}, recipe.DB, fw, "")
}

// TestEveryFrameworkEnvComposes is the anti-rot guard: every framework × env must
// resolve into a valid, ordered plan. Catches broken requires/conflicts/appliesTo
// as recipes are added, without needing Docker.
func TestEveryFrameworkEnvComposes(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, fw := range reg.OfKind(recipe.Framework) {
		envs := reg.ForFramework(fw.ID, recipe.Env)
		if len(envs) == 0 {
			t.Errorf("framework %s has no env", fw.ID)
		}
		for _, env := range envs {
			ids := []string{fw.ID, env.ID}
			// The default database is env-aware: Laravel defaults to PostgreSQL,
			// but its Docker stack brings up MySQL.
			if db := defaultDB(reg, fw.ID, env.ID); db != "" {
				ids = append(ids, db)
			}
			if _, err := resolver.Resolve(reg, ids); err != nil {
				t.Errorf("resolve %s / %s: %v", fw.ID, env.ID, err)
			}
		}
	}
}

// TestEveryAddonServiceFrontendComposes: each add-on / service / frontend / extra
// must resolve alongside its framework's default env, and the plan must produce
// steps without error.
func TestEveryAddonServiceFrontendComposes(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, fw := range reg.OfKind(recipe.Framework) {
		// Base plan = framework + default env + default db + all default services,
		// so a recipe's legitimate deps (e.g. horizon->redis) are satisfied.
		base := []string{fw.ID, defaultEnv(reg, fw.ID)}
		if db := defaultDB(reg, fw.ID, defaultEnv(reg, fw.ID)); db != "" {
			base = append(base, db)
		}
		for _, s := range reg.ForFramework(fw.ID, recipe.Service) {
			if s.Default {
				base = append(base, s.ID)
			}
		}
		inBase := func(id string) bool {
			for _, b := range base {
				if b == id {
					return true
				}
			}
			return false
		}
		for _, k := range []recipe.Kind{recipe.Addon, recipe.Service, recipe.Frontend, recipe.Extra} {
			for _, r := range reg.ForFramework(fw.ID, k) {
				if inBase(r.ID) {
					continue
				}
				// The recipe under test wins over a seeded default it cannot
				// coexist with. Apache is the case that matters: NGINX is the
				// default web server, so a base that keeps it would make every
				// Apache row fail on a conflict no user could ever create -
				// they answer one question and get one web server.
				ids := []string{}
				for _, b := range base {
					if bb, ok := reg.Get(b); ok && resolver.ConflictsWithAny(reg, []string{r.ID}, bb) {
						continue
					}
					ids = append(ids, b)
				}
				ids = append(ids, r.ID)
				// Satisfy the recipe's own capability requires from sibling addons/
				// frontends (e.g. DaisyUI requires the Tailwind addon).
				provided := map[string]bool{}
				addProvides := func(id string) {
					if rr, ok := reg.Get(id); ok {
						for _, p := range rr.Provides {
							provided[p] = true
						}
					}
				}
				for _, id := range ids {
					addProvides(id)
				}
				// Databases and services included: an ORM add-on requires "db",
				// and the default environment for the Node and Python frameworks
				// is the native one, which runs no database unless you name it.
				// A user hitting that gets a clear "requires db, which is not
				// selected" and adds --db postgres; the test does the same thing
				// rather than declaring the add-on broken.
				var siblings []recipe.Recipe
				for _, k := range []recipe.Kind{recipe.Addon, recipe.Frontend, recipe.DB, recipe.Service} {
					siblings = append(siblings, reg.ForFramework(fw.ID, k)...)
				}
				for _, req := range r.Requires {
					if provided[req] {
						continue
					}
					for _, cand := range siblings {
						has := false
						for _, p := range cand.Provides {
							if p == req {
								has = true
								break
							}
						}
						if has {
							ids = append(ids, cand.ID)
							addProvides(cand.ID)
							break
						}
					}
				}
				plan, err := resolver.Resolve(reg, ids)
				if err != nil {
					t.Errorf("resolve %s + %s (%s): %v", fw.ID, r.ID, k, err)
					continue
				}
				_ = engine.Steps(plan) // must not panic
			}
		}
	}
}

// TestBuiltinCatalogueLintsClean keeps the shipped recipes free of the failures
// that do not announce themselves: a {{token}} nothing defines (it reaches the
// shell literally), an alias shadowing a real id, an appliesTo naming a
// framework that does not exist. Two shipped recipes passed a literal
// "{{stack}}" and "{{panelPath}}" to real commands before this check existed.
func TestBuiltinCatalogueLintsClean(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	if findings := recipe.Lint(reg); len(findings) > 0 {
		t.Fatalf("built-in recipes should lint clean, got %d problem(s):\n%s", len(findings), recipe.Summary(findings))
	}
}

// buildOutputDirs are produced by a build step inside the Dockerfile rather than
// written by a recipe, so an entrypoint under one of them is legitimately absent
// from the rendered file set.
var buildOutputDirs = []string{"dist/", "build/", ".next/", ".output/", ".svelte-kit/", "public/build/"}

// entrypointRe matches a literal source path in a Dockerfile CMD/ENTRYPOINT exec
// form, e.g. CMD ["node", "src/index.js"] or CMD ["python", "app.py"]. Import
// strings (gunicorn's "app:app") carry no slash or extension and are not matched.
var entrypointRe = regexp.MustCompile(`"([A-Za-z0-9_./-]+\.(?:js|mjs|cjs|ts|py))"`)

// TestDockerfileEntrypointsExist: every literal source path named in a Dockerfile
// CMD or ENTRYPOINT must be a file the same plan actually writes.
//
// This failure mode is invisible to every other check in this package. The plan
// resolves, the steps render, no token leaks, the image builds - and then the
// container exits immediately on first start because the entrypoint is not there.
// It has already happened twice: express and fastify both shipped
// CMD ["node", "src/server.js"] against recipes that write src/index.js and
// src/app.js.
func TestDockerfileEntrypointsExist(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, fw := range reg.OfKind(recipe.Framework) {
		for _, env := range reg.ForFramework(fw.ID, recipe.Env) {
			ids := []string{fw.ID, env.ID}
			if db := defaultDB(reg, fw.ID, env.ID); db != "" {
				ids = append(ids, db)
			}
			plan, err := resolver.Resolve(reg, ids)
			if err != nil {
				continue // TestEveryFrameworkEnvComposes owns resolve failures.
			}
			files := engine.RenderedFiles(plan, "<project>")
			for path, content := range files {
				if filepath.Base(path) != "Dockerfile" {
					continue
				}
				for _, line := range strings.Split(content, "\n") {
					trimmed := strings.TrimSpace(line)
					if !strings.HasPrefix(trimmed, "CMD ") && !strings.HasPrefix(trimmed, "ENTRYPOINT ") {
						continue
					}
					for _, m := range entrypointRe.FindAllStringSubmatch(trimmed, -1) {
						ref := m[1]
						if isBuildOutput(ref) || flattensBuildOutput(content) {
							continue
						}
						if _, ok := files[ref]; !ok {
							t.Errorf("%s / %s: %s names entrypoint %q, which no recipe in the plan writes",
								fw.ID, env.ID, path, ref)
						}
					}
				}
			}
		}
	}
}

// flattensBuildOutput reports whether the Dockerfile copies a whole build stage
// into the workdir root, e.g. `COPY --from=build /app/build ./` (AdonisJS) or
// `COPY --from=build /app/.next/standalone ./` (Next.js standalone output). The
// entrypoint then sits at the root without any recipe having written it, and
// without a dist/ prefix to recognise it by.
func flattensBuildOutput(dockerfile string) bool {
	for _, line := range strings.Split(dockerfile, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) < 2 || f[0] != "COPY" || f[len(f)-1] != "./" {
			continue
		}
		if strings.Contains(line, "--from=") {
			return true
		}
	}
	return false
}

func isBuildOutput(ref string) bool {
	for _, d := range buildOutputDirs {
		if strings.HasPrefix(ref, d) {
			return true
		}
	}
	return false
}

// TestGeneratedYAMLParses: every YAML file a recipe writes must itself be valid
// YAML.
//
// `keel recipes validate` cannot catch this. A compose file lives inside a
// recipe as a block scalar, so the recipe parses cleanly no matter what the
// string contains, and the breakage only surfaces when docker compose reads it
// during a real build. A bad edit to one of these literals sailed through
// validate, lint and the whole test suite before this existed.
func TestGeneratedYAMLParses(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, fw := range reg.OfKind(recipe.Framework) {
		for _, env := range reg.ForFramework(fw.ID, recipe.Env) {
			ids := []string{fw.ID, env.ID}
			if db := defaultDB(reg, fw.ID, env.ID); db != "" {
				ids = append(ids, db)
			}
			plan, err := resolver.Resolve(reg, ids)
			if err != nil {
				continue // TestEveryFrameworkEnvComposes owns resolve failures.
			}
			for path, content := range engine.RenderedFiles(plan, "<project>") {
				ext := filepath.Ext(path)
				if ext != ".yaml" && ext != ".yml" {
					continue
				}
				var out any
				if err := yaml.Unmarshal([]byte(content), &out); err != nil {
					t.Errorf("%s / %s: %s is not valid YAML: %v", fw.ID, env.ID, path, err)
				}
			}
		}
	}
}

// nodeBaseRe pulls the major version out of a `FROM node:24-slim` line.
var nodeBaseRe = regexp.MustCompile(`FROM node:(\d+)`)

// nodeMinRe pulls the floor out of a node_version constraint like ">=24".
var nodeMinRe = regexp.MustCompile(`>=\s*(\d+)`)

// TestNodeImageMeetsDeclaredVersion: a framework that declares node_version
// ">=N" must not have its Dockerfile built on an older node image.
//
// AdonisJS declared ">=24" while every stage of its Dockerfile said node:22.
// Nothing failed at build time. The container started, the hot-hook loader threw
// inside read-pkg before the app ever listened, and all the symptom said was
// "container is unhealthy" - with no mention of a Node version anywhere.
func TestNodeImageMeetsDeclaredVersion(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, fw := range reg.OfKind(recipe.Framework) {
		m := nodeMinRe.FindStringSubmatch(fw.NodeVersion)
		if m == nil {
			continue // no floor declared, nothing to check against
		}
		min, _ := strconv.Atoi(m[1])
		for _, env := range reg.ForFramework(fw.ID, recipe.Env) {
			plan, err := resolver.Resolve(reg, []string{fw.ID, env.ID})
			if err != nil {
				continue
			}
			for path, content := range engine.RenderedFiles(plan, "<project>") {
				if filepath.Base(path) != "Dockerfile" {
					continue
				}
				for _, b := range nodeBaseRe.FindAllStringSubmatch(content, -1) {
					got, _ := strconv.Atoi(b[1])
					if got < min {
						t.Errorf("%s / %s: %s builds on node:%d but the framework declares node_version %q",
							fw.ID, env.ID, path, got, fw.NodeVersion)
					}
				}
			}
		}
	}
}
