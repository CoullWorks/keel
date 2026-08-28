package tui

import (
	"strings"
	"testing"
)

// The shared table draws a real bordered grid (rounded border + column rules),
// keeps every cell's content, and wraps a long cell onto a second line instead
// of truncating it with an ellipsis.
func TestRenderTable(t *testing.T) {
	cols := []TableColumn{{Title: "name", Width: 8}, {Title: "note", Width: 12}}
	rows := [][]string{
		{"alpha", "short"},
		{"beta", "a note long enough to wrap"},
	}
	out := RenderTable(cols, rows, nil)
	// Headers are upper-cased and present.
	for _, want := range []string{"NAME", "NOTE", "alpha", "beta", "short"} {
		if !strings.Contains(out, want) {
			t.Fatalf("table missing %q:\n%s", want, out)
		}
	}
	// A bordered grid, not hand-spaced columns.
	if !strings.ContainsAny(out, "╭╮╰╯│─┬┼") {
		t.Fatalf("table is not drawn with box-drawing borders:\n%s", out)
	}
	// The long note wrapped rather than being cut with an ellipsis.
	if strings.Contains(out, "…") {
		t.Fatalf("table truncated a cell instead of wrapping:\n%s", out)
	}
	if !strings.Contains(out, "wrap") {
		t.Fatalf("long cell lost its wrapped tail:\n%s", out)
	}
}

// keel plugins renders as one table whose details column folds a bundled tool's
// provenance and a load problem in, so nothing is dropped and it is not hand-spaced.
func TestRenderPluginsTable(t *testing.T) {
	out := RenderPlugins([]PluginRow{
		{Name: "example", Version: "1.0.0", Where: "built-in", State: "enabled", Adds: "command", Description: "A demo plugin", BuiltIn: true},
		{Name: "sonar", Version: "0.4.1", Where: "built-in", State: "enabled", Adds: "command", Description: "AI audit", BuiltIn: true, Bundled: true, Author: "COULLWORKS", Homepage: "coullworks.com"},
		{Name: "broke", Version: "0.1", Where: "installed", State: "not loaded", Problem: "binary not found", BuiltIn: false},
	}, "~/.keel/plugins")
	for _, want := range []string{
		"keel plugins", "2 built-in, 1 installed",
		"NAME", "VERSION", "WHERE", "STATE", "ADDS", "DETAILS",
		"example", "sonar", "broke", "not loaded",
		"separate tool by COULLWORKS", "binary not found",
		"add one with",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("RenderPlugins missing %q:\n%s", want, out)
		}
	}
	if !strings.ContainsAny(out, "╭│─┼") {
		t.Fatalf("plugins is not a bordered table:\n%s", out)
	}
}

// keel optimize renders each category's findings as one bordered table under its
// title, keeping the location/rule/message and colouring the severity mark.
func TestRenderFindingsTable(t *testing.T) {
	out := RenderFindings([]FindingGroup{
		{Title: "Security", Rows: []FindingRow{
			{Mark: "✗", Sev: "error", Location: "app/config.php:42", Rule: "hardcoded-key", Message: "leaked key"},
		}},
		{Title: "Hygiene", Rows: []FindingRow{
			{Mark: "·", Sev: "info", Location: "(repo)", Rule: "no-readme", Message: "add a README"},
		}},
	})
	for _, want := range []string{
		"SECURITY", "HYGIENE",
		"app/config.php:42", "hardcoded-key", "leaked key",
		"(repo)", "no-readme", "add a README",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("RenderFindings missing %q:\n%s", want, out)
		}
	}
	if !strings.ContainsAny(out, "╭│─┼") {
		t.Fatalf("findings is not a bordered table:\n%s", out)
	}
}

