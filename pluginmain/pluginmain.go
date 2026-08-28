// Package pluginmain adapts a compiled plugin.Plugin into a standalone
// keel-<name> binary that keel drives over the subprocess protocol. keel finds
// the plugin by its config/register.yaml and runs this binary for each
// contribution:
//
//	keel-<name> __screen <id>              prints the screen's View as JSON
//	keel-<name> __page <id>                prints a global page's View as JSON
//	keel-<name> __step-options <id>        prints the step's options as JSON
//	keel-<name> __step-apply <id> [vals]   applies the step
//	keel-<name> <command> [args]           runs the named command
//
// The project acted on comes from the environment (KEEL_PROJECT_DIR / FRAMEWORK /
// ENV), so there is no positional-argument contract to keep in sync. This is how a
// plugin that used to be compiled into keel keeps its full behaviour — commands,
// live screens and wizard steps — while living in its own repo.
package pluginmain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/coullworks/keel/plugin"
)

// Run is the entrypoint a keel-<name> binary calls with its plugin instance.
func Run(p plugin.Plugin) {
	if err := dispatch(p, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func dispatch(p plugin.Plugin, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: <command> | __screen <id> | __step-options <id> | __step-apply <id> [values]")
	}
	proj := plugin.Project{
		Dir:       os.Getenv("KEEL_PROJECT_DIR"),
		Framework: os.Getenv("KEEL_FRAMEWORK"),
		Env:       os.Getenv("KEEL_ENV"),
	}
	if proj.Dir == "" {
		proj.Dir, _ = os.Getwd()
	}
	ctx := context.Background()

	switch args[0] {
	case "__screen":
		if len(args) < 2 {
			return errors.New("__screen needs a screen id")
		}
		return emitScreen(ctx, p, args[1], proj)
	case "__step-options":
		if len(args) < 2 {
			return errors.New("__step-options needs a step id")
		}
		return emitStepOptions(ctx, p, args[1], proj)
	case "__step-apply":
		if len(args) < 2 {
			return errors.New("__step-apply needs a step id")
		}
		return applyStep(ctx, p, args[1], proj, args[2:])
	case "__page":
		if len(args) < 2 {
			return errors.New("__page needs a page id")
		}
		return emitPage(ctx, p, args[1])
	case "__ui":
		if len(args) < 3 {
			return errors.New("__ui needs a surface and an id")
		}
		return emitUI(ctx, p, args[1], args[2])
	case "__call":
		if len(args) < 2 {
			return errors.New("__call needs an action id")
		}
		return emitCall(ctx, p, args[1], args[2:])
	case "__overview":
		return emitOverview(ctx, p, proj)
	case "__actions":
		return emitActions(ctx, p, proj)
	case "__action":
		if len(args) < 2 {
			return errors.New("__action needs an action id")
		}
		return runAction(ctx, p, args[1], proj, args[2:])
	default:
		return runCommand(ctx, p, args[0], proj, args[1:])
	}
}

func emitOverview(ctx context.Context, p plugin.Plugin, proj plugin.Project) error {
	ov, ok := p.(plugin.Overviewer)
	if !ok {
		return json.NewEncoder(os.Stdout).Encode(plugin.View{})
	}
	secs, err := ov.OverviewSections(ctx, proj)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(plugin.View{Sections: secs})
}

func emitActions(ctx context.Context, p plugin.Plugin, proj plugin.Project) error {
	ar, ok := p.(plugin.Actioner)
	if !ok {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"actions": []any{}})
	}
	acts, err := ar.Actions(ctx, proj)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"actions": acts})
}

func runAction(ctx context.Context, p plugin.Plugin, id string, proj plugin.Project, kvs []string) error {
	ar, ok := p.(plugin.Actioner)
	if !ok {
		return fmt.Errorf("this plugin has no actions")
	}
	args := map[string]string{}
	for _, kv := range kvs {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			args[kv[:i]] = kv[i+1:]
		}
	}
	return ar.RunAction(ctx, stdio{}, proj, id, args)
}

