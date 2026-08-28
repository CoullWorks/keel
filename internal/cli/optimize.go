package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/optimize"
	"github.com/coullworks/keel/internal/project"
	"github.com/coullworks/keel/internal/tui"
	"github.com/spf13/cobra"
)

func optimizeCmd() *cobra.Command {
	var securityOnly, asJSON, fix bool
	c := &cobra.Command{
		Use: "optimize [path]",
		Example: "  keel optimize\n" +
			"  keel optimize --json            # machine-readable findings\n" +
			"  keel optimize --security\n",
		Short: "Scan a project for security, performance and hygiene issues (read-only)",
		Long: "Static checks over a project:\n" +
			"  - security     committed .env / secrets, hardcoded keys & tokens\n" +
			"  - performance  Next.js next/image + next/link + image optimization\n" +
			"  - hygiene      missing .gitignore/README, vendored or large files\n" +
			"Read-only by default. --json emits structured findings (for agents/CI);\n" +
			"--fix applies safe mechanical remediations (gitignore secrets, add .gitignore/README).",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			dir := "."
			if len(args) == 1 {
				dir = project.Expand(args[0])
				// A nonexistent or non-directory path is a mistake, not an empty
				// project: scanning it silently would report misleading findings
				// (no README, no .gitignore) for a directory that isn't there.
				if info, err := os.Stat(dir); err != nil || !info.IsDir() {
					return fmt.Errorf("no such directory: %s", args[0])
				}
			}
			fw := ""
			if m, err := engine.ReadManifest(dir); err == nil {
				fw = m.Framework
			} else {
				fw = project.Detect(dir)
			}
			rep := optimize.Run(dir, fw)

			if fix {
				fixed := optimize.Fix(dir, rep.Findings)
				rep = optimize.Run(dir, fw) // re-scan after fixing
				if !asJSON {
					for _, f := range fixed {
						fmt.Fprintln(out, "✓ fixed:", f)
					}
					if len(fixed) == 0 {
						fmt.Fprintln(out, "(nothing auto-fixable)")
					}
					fmt.Fprintln(out)
				}
			}

			if asJSON {
				b, _ := json.MarshalIndent(map[string]any{
					"findings": rep.Findings,
					"summary": map[string]int{
						"error": rep.Count(optimize.SevError), "warn": rep.Count(optimize.SevWarn), "info": rep.Count(optimize.SevInfo),
					},
				}, "", "  ")
				fmt.Fprintln(out, string(b))
			} else {
				printReport(out, rep, securityOnly)
			}
			if rep.Count(optimize.SevError) > 0 {
				return fmt.Errorf("%d error-level issue(s) found", rep.Count(optimize.SevError))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&securityOnly, "security", false, "only report security findings")
	c.Flags().BoolVar(&asJSON, "json", false, "emit structured JSON findings")
	c.Flags().BoolVar(&fix, "fix", false, "apply safe mechanical fixes, then re-scan")
	return c
}

var sevMark = map[optimize.Severity]string{
	optimize.SevError: "✗", optimize.SevWarn: "!", optimize.SevInfo: "·",
}

func printReport(out io.Writer, r optimize.Report, securityOnly bool) {
	cats := []optimize.Category{optimize.CatSecurity, optimize.CatPerf, optimize.CatHygiene}
	title := map[optimize.Category]string{
		optimize.CatSecurity: "Security", optimize.CatPerf: "Performance", optimize.CatHygiene: "Hygiene",
	}
	total := 0
	var groups []tui.FindingGroup
	for _, cat := range cats {
		if securityOnly && cat != optimize.CatSecurity {
			continue
		}
		var fs []optimize.Finding
		for _, f := range r.Findings {
			if f.Category == cat {
				fs = append(fs, f)
			}
		}
		if len(fs) == 0 {
			continue
		}
		sort.SliceStable(fs, func(i, j int) bool { return fs[i].File < fs[j].File })
		g := tui.FindingGroup{Title: title[cat]}
		for _, f := range fs {
			loc := f.File
			if f.Line > 0 {
				loc = fmt.Sprintf("%s:%d", f.File, f.Line)
			}
			if loc == "" {
				loc = "(repo)"
			}
			g.Rows = append(g.Rows, tui.FindingRow{
				Mark: sevMark[f.Severity], Sev: string(f.Severity),
				Location: loc, Rule: f.Rule, Message: f.Message,
			})
			total++
		}
		groups = append(groups, g)
	}
	if total == 0 {
		fmt.Fprintln(out, "✓ no issues found, clean.")
		return
	}
	fmt.Fprintln(out, tui.RenderFindings(groups))
	fmt.Fprintf(out, "\n%d finding(s): %d error · %d warn · %d info\n",
		total, r.Count(optimize.SevError), r.Count(optimize.SevWarn), r.Count(optimize.SevInfo))
}
