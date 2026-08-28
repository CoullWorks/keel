package gen

import (
	"strings"

	"github.com/coullworks/keel/internal/recipe"
)

// symfony.go drives Symfony's MakerBundle (`php bin/console make:*`), the exact
// analogue of Laravel's artisan. MakerBundle is installed by default in keel's
// Symfony recipe, so the CLI it exposes is what keel should be driving — before
// this, keel offered a Symfony project only its auth stack and nothing else.
//
// Each entry maps a component key onto its make subcommand. keel builds the argv
// and hands it to the same runner the Laravel path uses; the name is one argv
// element (never a shell fragment), validated by ValidateName up front.

// symfonyMaker is a Symfony MakerBundle component: its key, its menu label, and
// the make: subcommand it drives.
type symfonyMaker struct {
	key, label, make string
}

// SymfonyComponents is the MakerBundle catalogue keel drives. It mirrors the
// commands `symfony console list make` reports, limited to the ones that take a
// single name argument (or none) so keel's uniform name-in/argv-out contract
// holds. crud and migration take no name (the Maker prompts), so keel passes the
// name only when the subcommand accepts one — see symfonyTakesName.
var SymfonyComponents = []symfonyMaker{
	{"controller", "Controller", "make:controller"},
	{"entity", "Entity (Doctrine)", "make:entity"},
	{"form", "Form type", "make:form"},
	{"command", "Console command", "make:command"},
	{"subscriber", "Event subscriber", "make:subscriber"},
	{"voter", "Security voter", "make:voter"},
	{"crud", "CRUD (from an entity)", "make:crud"},
	{"migration", "Migration (from schema diff)", "make:migration"},
	{"user", "User class (security)", "make:user"},
	{"fixtures", "Doctrine fixtures", "make:fixtures"},
	{"twig-component", "Twig component", "make:twig-component"},
}

// symfonyByKey returns a Symfony maker by key.
func symfonyByKey(key string) (symfonyMaker, bool) {
	for _, c := range SymfonyComponents {
		if c.key == key {
			return c, true
		}
	}
	return symfonyMaker{}, false
}

// symfonyNoName is the set of make: subcommands that take no name argument:
// make:migration diffs the schema and make:crud is prompted for its entity
// interactively. Passing a name to these would be a stray positional the Maker
// rejects, so keel omits it.
var symfonyNoName = map[string]bool{"migration": true}

// symfonyCommand builds the argv that runs a MakerBundle command through env:
// `<env> exec php bin/console make:<thing> <Name>`. The env is field-split (it is
// a trusted env-vocabulary prefix like "ddev" that may carry several argv
// elements); the name is always exactly one argument. A no-name subcommand omits
// the trailing name.
func symfonyCommand(env, key, name string) ([]string, bool) {
	c, ok := symfonyByKey(key)
	if !ok {
		return nil, false
	}
	envFields := strings.Fields(env)
	argv := make([]string, 0, len(envFields)+5)
	argv = append(argv, envFields...)
	argv = append(argv, "exec", "php", "bin/console", c.make)
	if !symfonyNoName[key] && name != "" {
		argv = append(argv, name)
	}
	return argv, true
}

func init() {
	comps := make([]Generatable, 0, len(SymfonyComponents))
	for _, c := range SymfonyComponents {
		comps = append(comps, componentGeneratable("symfony", c.key, c.label, recipe.LevelCodeBlock))
	}
	Register(&FrameworkGen{
		Family:     "symfony",
		HasModule:  false,
		Components: comps,
		Command:    symfonyCommand,
	})
}
