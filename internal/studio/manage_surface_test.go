package studio

// Surface tests for the Issue-2 (data-driven overview) and Issue-3
// (manage-services) UI, in the same coarse style as
// overview_console_surface_test.go: read what index.html declares, paired with
// TestThePageParses proving the block runs under node.

import (
	"strings"
	"testing"
)

// Fix 2: the Overview is a data-led WIDGET dashboard — a top gauge/big-number
// tile row (Health / Uptime / DB), the services panel, and a framework-STATISTICS
// tile grid of real numbers. NOT a facts list, and NOT quick links.
func TestOverviewHasDataDrivenFacts(t *testing.T) {
	for _, want := range []string{
		`id="ovwidgets"`,             // the top gauge/big-number widget row host
		`id="ovfacts"`,               // the statistics grid host, drawn after services
		"function loadFacts(",        // ONE fetch that paints both widgets + stats grid
		"function renderWidgets(",    // draws the Health / Uptime / DB gauge tiles
		"function renderFacts(",      // draws the framework-STATISTICS tile grid
		`"/api/overview/facts?dir="`, // the facts endpoint
		"loadFacts(p.path)",          // renderOverview calls it on paint
		`class="ovwidgets"`,          // the top widget tile row
		`class="ovstats`,             // the framework-STATISTICS tile grid
		`class="ovstat"`,             // one big-number stat tile (value + label + sub)
		"function dbScore(",          // maps DB latency to a 0-100 ring score
		"sonRing(",                   // the reused AI-Visibility ring gauge for the tiles
		"Framework",                  // the identity stats the dashboard always shows
		"Database",                   // the DB widget
		"Health",                     // the health widget
		"Uptime",                     // the uptime widget
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("the data-led widget dashboard is missing %q", want)
		}
	}
	// Layout order: the top widget row, then the services panel, then the stats grid.
	widgets := strings.Index(indexHTML, `id="ovwidgets"`)
	svc := strings.Index(indexHTML, `id="ovservices"`)
	facts := strings.Index(indexHTML, `id="ovfacts"`)
	if widgets < 0 || svc < 0 || facts < 0 || !(widgets < svc && svc < facts) {
		t.Error("the overview must render widgets → services → statistics, in that order")
	}
	// NO Quick-actions or Plugins section, and none of the old multi-group cards.
	for _, gone := range []string{
		`actGroup("Quick actions"`, `actGroup("Develop"`, `actGroup("Configure"`, `actGroup("Ship"`,
		"function actGroup(", "function actRow(", "function loadPluginActions(", `id="ovpluginacts"`,
	} {
		if strings.Contains(indexHTML, gone) {
			t.Errorf("the overview must not carry quick-links / plugin-action remnants (%q)", gone)
		}
	}
}

// The framework STATISTICS render as a grid of big-number TILES (value + label +
// sub-line), not a text list — and the top row is real widgets (Health/Uptime/DB)
// that reuse the sonRing ring gauge, with an uptime badge on each running service.
func TestOverviewWidgetsAndStatTiles(t *testing.T) {
	for _, want := range []string{
		`class="ovw"`,     // a top widget tile
		`class="ovw-v"`,   // a widget's big value
		`class="ovstat-v`, // a stat tile's big number
		`class="ovstat-l`, // a stat tile's label
		`class="svc-up"`,  // the per-service uptime badge
		"s.uptime",        // the service row reads the parsed uptime
		"d.health",        // the health widget reads the health payload
		"db.up",           // the DB tile (renderDBTile) reads the db payload it is loaded with
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("the widget/stat-tile surface is missing %q", want)
		}
	}
}

// Dan's polish: the gauge tiles were "not inline and too close to text" and long
// stat values were cut mid-word ("laravel-docker" → "laravel-…"). The fix pins
// the tile layout — a real gap between gauge and text, a min-height for a
// consistent tile size, and a value that WRAPS (two-line clamp) with a title
// tooltip instead of a hard mid-word cut.
func TestOverviewWidgetSpacingAndNoMidWordCut(t *testing.T) {
	for _, want := range []string{
		`.ovw{`,                  // the widget tile rule
		`gap:16px`,               // a real gap between the gauge and the text
		`min-height:96px`,        // a consistent tile height across states
		`.ovw .ovw-body{`,        // the text column
		`.ovstat .ovstat-v{`,     // the stat value rule
		`-webkit-line-clamp:2`,   // the value wraps to two lines, not a hard cut
		`overflow-wrap:anywhere`, // a long value can break instead of overflow
		` title="${esc(v)}"`,     // the full value is on the value's title tooltip
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("the widget/stat spacing + no-mid-word-cut fix is missing %q", want)
		}
	}
	// The stat value must NOT be the old hard nowrap+ellipsis (which cut mid-word).
	i := strings.Index(indexHTML, `.ovstat .ovstat-v{`)
	if i < 0 {
		t.Fatal(".ovstat .ovstat-v rule not found")
	}
	rule := indexHTML[i : i+strings.Index(indexHTML[i:], "}")]
	if strings.Contains(rule, "white-space:nowrap") {
		t.Errorf(".ovstat-v must no longer be white-space:nowrap (it cut values mid-word); got: %s", rule)
	}
}

// The perf fix: the DB tile loads on its OWN async path so Health/Uptime/stats
// never wait ~5s on a down database. The facts feed no longer carries a pinged
// DB; the tile shows "checking…" then loadDBWidget swaps in the resolved tile
// from /api/overview/db.
func TestOverviewDBTileLoadsAsync(t *testing.T) {
	for _, want := range []string{
		`id="ovdb"`,               // the DB tile is its own async host
		"function loadDBWidget(",  // the DB tile's own loader
		"function renderDBTile(",  // the resolved-tile renderer, split out
		`"/api/overview/db?dir="`, // the DB tile's own endpoint
		"loadDBWidget(dir)",       // loadFacts re-kicks it after repaint
		"checking…",               // the calm placeholder while it pings
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("the async DB-tile load path is missing %q", want)
		}
	}
}

// Fix 3: the Manage-services tab lists the installed recipes with a Remove
// control, and still offers the add list — accurate on all frameworks (reads the
// manifest via /api/installed, not a per-framework guess).
func TestManageServicesListsInstalledWithRemove(t *testing.T) {
	for _, want := range []string{
		`"/api/installed?dir="`,      // the installed-recipes endpoint
		"function installedCard(",    // draws the installed list
		"function addableCard(",      // the add half stays
		"function removeRecipe(",     // wires Remove to keel remove
		`args:["remove",id,"--yes"]`, // the exec path keel remove uses
		"onclick='removeRecipe(",     // a per-recipe Remove control
		"Nothing added yet",          // the "none added yet" graceful case
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("the manage-services surface is missing %q", want)
		}
	}
	// The tab is reframed from add-only to manage: the nav label is "Manage
	// services", and the tab header leads with the installed list.
	if !strings.Contains(indexHTML, `label:"Manage services"`) {
		t.Error("the add tab must be reframed as Manage services")
	}
	if !strings.Contains(indexHTML, "Installed in ") {
		t.Error("the manage-services tab must show the installed recipes list")
	}
}
