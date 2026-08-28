// Package resolver turns a set of chosen recipe ids into a validated, ordered
// Plan. It enforces the compatibility rules from docs/ROUTES.md: exactly one
// framework, one env and one database, every recipe must apply to that
// framework, every Requires must be satisfied by some selected recipe's
// Provides, and no Conflicts may be present.
//
// It also assembles the effective recipes the engine will run: an id or alias
// resolves to its recipe, a recipe's env-family provisioning is merged in, and
// the config recipes matching the plan are injected. By the time a Plan leaves
// here, the engine can treat every recipe as a flat list of steps, files and
// patches, with no knowledge of families or overlays.
package resolver

import (
	"fmt"
	"sort"
	"strings"

	"github.com/coullworks/keel/internal/recipe"
)

// Plan is an ordered, validated set of recipes ready for the engine to execute.
type Plan struct {
	Framework string
	Recipes   []recipe.Recipe

	// Vocab is every command token the loaded catalogue defines, from any env.
	// A token an env does not define renders empty (so the step is skipped),
	// while a token nobody defines stays literal and surfaces the typo. Carried
	// on the plan so the engine learns the vocabulary from the recipes rather
	// than from a hardcoded list that packs could not extend.
	Vocab []string

	// Versions are the version choices the user made, keyed by pin name. The
	// engine applies them over the recipes' own pins, so a recipe keeps reading
	// {{pin.php}} whether the value was fixed or chosen.
	Versions map[string]string
}

// IDs returns the plan's recipe ids in execution order.
func (p *Plan) IDs() []string {
	out := make([]string, len(p.Recipes))
	for i, r := range p.Recipes {
		out[i] = r.ID
	}
	return out
}

// EnvFamily returns the family of the plan's env recipe (ddev, compose, sail,
// local), or "" when the plan has no env.
func (p *Plan) EnvFamily() string {
	for _, r := range p.Recipes {
		if r.Kind == recipe.Env {
			return envFamilyOf(r)
		}
	}
	return ""
}

// envFamilyOf reports an env recipe's family, defaulting to its id so an env
// from a schema v1 pack still has a stable family name.
func envFamilyOf(r recipe.Recipe) string {
	if r.EnvFamily != "" {
		return r.EnvFamily
	}
	return r.ID
}

// Resolve validates ids against reg and returns an ordered Plan, or an error
// explaining the first rule violated.
func Resolve(reg *recipe.Registry, ids []string) (*Plan, error) {
	// Version answers ride along with the recipe ids. Strip them out first, so
	// everything downstream sees the recipe list it always saw and an unknown-id
	// error still means an unknown recipe.
	versions := map[string]string{}
	recipeIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		if name, value, ok := recipe.ParseVersionToken(id); ok {
			versions[name] = value
			continue
		}
		recipeIDs = append(recipeIDs, id)
	}
	ids = recipeIDs

	picked, framework, err := pick(reg, ids)
	if err != nil {
		return nil, err
	}

	provided := map[string]bool{}
	for _, r := range picked {
		provided[r.ID] = true
		for _, cap := range r.Provides {
			provided[cap] = true
		}
	}

	picked = append(picked, matchingConfigs(reg, picked, framework, provided)...)

	family := ""
	for _, r := range picked {
		if r.Kind == recipe.Env {
			family = envFamilyOf(r)
		}
	}
	for i := range picked {
		if err := applyProvision(reg, framework, &picked[i], family); err != nil {
			return nil, err
		}
	}

	for _, r := range picked {
		if !r.AppliesToFramework(framework) {
			return nil, fmt.Errorf("%s does not apply to framework %s", r.ID, framework)
		}
		for _, req := range r.Requires {
			if !provided[req] {
				return nil, fmt.Errorf("%s requires %q, which is not selected", r.ID, req)
			}
		}
		for _, c := range r.Conflicts {
			if !provided[c] {
				continue
			}
			// db-<name> is the marker an environment uses for the database it
			// brings up itself. Reporting the raw capability ("conflicts with
			// db-mysql") tells the user nothing they can act on.
			if other, isDB := strings.CutPrefix(c, "db-"); isDB {
				return nil, fmt.Errorf("%s cannot be used here: %s already brings up %s",
					r.ID, providerOf(picked, c), other)
			}
			return nil, fmt.Errorf("%s conflicts with %q", r.ID, c)
		}
	}

	sort.SliceStable(picked, func(i, j int) bool {
		return picked[i].Kind.Rank() < picked[j].Kind.Rank()
	})
	return &Plan{Framework: framework, Recipes: picked, Vocab: reg.VocabKeys(), Versions: versions}, nil
}

