package cli

import "github.com/charmbracelet/huh"

// Prompts go through these package vars so command paths that ask a question are
// testable without a terminal. Swapping the var in a test is the whole seam.
var (
	// confirm asks a yes/no question, writing the answer into v.
	confirm = func(title string, v *bool) error {
		return huh.NewConfirm().Title(title).Value(v).Run()
	}
	// selectOne asks the user to pick one option, writing the choice into v.
	selectOne = func(title string, opts []huh.Option[string], v *string) error {
		return huh.NewSelect[string]().Title(title).Options(opts...).Value(v).Run()
	}
)
