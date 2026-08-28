package recipe

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Finding is one problem Lint found in the catalogue.
type Finding struct {
	Recipe string // recipe id
	Msg    string
}

func (f Finding) String() string { return f.Recipe + ": " + f.Msg }

// tokenRef matches a {{token}} reference in a recipe's templated text.
var tokenRef = regexp.MustCompile(`\{\{([a-zA-Z0-9_.]+)\}\}`)

// baseTokens are the ones the engine always defines, so a recipe may reference
// them without any env, var or pin declaring them.
//
// uid/gid are the invoking user's, and exist so a bind-mounting dev image can
// adopt them. http_port is the port the stack will be published on, read from
// the same HTTP_PORT the compose files read, so a recipe that writes a URL or a
// port into a project names the one it will actually answer on.
var baseTokens = map[string]bool{
	"env": true, "project": true, "create": true,
	"uid": true, "gid": true, "http_port": true,
}

// Lint checks the whole registry for problems that would otherwise only show up
// as a broken build: a {{token}} nothing defines, an id two recipes claim, an
// alias that shadows a real recipe, a config recipe that can never match.
//
// The expensive failures in this catalogue have all been silent ones — a token
// rendering to nothing and the step quietly doing nothing, a database applying
// to a framework that never reads it. Lint is where those become visible before
// anyone runs a build.
func Lint(reg *Registry) []Finding {
	var out []Finding
	all := reg.All()

	defined := definedTokens(reg)
	frameworks := map[string]bool{}
	capabilities := map[string]bool{}
	for _, r := range all {
		if r.Kind == Framework {
			frameworks[r.ID] = true
		}
		for _, c := range r.Provides {
			capabilities[c] = true
		}
	}

	// An alias must not collide with a real id or with another alias: a
	// collision means one of the two recipes is silently unreachable by name.
	claimed := map[string]string{}
	for _, r := range all {
		for _, a := range r.Aliases {
			if a == r.ID {
				out = append(out, Finding{r.ID, fmt.Sprintf("alias %q repeats the recipe's own id", a)})
				continue
			}
			if _, isReal := reg.byID[a]; isReal {
				out = append(out, Finding{r.ID, fmt.Sprintf("alias %q is already a recipe id", a)})
				continue
			}
			if owner, dup := claimed[a]; dup {
				out = append(out, Finding{r.ID, fmt.Sprintf("alias %q is also claimed by %s", a, owner)})
				continue
			}
			claimed[a] = r.ID
		}
	}

	for _, r := range all {
		for _, fw := range r.AppliesTo {
			if fw != "*" && !frameworks[fw] {
				out = append(out, Finding{r.ID, fmt.Sprintf("appliesTo names %q, which is not a framework recipe", fw)})
			}
		}
		// A recipe that conflicts with something it provides can never resolve, and
		// the error blames the capability rather than the recipe, so it reads like
		// a user mistake. Prisma shipped like this and was simply unusable.
		provides := map[string]bool{}
		for _, c := range r.Provides {
			provides[c] = true
		}
		for _, c := range r.Conflicts {
			if provides[c] {
				out = append(out, Finding{r.ID, fmt.Sprintf("conflicts with %q, which it also provides, so it can never resolve", c)})
			}
		}
		for _, req := range r.Requires {
			if !capabilities[req] && !frameworks[req] {
				out = append(out, Finding{r.ID, fmt.Sprintf("requires %q, which nothing provides", req)})
			}
		}
		if r.Kind == Config && r.When != nil {
			if r.When.Framework != "" && !frameworks[r.When.Framework] {
				out = append(out, Finding{r.ID, fmt.Sprintf("when.framework %q is not a framework recipe", r.When.Framework)})
			}
			for _, f := range r.When.Frameworks {
				if !frameworks[f] {
					out = append(out, Finding{r.ID, fmt.Sprintf("when.frameworks names %q, which is not a framework recipe", f)})
				}
			}
			if r.When.Uses != "" && !capabilities[r.When.Uses] {
				out = append(out, Finding{r.ID, fmt.Sprintf("when.uses %q is a capability nothing provides, so this never applies", r.When.Uses)})
			}
		}
		// A recipe that needs Composer credentials writes an auth.json into the
		// project under compose and Sail. Shipping that without a .gitignore
		// entry is how purchased-extension keys end up in a public repository.
		if needsComposerAuth(r) && !ignoresAuthJSON(r) {
			out = append(out, Finding{r.ID, "declares composer credentials but its .gitignore does not cover auth.json, which lands in the project root under compose and Sail"})
		}
		if r.Kind == Env && r.EnvFamily == "" && r.SchemaVersion >= 2 {
			out = append(out, Finding{r.ID, "env recipe has no env_family, so databases cannot describe how they come up here"})
		}

		// Undefined tokens: the failure mode is a step that renders to a
		// half-built command, or a literal {{token}} reaching the shell.
		local := map[string]bool{}
		for name := range r.Vars {
			local[name] = true
		}
		for name := range r.Pins {
			local["pin."+name] = true
		}
		// A version choice defines its pin too: the recommendation is the value
		// when nobody picks, so {{pin.<name>}} always resolves.
		for name := range r.Versions {
			local["pin."+name] = true
		}
		for _, tok := range referencedTokens(r) {
			if baseTokens[tok] || defined[tok] || local[tok] {
				continue
			}
			out = append(out, Finding{r.ID, fmt.Sprintf("references {{%s}}, which no env command, var or pin defines", tok)})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Recipe != out[j].Recipe {
			return out[i].Recipe < out[j].Recipe
		}
		return out[i].Msg < out[j].Msg
	})
	return out
}

