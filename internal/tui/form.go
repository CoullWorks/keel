// Package tui is Keel's Charm-based interactive front door: a stepped,
// mouse-and-keyboard wizard (see wizard.go) plus styled renders.
package tui

import (
	"sort"
	"strings"

	"github.com/coullworks/keel/internal/profile"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

// SelectRecipes runs the new-project wizard — framework first, then env / db /
// add-ons / services / extras filtered to that framework and pre-selected from
// the profile — and returns the chosen recipe ids alongside the raw wizard
// result.
//
// The raw result is returned as well as the ids because it also carries the
// brand-colour answer, which is not a recipe (IDsFromResult drops it) but is
// needed post-build by ApplyBrand. The caller threads res to that apply point.
//
// The steps themselves come from BuildSteps, which the console drives with its
// own renderer. Both front doors therefore ask the same questions in the same
// order from the same data: a step added here appears in both.
func SelectRecipes(reg *recipe.Registry, prof *profile.Profile) (ids []string, res [][]string, err error) {
	steps := BuildSteps(reg, prof)
	res, err = Wizard("New project", "compose a stack: keel builds it, wired and ready to run", steps)
	if err != nil {
		return nil, nil, err
	}
	return IDsFromResult(res), res, nil
}

// WebServers and OtherServices split a framework's services on the webserver
// capability. Split by what a recipe provides rather than by its id, so a pack
// that ships its own web server lands in the right question without keel
// knowing its name.
//
// Exported because `keel init` asks the same pair of questions when it records
// your default stack, and both wizards must offer each recipe exactly once.
func WebServers(rs []recipe.Recipe) []recipe.Recipe {
	return filterServices(rs, true)
}

func OtherServices(rs []recipe.Recipe) []recipe.Recipe {
	return filterServices(rs, false)
}

// WebServersFor returns the ingress recipes to offer in the single-select Web
// server step for framework fw, pruned by whether the framework serves its own
// traffic — the webserver analogue of the Database step's env-prune.
//
// A PHP or Python app has no HTTP listener of its own, so its ingress is a
// FRONT CONTROLLER (Apache/NGINX mounting the language handler). A Node app IS
// its own server, so offering it a PHP front controller builds a stack fronting
// an app the front controller has nothing to mount — the bug this fixes. A
// self-serving framework is therefore offered its PROCESS MANAGER (PM2) and any
// self-serving-scoped web server a pack ships, and never the shared front
// controllers. An optional reverse proxy is a separate Service (it provides
// reverse-proxy, not webserver), so it is not in this list.
//
// Non-self-serving frameworks keep exactly the old behaviour: every recipe that
// IsWebServer, which is what shipped before the split.
func WebServersFor(reg *recipe.Registry, fw string, rs []recipe.Recipe) []recipe.Recipe {
	all := WebServers(rs)
	if !reg.FrameworkSelfServes(fw) {
		return all
	}
	out := make([]recipe.Recipe, 0, len(all))
	for _, r := range all {
		// Self-serving apps do not want a front controller. Everything else a
		// web server can be (process manager) stays.
		if !r.IsFrontController() {
			out = append(out, r)
		}
	}
	return out
}

// SavedWebServer is the web server a profile has already chosen, and whether it
// answered the question at all.
//
// Both wizards need this and must agree: `keel init` records the answer and the
// new-project wizard pre-selects from it, so a profile that says Apache must not
// be shown NGINX ticked.
//
// Three states, and the difference matters. A profile written before this key
// existed kept its answer in the services list, so that is read as the answer
// rather than being dropped for the default; an empty key with nothing in the
// list means the question was never put, and the caller falls back to the
// framework's default. Only profile.NoWebServer means declined - reading an
// unanswered profile as a decline is what built stacks with nothing listening.
func SavedWebServer(rs []recipe.Recipe, prof *profile.Profile) (id string, answered bool) {
	if saved := strings.TrimSpace(prof.Defaults["webserver"]); saved != "" {
		return saved, true
	}
	inList := map[string]bool{}
	for _, s := range strings.Split(prof.Defaults["services"], ",") {
		if s = strings.TrimSpace(s); s != "" {
			inList[s] = true
		}
	}
	for _, r := range WebServers(rs) {
		if inList[r.ID] {
			return r.ID, true
		}
	}
	return "", false
}

func filterServices(rs []recipe.Recipe, wantWeb bool) []recipe.Recipe {
	out := make([]recipe.Recipe, 0, len(rs))
	for _, r := range rs {
		if r.IsWebServer() == wantWeb {
			out = append(out, r)
		}
	}
	return out
}

// webServerStep is the index of the "Web server" step in BuildSteps. That step
// is single-select and always ends with a profile.NoWebServer "None" option, so
// its answer is either a web server id or profile.NoWebServer (an explicit
// decline). Kept next to the step it names so the two cannot drift: reorder the
// steps and this constant moves with them. DeclinedWebServer reads it.
const webServerStep = 4

// envStep is the index of the "Local dev environment" step in BuildSteps. The
// Database step (which follows it) prunes its options against this answer, so
// an env-incompatible database (e.g. Supabase under a compose env) is never
// offered. Kept beside webServerStep so a reorder of the steps moves both.
const envStep = 3

// DeclinedWebServer reports whether the wizard result explicitly chose "None" on
// the web server step. This is the interactive twin of profile.NoWebServer: a
// stack with no ingress is unreachable, so runNewInteractive re-adds the
// framework default unless the user actually declined here. "Answered with a web
// server" and "answered None" both leave a definite trace; the ambiguous middle
// case the flag path guards against (a stale profile that never saw the question)
// cannot arise in the wizard, which always asks.
//
// The decline sentinel is profile.NoWebServer, the same value `keel init` records
// and SavedWebServer reads, so a "None" chosen here round-trips as a genuine
// decline rather than an empty answer the profile path cannot tell from silence.
func DeclinedWebServer(res [][]string) bool {
	if len(res) <= webServerStep {
		return false
	}
	ans := res[webServerStep]
	// A single-select step yields exactly one answer; profile.NoWebServer is the
	// "None" sentinel.
	return len(ans) == 1 && ans[0] == profile.NoWebServer
}

// IDsFromResult turns a completed BuildSteps result into recipe ids, including
// the version answers. Those carry a prefix the resolver strips into the plan;
// keeping them here is what records the chosen version in the manifest. Brand
// answers are dropped here (they are not recipes — BrandFromResult reads them).
func IDsFromResult(res [][]string) []string {
	var ids []string
	for i, keys := range res {
		if i == 0 || i == 1 { // 0 = language, 1 = framework family — groupings, not recipes (the concrete framework is the type step at index 2)
			continue
		}
		for _, k := range keys {
			// "" = the "no frontend" sentinel; profile.NoWebServer = the "None"
			// web-server decline (a sentinel, not a recipe id, so it must not reach
			// the resolver); brand:* is a colour choice, not a recipe.
			if k != "" && k != profile.NoWebServer && !strings.HasPrefix(k, brandPrefix) {
				ids = append(ids, k)
			}
		}
	}
	return ids
}

// BuildSteps returns the new-project wizard steps. Exported so the console can
// render the same questions inside its own shell rather than re-deriving them,
// which is what keeps the CLI and the studio genuinely the same tool.
func BuildSteps(reg *recipe.Registry, prof *profile.Profile) []Step {
	single := func(rs []recipe.Recipe, def string) []Choice {
		out := make([]Choice, 0, len(rs))
		for _, r := range rs {
			out = append(out, Choice{Key: r.ID, Label: r.Label, Selected: r.ID == def})
		}
		return out
	}
	multi := func(rs []recipe.Recipe) []Choice {
		out := make([]Choice, 0, len(rs))
		for _, r := range rs {
			out = append(out, Choice{Key: r.ID, Label: r.Label, Selected: r.Default})
		}
		return out
	}
	// catMulti is multi() with category grouping: sort by category then label and
	// prefix "Category · " so the flat CLI list reads grouped (Services in studio
	// use the same categories — CLI/studio parity).
	catMulti := func(rs []recipe.Recipe) []Choice {
		s := append([]recipe.Recipe(nil), rs...)
		sort.SliceStable(s, func(i, j int) bool {
			if s[i].Category != s[j].Category {
				return s[i].Category < s[j].Category
			}
			return s[i].Label < s[j].Label
		})
		out := make([]Choice, 0, len(s))
		for _, r := range s {
			label := r.Label
			if r.Category != "" {
				label = r.Category + " · " + r.Label
			}
			out = append(out, Choice{Key: r.ID, Label: label, Selected: r.Default})
		}
		return out
	}

	// Framework family grouping: variants sharing a Family (e.g. WooCommerce
	// Classic + Bedrock) collapse into one "Framework" entry, then a "Framework
	// type" step picks the variant. Primary = the recipe whose id == family.
	familyChoices := func(rs []recipe.Recipe, def string) []Choice {
		var out []Choice
		for _, g := range recipe.Families(rs) {
			sel := false
			for _, v := range g.Variants {
				if v.ID == def {
					sel = true
				}
			}
			out = append(out, Choice{Key: g.Primary.ID, Label: g.Primary.Label, Selected: sel})
		}
		return out
	}
	// variants of the family whose primary id == familyID; pre-select def if it's
	// one of them, else the primary. A single-variant family yields one option, so
	// the wizard auto-skips this step.
	variantChoices := func(rs []recipe.Recipe, familyID, def string) []Choice {
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
			var out []Choice
			for _, v := range g.Variants {
				lbl := v.Variant
				if lbl == "" {
					lbl = v.Label
				}
				out = append(out, Choice{Key: v.ID, Label: lbl, Selected: v.ID == pre})
			}
			return out
		}
		return nil
	}
	// The concrete framework id comes from the "Framework type" step (index 2);
	// the Framework step (1) only picks the family.
	fwOf := func(s [][]string) string {
		if len(s) > 2 && len(s[2]) > 0 {
			return s[2][0]
		}
		return ""
	}
	// envOf is the env recipe id chosen at the "Local dev environment" step
	// (index 3). It is chosen before the Database step (index 5), so the database
	// options can be pruned against it. "" when the step has not been answered
	// yet (e.g. a caller invoking a later step's Dynamic with a short prior).
	envOf := func(s [][]string) string {
		if len(s) > envStep && len(s[envStep]) > 0 {
			return s[envStep][0]
		}
		return ""
	}
	// dbChoices returns the Database options for the chosen framework, pruned to
	// those that can actually be built with the env already chosen. Supabase, for
	// instance, only provisions its local stack, so under a compose env it is
	// hidden rather than offered and left to die in resolver.Resolve. The doctrine
	// is docs/ROUTES.md §4: never build an invalid combo — filter it out up front.
	//
	// With no env answered yet, nothing is pruned (there is nothing to prune
	// against): resolver.SeedableWith needs the env in the base to know a db's
	// provision applies. The default pre-selection survives only if it is one of
	// the options that remains after pruning.
	dbChoices := func(s [][]string) []Choice {
		fw := fwOf(s)
		def := prof.Get(fw, "database")
		all := reg.ForFramework(fw, recipe.DB)
		env := envOf(s)
		if env == "" {
			return single(all, def)
		}
		base := []string{fw, env}
		kept := make([]recipe.Recipe, 0, len(all))
		for _, r := range all {
			if resolver.SeedableWith(reg, base, r) {
				kept = append(kept, r)
			}
		}
		return single(kept, def)
	}
	// Every language the catalogue can return needs an entry, or it renders as
	// the raw id next to properly-cased siblings ("PHP, Python, node").
	langLabel := map[string]string{"php": "PHP", "python": "Python", "node": "Node.js", "go": "Go", "other": "Other"}
	defLang := "php"
	if r, ok := reg.Get(prof.Defaults["framework"]); ok && r.Lang != "" {
		defLang = r.Lang
	}
	var langOpts []Choice
	for _, l := range reg.Languages() {
		lbl := langLabel[l]
		if lbl == "" {
			lbl = l
		}
		langOpts = append(langOpts, Choice{Key: l, Label: lbl, Selected: l == defLang})
	}

	// langOf/famOf mirror fwOf/envOf: the chosen language (step 0) and framework
	// family (step 1), or "" when the step has not been answered — which happens
	// when an empty catalogue leaves a single-select step with no options, so the
	// wizard auto-selects it to nil. Reading s[0][0] raw would panic there; a ""
	// yields empty choices instead, so a broken/empty registry degrades to "no
	// frameworks to offer" rather than a crash.
	langOf := func(s [][]string) string {
		if len(s) > 0 && len(s[0]) > 0 {
			return s[0][0]
		}
		return ""
	}
	famOf := func(s [][]string) string {
		if len(s) > 1 && len(s[1]) > 0 {
			return s[1][0]
		}
		return ""
	}

	steps := []Step{
		{Title: "Language", Help: "Which stack family?", Options: langOpts},
		{Title: "Framework", Help: "What are you building?", Dynamic: func(s [][]string) []Choice {
			return familyChoices(reg.FrameworksForLang(langOf(s)), prof.Defaults["framework"])
		}},
		{Title: "Framework type", Help: "Setup style / variant (e.g. Classic vs Bedrock).", Dynamic: func(s [][]string) []Choice {
			return variantChoices(reg.FrameworksForLang(langOf(s)), famOf(s), prof.Defaults["framework"])
		}},
		{Title: "Local dev environment", Help: "Where it runs on your machine.", Dynamic: func(s [][]string) []Choice {
			fw := fwOf(s)
			return single(reg.ForFramework(fw, recipe.Env), prof.Get(fw, "env"))
		}},
		// One question, not two tick-boxes. NGINX and Apache conflict, so
		// offering them in the Services list let you tick both and only find out
		// at resolve time. It also let you tick neither, which builds a stack
		// with no published port at all: healthy containers you cannot open.
		//
		// The options are pruned by framework the same way the Database step is
		// pruned by env: a PHP or Python app has no listener of its own, so its
		// answer is a front controller (Apache/NGINX + the language handler); a
		// self-serving Node app already listens for itself, so its answer is a
		// process manager (PM2), never a PHP front controller that would build a
		// stack fronting an app with nothing to mount. WebServersFor does that
		// split; both wizards call it so the CLI and studio agree.
		{Title: "Web server", Help: "How the app is served - the only way in. Pick one.", Dynamic: func(s [][]string) []Choice {
			fw := fwOf(s)
			services := reg.ForFramework(fw, recipe.Service)
			offered := WebServersFor(reg, fw, services)
			// Your saved answer wins, like every other step here. Without this
			// the question ignored the profile entirely, so an engineer whose
			// default is Apache was shown NGINX ticked on every new project.
			saved, answered := SavedWebServer(offered, prof)
			var out []Choice
			for _, r := range offered {
				sel := r.ID == saved
				if !answered { // never asked: offer the framework's default
					sel = r.IsDefaultFor(fw)
				}
				out = append(out, Choice{Key: r.ID, Label: r.Label, Selected: sel})
			}
			// Last, and pre-selected only if you have actually declined before:
			// choosing it means the app serves its own traffic, or you are
			// wiring your own proxy. Keyed profile.NoWebServer — the same value
			// `keel init` records — so a "None" chosen here round-trips as a
			// genuine decline that SavedWebServer recognises, rather than the empty
			// key it could not tell from an unanswered profile.
			return append(out, Choice{
				Key:      profile.NoWebServer,
				Label:    "None (I'll front it myself)",
				Selected: answered && saved == profile.NoWebServer,
			})
		}},
		// The database options are pruned against the env chosen two steps back:
		// an env can bring up its own database (compose) or run none at all, and a
		// database whose provisioning cannot satisfy that env would only fail at
		// resolve time. dbChoices hides those, so the wizard never lets you compose
		// an invalid combo (docs/ROUTES.md §4).
		{Title: "Database", Help: "The primary database.", Dynamic: dbChoices},
		{Title: "Add-ons", Multi: true, Help: "Optional packages. Click or space to toggle.", Dynamic: func(s [][]string) []Choice {
			return multi(reg.ForFramework(fwOf(s), recipe.Addon))
		}},
		{Title: "Services", Multi: true, Help: "Backing services, grouped Search / Cache / Queue / Storage / Database.", Dynamic: func(s [][]string) []Choice {
			// Web servers are excluded: they have their own single-select step,
			// because they are alternatives rather than additions.
			return catMulti(OtherServices(reg.ForFramework(fwOf(s), recipe.Service)))
		}},
		{Title: "Frontend", Help: "Add a decoupled Next.js frontend? (monorepo)", Dynamic: func(s [][]string) []Choice {
			opts := []Choice{{Key: "", Label: "No frontend (API / monolith only)", Selected: true}}
			for _, r := range reg.ForFramework(fwOf(s), recipe.Frontend) {
				opts = append(opts, Choice{Key: r.ID, Label: r.Label})
			}
			return opts
		}},
		{Title: "Extras", Multi: true, Help: "Tooling, git, AI-assistant config.", Dynamic: func(s [][]string) []Choice {
			return multi(reg.ForFramework(fwOf(s), recipe.Extra))
		}},
		// Brand comes after the recipe steps so a UI kit chosen in Add-ons or
		// Frontend is already known. With no UI kit in the plan the step resolves
		// to one option and the wizard auto-skips it — no brand question where
		// there's nothing to brand. brand.Apply runs post-build via ApplyBrand.
		{Title: "Brand colours", Help: "Pick a palette for your UI kit (applied after the build).", Dynamic: func(s [][]string) []Choice {
			return brandChoices(reg, s)
		}},
	}

	// Version steps come last, because what can be chosen depends on everything
	// chosen before it: the PHP question belongs to the framework, the engine
	// version to the database that was picked two steps earlier.
	//
	// One step per pin name offered by the selected recipes. A recommendation is
	// pre-ticked, so the common case is still one Enter, and the alternatives are
	// right there for anyone who wants them.
	for _, name := range versionStepNames(reg) {
		name := name
		steps = append(steps, Step{
			Title: versionTitle(reg, name),
			Help:  "Recommended is pre-selected. The others are supported, not second-class.",
			Dynamic: func(s [][]string) []Choice {
				vc, ok := recipe.VersionOptions(chosenRecipes(reg, s), fwOf(s))[name]
				if !ok {
					return nil // not offered by this stack; the step renders empty and is skipped
				}
				out := make([]Choice, 0, len(vc.Options))
				for _, o := range vc.Options {
					label := o.Label
					if label == "" {
						label = o.Value
					}
					if o.Note != "" {
						label += "  (" + o.Note + ")"
					}
					out = append(out, Choice{Key: versionKey(name, o.Value), Label: label, Selected: o.Value == vc.Recommended})
				}
				return out
			},
		})
	}

	return steps
}
