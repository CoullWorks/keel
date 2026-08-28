package catalog

import (
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

// TestGeneratedFilesUseNoTypographicDashes.
//
// keel's copy uses a plain hyphen, never an en dash or an em dash. The reason is
// practical rather than stylistic: this text lands in a stranger's terminal,
// editor and diff viewer, where the encoding is not ours to assume, and it lands
// in files people copy, grep and paste into shells.
//
// The rule was being kept by hand and had already drifted - a Django settings
// comment shipped an em dash into every generated project. Hand-checking cannot
// hold a line across 22 frameworks, so it is checked here instead.
//
// Scope is what a user actually receives: the rendered files a build writes.
// Comments in the recipe YAML itself are keel's own source and never leave the
// repository, so they are not the subject.
func TestGeneratedFilesUseNoTypographicDashes(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	// U+2013 EN DASH and U+2014 EM DASH.
	const dashes = "–—"

	checked := 0
	for _, fw := range reg.OfKind(recipe.Framework) {
		for _, env := range reg.ForFramework(fw.ID, recipe.Env) {
			ids := []string{fw.ID, env.ID}
			if db := defaultDB(reg, fw.ID, env.ID); db != "" {
				ids = append(ids, db)
			}
			plan, err := resolver.Resolve(reg, ids)
			if err != nil {
				continue // TestEveryFrameworkEnvComposes owns resolve failures.
			}
			for path, content := range engine.RenderedFiles(plan, "<project>") {
				checked++
				i := strings.IndexAny(content, dashes)
				if i < 0 {
					continue
				}
				t.Errorf("%s/%s: %s ships a typographic dash, which keel's copy does not use:\n  %s",
					fw.ID, env.ID, path, lineAround(content, i))
			}
		}
	}
	if checked == 0 {
		t.Fatal("no generated file was read, so this checked nothing")
	}
}

// TestRecipeUserFacingTextUsesNoTypographicDashes extends the no-dash rule from
// generated files to the recipe metadata a user actually reads, across EVERY
// recipe kind — the gap the rendered-files test could not see.
//
// TestGeneratedFilesUseNoTypographicDashes only resolves framework+env+db, so a
// dash in a frontend's shipped file, an extra's guidance body, a recipe label
// shown in the studio Build tree, or a generator's input prompt slipped past it.
// Those are exactly what a user sees, so they are checked here by walking the
// catalogue directly: label, help, prompts, hook messages and every file content
// a recipe drops in. Recipe YAML `#` source comments never ship, so they are not
// the subject; anything scanned here does reach the user.
func TestRecipeUserFacingTextUsesNoTypographicDashes(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	const dashes = "–—" // U+2013 EN DASH, U+2014 EM DASH
	check := func(id, field, text string) {
		if i := strings.IndexAny(text, dashes); i >= 0 {
			t.Errorf("recipe %s: %s carries a typographic dash, which keel's copy does not use:\n  %s",
				id, field, lineAround(text, i))
		}
	}
	for _, r := range reg.All() {
		check(r.ID, "label", r.Label)
		for _, in := range r.Inputs {
			check(r.ID, "input "+in.Name+" label", in.Label)
			check(r.ID, "input "+in.Name+" help", in.Help)
		}
		for _, c := range r.Credentials {
			check(r.ID, "credential "+c.ID+" label", c.Label)
			check(r.ID, "credential "+c.ID+" help", c.Help)
		}
		for stage, hooks := range r.Hooks {
			for _, h := range hooks {
				check(r.ID, "hook "+stage+" message", h.Message)
			}
		}
		for _, f := range r.Files {
			check(r.ID, "file "+f.Path, f.Content)
		}
	}
}

// lineAround returns the whole line containing byte offset i, trimmed, so the
// failure names the sentence to fix rather than the file.
func lineAround(s string, i int) string {
	start := strings.LastIndexByte(s[:i], '\n') + 1
	end := strings.IndexByte(s[i:], '\n')
	if end < 0 {
		return strings.TrimSpace(s[start:])
	}
	return strings.TrimSpace(s[start : i+end])
}