// pick looks up each id (or alias) and enforces the one-of rules. Two envs or
// two databases used to be accepted, with the engine silently using whichever
// sorted first.
func pick(reg *recipe.Registry, ids []string) ([]recipe.Recipe, string, error) {
	var picked []recipe.Recipe
	seen := map[string]bool{}
	framework, env, db := "", "", ""

	for _, id := range ids {
		r, ok := reg.Get(id)
		if !ok {
			return nil, "", fmt.Errorf("unknown recipe %q", id)
		}
		if r.Kind == recipe.Config {
			// Injected from the rest of the plan, never chosen. Ignore it rather
			// than failing: a manifest written by an older keel may list one, and
			// the matching pass adds it back regardless.
			continue
		}
		if seen[r.ID] {
			continue // the same recipe named twice (often via an alias) is not an error
		}
		seen[r.ID] = true

		switch r.Kind {
		case recipe.Framework:
			if framework != "" {
				return nil, "", fmt.Errorf("more than one framework selected (%s and %s)", framework, r.ID)
			}
			framework = r.ID
		case recipe.Env:
			if env != "" {
				return nil, "", fmt.Errorf("more than one environment selected (%s and %s)", env, r.ID)
			}
			env = r.ID
		case recipe.DB:
			if db != "" {
				return nil, "", fmt.Errorf("more than one database selected (%s and %s)", db, r.ID)
			}
			db = r.ID
		}
		picked = append(picked, r)
	}
	if framework == "" {
		return nil, "", fmt.Errorf("no framework selected")
	}
	return picked, framework, nil
}

