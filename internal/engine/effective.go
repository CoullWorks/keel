package engine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

// Effective renders everything a plan would do, as deterministic text: the
// recipes in execution order, the install steps, the files written, the config
// patches applied, and the lifecycle hooks fired.
//
// This exists so a refactor of the recipe catalogue can be proved behaviour-
// preserving: snapshot every framework/env/database combination before the
// change, and diff after. Anything that moves shows up as a diff to explain
// rather than a silent change in what users' projects get.
func Effective(plan *resolver.Plan, project string) string {
	vars := planVars(plan, project)
	var b strings.Builder

	fmt.Fprintf(&b, "framework: %s\n", plan.Framework)
	b.WriteString("recipes:\n")
	for _, r := range ordered(plan) {
		fmt.Fprintf(&b, "  - %s (%s)\n", r.ID, r.Kind)
	}

	b.WriteString("steps:\n")
	for _, s := range Steps(plan) {
		fmt.Fprintf(&b, "  $ %s\n", s)
	}

	b.WriteString("files:\n")
	files := RenderedFiles(plan, project)
	for _, path := range sortedKeys(files) {
		// Hash rather than inline: this is a change detector, and full file
		// bodies would bury the diff that matters.
		fmt.Fprintf(&b, "  %s sha256:%s\n", path, Sha(files[path]))
	}

	b.WriteString("patches:\n")
	for _, r := range ordered(plan) {
		for _, p := range r.Patch {
			file := render(p.File, vars)
			for _, k := range sortedKeys(p.Set) {
				fmt.Fprintf(&b, "  %s: %s=%s (from %s)\n", file, k, render(p.Set[k], vars), r.ID)
			}
		}
	}

	b.WriteString("hooks:\n")
	for _, stage := range hookStages() {
		for _, r := range ordered(plan) {
			for _, h := range r.Hooks[stage] {
				if skipHook(h, vars) {
					continue
				}
				if c, _ := hookCmd(h, vars, "."); c != "" {
					fmt.Fprintf(&b, "  %s: %s (from %s)\n", stage, c, r.ID)
				}
			}
		}
	}

	b.WriteString("smoke:\n")
	for _, s := range SmokeSteps(plan, project) {
		fmt.Fprintf(&b, "  $ %s\n", s)
	}
	return b.String()
}

// hookStages lists the lifecycle stages in a stable order (recipe.Stages is a
// map, so iterating it directly would reorder the snapshot run to run).
func hookStages() []string {
	out := make([]string, 0, len(recipe.Stages))
	for s := range recipe.Stages {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