// keel service renders services as one bordered table, keeping the state, name
// and (when present) the kind, and drops the kind column when no service has one.
func TestRenderServicesTable(t *testing.T) {
	out := RenderServices([]ServiceRow{
		{State: "up", Name: "db", Kind: "postgres:16"},
		{State: "down", Name: "redis"},
	})
	for _, want := range []string{"STATE", "NAME", "KIND", "up", "db", "postgres:16", "down", "redis"} {
		if !strings.Contains(out, want) {
			t.Fatalf("RenderServices missing %q:\n%s", want, out)
		}
	}
	if !strings.ContainsAny(out, "╭│─┼") {
		t.Fatalf("services is not a bordered table:\n%s", out)
	}

	// With no kind on any row the KIND column is dropped, so a bare listing is
	// not padded with an empty column.
	bare := RenderServices([]ServiceRow{{State: "up", Name: "app"}})
	if strings.Contains(bare, "KIND") {
		t.Fatalf("services with no kinds should not draw a KIND column:\n%s", bare)
	}
}

// keel status renders its services block as a bordered state+name table, without
// the kind column status deliberately omits.
func TestRenderStatusServicesTable(t *testing.T) {
	out := RenderStatusServices([]ServiceRow{
		{State: "up", Name: "db"},
		{State: "down", Name: "app"},
	})
	for _, want := range []string{"STATE", "NAME", "up", "db", "down", "app"} {
		if !strings.Contains(out, want) {
			t.Fatalf("RenderStatusServices missing %q:\n%s", want, out)
		}
	}
	if !strings.ContainsAny(out, "╭│─┼") {
		t.Fatalf("status services is not a bordered table:\n%s", out)
	}
}

// keel recipes freshness renders a bordered table and only draws the pins column
// when some recipe carries version pins.
func TestRenderFreshnessTable(t *testing.T) {
	out := RenderFreshness([]FreshnessRow{
		{ID: "laravel", Kind: "framework", Updated: "2026-08-01", Status: "ok", Pins: "php=8.4"},
		{ID: "redis", Kind: "service", Updated: "-", Status: "no date"},
	})
	for _, want := range []string{"ID", "KIND", "UPDATED", "STATUS", "PINS", "laravel", "no date", "php=8.4"} {
		if !strings.Contains(out, want) {
			t.Fatalf("RenderFreshness missing %q:\n%s", want, out)
		}
	}
	if !strings.ContainsAny(out, "╭│─┼") {
		t.Fatalf("freshness is not a bordered table:\n%s", out)
	}
	// No pins anywhere -> no PINS column.
	noPins := RenderFreshness([]FreshnessRow{{ID: "x", Kind: "env", Updated: "-", Status: "ok"}})
	if strings.Contains(noPins, "PINS") {
		t.Fatalf("freshness with no pins should not draw a PINS column:\n%s", noPins)
	}
}

// keel recipes list --packs, keel secrets credentials and keel proxy status all
// render as bordered tables keeping their identifying values.
func TestRenderPacksCredentialsProxyTables(t *testing.T) {
	packs := RenderPacks([]PackRow{{Name: "demopack", Version: "1.2.0", Commit: "abc1234", Trusted: false}})
	for _, want := range []string{"NAME", "VERSION", "COMMIT", "TRUST", "demopack", "1.2.0", "abc1234", "untrusted"} {
		if !strings.Contains(packs, want) {
			t.Fatalf("RenderPacks missing %q:\n%s", want, packs)
		}
	}

	creds := RenderCredentials([]CredentialRow{{Kind: "composer", ID: "repo.magento.com", Detail: "pubkey"}})
	for _, want := range []string{"TYPE", "ID", "DETAIL", "composer", "repo.magento.com", "pubkey"} {
		if !strings.Contains(creds, want) {
			t.Fatalf("RenderCredentials missing %q:\n%s", want, creds)
		}
	}

	proxy := RenderProxyStatus([]ProxyRow{{Name: "shop", URL: "http://shop.localhost", PID: 4321}})
	for _, want := range []string{"NAME", "URL", "PID", "shop", "http://shop.localhost", "4321"} {
		if !strings.Contains(proxy, want) {
			t.Fatalf("RenderProxyStatus missing %q:\n%s", want, proxy)
		}
	}

	for _, out := range []string{packs, creds, proxy} {
		if !strings.ContainsAny(out, "╭│─┼") {
			t.Fatalf("expected a bordered table:\n%s", out)
		}
	}
}
