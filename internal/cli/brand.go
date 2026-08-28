package cli

import (
	"fmt"

	"github.com/coullworks/keel/internal/brand"
	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/project"
	"github.com/spf13/cobra"
)

// brandCmd is `keel brand`. It keeps its original two-colour form working
// (`keel brand <primary> [accent]` applies a brand to the project in --dir) and
// adds the brand-as-data subcommands the Fitness Round-2 item calls for:
//
//	keel brand set <primary> [accent]   — edit the global default (~/.config/keel/brand.yaml)
//	keel brand apply [--global|--project] [--dir]  — apply the resolved brand to a project
//	keel brand show [--dir]             — print the resolved tokens and which layer won
//
// The bare-args form and `apply` both funnel through brand.Apply/ApplyTokens so
// the two-colour path and the token path can't drift. root.go is untouched — the
// subcommands hang off the existing brandCmd only.
// mustStr reads a string flag, ignoring the (never-hit for defined flags) error.
func mustStr(cmd *cobra.Command, name string) string { s, _ := cmd.Flags().GetString(name); return s }

func brandCmd() *cobra.Command {
	c := &cobra.Command{
		Use: "brand [primary] [accent]",
		Long: "Writes your brand into whatever CSS framework the project uses.\n\n" +
			"With a colour argument it applies that colour directly (back-compat):\n" +
			"keel detects Tailwind v4 (@theme in the CSS entry), Tailwind v3\n" +
			"(tailwind.config.*) or Bootstrap (Sass $primary) and writes the correct,\n" +
			"idiomatic token block: a full 50-950 scale for brand/accent/semantic\n" +
			"roles plus surface, radius, font and a dark variant, generated from the\n" +
			"seed colour(s). Colours are #rgb or #rrggbb hex.\n\n" +
			"With no colour it prints the resolved brand (project override -> global\n" +
			"default -> the kit's own colours). Use the subcommands to manage the\n" +
			"global default and apply the resolved tokens.\n",
		Example: "  keel brand #5b21b6\n" +
			"  keel brand #5b21b6 #3ab7bf\n" +
			"  keel brand #5b21b6 --accent #3ab7bf --radius 0.75rem --font \"Inter, system-ui\"\n" +
			"  keel brand #5b21b6 --logo ./logo.svg --favicon ./favicon.ico\n" +
			"  keel brand --logo ./logo.svg          # set just the logo\n" +
			"  keel brand set #5b21b6 #3ab7bf        # set the global default\n" +
			"  keel brand show --dir ./web           # print the resolved tokens\n",
		Short: "Manage and apply brand tokens (global default + per-project override)",
		Args:  cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			dir := project.Expand(mustStr(cmd, "dir"))
			accentF := mustStr(cmd, "accent")
			radius := mustStr(cmd, "radius")
			fontSans := mustStr(cmd, "font")
			logo := mustStr(cmd, "logo")
			favicon := mustStr(cmd, "favicon")

			// No colour and no theming inputs at all: show the resolved brand.
			if len(args) == 0 && accentF == "" && radius == "" && fontSans == "" && logo == "" && favicon == "" {
				return runBrandShow(cmd, dir)
			}

			// Colours (+ radius/font): a colour is required to (re)generate the token set.
			if len(args) >= 1 {
				primary := args[0]
				accent := accentF
				if accent == "" && len(args) == 2 {
					accent = args[1]
				}
				tokens, err := brand.Generate(primary, accent)
				if err != nil {
					return err
				}
				if radius != "" {
					tokens.Radius.Base = radius
				}
				if fontSans != "" {
					tokens.Font.Sans = fontSans
				}
				res, err := brand.ApplyTokens(dir, tokens)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "✓ %s brand applied → %s\n", res.Stack, res.File)
				if res.Note != "" {
					fmt.Fprintf(out, "  %s\n", res.Note)
				}
			} else if radius != "" || fontSans != "" {
				return fmt.Errorf("--radius and --font need a colour too, e.g. keel brand #5b21b6 --radius 0.75rem")
			}

			// Logo and favicon: no-code assets, settable with or without colours.
			if logo != "" || favicon != "" {
				ar, err := brand.ApplyAssets(dir, logo, favicon)
				if err != nil {
					return err
				}
				for _, f := range ar.Written {
					fmt.Fprintf(out, "✓ wrote %s\n", f)
				}
			}
			return nil
		},
	}
	c.Flags().String("dir", ".", "project directory to apply the brand to")
	c.Flags().String("accent", "", "accent colour (hex); same as the second positional arg")
	c.Flags().String("radius", "", "base corner radius, e.g. 0.75rem")
	c.Flags().String("font", "", "sans font-family stack, e.g. \"Inter, system-ui\"")
	c.Flags().String("logo", "", "path to a logo image to place in the project")
	c.Flags().String("favicon", "", "path to a favicon (.ico/.png) to place in the project")
	c.AddCommand(brandSetCmd(), brandApplyCmd(), brandShowCmd())
	return c
}

