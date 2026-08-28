package gen

import (
	"sort"

	"github.com/coullworks/keel/internal/recipe"
)

// FrameworkGen is one framework's built-in generation stance: the code-block
// catalogue keel ships for it, whether it owns a real module concept, and how a
// named component turns into output — argv that drives the framework's own
// generator CLI, or files rendered from templates.
//
// It is the extension point the fitness review asks for. Before this, the
// built-in catalogue was a hardcoded switch over laravel+magento in
// catalog.go, so adding a framework meant editing that shared switch. Now each
// framework file (laravel.go, symfony.go, nestjs.go, …) registers its own
// FrameworkGen in an init(); Generatables/Supports/Frameworks all derive from
// this map, so a new framework is one Register call and never a diff to a shared
// function.
//
// Command and Render are alternatives, not both: a framework either drives its
// own generator CLI (Command builds the argv, mirroring how Laravel drives
// `artisan make:*`) or ships templates (Render emits OutFiles, like Magento).
// Command wins when set — the CLI runs the argv; otherwise Render writes files.
type FrameworkGen struct {
	// Family is the framework family this stance is registered under (e.g.
	// "laravel", "nestjs"). A variant framework (magento-mageos) resolves to its
	// family before lookup, so it inherits the family's stance.
	Family string

	// HasModule reports whether the framework has a genuine module/namespace
	// concept a whole-plan generator groups components under — TRUE only where the
	// framework really has one (Magento's Vendor_Module, NestJS's @Module). Every
	// other framework leaves it false so a UI does not show a spurious "Module"
	// name box for what is really a per-file scaffold. This is the signal the
	// studio's render fix reads via HasModuleConcept; gen exposes it, the studio
	// consumes it.
	HasModule bool

	// Components is the built-in code-block catalogue for this framework, in menu
	// order. It is nil for a framework that ships no built-in generators and relies
	// only on recipe-driven stacks (auth/tests).
	Components []Generatable

	// Command builds the argv that generates the component named key as name,
	// through env, for a CLI-driven framework (Laravel/Symfony/NestJS/Adonis/
	// Django). It returns argv, never a shell string: name is user/agent supplied,
	// so it must never pass through a shell. Nil for a template-driven framework.
	Command func(env, key, name string) ([]string, bool)

	// Render emits the files for the component named key as name, for a
	// template-driven framework (Magento, Next.js, FastAPI, …). params carries the
	// component's collected form values; fields the typed column list a
	// model-shaped component renders. Nil for a CLI-driven framework.
	Render func(key, name string, fields []Field, params map[string]any) ([]OutFile, bool, error)
}

// registry is the per-family generation catalogue. Each framework file adds its
// entry in an init(); nothing else writes to it after program start, so it needs
// no lock.
var registry = map[string]*FrameworkGen{}

// Register records a framework's generation stance. Framework files call it from
// init(). A duplicate family is a programming error (two files claiming the same
// framework), so it panics — the same discipline the resolver uses for a
// duplicate recipe id, surfaced at startup rather than as a silent last-wins.
func Register(fg *FrameworkGen) {
	if fg.Family == "" {
		panic("gen.Register: FrameworkGen has no Family")
	}
	if _, dup := registry[fg.Family]; dup {
		panic("gen.Register: duplicate framework family " + fg.Family)
	}
	registry[fg.Family] = fg
	Frameworks = append(Frameworks, fg.Family)
	sort.Strings(Frameworks)
}

// FrameworkCommand builds the argv that generates the component named key as name
// for a CLI-driven framework family, through env. It is the framework-neutral
// generalisation of Command (which is Laravel-only): it dispatches to the
// registered framework's own argv builder, so Symfony drives `php bin/console
// make:*`, NestJS `nest generate`, Adonis `node ace make:*` and Django
// `manage.py`, exactly as Laravel drives `artisan make:*`.
//
// It returns argv and ok=false when the family is not CLI-driven (a
// template-driven framework renders files via FrameworkRender instead) or the key
// is unknown. Callers validate name with ValidateName first; the builder never
// passes it through a shell.
func FrameworkCommand(family, env, key, name string) ([]string, bool) {
	fg := lookup(family)
	if fg == nil || fg.Command == nil {
		return nil, false
	}
	return fg.Command(env, key, name)
}

