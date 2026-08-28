package catalog

import (
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
	"gopkg.in/yaml.v3"
)

// composeDoc is the union of what the compose guards here need to read. One
// struct and one parse, so three checks cannot disagree about the shape of a
// compose file.
type composeDoc struct {
	Services map[string]struct {
		Image       string   `yaml:"image"`
		User        string   `yaml:"user"`
		Command     any      `yaml:"command"`
		Environment any      `yaml:"environment"`
		Volumes     []string `yaml:"volumes"`
	} `yaml:"services"`
	Volumes map[string]any `yaml:"volumes"`
}

// eachComposeDoc calls fn for every compose document of every compose stack keel
// can build, seeded the way a real build seeds one, and rendered.
//
// Rendered, because recipe source is not what a user gets: a recipe's Content
// still holds its {{tokens}}, so yaml.Unmarshal fails on it. Guards here used to
// skip a file they could not parse, which meant skipping almost every compose
// file keel ships while reporting green - the mount-parent guard passed for
// weeks while Gradio mounted a volume that left a root-owned directory on the
// host, and a real build found it instead of the test.
//
// So a document that does not parse fails the test. There is no case where the
// right answer is to look away from a compose file keel is about to write.
//
// fn also receives the whole rendered file set, because some checks are about
// what is missing beside the compose file rather than what is in it.
func eachComposeDoc(t *testing.T, fn func(where string, doc composeDoc, files map[string]string)) {
	t.Helper()
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, fw := range reg.OfKind(recipe.Framework) {
		for _, env := range reg.ForFramework(fw.ID, recipe.Env) {
			if env.EnvFamily != "compose" {
				continue
			}
			// Seeded through the same function a real build uses, so these guards
			// check the stack a user gets. Written against a different rule they
			// checked a plan nobody can build, and passed while the real one was
			// broken.
			ids := resolver.SeedDefaults(reg, []string{fw.ID, env.ID}, fw.ID)
			plan, err := resolver.Resolve(reg, ids)
			if err != nil {
				continue // not a combination keel offers
			}
			files := engine.RenderedFiles(plan, "proj")
			for path, body := range files {
				if !isComposeFile(path) {
					continue
				}
				where := fw.ID + "/" + env.ID + ": " + path
				var doc composeDoc
				if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
					t.Errorf("%s does not parse as YAML, so nothing in it was checked "+
						"and compose would reject it too: %v", where, err)
					continue
				}
				seen++
				fn(where, doc, files)
			}
		}
	}
	// A guard that walks nothing passes forever.
	if seen == 0 {
		t.Fatal("no compose document was parsed, so this checked nothing")
	}
}

func isComposeFile(path string) bool {
	base := path[strings.LastIndex(path, "/")+1:]
	return base == "compose.yaml" || base == "compose.override.yaml" ||
		strings.HasPrefix(base, "docker-compose.")
}
