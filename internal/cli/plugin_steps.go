package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/charmbracelet/huh"

	"github.com/coullworks/keel/internal/tui"
	"github.com/coullworks/keel/plugin"
)

// runPluginSteps asks every registered plugin's wizard steps and applies the
// answers.
//
// It runs after the project exists, so a step can write into it. keel does the
// prompting rather than the plugin, which is what makes a plugin's step look
// and behave like a built-in one and lets the same declaration drive the studio.
//
// yes skips the prompt and takes each step's defaults, so `keel new --yes` stays
// non-interactive.
func runPluginSteps(ctx context.Context, out io.Writer, p plugin.Project, yes bool) error {
	steps := registry().StepsFor(p.Framework)
	if len(steps) == 0 {
		return nil
	}
	pio := newIO(out, out)

	for _, s := range steps {
		opts, err := s.Options(ctx, p)
		if err != nil {
			// One plugin failing to offer its options must not stop a build that
			// has already succeeded.
			fmt.Fprintln(out, tui.RenderWarn(fmt.Sprintf("%s: %v", s.ID, err)))
			continue
		}
		if len(opts) == 0 {
			continue
		}

		chosen := defaults(opts)
		if !yes {
			chosen, err = ask(s, opts)
			if err != nil {
				return err
			}
		}
		if len(chosen) == 0 {
			continue
		}
		if err := s.Apply(ctx, pio, p, chosen); err != nil {
			fmt.Fprintln(out, tui.RenderWarn(fmt.Sprintf("%s: %v", s.ID, err)))
		}
	}
	return nil
}

// defaults is what a step gets when nobody is asked.
func defaults(opts []plugin.Option) []string {
	var out []string
	for _, o := range opts {
		if o.Default {
			out = append(out, o.Value)
		}
	}
	return out
}

// ask prompts for one step, multi-select or single depending on what it
// declared.
func ask(s plugin.Step, opts []plugin.Option) ([]string, error) {
	choices := make([]huh.Option[string], 0, len(opts))
	for _, o := range opts {
		label := o.Label
		if o.Description != "" {
			label = fmt.Sprintf("%-24s %s", o.Label, o.Description)
		}
		choices = append(choices, huh.NewOption(label, o.Value).Selected(o.Default))
	}

	if !s.Multi {
		var picked string
		f := huh.NewSelect[string]().Title(s.Title).Description(s.Help).Options(choices...).Value(&picked)
		if err := f.Run(); err != nil {
			return nil, err
		}
		if picked == "" {
			return nil, nil
		}
		return []string{picked}, nil
	}

	var picked []string
	f := huh.NewMultiSelect[string]().Title(s.Title).Description(s.Help).Options(choices...).Value(&picked)
	if err := f.Run(); err != nil {
		return nil, err
	}
	return picked, nil
}