// FrameworkRender emits the files for the component named key as name for a
// template-driven framework family. params carries the component's collected form
// values, fields the typed column list a model-shaped component renders. It is the
// framework-neutral generalisation of RenderMagento/RenderLaravelModel.
//
// ok=false means the family is not template-driven (it is CLI-driven, so
// FrameworkCommand applies) or the key is unknown for it.
func FrameworkRender(family, key, name string, fields []Field, params map[string]any) ([]OutFile, bool, error) {
	fg := lookup(family)
	if fg == nil || fg.Render == nil {
		return nil, false, nil
	}
	return fg.Render(key, name, fields, params)
}

// CLIDriven reports whether a framework family generates by driving its own
// generator CLI (argv via FrameworkCommand) rather than by rendering templates.
func CLIDriven(family string) bool {
	fg := lookup(family)
	return fg != nil && fg.Command != nil
}

// lookup returns the registered stance for a family, or nil.
func lookup(family string) *FrameworkGen { return registry[family] }

// ComponentKeys lists the built-in code-block component keys a framework family
// generates, in menu order, for CLI help, completion and the "unknown component"
// error. It is the registry-driven generalisation of Keys() (Laravel) and
// MagentoKeys(): every registered framework, not the two that were hardcoded.
func ComponentKeys(family string) []string {
	fg := lookup(family)
	if fg == nil {
		return nil
	}
	out := make([]string, 0, len(fg.Components))
	for _, c := range fg.Components {
		out = append(out, c.Key)
	}
	return out
}

// ComponentMode reports how a framework family generates a given component key:
// "command" (keel drives the framework's own generator CLI via FrameworkCommand)
// or "render" (keel writes templated files via FrameworkRender). It returns ""
// for a key the family does not generate, so the CLI can list the real keys.
//
// A hybrid framework (Django, Laravel) sets both Command and Render; this decides
// per key, so `django startapp` routes to the command and `django model` to the
// template, without the caller hardcoding which is which.
func ComponentMode(family, key string) string {
	fg := lookup(family)
	if fg == nil {
		return ""
	}
	known := false
	for _, c := range fg.Components {
		if c.Key == key {
			known = true
			break
		}
	}
	if !known {
		return ""
	}
	// Command wins when it owns the key (mirrors FrameworkCommand's precedence);
	// otherwise a known key with a Render belongs to the template path.
	if fg.Command != nil {
		if _, ok := fg.Command("", key, ""); ok {
			return "command"
		}
	}
	if fg.Render != nil {
		return "render"
	}
	return ""
}

// RegisteredFrameworks returns the families that have a built-in generation
// stance, sorted, so Frameworks and help text derive from the registry rather
// than a stale hand-maintained list.
func RegisteredFrameworks() []string {
	out := make([]string, 0, len(registry))
	for fam := range registry {
		out = append(out, fam)
	}
	sort.Strings(out)
	return out
}

// HasModuleConcept reports whether a framework family has a genuine module
// concept — a real @Module / Vendor_Module grouping — as opposed to per-file
// scaffolding. It is TRUE only for magento (and its variants) and nestjs.
//
// This is the signal the studio render fix consumes: it may show a "Module"
// header/name box only when this returns true, so Next/Laravel/Symfony/Django,
// whose generatables are per-file, never get a spurious Module prompt. gen owns
// the truth (it knows each framework's shape); the studio reads it and does not
// hardcode the list.
func HasModuleConcept(family string) bool {
	if fg := lookup(family); fg != nil {
		return fg.HasModule
	}
	return false
}

// componentGeneratable builds a Generatable for a built-in code-block, wiring its
// per-component typed form from inputsFor so every framework shares the one form
// machinery and only its catalogue differs.
func componentGeneratable(family, key, label string, level recipe.GenLevel) Generatable {
	return Generatable{
		Key: key, Label: label, Level: level,
		Framework: family, Inputs: inputsFor(family, key),
	}
}
