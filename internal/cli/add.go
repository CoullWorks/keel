package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
	"github.com/spf13/cobra"
)

// addCmd is the lifecycle inverse of `keel new --with`: it adds a service, DB or
// UI kit to a project that already exists, instead of only at creation. `keel
// new` composes recipes once; `keel update` only re-renders what is already in
// the manifest; nothing could append one. This does.
func addCmd() *cobra.Command {
	var dryRun, yes, trust bool
	c := &cobra.Command{
		Use: "add <recipe...>",
		Example: "  keel add redis                    # add a service to the project you're in\n" +
			"  keel add pest telescope           # add several at once\n" +
			"  keel add mysql --dry-run          # show what would run, change nothing\n",
		Args:  cobra.MinimumNArgs(1),
		Short: "Add recipe(s) to a built project (the inverse of new --with)",
		Long: "Resolves the new recipe(s) together with the project's existing stack, so\n" +
			"requires and conflicts are honoured against what is already there, then\n" +
			"installs only the new recipe(s) into this project and records them in\n" +
			".keel/manifest.yaml. The rest of the project is left untouched - this is\n" +
			"not a rebuild. Adding a recipe that is already present is a no-op.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdd(cmd.Context(), cmd.OutOrStdout(), args, dryRun, yes, trust)
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "show the steps without running them")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	c.Flags().BoolVar(&trust, "trust", false, "run untrusted pack recipes without prompting")
	return c
}

// removeCmd is the symmetric inverse: drop recipe(s) from the manifest. It does
// not uninstall — a service's files and packages are the framework's to remove,
// and guessing which are safe to delete is how a scaffolder corrupts a project.
// What it does is honest and bounded: stop managing the recipe, re-resolve the
// remaining stack so a later `keel update` no longer renders the removed one, and
// say plainly that any files it dropped are yours to clean up.
func removeCmd() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:     "remove <recipe...>",
		Aliases: []string{"rm"},
		Example: "  keel remove redis                 # stop managing a recipe in this project\n",
		Args:    cobra.MinimumNArgs(1),
		Short:   "Remove recipe(s) from a project's manifest (does not uninstall files)",
		Long: "Drops the named recipe(s) from .keel/manifest.yaml and re-resolves the rest,\n" +
			"so `keel update` no longer manages them. It does not delete files an\n" +
			"installer created - which of those are safe to remove is the framework's\n" +
			"call, not keel's - so anything the recipe wrote is left for you to remove.\n" +
			"You cannot remove the framework or the environment; those define the project.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemove(cmd.OutOrStdout(), args, yes)
		},
	}
	c.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return c
}

// runAdd resolves the new recipes together with the project's existing stack,
// installs only the delta, and records it.
func runAdd(ctx context.Context, out io.Writer, names []string, dryRun, yes, trust bool) error {
	m, err := engine.ReadManifest(".")
	if err != nil {
		return manifestErr(err)
	}
	reg, err := catalog.Registry()
	if err != nil {
		return err
	}

	// Canonicalise the requested ids up front so an alias, an unknown id, and an
	// already-present recipe are each answered before any resolve work. Ids are
	// compared canonically, so `keel add redis` on a project that already has
	// redis (however it was named) is correctly a no-op.
	want := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, n := range names {
		id, ok := reg.Canonical(n)
		if !ok {
			return fmt.Errorf("unknown recipe %q", n)
		}
		if !seen[id] {
			seen[id] = true
			want = append(want, id)
		}
	}
	have := manifestIDSet(reg, m.Recipes)
	var toAdd []string
	var already []string
	for _, id := range want {
		if have[id] {
			already = append(already, id)
			continue
		}
		toAdd = append(toAdd, id)
	}
	if len(already) > 0 {
		fmt.Fprintf(out, "already present, skipping: %s\n", strings.Join(already, ", "))
	}
	if len(toAdd) == 0 {
		fmt.Fprintln(out, "nothing to add.")
		return nil
	}

	// Resolve the whole stack (existing + new) so the new recipe is validated
	// against what is already there - a db it conflicts with, a capability it
	// requires. This is the same resolver `keel new --with` uses; an add that
	// cannot sit in this project fails here rather than half-installing.
	fullPlan, err := resolver.Resolve(reg, append(append([]string{}, m.Recipes...), toAdd...))
	if err != nil {
		return fmt.Errorf("adding %s to this project: %w", strings.Join(toAdd, ", "), err)
	}
	// Resolve the project as it stands, to isolate exactly what the add
	// introduces - the named recipes plus any config overlay they pull in.
	basePlan, err := resolver.Resolve(reg, m.Recipes)
	if err != nil {
		return fmt.Errorf("re-resolving this project's recipes: %w", err)
	}
	delta := planDelta(fullPlan, basePlan)
	if len(delta) == 0 {
		// Everything the resolve added was already installed (e.g. only config
		// recipes that this project already carries). Nothing left to do.
		fmt.Fprintln(out, "nothing to add.")
		return nil
	}

	fmt.Fprintf(out, "adding to %s: %s\n", fullPlan.Framework, strings.Join(deltaIDs(delta), ", "))
	for _, s := range engine.ApplySteps(fullPlan, delta) {
		fmt.Fprintf(out, "  → %s\n", s)
	}

	// Consent gate mirrors `keel new`: installing a pack/url recipe into an
	// existing project runs its shell, so an untrusted delta needs an explicit
	// yes. --trust opts in; a fully built-in/user delta needs no prompt.
	consented := trust
	if !dryRun && !engine.RecipesTrusted(delta) && !trust {
		fmt.Fprintln(out, "\n⚠ This includes recipes from an untrusted pack.")
		if !confirmOrYes(yes, "Run these untrusted recipes?") {
			fmt.Fprintln(out, "cancelled")
			return nil
		}
		consented = true
	}
	if !dryRun && !confirmOrYes(yes, fmt.Sprintf("Add %s here?", strings.Join(deltaIDs(delta), ", "))) {
		fmt.Fprintln(out, "cancelled")
		return nil
	}

	if err := engine.Apply(ctx, fullPlan, delta, engine.Options{
		Dir:      ".",
		DryRun:   dryRun,
		DockerUp: DockerRunning,
		Out:      out,
		Trusted:  consented,
	}); err != nil {
		return err
	}
	if dryRun {
		fmt.Fprintln(out, "(dry-run) nothing written.")
		return nil
	}
	// Record only what the user named (toAdd); injected config recipes are
	// derived on every resolve, exactly as a fresh build records them.
	if err := engine.RecordAdd(".", fullPlan, m, toAdd); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ added %s\n", strings.Join(toAdd, ", "))
	return nil
}