// emitUI prints one surface's own HTML (a "webview"). keel hosts it in a
// sandboxed iframe; the plugin owns everything inside. surface is
// "page"|"screen"|"hook"; a screen/hook reads its project from the environment.
func emitUI(ctx context.Context, p plugin.Plugin, surface, id string) error {
	w, ok := p.(plugin.WebUI)
	if !ok {
		return fmt.Errorf("this plugin renders no HTML surfaces")
	}
	html, err := w.UI(ctx, surface, id)
	if err != nil {
		return err
	}
	_, err = os.Stdout.WriteString(html)
	return err
}

// emitCall runs one action the plugin's own UI invoked through keel's bridge and
// prints its result as JSON. The work happens here, in the plugin's process; keel
// only proxied the call. Args arrive as key=value pairs.
func emitCall(ctx context.Context, p plugin.Plugin, action string, kvs []string) error {
	c, ok := p.(plugin.Caller)
	if !ok {
		return fmt.Errorf("this plugin answers no calls")
	}
	args := map[string]string{}
	for _, kv := range kvs {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			args[kv[:i]] = kv[i+1:]
		}
	}
	res, err := c.Call(ctx, action, args)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(res)
}

// emitPage renders one global page (no project, since a page is not scoped to
// one) and prints its View as JSON — the page twin of emitScreen.
func emitPage(ctx context.Context, p plugin.Plugin, id string) error {
	pr, ok := p.(plugin.Pager)
	if !ok {
		return fmt.Errorf("this plugin has no pages")
	}
	for _, pg := range pr.Pages() {
		if pg.ID == id {
			if pg.Render == nil {
				return json.NewEncoder(os.Stdout).Encode(plugin.View{})
			}
			v, err := pg.Render(ctx)
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(v)
		}
	}
	return fmt.Errorf("no page %q", id)
}

func emitScreen(ctx context.Context, p plugin.Plugin, id string, proj plugin.Project) error {
	sr, ok := p.(plugin.Screener)
	if !ok {
		return fmt.Errorf("this plugin has no screens")
	}
	for _, s := range sr.Screens() {
		if s.ID == id {
			v, err := s.Render(ctx, proj)
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(v)
		}
	}
	return fmt.Errorf("no screen %q", id)
}

func emitStepOptions(ctx context.Context, p plugin.Plugin, id string, proj plugin.Project) error {
	st, ok := p.(plugin.Stepper)
	if !ok {
		return fmt.Errorf("this plugin has no steps")
	}
	for _, s := range st.Steps() {
		if s.ID == id {
			opts, err := s.Options(ctx, proj)
			if err != nil {
				return err
			}
			out := make([]map[string]any, 0, len(opts))
			for _, o := range opts {
				out = append(out, map[string]any{"value": o.Value, "label": o.Label, "description": o.Description, "default": o.Default})
			}
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"options": out})
		}
	}
	return fmt.Errorf("no step %q", id)
}

func applyStep(ctx context.Context, p plugin.Plugin, id string, proj plugin.Project, values []string) error {
	st, ok := p.(plugin.Stepper)
	if !ok {
		return fmt.Errorf("this plugin has no steps")
	}
	for _, s := range st.Steps() {
		if s.ID == id {
			if s.Apply == nil {
				return nil
			}
			return s.Apply(ctx, stdio{}, proj, values)
		}
	}
	return fmt.Errorf("no step %q", id)
}

func runCommand(ctx context.Context, p plugin.Plugin, name string, proj plugin.Project, args []string) error {
	cr, ok := p.(plugin.Commander)
	if !ok {
		return fmt.Errorf("this plugin has no commands")
	}
	for _, c := range cr.Commands() {
		if c.Name == name {
			if c.Run == nil {
				return nil
			}
			return c.Run(ctx, stdio{}, proj, args)
		}
	}
	return fmt.Errorf("no command %q", name)
}

// stdio prints a command's output to stdout so keel, which captures the
// subprocess's output, themes it like any other plugin output.
type stdio struct{}

func (stdio) Title(t string)             { fmt.Println(t) }
func (stdio) Detail(label, value string) { fmt.Printf("%s: %s\n", label, value) }
func (stdio) Note(t string)              { fmt.Println(t) }
func (stdio) OK(t string)                { fmt.Println(t) }
func (stdio) Warn(t string)              { fmt.Fprintln(os.Stderr, t) }
func (stdio) Bad(t string)               { fmt.Fprintln(os.Stderr, t) }
func (stdio) List(title string, items []string) {
	fmt.Println(title)
	for _, it := range items {
		fmt.Println("  - " + it)
	}
}
