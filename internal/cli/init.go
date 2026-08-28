package cli

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/profile"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/tui"
	"github.com/spf13/cobra"
)

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use: "init",
		Long: "Records the defaults keel pre-fills: your name, where projects live, and the\n" +
			"stack you reach for. Everything here is a starting point, not a lock-in - any\n" +
			"build can override it.\n",
		Args:  cobra.NoArgs,
		Short: "Set up your engineer profile (the defaults Keel pre-fills)",
		RunE:  func(cmd *cobra.Command, args []string) error { return runInit(cmd.OutOrStdout()) },
	}
}

// initWizard is the wizard entrypoint behind a package var: a test seam so
// runInit's profile-writing logic can be covered with canned selections instead
// of a real terminal.
var initWizard = tui.Wizard

// runInit is the setup wizard. Both `keel init` and first-run cold-start call it.
func runInit(out io.Writer) error {
	reg, err := catalog.Registry()
	if err != nil {
		return err
	}
	p, err := profile.Load()
	if err != nil {
		return err
	}

	single := func(rs []recipe.Recipe, def string) []tui.Choice {
		out := make([]tui.Choice, 0, len(rs))
		for _, r := range rs {
			out = append(out, tui.Choice{Key: r.ID, Label: r.Label, Selected: r.ID == def})
		}
		return out
	}
	// multi builds a multi-select list, pre-checking ids in the profile's list
	// (comma-separated), falling back to each recipe's own default:true.
	multi := func(rs []recipe.Recipe, csv string) []tui.Choice {
		chosen := map[string]bool{}
		for _, id := range strings.Split(csv, ",") {
			if id = strings.TrimSpace(id); id != "" {
				chosen[id] = true
			}
		}
		hasProfile := len(chosen) > 0
		// Category grouping (Services): sort by category then label and prefix
		// "Category · " so the CLI list reads grouped, matching the studio.
		rs2 := rs
		hasCat := false
		for _, r := range rs {
			if r.Category != "" {
				hasCat = true
				break
			}
		}
		if hasCat {
			rs2 = append([]recipe.Recipe(nil), rs...)
			sort.SliceStable(rs2, func(i, j int) bool {
				if rs2[i].Category != rs2[j].Category {
					return rs2[i].Category < rs2[j].Category
				}
				return rs2[i].Label < rs2[j].Label
			})
		}
		out := make([]tui.Choice, 0, len(rs2))
		for _, r := range rs2 {
			sel := r.Default
			if hasProfile {
				sel = chosen[r.ID]
			}
			label := r.Label
			if r.Category != "" {
				label = r.Category + " · " + r.Label
			}
			out = append(out, tui.Choice{Key: r.ID, Label: label, Selected: sel})
		}
		return out
	}
	editors := []tui.Choice{
		{Key: "code", Label: "VS Code"},
		{Key: "pstorm", Label: "PhpStorm"},
		{Key: "cursor", Label: "Cursor"},
		{Key: "zed", Label: "Zed"},
		{Key: "nvim", Label: "Neovim"},
		{Key: "subl", Label: "Sublime Text"},
		{Key: "", Label: "None"},
	}
	for i := range editors {
		editors[i].Selected = editors[i].Key == p.Defaults["editor"]
	}

	// Language grouping + framework-filtered env/db, exactly like `keel new` — so
	// picking Laravel only offers Laravel's envs (DDEV/Local/Sail/Docker), never
	// Django's uv-local.
	langLabel := map[string]string{"php": "PHP", "python": "Python", "other": "Other"}
	defLang := "php"
	if r, ok := reg.Get(p.Defaults["framework"]); ok && r.Lang != "" {
		defLang = r.Lang
	}
	var langOpts []tui.Choice
	for _, l := range reg.Languages() {
		lbl := langLabel[l]
		if lbl == "" {
			lbl = l
		}
		langOpts = append(langOpts, tui.Choice{Key: l, Label: lbl, Selected: l == defLang})
	}
	// Framework family grouping (WooCommerce Classic/Bedrock etc.): the framework
	// step picks the family, a "type" step picks the variant (auto-skipped when a
	// family has just one). The concrete framework id is the type step (index 2).
	familyChoices := func(rs []recipe.Recipe, def string) []tui.Choice {
		var out []tui.Choice
		for _, g := range recipe.Families(rs) {
			sel := false
			for _, v := range g.Variants {
				if v.ID == def {
					sel = true
				}
			}
			out = append(out, tui.Choice{Key: g.Primary.ID, Label: g.Primary.Label, Selected: sel})
		}
		return out
	}
	variantChoices := func(rs []recipe.Recipe, familyID, def string) []tui.Choice {
		for _, g := range recipe.Families(rs) {
			if g.Primary.ID != familyID {
				continue
			}
			pre := g.Primary.ID
			for _, v := range g.Variants {
				if v.ID == def {
					pre = def
				}
			}
			var out []tui.Choice
			for _, v := range g.Variants {
				lbl := v.Variant
				if lbl == "" {
					lbl = v.Label
				}
				out = append(out, tui.Choice{Key: v.ID, Label: lbl, Selected: v.ID == pre})
			}
			return out
		}
		return nil
	}
	fwOf := func(s [][]string) string {
		if len(s) > 2 && len(s[2]) > 0 {
			return s[2][0]
		}
		return ""
	}
	steps := []tui.Step{
		{Title: "Language", Help: "Which stack family?", Options: langOpts},
		{Title: "Default framework", Help: "Your usual stack, pre-selected on every new project.", Dynamic: func(s [][]string) []tui.Choice {
			return familyChoices(reg.FrameworksForLang(s[0][0]), p.Defaults["framework"])
		}},
		{Title: "Framework type", Help: "Setup style / variant (e.g. Classic vs Bedrock).", Dynamic: func(s [][]string) []tui.Choice {
			return variantChoices(reg.FrameworksForLang(s[0][0]), s[1][0], p.Defaults["framework"])
		}},
		{Title: "Default local dev env", Help: "How projects run locally.", Dynamic: func(s [][]string) []tui.Choice {
			fw := fwOf(s)
			return single(reg.ForFramework(fw, recipe.Env), p.Get(fw, "env"))
		}},
		{Title: "Default database", Help: "Your preferred database.", Dynamic: func(s [][]string) []tui.Choice {
			fw := fwOf(s)
			return single(reg.ForFramework(fw, recipe.DB), p.Get(fw, "database"))
		}},
		{Title: "Default frontend", Help: "Add a decoupled frontend by default? (Magento defaults to Hyvä.)", Dynamic: func(s [][]string) []tui.Choice {
			fw := fwOf(s)
			fes := reg.ForFramework(fw, recipe.Frontend)
			def := p.Defaults["frontend"]
			if def == "" { // no saved choice: pre-select the framework's default front end (e.g. Hyvä)
				for _, r := range fes {
					if r.IsDefaultFor(fw) {
						def = r.ID
					}
				}
			}
			opts := []tui.Choice{{Key: "", Label: "None", Selected: def == ""}}
			return append(opts, single(fes, def)...)
		}},
		// Its own question, because NGINX and Apache are alternatives: ticking
		// both fails to resolve, ticking neither builds a stack with no
		// published port. Saved under its own profile key rather than into the
		// services list, because declining has to be distinguishable from never
		// being asked - see profile.NoWebServer.
		{Title: "Default web server", Help: "Fronts every project - the only way in.", Dynamic: func(s [][]string) []tui.Choice {
			fw := fwOf(s)
			services := reg.ForFramework(fw, recipe.Service)
			// Shared with the new-project wizard, so the two cannot disagree
			// about what your profile already says.
			offered := tui.WebServersFor(reg, fw, services)
			chosen, answered := tui.SavedWebServer(offered, p)
			var out []tui.Choice
			for _, r := range offered {
				sel := r.ID == chosen
				if !answered { // never answered: offer the framework's default
					sel = r.IsDefaultFor(fw)
				}
				out = append(out, tui.Choice{Key: r.ID, Label: r.Label, Selected: sel})
			}
			return append(out, tui.Choice{
				Key:      profile.NoWebServer,
				Label:    "None (I'll front it myself)",
				Selected: answered && chosen == profile.NoWebServer,
			})
		}},
		{Title: "Default services", Multi: true, Help: "Backing services wired into every new project.", Dynamic: func(s [][]string) []tui.Choice {
			return multi(tui.OtherServices(reg.ForFramework(fwOf(s), recipe.Service)), p.Defaults["services"])
		}},
		{Title: "Default add-ons", Multi: true, Help: "Packages added to every new project.", Dynamic: func(s [][]string) []tui.Choice {
			return multi(reg.ForFramework(fwOf(s), recipe.Addon), p.Defaults["addons"])
		}},
		{Title: "Default extras", Multi: true, Help: "CI, AI rules, git init, etc.", Dynamic: func(s [][]string) []tui.Choice {
			return multi(reg.ForFramework(fwOf(s), recipe.Extra), p.Defaults["extras"])
		}},
		{Title: "Editor", Help: "Launched by `keel open`.", Options: editors},
	}

	res, err := initWizard("keel", "let's set your defaults once - every project starts from these", steps)
	if err != nil {
		if errors.Is(err, tui.ErrCancelled) {
			fmt.Fprintln(out, "cancelled")
			return nil
		}
		return err
	}
	// res: [language, framework-family, framework-type, env, db, frontend,
	//       web-server, services, addons, extras, editor]
	// The concrete framework id is the type step (res[2]); it auto-selects for single-variant frameworks.
	p.Defaults["framework"], p.Defaults["env"], p.Defaults["database"] = res[2][0], res[3][0], res[4][0]
	p.Defaults["frontend"] = first(res[5])
	// The web server gets its own key, and the answer is always written - the
	// opt-out included. That is the whole point of the key: a services list with
	// no web server in it cannot say whether you declined one or were never
	// asked, and guessing wrong builds a stack nothing can reach.
	p.Defaults["webserver"] = first(res[6])
	// Overwritten, not merged, so a web server left in an older profile's list
	// is dropped: the services question offers only non-web services, so it can
	// never come back, and the two can never disagree about what fronts a build.
	p.Defaults["services"] = strings.Join(res[7], ",")
	p.Defaults["addons"] = strings.Join(res[8], ",")
	p.Defaults["extras"] = strings.Join(res[9], ",")
	p.Defaults["editor"] = first(res[10])
	// Git identity comes from `git config` - no need to ask.
	if p.Git.Name == "" {
		p.Git.Name = gitConfig("user.name")
	}
	if p.Git.Email == "" {
		p.Git.Email = gitConfig("user.email")
	}
	if err := p.Save(); err != nil {
		return err
	}
	fmt.Fprintln(out, "saved profile ->", profile.Path())
	return nil
}

// csvContains indexes a saved comma-separated profile list for lookup.
func csvContains(list string) map[string]bool {
	out := map[string]bool{}
	for _, id := range strings.Split(list, ",") {
		if id = strings.TrimSpace(id); id != "" {
			out[id] = true
		}
	}
	return out
}

// first returns the first element of a wizard answer slice (or "" for none).
func first(s []string) string {
	if len(s) > 0 {
		return s[0]
	}
	return ""
}

func gitConfig(key string) string {
	out, _ := exec.Command("git", "config", key).Output()
	return strings.TrimSpace(string(out))
}
