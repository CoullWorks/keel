package engine

import "github.com/coullworks/keel/internal/resolver"

// PlanVarsForTest and RenderForTest expose the template layer to tests in other
// packages, so a test can assert what a recipe would actually write rather than
// re-implementing the substitution and asserting on its own copy.
func PlanVarsForTest(plan *resolver.Plan, project string) map[string]string {
	return planVars(plan, project)
}

func RenderForTest(s string, vars map[string]string) string { return render(s, vars) }
