package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/pack"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/tui"
	"github.com/spf13/cobra"
)

func recipesCmd() *cobra.Command {
	c := requireSubcommand(&cobra.Command{
		Use:   "recipes",
		Short: "List, add, remove and validate recipe packs",
		Example: "  keel recipes list\n" +
			"  keel recipes add coullworks/keel-recipes\n",
	})
	c.AddCommand(recipesListCmd(), recipesAddCmd(), recipesRemoveCmd(), recipesCreateCmd(), recipesValidateCmd(), recipesSearchCmd(), recipesVerifyCmd(), recipesFreshnessCmd(), recipesLintCmd())
	return c
}

// freshnessStatus classifies a recipe's `updated` date. An empty/unparseable
// date is "unknown"; older than staleAfterDays is "review-due"; else "fresh".
func freshnessStatus(updated string, now time.Time, staleAfterDays int) (status string, ageDays int) {
	if strings.TrimSpace(updated) == "" {
		return "unknown", -1
	}
	t, err := time.Parse("2006-01-02", strings.TrimSpace(updated))
	if err != nil {
		return "unknown", -1
	}
	ageDays = int(now.Sub(t).Hours() / 24)
	if ageDays > staleAfterDays {
		return "review-due", ageDays
	}
	return "fresh", ageDays
}

func recipesFreshnessCmd() *cobra.Command {
	var staleAfter int
	var asJSON, staleOnly bool
	c := &cobra.Command{
		Use:   "freshness [framework]",
		Short: "Report how current each recipe is (last-reviewed date + version pins)",
		Long: "Recipes rot: installers drift and pinned versions fall behind. `freshness`\n" +
			"surfaces each recipe's last-reviewed date and any version pins, and flags\n" +
			"recipes not reviewed within --stale-after days (default 180) as review-due.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			reg, err := catalog.Registry()
			if err != nil {
				return err
			}
			now := time.Now()
			type row struct {
				ID      string            `json:"id"`
				Kind    string            `json:"kind"`
				Source  string            `json:"source"`
				Updated string            `json:"updated,omitempty"`
				Status  string            `json:"status"`
				AgeDays int               `json:"age_days"`
				Pins    map[string]string `json:"pins,omitempty"`
			}
			var rows []row
			for _, r := range reg.All() {
				if len(args) == 1 && !r.AppliesToFramework(args[0]) && r.ID != args[0] {
					continue
				}
				status, age := freshnessStatus(r.Updated, now, staleAfter)
				if staleOnly && status == "fresh" {
					continue
				}
				src := r.Source
				if src == "" {
					src = "builtin"
				}
				rows = append(rows, row{r.ID, string(r.Kind), src, r.Updated, status, age, r.Pins})
			}
			sort.SliceStable(rows, func(i, j int) bool {
				rank := map[string]int{"review-due": 0, "unknown": 1, "fresh": 2}
				if rank[rows[i].Status] != rank[rows[j].Status] {
					return rank[rows[i].Status] < rank[rows[j].Status]
				}
				return rows[i].ID < rows[j].ID
			})
			if asJSON {
				return json.NewEncoder(out).Encode(rows)
			}
			var due, unknown int
			frows := make([]tui.FreshnessRow, 0, len(rows))
			for _, r := range rows {
				mark := "ok"
				switch r.Status {
				case "review-due":
					mark, due = fmt.Sprintf("review-due (%dd)", r.AgeDays), due+1
				case "unknown":
					mark, unknown = "no date", unknown+1
				}
				updated := r.Updated
				if updated == "" {
					updated = "-"
				}
				var pins string
				if len(r.Pins) > 0 {
					ps := make([]string, 0, len(r.Pins))
					for k, v := range r.Pins {
						ps = append(ps, k+"="+v)
					}
					sort.Strings(ps)
					pins = strings.Join(ps, "  ")
				}
				frows = append(frows, tui.FreshnessRow{ID: r.ID, Kind: r.Kind, Updated: updated, Status: mark, Pins: pins})
			}
			fmt.Fprintln(out, tui.RenderFreshness(frows))
			fmt.Fprintf(out, "\n%d recipes · %d review-due · %d without a date (--stale-after=%d days)\n", len(rows), due, unknown, staleAfter)
			return nil
		},
	}
	c.Flags().IntVar(&staleAfter, "stale-after", 180, "flag recipes not reviewed within this many days")
	c.Flags().BoolVar(&staleOnly, "stale", false, "show only review-due recipes")
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return c
}