// brandSetCmd is `keel brand set <primary> [accent]`: it generates a full token
// set from the seed(s) and persists it as the global default, always applied to
// every theme keel builds thereafter (unless a project overrides it).
func brandSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <primary> [accent]",
		Short: "Set the global default brand (~/.config/keel/brand.yaml)",
		Long: "Generates a comprehensive token set from one or two seed hexes and saves\n" +
			"it as your global default, next to your profile. Every Tailwind/Bootstrap\n" +
			"theme keel builds picks it up, unless a project sets its own override.\n",
		Example: "  keel brand set #5b21b6\n  keel brand set #5b21b6 #3ab7bf\n",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			accent := ""
			if len(args) == 2 {
				accent = args[1]
			}
			tokens, err := brand.Generate(args[0], accent)
			if err != nil {
				return err
			}
			if err := brand.SaveGlobal(tokens); err != nil {
				return err
			}
			fmt.Fprintf(out, "✓ global brand default saved → %s\n", brand.GlobalPath())
			fmt.Fprint(out, tokens.String())
			return nil
		},
	}
}

// brandApplyCmd is `keel brand apply [--global|--project] [--dir]`: it applies
// the resolved brand to the project's CSS. --project records the project's own
// override in its manifest first (from the resolved seed); --global forces the
// global default even if the project has an override; the default is the normal
// resolution order (project override → global default → kit).
func brandApplyCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "apply",
		Short: "Apply the resolved brand tokens to a project's CSS framework",
		Long: "Resolves the brand (project override -> global default -> the kit's own\n" +
			"colours) and writes the full token set into the project. --global forces\n" +
			"the global default; --project first records the resolved seed as the\n" +
			"project's own override in .keel/manifest.yaml, then applies it.\n",
		Example: "  keel brand apply --dir ./web\n" +
			"  keel brand apply --global --dir ./web\n" +
			"  keel brand apply --project --dir ./web\n",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			dir := project.Expand(mustFlag(cmd, "dir"))
			global, _ := cmd.Flags().GetBool("global")
			asProject, _ := cmd.Flags().GetBool("project")
			if global && asProject {
				return fmt.Errorf("--global and --project are mutually exclusive")
			}

			var tokens brand.BrandTokens
			var source brand.Source
			switch {
			case global:
				g, ok, err := brand.LoadGlobal()
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("no global brand default set - run `keel brand set <primary>` first")
				}
				tokens, source = g, brand.SourceGlobal
			default:
				r, err := brand.Resolve(dir)
				if err != nil {
					return err
				}
				if !r.HasTokens {
					return fmt.Errorf("no brand to apply: no project override and no global default (run `keel brand set <primary>`)")
				}
				tokens, source = r.Tokens, r.Source
			}

			// --project: persist the seed into the manifest as this project's
			// override before applying, so it wins next time.
			if asProject {
				if err := writeProjectBrand(dir, tokens.Seed); err != nil {
					return err
				}
				source = brand.SourceProject
			}

			res, err := brand.ApplyTokens(dir, tokens)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "✓ %s brand applied (%s) → %s\n", res.Stack, source, res.File)
			if res.Note != "" {
				fmt.Fprintf(out, "  %s\n", res.Note)
			}
			return nil
		},
	}
	c.Flags().String("dir", ".", "project directory to apply the brand to")
	c.Flags().Bool("global", false, "apply the global default, ignoring any project override")
	c.Flags().Bool("project", false, "record the resolved seed as the project's override, then apply")
	return c
}

// brandShowCmd is `keel brand show [--dir]`: prints the resolved token set and
// which layer it came from, without writing anything.
func brandShowCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "show",
		Short:   "Print the resolved brand tokens (project -> global -> kit)",
		Args:    cobra.NoArgs,
		Example: "  keel brand show --dir ./web\n",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBrandShow(cmd, project.Expand(mustFlag(cmd, "dir")))
		},
	}
	c.Flags().String("dir", ".", "project directory to resolve the brand for")
	return c
}

// runBrandShow resolves and prints the brand for dir. Shared by `brand show` and
// the bare `keel brand` (no-arg) form.
func runBrandShow(cmd *cobra.Command, dir string) error {
	out := cmd.OutOrStdout()
	r, err := brand.Resolve(dir)
	if err != nil {
		return err
	}
	switch r.Source {
	case brand.SourceKit:
		fmt.Fprintln(out, "brand: none - no project override and no global default; the kit's own colours stand.")
		fmt.Fprintln(out, "set a global default with `keel brand set <primary> [accent]`.")
		return nil
	case brand.SourceProject:
		fmt.Fprintf(out, "brand source: project override (.keel/manifest.yaml)\n\n")
	case brand.SourceGlobal:
		fmt.Fprintf(out, "brand source: global default (%s)\n\n", brand.GlobalPath())
	}
	fmt.Fprint(out, r.Tokens.String())
	return nil
}

// writeProjectBrand records a brand seed as the project's manifest override. It
// reads the existing manifest, sets the Brand block and writes it back, so a
// project pins its own brand without touching its other recipes.
func writeProjectBrand(dir string, seed brand.Seed) error {
	m, err := engine.ReadManifest(dir)
	if err != nil {
		return fmt.Errorf("no keel manifest in %s (run `keel adopt` first): %w", dir, err)
	}
	m.Brand = &engine.BrandRef{Primary: seed.Primary, Accent: seed.Accent}
	return engine.WriteManifestFile(dir, m)
}

// mustFlag reads a string flag, returning "" if unset (flags always exist here,
// so the error is impossible — this keeps the call sites uncluttered).
func mustFlag(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}
