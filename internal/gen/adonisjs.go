package gen

import (
	"strings"

	"github.com/coullworks/keel/internal/recipe"
)

// adonisjs.go drives AdonisJS's ace CLI (`node ace make:*`). Adonis is "Laravel
// for Node" and ace is its artisan: the same make:<thing> <Name> shape, so keel
// drives it exactly as it drives artisan. Before this a Adonis project got no
// generation at all despite shipping a first-class generator CLI.
//
// Each entry maps a component key onto its ace make: subcommand. keel builds the
// argv; the name is one argv element (validated by ValidateName), never a shell
// fragment.

// aceMaker is an AdonisJS ace command: its component key, menu label, and the
// make: subcommand it drives.
type aceMaker struct {
	key, label, make string
}

// AdonisComponents is the `node ace make:*` catalogue keel drives, in menu order.
var AdonisComponents = []aceMaker{
	{"controller", "HTTP controller", "make:controller"},
	{"model", "Lucid model", "make:model"},
	{"migration", "Migration", "make:migration"},
	{"middleware", "Middleware", "make:middleware"},
	{"command", "Ace command", "make:command"},
	{"provider", "Service provider", "make:provider"},
	{"validator", "VineJS validator", "make:validator"},
	{"service", "Service", "make:service"},
	{"factory", "Model factory", "make:factory"},
	{"seeder", "Database seeder", "make:seeder"},
}

// adonisByKey returns an ace maker by key.
func adonisByKey(key string) (aceMaker, bool) {
	for _, c := range AdonisComponents {
		if c.key == key {
			return c, true
		}
	}
	return aceMaker{}, false
}

// adonisCommand builds the argv that runs an ace command through env:
// `<env> exec node ace make:<thing> <Name>`. The env is field-split (a trusted
// prefix); the name is exactly one argument.
func adonisCommand(env, key, name string) ([]string, bool) {
	c, ok := adonisByKey(key)
	if !ok {
		return nil, false
	}
	envFields := strings.Fields(env)
	argv := make([]string, 0, len(envFields)+5)
	argv = append(argv, envFields...)
	argv = append(argv, "exec", "node", "ace", c.make)
	if name != "" {
		argv = append(argv, name)
	}
	return argv, true
}

func init() {
	comps := make([]Generatable, 0, len(AdonisComponents))
	for _, c := range AdonisComponents {
		comps = append(comps, componentGeneratable("adonisjs", c.key, c.label, recipe.LevelCodeBlock))
	}
	Register(&FrameworkGen{
		Family:     "adonisjs",
		HasModule:  false,
		Components: comps,
		Command:    adonisCommand,
	})
}
