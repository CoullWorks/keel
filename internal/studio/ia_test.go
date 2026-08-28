package studio

import (
	"strings"
	"testing"
)

// These tests pin the project-first information architecture of the studio page
// the same way ui_surface_test.go pins the API surface: by reading what the page
// declares. They are substring checks — a coarse guard — paired with the node
// syntax check (TestThePageParses) that proves the block actually runs.

// The IA is project-first: the top-level nav is Projects + global-only items,
// and the project OPERATIONS are declared as project-detail tabs, not top-level
// areas. This is the #1 fitness-review complaint (area-first → project-first).
func TestPageIsProjectFirst(t *testing.T) {
	for _, want := range []string{
		"const NAV=[",              // a top-level nav (not the old 9-area AREAS list)
		"const PTABS=[",            // project-detail tabs live inside a focused project
		`VIEW="home"`,              // the top-level route defaults to the Projects home
		"function renderProject(",  // a real project detail view
		"function renderProjTabs(", // with its own tab strip
		"function focusProject(",   // clicking a project focuses its detail view
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("the project-first IA is missing %q", want)
		}
	}
	// Every operation the fitness review said must live inside a project is a
	// project-detail tab id. Auth is deliberately NOT here: it is a level:stack
	// generatable, not a code component, so it lives inside Generate → Stacks and
	// the duplicate Auth tab was removed (fitness Round-2 [Gen] "Kill the Auth tab").
	for _, tab := range []string{`id:"overview"`, `id:"data"`, `id:"run"`, `id:"generate"`, `id:"secrets"`, `id:"add"`, `id:"deploy"`} {
		if !strings.Contains(indexHTML, tab) {
			t.Errorf("project-detail tab %q is missing", tab)
		}
	}
	// The standalone Auth tab must be gone — auth is reachable through Generate.
	if strings.Contains(indexHTML, `{id:"auth"`) || strings.Contains(indexHTML, "function renderAuth(") {
		t.Error("the standalone Auth tab is still present; auth must live in Generate → Stacks")
	}
}

// The old area-first model is gone: no flat AREAS list, no hidden global PROJ
// picker driven by a top-level area switch. Its removal is what makes the
// restructure real rather than additive.
func TestPageDropsAreaFirstModel(t *testing.T) {
	for _, gone := range []string{
		"const AREAS=[",            // the old flat 9-area nav
		"function renderProjects(", // the old boxy dashboard renderer
		"needProjectNotice(",       // the old "pick a project" area picker
		"openArea(",                // the old area-switch entry point
	} {
		if strings.Contains(indexHTML, gone) {
			t.Errorf("the area-first construct %q is still present; the restructure is incomplete", gone)
		}
	}
	// The boxy 3-up project cards are replaced by a rail + detail pane.
	if strings.Contains(indexHTML, "class=\"pgrid\"") {
		t.Error("the boxy 3-up project-card grid (.pgrid) is still rendered; use the rail + detail layout")
	}
}

// A persistent current-project header exists, so the user always knows what they
// are acting on — replacing the hidden PROJ global the review called out.
func TestPageHasCurrentProjectHeader(t *testing.T) {
	for _, want := range []string{
		"function renderProjHeader(", // the persistent header renderer
		`class="phead"`,              // its markup
		"function pingConn(",         // a live connectivity indicator in the header
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("the current-project header is missing %q", want)
		}
	}
	// Breadcrumb reads Projects › <name> › <tab>.
	if !strings.Contains(indexHTML, `go('home')`) || !strings.Contains(indexHTML, "renderCrumbs(") {
		t.Error("the breadcrumb should link back to the Projects home")
	}
}

// Monorepo drill-down: a monorepo focuses to a members list, a member opens its
// OWN full detail view (not just Data), and the shared backend is surfaced from
// the member-backend endpoint (project.EffectiveBackend).
func TestPageMonorepoDrilldown(t *testing.T) {
	for _, want := range []string{
		"function monorepoBody(", // the monorepo members view
		"function memberRow(",    // each member row
		"/api/member/backend",    // wires the effective shared backend
		"loadBackend(",           // fetched on focus for the header + Data tab
		`class="backend"`,        // the shared-backend banner shown once at the monorepo level
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("monorepo drill-down is missing %q", want)
		}
	}
	// A lib member is listed but non-runnable (its detail tabs stay gated).
	if !strings.Contains(indexHTML, "isLib(") {
		t.Error("a lib member must be marked non-runnable")
	}
}

// Cmd+K jumps to a project or a project-scoped action, not just a top-level area.
func TestPaletteJumpsToProjectActions(t *testing.T) {
	for _, want := range []string{
		"function openPTab(", // a palette entry into a project tab
		"Open project · ",    // jump straight to a project
		"Browse data · ",     // and to a project-scoped action
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("the command palette is missing %q", want)
		}
	}
}