func needsComposerAuth(r Recipe) bool {
	for _, c := range r.Credentials {
		if c.Kind == CredComposer {
			return true
		}
	}
	return false
}

// ignoresAuthJSON reports whether this recipe writes a .gitignore covering
// auth.json.
func ignoresAuthJSON(r Recipe) bool {
	for _, f := range r.Files {
		if path.Base(f.Path) != ".gitignore" {
			continue
		}
		for _, line := range strings.Split(f.Content, "\n") {
			if strings.HasSuffix(strings.TrimSpace(line), "auth.json") {
				return true
			}
		}
	}
	return false
}

// definedTokens is every token any recipe in the registry supplies: env command
// vocabulary, published vars, and version pins.
func definedTokens(reg *Registry) map[string]bool {
	out := map[string]bool{}
	for _, r := range reg.byID {
		for k := range r.Commands {
			out[k] = true
		}
		for k := range r.Vars {
			out[k] = true
		}
		for k := range r.Pins {
			out["pin."+k] = true
		}
		for k := range r.Versions {
			out["pin."+k] = true
		}
	}
	return out
}

// referencedTokens lists every {{token}} a recipe's templated text mentions.
func referencedTokens(r Recipe) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		for _, m := range tokenRef.FindAllStringSubmatch(s, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
	}
	addAll := func(ss []string) {
		for _, s := range ss {
			add(s)
		}
	}
	addAll(r.Install)
	addAll(r.Smoke)
	for _, f := range r.Files {
		add(f.Path)
		add(f.Content)
	}
	for _, p := range r.Patch {
		add(p.File)
		for _, v := range p.Set {
			add(v)
		}
	}
	for _, cmd := range r.Tasks {
		add(cmd)
	}
	for _, frag := range r.Provision {
		addAll(frag.Install)
		for _, f := range frag.Files {
			add(f.Path)
			add(f.Content)
		}
		for _, p := range frag.Patch {
			add(p.File)
			for _, v := range p.Set {
				add(v)
			}
		}
	}
	for _, hooks := range r.Hooks {
		for _, h := range hooks {
			add(h.Run)
			add(h.Script)
			add(h.When)
			add(h.WorkingDir)
			add(h.Message)
		}
	}
	for _, c := range r.Create {
		add(c)
	}
	for _, v := range r.Vars {
		add(v)
	}
	sort.Strings(out)
	return out
}

// Summary renders findings as one line each, grouped by recipe.
func Summary(fs []Finding) string {
	var b strings.Builder
	for _, f := range fs {
		b.WriteString(f.String())
		b.WriteString("\n")
	}
	return b.String()
}
