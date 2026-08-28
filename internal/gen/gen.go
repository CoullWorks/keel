// Package gen is keel's code-component generator (`keel gen`). For Laravel it
// orchestrates `artisan make:*` (Laravel ships the generators, so keel just
// drives them). For Magento — which has no rich generator — the plan is
// template-based file generation modelled on mage2gen; see docs/GEN.md.
package gen

import (
	"fmt"
	"regexp"
	"strings"
)

// Component is a code artifact keel can generate.
type Component struct {
	Key   string
	Label string
	Make  string // artisan make subcommand, e.g. "make:event"
	Flags string // extra flags, e.g. "-mfs" on a model
}

// LaravelComponents is the catalogue offered inside a Laravel project. These map
// straight onto Laravel's built-in generators.
var LaravelComponents = []Component{
	{"model", "Model (+ migration, factory, seeder)", "make:model", "-mfs"},
	{"controller", "Controller", "make:controller", ""},
	{"event", "Event", "make:event", ""},
	{"listener", "Listener", "make:listener", ""},
	{"migration", "Migration", "make:migration", ""},
	{"request", "Form request", "make:request", ""},
	{"resource", "API resource", "make:resource", ""},
	{"job", "Job", "make:job", ""},
	{"command", "Console command", "make:command", ""},
	{"policy", "Policy", "make:policy", ""},
	{"middleware", "Middleware", "make:middleware", ""},
	{"test", "Test (Pest)", "make:test", ""},
	{"filament-resource", "Filament resource", "make:filament-resource", ""},
}

// ByKey returns a Laravel component by its key.
func ByKey(key string) (Component, bool) {
	for _, c := range LaravelComponents {
		if c.Key == key {
			return c, true
		}
	}
	return Component{}, false
}

// Frameworks are the framework families `keel gen` can generate for. It is
// populated by Register as each framework file registers its stance in init(),
// then kept sorted, so it is never a stale hand-maintained list: it is exactly
// the set the registry actually serves. By the time any command reads it (long
// after package init) it is complete.
//
// gen is deliberately not a generator for everything keel scaffolds: it drives a
// framework's own generators (Laravel's artisan make:*, Symfony's bin/console
// make, NestJS's nest generate, Adonis's ace make, Django's manage.py) or ships
// templates where the framework has none (Magento, the Node meta-frameworks, the
// Python micro-frameworks). A project on a stack with neither is told so plainly,
// because the alternative — what keel did before this gate existed — was silently
// running one stack's generator against another's project, so a Django project
// asking for a model got `php artisan make:model`.
var Frameworks []string

// Supports reports whether keel gen has built-in code-block generators for a
// framework family. It reads the registry directly, so it can never drift from
// what is actually registered.
func Supports(family string) bool {
	return lookup(family) != nil
}

// Keys lists the Laravel component keys, for help text, shell completion and the
// "unknown component" error. Before this the catalogue was only discoverable by
// running the picker or reading the source.
func Keys() []string {
	out := make([]string, len(LaravelComponents))
	for i, c := range LaravelComponents {
		out[i] = c.Key
	}
	return out
}

// MagentoKeys lists the Magento component keys, for the same three uses.
func MagentoKeys() []string {
	out := make([]string, len(MagentoComponents))
	for i, c := range MagentoComponents {
		out[i] = c.Key
	}
	return out
}

// namePattern is what a generated component may be called: a PHP class or
// migration name, optionally namespaced with slashes (e.g. "Admin/UserController",
// "create_users_table"). Everything a shell or a filesystem could reinterpret is
// outside it.
var namePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(/[A-Za-z_][A-Za-z0-9_]*)*$`)

// ValidateName rejects a component name that is not a plain identifier.
//
// Names reach keel from the command line and, through the MCP write tools, from
// an AI agent. They end up in a command's argv and in generated file paths, so
// anything that could be read as a shell fragment, a flag or a path segment out
// of the project is refused here rather than defused at each use.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("name is empty")
	}
	if !namePattern.MatchString(name) {
		return fmt.Errorf("invalid name %q: use letters, digits and underscores (slashes for namespaces)", name)
	}
	return nil
}

// Command builds the argv that generates component c named name inside env
// (e.g. "ddev"). Example: Command("ddev", event, "OrderPlaced") ->
// ["ddev", "exec", "php", "artisan", "make:event", "OrderPlaced"].
//
// It returns argv, never a command line: the name is user (or agent) supplied,
// so it must never pass through a shell that would treat ";" or "|" in it as
// syntax. Callers validate with ValidateName first.
func Command(env string, c Component, name string) []string {
	// env is intentionally field-split: it is a trusted env-vocabulary string
	// ("ddev", or "docker compose exec app") that legitimately carries several
	// argv elements, unlike name, which is one argument whatever it contains.
	// Flags ("-mfs") split the same way. Build argv explicitly rather than
	// appending onto strings.Fields(env)'s result, whose backing array append may
	// reuse — a fragile aliasing footgun in a security-sensitive argv builder.
	envFields := strings.Fields(env)
	flagFields := strings.Fields(c.Flags)
	argv := make([]string, 0, len(envFields)+5+len(flagFields))
	argv = append(argv, envFields...)
	argv = append(argv, "exec", "php", "artisan", c.Make, name)
	argv = append(argv, flagFields...)
	return argv
}
