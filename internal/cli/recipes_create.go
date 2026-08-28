package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/coullworks/keel/internal/engine"
	"github.com/spf13/cobra"
)

// recipesCreateCmd scaffolds a new, forkable recipe pack seeded from the
// example-pack template (a service recipe + a generator recipe + a README +
// hook). It is the pack twin of `plugins/example` for plugins: the fast path to
// "give me a real pack I can edit", where `keel recipes add` then installs it.
//
// It reuses the same on-disk shape `keel recipes add`/`validate` expect
// (keel.pack.yaml + recipes/), so a freshly created pack validates immediately —
// the last line of the output points the user straight at that check.
func recipesCreateCmd() *cobra.Command {
	var dir string
	c := &cobra.Command{
		Use:   "create <name>",
		Short: "Scaffold a new recipe pack from the example template (data only, no code)",
		Long: "Writes a forkable recipe pack (manifest, a service recipe, a generator recipe\n" +
			"with typed inputs + a fields table, and a README) seeded from the example\n" +
			"pack. Recipes are data, so this is the whole of what it takes to distribute a\n" +
			"new stack, service or generator. Install a pack with `keel recipes add`.\n",
		Example: "  keel recipes create my-pack\n" +
			"  keel recipes create my-pack -o ./packs/my-pack\n",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			if name == "" {
				if err := huh.NewInput().Title("Pack name").Placeholder("my-pack").Value(&name).Run(); err != nil {
					return err
				}
			}
			name = strings.TrimSpace(name)
			if name == "" {
				return fmt.Errorf("a pack name is required")
			}
			target := dir
			if target == "" {
				target = "./" + name
			}
			return scaffoldExamplePack(out, target, name)
		},
	}
	c.Flags().StringVarP(&dir, "dir", "o", "", "target directory (default: ./<name>)")
	return c
}

// scaffoldExamplePack writes the example-pack layout under dir, renamed to name.
// It mirrors example-pack/ so the two never drift in shape: a manifest, a
// service recipe, a generator recipe with typed inputs, a README and a hook.
//
// The templates use {{PACK}}/{{AUTHOR}} for scaffold-time substitution, NOT
// {{name}}: a generator recipe's own runtime token is {{name}} (the model name a
// user later types into keel gen), and it must survive scaffolding untouched
// rather than being clobbered with the pack name here.
func scaffoldExamplePack(out io.Writer, dir, name string) error {
	author := strings.TrimSpace(gitConfig("user.name") + " <" + gitConfig("user.email") + ">")
	if author == "<>" {
		author = "You <you@example.com>"
	}
	r := strings.NewReplacer("{{PACK}}", name, "{{AUTHOR}}", author)

	files := map[string]string{
		"keel.pack.yaml":         r.Replace(examplePackManifest),
		"README.md":              r.Replace(examplePackReadme),
		"recipes/service.yaml":   r.Replace(examplePackService),
		"recipes/generator.yaml": r.Replace(examplePackGenerator),
		"hooks/post_create.sh":   examplePackHook,
	}
	for rel, content := range files {
		if err := engine.WriteFile(dir, rel, content); err != nil {
			return err
		}
		fmt.Fprintf(out, "✎ %s/%s\n", dir, rel)
	}
	fmt.Fprintf(out, "✓ pack %q scaffolded from the example template.\n", name)
	fmt.Fprintf(out, "  Validate it:  keel recipes validate %s\n", dir)
	fmt.Fprintf(out, "  Then install: keel recipes add %s\n", dir)
	return nil
}

const examplePackManifest = `schema_version: 2
name: {{PACK}}
version: 0.1.0
keel_version_constraint: ">= 0.1.0"
author: "{{AUTHOR}}"
description: >-
  A keel recipe pack. Recipes are DATA (no Go). Install with:
  keel recipes add <git-url>.
recipes:
  - recipes/service.yaml
  - recipes/generator.yaml
`

const examplePackReadme = `# {{PACK}}: a keel recipe pack

Recipe packs are **data, not code**: git-distributable recipe YAML installed with
` + "`keel recipes add`" + `. (A *plugin* is Go code, see docs/EXTENDING.md.)

## What's here

    {{PACK}}/
      keel.pack.yaml        the manifest
      recipes/service.yaml  a kind: service,   a backing service added to a build
      recipes/generator.yaml a kind: generator, a new thing keel gen can create
      hooks/post_create.sh  script escape-hatch for a recipe's hooks: block

## Use it

    keel recipes validate .            # read-only; runs no recipe code
    keel recipes add <git-url-of-fork> # fetch + validate only; nothing executes
    keel recipes list                  # your recipes now appear, marked untrusted

Pack recipes are UNTRUSTED until you consent: keel prints the exact commands and
asks before running them on the first ` + "`keel new`" + ` that uses them.
`

const examplePackService = `id: {{PACK}}-service
schema_version: 2
kind: service
label: "Example service (added by the {{PACK}} pack)"
category: Database
# A backing service added to a build. Narrow appliesTo to the frameworks you
# support; default:false keeps it opt-in (it appears in keel new's Services step
# to tick, never forced in).
appliesTo: ["*"]
provides: [{{PACK}}-service]
default: false
updated: 2026-01-01
provision:
  # How this service comes up per environment family. A missing key for the
  # chosen env is an error the user can read, not a silent no-op.
  compose:
    files:
      - path: compose.override.yaml
        content: |
          # Merged over compose.yaml, so the env recipe's services are untouched.
          services:
            example:
              image: hello-world
  local: {}
files:
  - path: "{{PACK}}-SERVICE.md"
    content: |
      # {{PACK}} service
      <!-- keel-generated -->
      Added by the {{PACK}} pack. Replace this with a real service.
`

// examplePackGenerator's {{name}} tokens are the GENERATOR's runtime input (the
// model name typed into keel gen), NOT scaffold tokens — they must survive
// scaffolding verbatim, which is why the pack name uses {{PACK}} instead.
const examplePackGenerator = `id: {{PACK}}-model
schema_version: 2
kind: generator
level: code-block
label: "Example model ({{PACK}} pack, typed inputs + a fields table)"
# A generator teaches keel a new thing keel gen / the studio can create. This is
# a code-block generator (the smallest level). Its typed inputs describe the
# form keel collects — ONE generation shell renders it for every framework.
appliesTo: [laravel, django, nextjs]
provides: [{{PACK}}-model]
updated: 2026-01-01
inputs:
  - name: name
    type: class
    label: "Model name"
    required: true
    help: "PascalCase, e.g. Product"
  - name: timestamps
    type: bool
    label: "Add created_at / updated_at?"
    default: "true"
  # keel's mage2gen primitive: a typed column table feeding every framework's DDL.
  - name: fields
    type: fields
    label: "Columns"
    help: "name, type, nullable, unique, index, default"
    repeatable: true
files:
  # {{name}} here is the model name the user types into keel gen — a runtime
  # token, left untouched by scaffolding.
  - path: "docs/models/{{name}}.md"
    content: |
      # Generated model
      <!-- keel-generated -->
      Scaffolded by the {{PACK}} pack generator. Emit your framework's real
      model + migration from the fields table above.
`

const examplePackHook = `#!/bin/sh
# keel post_create hook — fires once, after the framework skeleton exists.
# Pack hooks are UNTRUSTED: keel shows the command and asks before running it.
# keel sets $KEEL_PROJECT_DIR, $KEEL_PROJECT, $KEEL_ENV.
echo "post_create ran for $KEEL_PROJECT"
`
