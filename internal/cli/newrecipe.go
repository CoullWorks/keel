package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/spf13/cobra"
)

// recipeKinds is the closed set of kinds `keel new-recipe --kind` accepts, taken
// from the recipe.Kind constants so it cannot drift from the engine. It matches
// the help string exactly; an unknown kind is rejected rather than scaffolding a
// recipe with a kind nothing resolves.
var recipeKinds = []recipe.Kind{
	recipe.Framework, recipe.Addon, recipe.Env, recipe.DB,
	recipe.Service, recipe.Frontend, recipe.Extra, recipe.Generator,
}

// validateRecipeKind rejects a --kind value that is not in the closed set.
func validateRecipeKind(kind string) error {
	for _, k := range recipeKinds {
		if string(k) == kind {
			return nil
		}
	}
	return fmt.Errorf("unknown recipe kind %q (want one of: %s)", kind, recipeKindsString())
}

// recipeKindsString lists the valid kinds for help and error messages.
func recipeKindsString() string {
	s := make([]string, len(recipeKinds))
	for i, k := range recipeKinds {
		s[i] = string(k)
	}
	return strings.Join(s, "|")
}

func newRecipeCmd() *cobra.Command {
	var asPack bool
	var kind, dir string
	c := &cobra.Command{
		Use: "new-recipe [name]",
		Long: "Writes a starter recipe (or, with --pack, a whole distributable pack) you can\n" +
			"edit and validate. Recipes are data, so this is the whole of what it takes to\n" +
			"teach keel a new stack.\n",
		Example: "  keel new-recipe my-stack\n" +
			"  keel new-recipe my-pack --pack\n",
		Short: "Scaffold a starter recipe (or a full pack with --pack)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			if name == "" {
				if err := huh.NewInput().Title("Recipe / pack name").Placeholder("my-stack").Value(&name).Run(); err != nil {
					return err
				}
			}
			name = strings.TrimSpace(name)
			if name == "" {
				return fmt.Errorf("a name is required")
			}
			if err := validateRecipeKind(kind); err != nil {
				return err
			}
			if dir == "" {
				dir = "./" + name
			}
			if asPack {
				return scaffoldPack(out, dir, name)
			}
			return scaffoldRecipe(out, dir, name, kind)
		},
	}
	c.Flags().BoolVar(&asPack, "pack", false, "scaffold a full pack repo (manifest + recipes/ + hooks/)")
	c.Flags().StringVar(&kind, "kind", "framework", "recipe kind ("+recipeKindsString()+")")
	c.Flags().StringVarP(&dir, "dir", "o", "", "target directory (default: ./<name>)")
	_ = c.RegisterFlagCompletionFunc("kind", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		out := make([]string, len(recipeKinds))
		for i, k := range recipeKinds {
			out[i] = string(k)
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	})
	return c
}

func starterRecipe(name, kind string) string {
	return strings.NewReplacer("{{name}}", name, "{{kind}}", kind).Replace(`schema_version: 1
id: {{name}}
kind: {{kind}}
label: "{{name}}"
# lang: php             # framework recipes: the language group (php|python)
# provides: [{{name}}]  # capabilities this contributes
# requires: []          # capabilities that must already be present
# appliesTo: [laravel]  # non-framework recipes: which frameworks this is valid under

install:
  # Shell steps. Use the env command vocabulary so it works under any env:
  #   {{composer}}, {{artisan}}, {{magento}}, {{manage}}, {{exec}}, {{start}}
  - "echo scaffolding {{name}}"

files:
  - path: "NOTES-{{name}}.md"
    content: |
      # {{name}}
      <!-- keel-generated -->
      Dropped by the {{name}} recipe.

hooks:
  post_create:
    - message: "{{name}} recipe applied"
`)
}

func scaffoldRecipe(out io.Writer, dir, name, kind string) error {
	rel := name + ".yaml"
	if err := engine.WriteFile(dir, rel, starterRecipe(name, kind)); err != nil {
		return err
	}
	fmt.Fprintf(out, "✎ %s/%s\n", dir, rel)
	fmt.Fprintf(out, "✓ starter recipe created. Validate it with: keel recipes validate %s/%s\n", dir, rel)
	return nil
}

func scaffoldPack(out io.Writer, dir, name string) error {
	author := strings.TrimSpace(gitConfig("user.name") + " <" + gitConfig("user.email") + ">")
	manifest := strings.NewReplacer("{{name}}", name, "{{author}}", author).Replace(`schema_version: 1
name: {{name}}
version: 0.1.0
keel_version_constraint: ">= 0.1.0"
author: "{{author}}"
description: "A keel recipe pack."
recipes:
  - recipes/{{name}}.yaml
`)
	readme := strings.NewReplacer("{{name}}", name).Replace(`# {{name}}: a keel recipe pack

Install:

    keel recipes add <git-url-of-this-repo>

Then use its recipes in ` + "`keel new`" + `. Its recipes are untrusted until you
review and consent to the commands on first build.
`)
	files := map[string]string{
		"keel.pack.yaml":            manifest,
		"recipes/" + name + ".yaml": starterRecipe(name, "framework"),
		"hooks/post_create.sh":      "#!/bin/sh\n# keel post_create hook. $KEEL_PROJECT_DIR, $KEEL_PROJECT, $KEEL_ENV are set.\necho \"post_create for $KEEL_PROJECT\"\n",
		"README.md":                 readme,
	}
	for rel, content := range files {
		if err := engine.WriteFile(dir, rel, content); err != nil {
			return err
		}
		fmt.Fprintf(out, "✎ %s/%s\n", dir, rel)
	}
	fmt.Fprintf(out, "✓ pack scaffolded. Validate it with: keel recipes validate %s\n", dir)
	return nil
}