func trusted(source string) bool { return source == "builtin" || source == "user" }

func sourceRank(source string) int {
	switch {
	case source == "builtin":
		return 0
	case source == "user":
		return 1
	default:
		return 2
	}
}

func recipesListCmd() *cobra.Command {
	var packsOnly, asJSON bool
	c := &cobra.Command{
		Use:   "list",
		Args:  cobra.NoArgs,
		Short: "List available recipes (built-in + installed packs)",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if packsOnly {
				reg, err := pack.Load()
				if err != nil {
					return err
				}
				// Installed packs (packs.yaml) first, then any pack discovered loose
				// under home — the pack twin of plugin home-discovery — so a pack
				// cloned anywhere in the dev tree lists with zero config. Installed
				// wins a name clash. The JSON shape stays []pack.Installed; a
				// discovered pack is represented with its name/version and Source set
				// to its directory so a consumer can tell it apart from an installed one.
				packs := append([]pack.Installed(nil), reg.Packs...)
				seen := map[string]bool{}
				for _, p := range reg.Packs {
					seen[p.Name] = true
				}
				for _, dp := range pack.Discover() {
					if dp.Installed || seen[dp.Name] {
						continue
					}
					seen[dp.Name] = true
					v := ""
					if dp.Manifest != nil {
						v = dp.Manifest.Version
					}
					packs = append(packs, pack.Installed{Name: dp.Name, Version: v, Source: dp.Dir})
				}
				if asJSON {
					return json.NewEncoder(out).Encode(packs)
				}
				if len(packs) == 0 {
					fmt.Fprintln(out, "No packs installed. Add one with: keel recipes add <git-url>")
					return nil
				}
				prows := make([]tui.PackRow, len(packs))
				for i, p := range packs {
					prows[i] = tui.PackRow{Name: p.Name, Version: p.Version, Commit: p.Commit, Trusted: p.Trusted}
				}
				fmt.Fprintln(out, tui.RenderPacks(prows))
				return nil
			}
			reg, err := catalog.Registry()
			if err != nil {
				return err
			}
			var rows []tui.RecipeRow
			for _, r := range reg.All() {
				src := r.Source
				if src == "" {
					src = "builtin"
				}
				// Scope is the one fact that matters per kind: what language a
				// framework is, and for everything else which frameworks it can
				// go under. "any" rather than "*", because the listing is for
				// people.
				scope := r.Lang
				if r.Kind != recipe.Framework {
					scope = strings.Join(r.AppliesTo, ", ")
					if scope == "*" || scope == "" {
						scope = "any"
					}
				}
				rows = append(rows, tui.RecipeRow{
					ID: r.ID, Kind: string(r.Kind), Label: r.Label, Source: src,
					Trusted: trusted(src), Scope: scope, Default: r.Default,
				})
			}
			sort.SliceStable(rows, func(i, j int) bool { return sourceRank(rows[i].Source) < sourceRank(rows[j].Source) })
			if asJSON {
				return json.NewEncoder(out).Encode(rows)
			}
			fmt.Fprint(out, tui.RenderRecipes(rows))
			return nil
		},
	}
	c.Flags().BoolVar(&packsOnly, "packs", false, "list installed packs only")
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output, for editors and scripts")
	return c
}

func recipesValidateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "validate [path]",
		Short: "Lint a recipe or pack against the schema",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if len(args) == 0 {
				if _, err := catalog.RegistryStrict(); err != nil { // parses+validates everything installed, bad file = error not skip
					return err
				}
				fmt.Fprintln(out, "✓ all installed recipes valid")
				return nil
			}
			path := args[0]
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			if info.IsDir() {
				return validatePackDir(out, path)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if err := recipe.AddYAML(recipe.NewRegistry(), b, "user", ""); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			fmt.Fprintf(out, "✓ %s valid\n", path)
			return nil
		},
	}
	return c
}

func validatePackDir(out io.Writer, dir string) error {
	m, err := pack.ReadManifest(dir)
	if err != nil {
		return fmt.Errorf("not a pack (no keel.pack.yaml): %w", err)
	}
	if ok, err := pack.SatisfiesKeel(m.KeelVersion, Version); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("pack %s requires keel %s (have %s)", m.Name, m.KeelVersion, Version)
	}
	reg := recipe.NewRegistry()
	if err := recipe.LoadInto(reg, os.DirFS(dir), "pack:"+m.Name, m.Name); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ pack %s (%s): %d recipes valid\n", m.Name, m.Version, reg.Len())
	return nil
}

