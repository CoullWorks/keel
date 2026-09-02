package brand

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Source names where a resolved token set came from, so a caller (and
// `keel brand show`) can report which layer won without re-deriving it.
type Source string

const (
	// SourceProject means the project's own manifest brand override was used.
	SourceProject Source = "project"
	// SourceGlobal means the global default (~/.config/keel/brand.yaml) was used.
	SourceGlobal Source = "global"
	// SourceKit means neither layer set a brand, so the kit's own colours stand
	// and no keel tokens apply.
	SourceKit Source = "none"
)

// projectBrandRef is the minimal shape the brand package reads out of a
// project's .keel/manifest.yaml. It intentionally decodes only the brand block —
// engine.Manifest owns the full schema; duplicating one nested struct here
// keeps brand free of an engine import (which would otherwise be a cycle, since
// a future engine could want to resolve a brand at build time).
type projectBrandRef struct {
	Brand *struct {
		Primary string `yaml:"primary"`
		Accent  string `yaml:"accent,omitempty"`
	} `yaml:"brand"`
}

// ProjectOverride returns the project's brand seed from its manifest, or
// (Seed{}, false) when the project has no override (or no manifest). Reading only
// the brand block means a manifest with fields this package doesn't know about
// still decodes.
func ProjectOverride(dir string) (Seed, bool) {
	safeBase, err := filepath.Abs(dir)
	if err != nil {
		return Seed{}, false
	}
	p, err := filepath.Abs(filepath.Join(dir, ".keel", "manifest.yaml"))
	if err != nil || !strings.HasPrefix(p, safeBase) {
		return Seed{}, false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return Seed{}, false
	}
	var ref projectBrandRef
	if err := yaml.Unmarshal(b, &ref); err != nil {
		return Seed{}, false
	}
	if ref.Brand == nil || ref.Brand.Primary == "" {
		return Seed{}, false
	}
	return Seed{Primary: ref.Brand.Primary, Accent: ref.Brand.Accent}, true
}

// Resolved is the outcome of Resolve: the tokens to apply and which layer they
// came from. When Source is SourceKit, HasTokens is false and Tokens is the zero
// value — the caller should leave the kit's own colours in place.
type Resolved struct {
	Tokens    BrandTokens
	Source    Source
	HasTokens bool
}

// Resolve implements keel's brand resolution order: project override → global
// default → kit's own colours. It reads the project's manifest brand block
// (generating tokens from that seed), else the persisted global default, else
// reports SourceKit so the caller knows to leave the framework's own palette
// untouched.
//
// A project override is a seed, so it is generated fresh here (picking up the
// current generation logic); the global default is loaded as stored (a user may
// have hand-edited a shade). This is the single accessor the studio brand UI,
// the CLI (`keel brand apply`/`show`) and the build wizard consume.
func Resolve(dir string) (Resolved, error) {
	if seed, ok := ProjectOverride(dir); ok {
		t, err := Generate(seed.Primary, seed.Accent)
		if err != nil {
			return Resolved{}, err
		}
		return Resolved{Tokens: t, Source: SourceProject, HasTokens: true}, nil
	}
	g, ok, err := LoadGlobal()
	if err != nil {
		return Resolved{}, err
	}
	if ok {
		return Resolved{Tokens: g, Source: SourceGlobal, HasTokens: true}, nil
	}
	return Resolved{Source: SourceKit}, nil
}