// matchingConfigs returns the config recipes that apply to this plan. They are
// never chosen by the user: a config recipe is how a framework contributes its
// own wiring for a shared database or service, so the database does not need to
// know which frameworks exist and vice versa.
func matchingConfigs(reg *recipe.Registry, picked []recipe.Recipe, framework string, provided map[string]bool) []recipe.Recipe {
	env := ""
	family := ""
	for _, r := range picked {
		if r.Kind == recipe.Env {
			env, family = r.ID, envFamilyOf(r)
		}
	}
	var out []recipe.Recipe
	for _, r := range reg.OfKind(recipe.Config) {
		w := r.When
		if w == nil {
			continue
		}
		if !w.MatchesFramework(framework) {
			continue
		}
		if w.Uses != "" && !provided[w.Uses] {
			continue
		}
		if w.Env != "" && w.Env != env && w.Env != family {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// applyProvision folds a recipe's entry for this env family into the steps,
// files and patches the engine will run, so nothing downstream needs to know
// families exist.
//
// A recipe that describes provisioning but says nothing about the chosen env is
// an error, not a no-op. That is the check that would have caught
// "keel new django --env local --db postgres" quietly building on SQLite.
func applyProvision(reg *recipe.Registry, framework string, r *recipe.Recipe, family string) error {
	if len(r.Provision) == 0 {
		return nil
	}
	frag, ok := r.Provision[family]
	if !ok {
		// Name the env recipes the user could actually switch to, not only the
		// abstract families: "provisions for: local" tells you which family works
		// but "try --env django-local" tells you the flag to type. Fall back to
		// the families when the catalogue has no concrete env for them.
		envs := CompatibleEnvs(reg, framework, *r)
		if len(envs) == 0 {
			envs = sortedFamilies(r.Provision)
		}
		return fmt.Errorf("%s does not support the %s environment; it needs one of: %s",
			r.ID, familyLabel(family), strings.Join(envs, ", "))
	}
	r.Install = append(append([]string{}, r.Install...), frag.Install...)
	r.Files = append(append([]recipe.File{}, r.Files...), frag.Files...)
	r.Patch = append(append([]recipe.Patch{}, r.Patch...), frag.Patch...)
	return nil
}

// CompatibleEnvs returns the ids of framework's env recipes whose family this
// recipe can provision for, sorted. A recipe with no Provision map works under
// every env, so all the framework's envs are returned. Used to turn an abstract
// "provisions for: local" into the concrete "try --env django-local", both in
// the resolve-time error and in the early flag guard in `keel new`.
func CompatibleEnvs(reg *recipe.Registry, framework string, r recipe.Recipe) []string {
	var out []string
	for _, e := range reg.ForFramework(framework, recipe.Env) {
		if len(r.Provision) == 0 {
			out = append(out, e.ID)
			continue
		}
		if _, ok := r.Provision[envFamilyOf(e)]; ok {
			out = append(out, e.ID)
		}
	}
	sort.Strings(out)
	return out
}

func familyLabel(family string) string {
	if family == "" {
		return "selected"
	}
	return family
}

func sortedFamilies(m map[string]recipe.Fragment) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// CompatibleDefault picks the recipe of a kind that actually works with what has
// already been chosen, and returns "" when nothing does. prefer names a saved
// preference to try ahead of the catalogue's defaults.
//
// Choosing a default in isolation is how keel used to build the wrong thing:
// Laravel's default database is PostgreSQL, but its Docker environment brings up
// MySQL, so the "default" was one the plan could never satisfy. Candidates are
// tried through a real Resolve, so every compatibility rule is applied once,
// here, rather than approximated at each call site.
//
// The per-candidate Resolve is affordable because the registry is immutable
// after load: the lookups it repeats — VocabKeys, OfKind(Config), ForFramework —
// are precomputed once and served from Registry's cache, so this is no longer
// "re-sort the whole registry per candidate" but a walk over already-sorted
// slices. SeedDefaults calls it per kind without that turning quadratic.
func CompatibleDefault(reg *recipe.Registry, base []string, kind recipe.Kind, framework, prefer string) string {
	candidates := reg.ForFramework(framework, kind)
	// A saved preference is tried first, then the catalogue's own defaults, then
	// anything else that fits. All three go through the same checks: a preference
	// is a preference, not an instruction, and one that cannot work here should
	// fall through rather than produce a plan the user did not ask for.
	var ordered []recipe.Recipe
	seen := map[string]bool{}
	push := func(r recipe.Recipe) {
		if !seen[r.ID] {
			seen[r.ID] = true
			ordered = append(ordered, r)
		}
	}
	if prefer != "" {
		if r, ok := reg.Get(prefer); ok && r.Kind == kind && r.AppliesToFramework(framework) {
			push(r)
		}
	}
	for _, r := range candidates {
		if r.IsDefaultFor(framework) {
			push(r)
		}
	}
	for _, r := range candidates {
		push(r)
	}
	family := familyOf(reg, base)
	for _, r := range ordered {
		if !autoSelectable(r, family) {
			// Choosing this by default would start containers behind the back of
			// someone who picked a native env precisely to avoid them. Asking for
			// it explicitly still works.
			continue
		}
		if _, err := Resolve(reg, append(append([]string{}, base...), r.ID)); err == nil {
			return r.ID
		}
	}
	return ""
}

// containerised env families run containers as their whole purpose, so a
// provision step that starts or configures one is expected rather than a
// surprise. `local` is the odd one out: it is chosen to stay off Docker.
var containerised = map[string]bool{"ddev": true, "compose": true, "sail": true}

// autoSelectable reports whether this recipe may be picked as a default under
// the given env family.
//
// Under a containerised family the answer is always yes. Install steps there are
// ordinary: DDEV configures its database through `ddev config --database=...`
// and by dropping tuning files into .ddev/, which is exactly how DDEV runs a
// database rather than evidence that it does not.
//
// Under `local` an install step is a different matter, because the family was
// chosen to avoid exactly that. A recipe whose local provisioning runs
// `docker compose up -d` is still available on request; it just will not be
// selected on someone's behalf.
func autoSelectable(r recipe.Recipe, family string) bool {
	if len(r.Provision) == 0 || containerised[family] {
		return true
	}
	frag, ok := r.Provision[family]
	return ok && len(frag.Install) == 0
}

// familyOf finds the env family among already-chosen ids.
func familyOf(reg *recipe.Registry, ids []string) string {
	for _, id := range ids {
		if r, ok := reg.Get(id); ok && r.Kind == recipe.Env {
			return envFamilyOf(r)
		}
	}
	return ""
}

// providerOf names the recipe that contributed a capability, so a conflict can
// say which choice is in the way rather than only which token clashed.
func providerOf(picked []recipe.Recipe, capability string) string {
	for _, r := range picked {
		for _, c := range r.Provides {
			if c == capability {
				return r.ID
			}
		}
	}
	return "another recipe"
}

// ConflictsWithAny reports whether recipe r cannot sit alongside the recipes
// named in ids, in either direction: r may refuse a capability they provide, or
// one of them may refuse a capability r provides.
//
// This exists so that a default never fights an explicit choice. keel seeds the
// recipes marked default for the framework, and the web server is one of them,
// because a stack with neither NGINX nor Apache publishes no port and cannot be
// opened. Seeding it blindly would mean `--with apache` produced "apache
// conflicts with nginx" and refused to build - the default overruling the thing
// the user actually asked for.
func ConflictsWithAny(reg *recipe.Registry, ids []string, r recipe.Recipe) bool {
	refuses := func(a, b recipe.Recipe) bool {
		for _, c := range a.Conflicts {
			for _, p := range b.Provides {
				if c == p {
					return true
				}
			}
		}
		return false
	}
	for _, id := range ids {
		other, ok := reg.Get(id)
		if !ok {
			continue
		}
		if refuses(r, other) || refuses(other, r) {
			return true
		}
	}
	return false
}

// SeedableWith reports whether recipe r can be added to ids without turning a
// working plan into a resolve error: it must neither conflict with anything
// already chosen, nor lack a provision for the environment family in play.
//
// SeedDefaults returns ids with the database and services a build would add on
// the user's behalf, in the order they are seeded.
//
// One rule, in one place, because three copies of it disagreed. `keel new`
// picked the database with CompatibleDefault while `keel recipes verify` used
// IsDefaultFor, and for several frameworks that means verify built a stack with
// no database at all - whose compose.yaml then referenced {{db.type}} and
// friends as literal text and was not valid YAML. Verification that composes a
// plan no user can get is worse than none: it goes green on something nobody
// runs, and red on something nobody wrote.
//
// The caller adds the framework and env; everything after that is a default,
// and a default yields rather than breaking the build.
func SeedDefaults(reg *recipe.Registry, ids []string, framework string) []string {
	if db := CompatibleDefault(reg, ids, recipe.DB, framework, ""); db != "" {
		ids = append(ids, db)
	}
	for _, s := range reg.ForFramework(framework, recipe.Service) {
		if s.IsDefaultFor(framework) && SeedableWith(reg, ids, s) {
			ids = append(ids, s.ID)
		}
	}
	return ids
}

// The rule this encodes is that a default yields. Recipes marked default are
// seeded on the user's behalf, so if one cannot apply here it steps aside
// silently. A recipe the user named explicitly still fails loudly, because
// there the mismatch is worth knowing about.
func SeedableWith(reg *recipe.Registry, ids []string, r recipe.Recipe) bool {
	if ConflictsWithAny(reg, ids, r) {
		return false
	}
	// Unmet requirements disqualify a default too. Telescope is selected by
	// default on Laravel, and briefly declared requires: [db]; a native
	// environment starts no database, so `keel new laravel --env laravel-local`
	// refused to build over an add-on the user had not asked for. An explicitly
	// named recipe still fails loudly on the same condition, because there the
	// user chose it and the mismatch is the answer they need.
	provided := map[string]bool{}
	for _, id := range ids {
		if other, ok := reg.Get(id); ok {
			for _, p := range other.Provides {
				provided[p] = true
			}
		}
	}
	for _, req := range r.Requires {
		if !provided[req] {
			return false
		}
	}
	if len(r.Provision) == 0 {
		return true // provisions the same way everywhere
	}
	// A provision map is keyed by environment family, so with no environment
	// selected there is no key to look up and nothing this recipe could
	// contribute. Seeding it anyway is how `keel new <pack-framework>` came to
	// fail on a web server nobody asked for.
	seedable := false
	for _, id := range ids {
		other, ok := reg.Get(id)
		if !ok || other.Kind != recipe.Env {
			continue
		}
		if _, supported := r.Provision[envFamilyOf(other)]; !supported {
			return false
		}
		seedable = true
	}
	return seedable
}
