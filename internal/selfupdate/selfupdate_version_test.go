package selfupdate

import "testing"

// TestNewerEdgeCases hits the branches TestNewer misses: unparseable versions
// (best-effort string inequality) and non-dev equal-after-norm handling.
func TestNewerEdgeCases(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"1.2.3.4", "1.2.3.5", true},      // >3 fields → unparseable → l != c
		{"weird", "weirder", true},        // non-numeric → best effort, differ
		{"same", "same", false},           // unparseable but equal after norm
		{"1.2.x", "1.2.0", true},          // Atoi fails on "x" → unparseable, differ
		{"1.2.0-dev", "1.2.0-dev", false}, // equal after norm → not newer even though dev
		{"1", "2", true},                  // single-field versions parse
		{"1.5", "1.4", false},             // two-field versions parse
		// A version merely *containing* "dev" (not a -dev suffix) must be compared
		// numerically, not treated as always-older. Here current 2.0.0-devil > 1.9.9,
		// so it is NOT newer — the old strings.Contains(current,"dev") got this wrong.
		{"2.0.0-devil", "1.9.9", false},
		{"1.0.0-development", "2.0.0", true}, // still older than 2.0.0 numerically
	}
	for _, c := range cases {
		if got := Newer(c.cur, c.latest); got != c.want {
			t.Errorf("Newer(%q,%q) = %v, want %v", c.cur, c.latest, got, c.want)
		}
	}
}

func TestParseVer(t *testing.T) {
	cases := []struct {
		in   string
		want [3]int
		ok   bool
	}{
		{"1.2.3", [3]int{1, 2, 3}, true},
		{"1.2", [3]int{1, 2, 0}, true},
		{"1", [3]int{1, 0, 0}, true},
		{"1.2.3.4", [3]int{}, false}, // too many fields
		{"1.x.3", [3]int{}, false},   // non-numeric field
		{"", [3]int{}, false},        // empty → Atoi("") fails
	}
	for _, c := range cases {
		got, ok := parseVer(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseVer(%q) = %v,%v; want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}
