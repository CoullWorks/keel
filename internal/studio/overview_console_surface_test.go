package studio

import (
	"strings"
	"testing"
)

// These tests pin the three studio fixes the same coarse way ia_test.go pins the
// IA: by reading what index.html declares, paired with TestThePageParses proving
// the block actually runs.

// Fix 1 + 2: the Overview is a running-services dashboard and carries NO plugin
// content. The services section is drawn first; the old plugin-overview block
// (loadOverviewSections / #ovplugins) is gone — plugins live in the Plugins nav
// and their screen tabs, not the overview.
func TestOverviewIsServicesDashboardWithoutPlugins(t *testing.T) {
	// The services dashboard exists and is wired into renderOverview.
	for _, want := range []string{
		`id="ovservices"`,          // the primary overview section host
		"function loadServices(",   // fetches /api/services and draws it
		"function renderServices(", // draws the per-service rows + overall state
		"function svcAction(",      // per-service start/stop/restart control
		`"/api/services?dir="`,     // the enumeration endpoint
		`"/api/service/action"`,    // the per-service control endpoint
		"loadServices(p.path)",     // renderOverview calls it on paint
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("the overview services dashboard is missing %q", want)
		}
	}
	// The plugin-overview block must be GONE from the overview: neither the host
	// div nor the loader that filled it may remain.
	for _, gone := range []string{
		`id="ovplugins"`,
		"function loadOverviewSections(",
		"loadOverviewSections(",
	} {
		if strings.Contains(indexHTML, gone) {
			t.Errorf("the overview still carries plugin content (%q); plugins belong in the Plugins nav", gone)
		}
	}
	// A per-service row shows name + up/down state + kind, and the controls are
	// gated on the backend saying the runtime supports them.
	for _, want := range []string{`class="svc"`, "svc-state", "d.controls?"} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("a service row is missing %q", want)
		}
	}
}

// Fix 3: the console is always reopenable from the top bar, the drag is clamped
// so it can never vanish, and its state is persisted.
func TestConsoleToggleAndClampedDrag(t *testing.T) {
	for _, want := range []string{
		`id="consoletoggle"`,                       // a persistent top-bar toggle button in #head
		"function toggleConsole(",                  // opens/closes the drawer
		"function setTermHeight(",                  // the single, clamped height writer
		"function openConsole(",                    // reopen restores a sensible open height
		"function restoreConsole(",                 // persisted state applied on load
		"TERM_CLOSED_PX",                           // the thin, still-visible closed floor
		"localStorage.setItem(\"keel.termClosed\"", // persistence of open/closed
		"localStorage.setItem(\"keel.termPx\"",     // persistence of the height
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("the console toggle/persist logic is missing %q", want)
		}
	}
	// The drag must clamp to a floor (never zero) and a max (never crush #content):
	// setTermHeight uses Math.max(TERM_CLOSED_PX, …) and a termMaxPx() ceiling.
	if !strings.Contains(indexHTML, "Math.max(TERM_CLOSED_PX,Math.min(px,termMaxPx()))") {
		t.Error("setTermHeight must clamp between the closed floor and a bounded max, so the console can neither vanish nor crush the content pane")
	}
	// The dragbar routes through the single clamped writer, not a raw
	// gridTemplateRows assignment with a floor of 80 that the old bug used.
	if !strings.Contains(indexHTML, "setTermHeight(rect.bottom-e.clientY)") {
		t.Error("the dragbar must set the height through setTermHeight so the clamp applies")
	}
	// The content pane must stay scrollable at every drawer height: the grid keeps
	// #content overflow:auto + min-height:0 (the earlier scroll fix), independent
	// of the console size.
	if !strings.Contains(indexHTML, "#content{min-height:0;overflow:auto") {
		t.Error("#content must remain overflow:auto;min-height:0 so a collapsed or huge console never breaks page scroll")
	}
	// The #body min-height:0 scroll fix must remain intact.
	if !strings.Contains(indexHTML, "#body{grid-area:body;min-width:0;min-height:0") {
		t.Error("the #body{min-height:0} scroll fix must not be regressed")
	}
}
