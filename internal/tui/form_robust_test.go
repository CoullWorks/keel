package tui

import (
	"testing"

	"github.com/coullworks/keel/internal/profile"
	"github.com/coullworks/keel/internal/recipe"
)

// TestBuildStepsDynamicSurvivesEmptyPrior is the robustness claim for the wizard's
// Dynamic steps: called with an empty or short prior selection — which happens
// when an empty catalogue leaves the Language/Framework steps with no options and
// the wizard auto-selects them to nil — they must return empty choices, never
// panic on s[0][0] / s[1][0].
func TestBuildStepsDynamicSurvivesEmptyPrior(t *testing.T) {
	reg := recipe.NewRegistry() // deliberately empty: the worst case
	steps := BuildSteps(reg, &profile.Profile{Defaults: map[string]string{}})

	priors := [][][]string{
		nil,       // no prior selections at all
		{},        // empty slice
		{nil},     // language answered as nil (auto-selected, no options)
		{{}},      // language answered as empty
		{{}, {}},  // language + framework both empty
		{{"php"}}, // language only, framework not yet answered
		{{"php"}, {}},
	}
	for _, st := range steps {
		if st.Dynamic == nil {
			continue
		}
		for _, prior := range priors {
			// The whole point: this must not panic. A returned nil/empty is fine —
			// an empty catalogue simply has nothing to offer.
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("step %q Dynamic panicked on prior %v: %v", st.Title, prior, r)
					}
				}()
				_ = st.Dynamic(prior)
			}()
		}
	}
}
