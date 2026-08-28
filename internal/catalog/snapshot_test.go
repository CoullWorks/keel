package catalog_test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

// update rewrites the golden file instead of comparing against it:
//
//	go test ./internal/catalog -run Snapshot -update
//
// Do that only when a diff has been read and is intended, and say why in the
// commit: this file is the tripwire for accidental changes to what every stack
// actually builds.
var update = flag.Bool("update", false, "rewrite the effective-plan golden file")

const goldenPath = "testdata/effective-plans.txt"

// combos returns every framework x env x database combination the catalogue
// currently offers, in a stable order. A framework with no applicable database
// still yields one row (env only), so frameworks are never silently uncovered.
func combos(t *testing.T, reg *recipe.Registry) [][]string {
	t.Helper()
	var out [][]string
	for _, fw := range ofKind(reg, recipe.Framework) {
		envs := applicable(reg, recipe.Env, fw.ID)
		if len(envs) == 0 {
			t.Errorf("framework %s has no applicable env recipe", fw.ID)
			continue
		}
		dbs := applicable(reg, recipe.DB, fw.ID)
		for _, env := range envs {
			if len(dbs) == 0 {
				out = append(out, []string{fw.ID, env.ID})
				continue
			}
			for _, db := range dbs {
				out = append(out, []string{fw.ID, env.ID, db.ID})
			}
		}
	}
	return out
}

func ofKind(reg *recipe.Registry, k recipe.Kind) []recipe.Recipe {
	var out []recipe.Recipe
	for _, r := range reg.All() {
		if r.Kind == k {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func applicable(reg *recipe.Registry, k recipe.Kind, fw string) []recipe.Recipe {
	var out []recipe.Recipe
	for _, r := range ofKind(reg, k) {
		if r.AppliesToFramework(fw) {
			out = append(out, r)
		}
	}
	return out
}

// TestEffectivePlanSnapshot pins what every framework/env/database combination
// builds: the ordered recipes, install steps, files, patches, hooks and smoke
// commands. A recipe edit that changes any of them fails here with the exact
// diff, which is what makes a catalogue refactor reviewable.
func TestEffectivePlanSnapshot(t *testing.T) {
	reg, err := catalog.Registry()
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, ids := range combos(t, reg) {
		fmt.Fprintf(&b, "=== %s\n", strings.Join(ids, " + "))
		plan, err := resolver.Resolve(reg, ids)
		if err != nil {
			// A combination the resolver refuses is itself part of the
			// behaviour being pinned: it must stay refused, or start working
			// deliberately.
			fmt.Fprintf(&b, "unresolvable: %v\n\n", err)
			continue
		}
		b.WriteString(engine.Effective(plan, "<project>"))
		b.WriteString("\n")
	}
	got := b.String()

	if *update {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d combinations)", goldenPath, len(combos(t, reg)))
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("missing golden file (run: go test ./internal/catalog -run Snapshot -update): %v", err)
	}
	if got == string(want) {
		return
	}
	t.Errorf("effective plans changed.\n%s\n\nIf the change is intended, re-run with -update and explain the diff in the commit.",
		firstDiff(string(want), got))
}

// firstDiff reports the first differing line with a little context, which is far
// easier to act on than a dump of two multi-thousand-line files.
func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		wl, gl := "", ""
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl == gl {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "first difference at line %d (in %s)\n", i+1, sectionOf(g, i))
		for j := max(0, i-3); j < min(len(g), i+4); j++ {
			marker := "  "
			if j == i {
				marker = "> "
			}
			fmt.Fprintf(&b, "%s%s\n", marker, g[j])
		}
		fmt.Fprintf(&b, "--- want: %s\n+++ got:  %s\n", wl, gl)
		fmt.Fprintf(&b, "(%d lines before, %d after)", len(w), len(g))
		return b.String()
	}
	return "files differ only in trailing content"
}

// sectionOf finds the "=== <combination>" header the given line sits under.
func sectionOf(lines []string, i int) string {
	for j := min(i, len(lines)-1); j >= 0; j-- {
		if strings.HasPrefix(lines[j], "=== ") {
			return strings.TrimPrefix(lines[j], "=== ")
		}
	}
	return "unknown combination"
}