func recipesAddCmd() *cobra.Command {
	var force, dryRun bool
	var ref string
	c := &cobra.Command{
		Use:   "add <git-url|owner/repo|path>",
		Short: "Install a recipe pack (fetch + validate only; never runs its code)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			return recipesAdd(cmd.Context(), out, args[0], ref, force, dryRun)
		},
	}
	c.Flags().BoolVar(&force, "force", false, "allow overriding existing recipe ids")
	c.Flags().StringVar(&ref, "ref", "", "git branch/tag/commit to pin")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "fetch + validate only, install nothing")
	return c
}

func recipesAdd(ctx context.Context, out io.Writer, source, ref string, force, dryRun bool) error {
	tmp, commit, err := pack.Fetch(ctx, source, ref)
	if err != nil {
		return err
	}
	moved := false
	defer func() {
		if !moved {
			os.RemoveAll(tmp)
		}
	}()

	m, err := pack.ReadManifest(tmp)
	if err != nil {
		return fmt.Errorf("not a keel pack (no keel.pack.yaml): %w", err)
	}
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("pack manifest is missing a name")
	}
	if m.SchemaVersion > recipe.SupportedSchema {
		return fmt.Errorf("pack schema_version %d is newer than this keel (%d) - upgrade keel", m.SchemaVersion, recipe.SupportedSchema)
	}
	if ok, err := pack.SatisfiesKeel(m.KeelVersion, Version); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("pack %s requires keel %s (have %s)", m.Name, m.KeelVersion, Version)
	}

	packReg := recipe.NewRegistry()
	if err := recipe.LoadInto(packReg, os.DirFS(tmp), "pack:"+m.Name, m.Name); err != nil {
		return fmt.Errorf("invalid recipe in pack: %w", err)
	}
	base, err := catalog.Registry()
	if err != nil {
		return err
	}
	var collisions, added []string
	for _, r := range packReg.All() {
		added = append(added, r.ID)
		if existing, ok := base.Get(r.ID); ok && existing.Pack != m.Name {
			collisions = append(collisions, r.ID)
		}
	}
	if len(collisions) > 0 && !force {
		return fmt.Errorf("pack recipe ids collide with existing: %s (use --force to override)", strings.Join(collisions, ", "))
	}

	fmt.Fprintf(out, "%s %s by %s, %d recipes: %s\n", m.Name, m.Version, m.Author, len(added), strings.Join(added, ", "))
	if dryRun {
		fmt.Fprintln(out, "(dry-run) pack is valid; nothing installed.")
		return nil
	}
	if err := pack.Move(tmp, pack.Dir(m.Name)); err != nil {
		return err
	}
	moved = true

	reg, err := pack.Load()
	if err != nil {
		return err
	}
	reg.Upsert(pack.Installed{
		Name: m.Name, Source: source, Version: m.Version, Commit: commit,
		InstalledAt: time.Now().UTC().Format(time.RFC3339), Trusted: false,
	})
	if err := reg.Save(); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ installed %s into %s\n", m.Name, pack.Dir(m.Name))
	fmt.Fprintln(out, "  Its recipes are UNTRUSTED: keel shows the exact commands and asks before running them.")
	return nil
}

func recipesRemoveCmd() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:     "remove <pack>",
		Aliases: []string{"rm"},
		Short:   "Uninstall a recipe pack",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			name := args[0]
			reg, err := pack.Load()
			if err != nil {
				return err
			}
			if _, ok := reg.Get(name); !ok {
				return fmt.Errorf("pack %q is not installed", name)
			}
			if !yes {
				ok := false
				if err := huh.NewConfirm().Title(fmt.Sprintf("Remove pack %s?", name)).Value(&ok).Run(); err != nil {
					return err
				}
				if !ok {
					fmt.Fprintln(out, "cancelled")
					return nil
				}
			}
			if _, err := pack.Uninstall(name); err != nil {
				return err
			}
			fmt.Fprintf(out, "✓ removed %s. (Already-generated projects are untouched.)\n", name)
			return nil
		},
	}
	c.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation")
	return c
}
