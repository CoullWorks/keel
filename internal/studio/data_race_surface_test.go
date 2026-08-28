package studio

import (
	"strings"
	"testing"
)

// The Data grid renders are async (they await /api/db/*), so a write that lands
// after the user navigated away used to call .innerHTML on a null grid host —
// the "Cannot set properties of null" crash the fitness audit found on the Next
// Generate tab and the monorepo root. This is a surface test (reads what
// index.html declares, paired with TestThePageParses proving the block runs):
// the guarded writer gridSet must exist, bail when the host is gone or the tab
// changed, and every post-await grid write must go through it, not a raw
// gridEl().innerHTML.
func TestDataPreflightGuardsAgainstNullGrid(t *testing.T) {
	// The guard helper exists and gates on both a missing host and a tab change.
	for _, want := range []string{
		"function gridSet(html){",
		`if(!el||PTAB!=="data")return false;`,
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("the grid-write guard is missing %q", want)
		}
	}

	// dataPreflight bails via gridSet on its first paint and does not write to a
	// raw gridEl().innerHTML anymore.
	if !strings.Contains(indexHTML, `if(!gridSet('<span class="muted">checking the database…</span>'))return;`) {
		t.Errorf("dataPreflight should bail via gridSet when the Data tab is gone")
	}

	// The dangerous raw pattern must be gone from the async grid path. gridEl() is
	// still allowed to READ the host (e.g. editCell's querySelectorAll); what must
	// not survive is a raw `gridEl().innerHTML=` write that a mid-load navigation
	// can crash on.
	if strings.Contains(indexHTML, "gridEl().innerHTML=") {
		t.Errorf("a raw gridEl().innerHTML= write survives — post-await grid writes must go through gridSet")
	}
}
