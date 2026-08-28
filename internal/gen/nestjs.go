package gen

import (
	"strings"

	"github.com/coullworks/keel/internal/recipe"
)

// nestjs.go drives the NestJS CLI (`nest generate <schematic> <name>`), the exact
// analogue of Laravel's artisan. NestJS is also the one non-Magento framework
// with a GENUINE module concept (`@Module`), so HasModule is true here and only
// here among the Node stacks — a UI may legitimately show a "Module" affordance
// for a Nest project.
//
// Each entry maps a component key onto its Nest schematic. keel builds the argv;
// the name is one argv element (validated by ValidateName), never a shell
// fragment.

// nestSchematic is a NestJS CLI schematic: its component key, menu label, and the
// `nest generate` schematic name it drives.
type nestSchematic struct {
	key, label, schematic string
}

// NestComponents is the `nest generate` catalogue keel drives, in menu order.
// module leads because it is the framework's organising unit; the rest are the
// providers and handlers a module is built from.
var NestComponents = []nestSchematic{
	{"module", "Module (@Module)", "module"},
	{"controller", "Controller", "controller"},
	{"service", "Service (provider)", "service"},
	{"guard", "Guard", "guard"},
	{"pipe", "Pipe", "pipe"},
	{"interceptor", "Interceptor", "interceptor"},
	{"resolver", "GraphQL resolver", "resolver"},
	{"gateway", "WebSocket gateway", "gateway"},
	{"middleware", "Middleware", "middleware"},
	{"decorator", "Decorator", "decorator"},
}

// nestByKey returns a Nest schematic by key.
func nestByKey(key string) (nestSchematic, bool) {
	for _, c := range NestComponents {
		if c.key == key {
			return c, true
		}
	}
	return nestSchematic{}, false
}

// nestCommand builds the argv that runs a Nest schematic through env:
// `<env> exec npx nest generate <schematic> <Name>`. npx runs the project-local
// @nestjs/cli so the command works without a global install. The env is
// field-split (a trusted prefix); the name is exactly one argument.
func nestCommand(env, key, name string) ([]string, bool) {
	c, ok := nestByKey(key)
	if !ok {
		return nil, false
	}
	envFields := strings.Fields(env)
	argv := make([]string, 0, len(envFields)+6)
	argv = append(argv, envFields...)
	argv = append(argv, "exec", "npx", "nest", "generate", c.schematic)
	if name != "" {
		argv = append(argv, name)
	}
	return argv, true
}

func init() {
	comps := make([]Generatable, 0, len(NestComponents))
	for _, c := range NestComponents {
		level := recipe.LevelCodeBlock
		if c.key == "module" {
			level = recipe.LevelModule
		}
		comps = append(comps, componentGeneratable("nestjs", c.key, c.label, level))
	}
	Register(&FrameworkGen{
		Family:     "nestjs",
		HasModule:  true,
		Components: comps,
		Command:    nestCommand,
	})
}
