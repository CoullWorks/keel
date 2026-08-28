package studio

import (
	"strings"
	"testing"
)

// These tests pin the fitness Round-2 [UI]/[Routing]/[Gen] front-end work the same
// coarse way ia_test.go pins the IA: by reading what the page declares, paired with
// TestThePageParses proving the block actually runs.

// One design system, shared by the wizard AND generate: token scales, an
// accordion, a real radio list, a checkbox list and an add-row — and the .opt pill
// retired as a selection control.
func TestPageHasDesignSystemPrimitives(t *testing.T) {
	for _, want := range []string{
		"--s1:", "--s6:", // spacing scale
		"--r-sm:", "--r-pill:", // radius scale
		"--dur:", "--ease:", // motion tokens
		".acc{", ".acc-badge", ".acc.done", ".acc.locked", // accordion + its states
		"grid-template-rows:0fr", "grid-template-rows:1fr", // pure-CSS expand
		".rlist", ".ritem", // single-select radio list
		".mlist", ".mitem", ".msub", // multi-select + category subheads
		".marows", ".marow", ".ma-add", // shared add-row
		"function accHTML(", "function rlistHTML(", "function mitemHTML(", // the builders
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("the shared design system is missing %q", want)
		}
	}
	// focus-visible states exist on the interactive primitives.
	if !strings.Contains(indexHTML, ":focus-visible") {
		t.Error("the primitives must declare :focus-visible states")
	}
}

// The build wizard renders as accordions driven by the existing buildSteps logic —
// the pill breadcrumb and pill clouds are gone.
func TestBuildWizardUsesAccordions(t *testing.T) {
	for _, want := range []string{
		"function buildSteps(",      // reused step model
		"function buildStepBody(",   // per-step body (rlist/mlist)
		"function buildStepChosen(", // prereq gate → done/locked/open
		"accHTML(i+1,st.title",      // one accordion per step
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("the accordion build wizard is missing %q", want)
		}
	}
	// The old crumb-pill breadcrumb inside the wizard is gone.
	if strings.Contains(indexHTML, `crumb.className="opts"`) {
		t.Error("the wizard still renders the old pill-cloud breadcrumb")
	}
}

// The Generate surface is the uniform module-plan accordion: a module header, an
// accordion per component type, per-component typed forms from GenInput, a staged
// add-many list backing a ModulePlan, and a whole-plan submit to /api/generate.
func TestGenerateIsUniformModulePlan(t *testing.T) {
	for _, want := range []string{
		"function genHeaderHTML(",         // module header (vendor/module/target)
		"function genComponentAccordion(", // an accordion per component type
		"function genDraftForm(",          // per-component typed form
		"function genScalarControl(",      // rendered generically from GenInput
		"function genFieldsTable(",        // the shared full fields table
		"function genStagedRow(",          // staged add-many list (edit/dup/remove)
		"function commitDraft(",           // stage an instance
		"function submitGenPlan(",         // submit the whole plan
		"/api/generate",                   // to the uniform endpoint
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("the uniform Generate surface is missing %q", want)
		}
	}
	// The full fields table carries the whole attribute set, not just nullable.
	for _, col := range []string{">Unique<", ">Index<", ">Default<", ">Len<"} {
		if !strings.Contains(indexHTML, col) {
			t.Errorf("the fields table is missing the %q column", col)
		}
	}
	// The old single-select pill generate menu is gone.
	if strings.Contains(indexHTML, "function selectGen(") || strings.Contains(indexHTML, "function renderGenMenu(") {
		t.Error("the old pill-cloud generate menu is still present")
	}
}

// The hash router exists and covers the required routes, driving the existing nav
// functions with a boot-from-URL.
func TestPageHasHashRouter(t *testing.T) {
	for _, want := range []string{
		"function serialise(",         // state → #/…
		"function navTo(",             // pushState
		"function applyRoute(",        // drive nav from a hash
		"function syncURL(",           // choke-point sync
		`addEventListener("popstate"`, // Back/Forward
		"history.pushState",           // real history entries
		"#/p/",                        // project deep link
		"/screen/",                    // a plugin screen deep link
		"applyRoute(location.hash)",   // boot-from-URL
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("the hash router is missing %q", want)
		}
	}
	// The choke points sync the URL.
	if !strings.Contains(indexHTML, "renderContent();syncURL()") {
		t.Error("go() must sync the URL")
	}
}