// runRemove drops recipe(s) from the manifest and re-resolves the rest.
func runRemove(out io.Writer, names []string, yes bool) error {
	m, err := engine.ReadManifest(".")
	if err != nil {
		return manifestErr(err)
	}
	reg, err := catalog.Registry()
	if err != nil {
		return err
	}
	have := manifestIDSet(reg, m.Recipes)
	drop := map[string]bool{}
	var missing []string
	for _, n := range names {
		id, ok := reg.Canonical(n)
		if !ok {
			return fmt.Errorf("unknown recipe %q", n)
		}
		if !have[id] {
			missing = append(missing, n)
			continue
		}
		// The framework and the environment define the project; removing either
		// leaves a manifest that no longer resolves. Refuse plainly rather than
		// write a broken record.
		if r, ok := reg.Get(id); ok && (r.Kind == recipe.Framework || r.Kind == recipe.Env) {
			return fmt.Errorf("cannot remove %s: the %s is what defines this project", id, r.Kind)
		}
		drop[id] = true
	}
	if len(missing) > 0 {
		fmt.Fprintf(out, "not in this project, skipping: %s\n", strings.Join(missing, ", "))
	}
	if len(drop) == 0 {
		fmt.Fprintln(out, "nothing to remove.")
		return nil
	}

	// Keep the manifest ids the user did not ask to drop, matched canonically so
	// an alias in the manifest is removed by its canonical name.
	var kept []string
	var removed []string
	for _, id := range m.Recipes {
		canon, _ := reg.Canonical(id)
		if drop[canon] {
			removed = append(removed, id)
			continue
		}
		kept = append(kept, id)
	}
	// The remaining stack must still resolve, or the removal would leave the
	// project unmanageable (a service still requiring the one just dropped).
	if _, err := resolver.Resolve(reg, kept); err != nil {
		return fmt.Errorf("removing %s would leave an unresolvable project: %w", strings.Join(removed, ", "), err)
	}

	fmt.Fprintf(out, "removing from the manifest: %s\n", strings.Join(removed, ", "))
	fmt.Fprintln(out, "  (files these recipes installed are left in place - remove them yourself if you want them gone)")
	if !confirmOrYes(yes, "Remove these from the manifest?") {
		fmt.Fprintln(out, "cancelled")
		return nil
	}

	m.Recipes = kept
	if err := engine.WriteManifestFile(".", m); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ removed %s from the manifest\n", strings.Join(removed, ", "))
	return nil
}

// manifestIDSet is the set of recipe ids a manifest holds, canonicalised so an
// alias and its recipe count as the same membership.
func manifestIDSet(reg *recipe.Registry, ids []string) map[string]bool {
	set := map[string]bool{}
	for _, id := range ids {
		if canon, ok := reg.Canonical(id); ok {
			set[canon] = true
		} else {
			set[id] = true
		}
	}
	return set
}

// planDelta returns the recipes in full that the base plan does not already
// hold, in full's execution order. This is what an add introduces: the named
// recipes plus any config overlay the resolver injected for them.
func planDelta(full, base *resolver.Plan) []recipe.Recipe {
	inBase := map[string]bool{}
	for _, r := range base.Recipes {
		inBase[r.ID] = true
	}
	var out []recipe.Recipe
	for _, r := range full.Recipes {
		if !inBase[r.ID] {
			out = append(out, r)
		}
	}
	return out
}

// deltaIDs lists the delta's recipe ids, sorted for a stable message.
func deltaIDs(delta []recipe.Recipe) []string {
	out := make([]string, 0, len(delta))
	for _, r := range delta {
		out = append(out, r.ID)
	}
	sort.Strings(out)
	return out
}

// confirmOrYes prompts through the confirm seam unless yes is set. A prompt that
// errors (no TTY, or a test seam returning one) is read as "no", so a scripted
// run without --yes declines rather than proceeds unasked.
func confirmOrYes(yes bool, title string) bool {
	if yes {
		return true
	}
	ok := false
	if err := confirm(title, &ok); err != nil {
		return false
	}
	return ok
}
