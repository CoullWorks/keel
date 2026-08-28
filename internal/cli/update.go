package cli

import (
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/merge"
	"github.com/coullworks/keel/internal/resolver"
	"github.com/spf13/cobra"
)

func updateCmd() *cobra.Command {
	var dryRun bool
	c := &cobra.Command{
		Use: "update",
		Example: "  keel update\n" +
			"  keel update --dry-run           # show what would change\n",
		Args:  cobra.NoArgs,
		Short: "Refresh keel-owned files to the latest recipes (Copier-style, non-destructive)",
		Long: "Re-renders the files keel owns for this project and updates the ones you\n" +
			"haven't touched. Files you edited are left alone; if the recipe also\n" +
			"changed, the new version is written alongside as <file>.keel-new so you\n" +
			"can merge by hand. Files created by installers (composer, npm) are never\n" +
			"touched: keel only manages the joins it generated.",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			return runUpdate(out, dryRun)
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change, write nothing")
	return c
}

func runUpdate(out io.Writer, dryRun bool) error {
	m, err := engine.ReadManifest(".")
	if err != nil {
		return manifestErr(err)
	}
	reg, err := catalog.Registry()
	if err != nil {
		return err
	}
	plan, err := resolver.Resolve(reg, m.Recipes)
	if err != nil {
		return fmt.Errorf("re-resolving this project's recipes: %w", err)
	}
	rendered := engine.RenderedFiles(plan, ".")

	var updated, merged, conflicts, restored, kept, unchanged []string
	newHashes := map[string]string{}
	if m.Files != nil {
		for k, v := range m.Files {
			newHashes[k] = v
		}
	}

	paths := make([]string, 0, len(rendered))
	for p := range rendered {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, path := range paths {
		newContent := rendered[path]
		newHash := engine.Sha(newContent)
		recorded := ""
		if m.Files != nil {
			recorded = m.Files[path]
		}

		disk, readErr := os.ReadFile(path)
		theirs := newContent // the new recipe output for this file
		write := func(content string) error {
			if dryRun {
				return nil
			}
			return engine.WriteFile(".", path, content)
		}
		switch {
		case os.IsNotExist(readErr): // keel-owned file went missing → restore it
			restored = append(restored, path)
			if err := write(theirs); err != nil {
				return err
			}
			newHashes[path] = newHash
		case readErr != nil:
			return readErr
		case engine.Sha(string(disk)) == newHash: // already current
			unchanged = append(unchanged, path)
			newHashes[path] = newHash
		default:
			base, hasBase := engine.ReadBase(".", path)
			switch {
			case !hasBase: // pre-snapshot project → old behaviour (hash + .keel-new)
				if engine.Sha(string(disk)) == recorded {
					updated = append(updated, path)
					if err := write(theirs); err != nil {
						return err
					}
					newHashes[path] = newHash
				} else {
					conflicts = append(conflicts, path)
					if !dryRun {
						if err := engine.WriteFile(".", path+".keel-new", theirs); err != nil {
							return err
						}
					}
				}
			case string(disk) == base: // untouched by you → clean take the new version
				updated = append(updated, path)
				if err := write(theirs); err != nil {
					return err
				}
				newHashes[path] = newHash
			case theirs == base: // recipe unchanged, you edited → keep yours
				kept = append(kept, path)
				newHashes[path] = engine.Sha(string(disk))
			default: // both changed → true 3-way merge
				out, nconf := merge.ThreeWay(base, string(disk), theirs)
				if err := write(out); err != nil {
					return err
				}
				if nconf > 0 {
					conflicts = append(conflicts, fmt.Sprintf("%s (%d conflict marker(s) to resolve)", path, nconf))
				} else {
					merged = append(merged, path)
				}
				newHashes[path] = engine.Sha(out)
			}
			// advance the merge base to the latest recipe output.
			if !dryRun {
				_ = engine.WriteBase(".", path, theirs)
			}
		}
	}

	report(out, "refreshed", updated)
	report(out, "merged (kept your edits + the recipe's)", merged)
	report(out, "restored", restored)
	report(out, "kept yours (recipe unchanged)", kept)
	report(out, "CONFLICT: resolve the markers in the file", conflicts)
	touched := len(updated) + len(merged) + len(restored) + len(conflicts)
	if touched+len(kept) == 0 {
		fmt.Fprintf(out, "✓ up to date (%d keel-owned files)\n", len(unchanged))
		return nil
	}

	if dryRun {
		fmt.Fprintln(out, "(dry-run) nothing written.")
		return nil
	}
	m.Files = newHashes
	if err := engine.WriteManifestFile(".", m); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ update complete: %d refreshed, %d merged, %d restored, %d conflict(s)\n",
		len(updated), len(merged), len(restored), len(conflicts))
	if len(conflicts) > 0 {
		fmt.Fprintln(out, "  resolve the <<<<<<< / >>>>>>> markers, then you're done.")
	}
	return nil
}

func report(out io.Writer, label string, paths []string) {
	for _, p := range paths {
		fmt.Fprintf(out, "  %s: %s\n", label, p)
	}
}
