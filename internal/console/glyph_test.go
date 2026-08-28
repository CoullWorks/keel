package console

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// The sidebar icons are Nerd Font glyphs in the Unicode private use area.
//
// Terminals size a private-use codepoint from the font, and lipgloss sizes it
// from go-runewidth's tables. If those disagree the sidebar's border sits one
// column off on the rows that have an icon, which is exactly the kind of thing
// that looks like a rendering bug and is really an arithmetic one. This pins
// what keel believes, and asserts the sidebar is padded to a single width
// regardless.
func TestSidebarIconsAreSingleWidth(t *testing.T) {
	for _, a := range areaList() {
		if n := lipgloss.Width(a.icon); n != 1 {
			t.Errorf("%s icon %q measures %d columns; the sidebar is laid out assuming 1", a.title, a.icon, n)
		}
	}
	for _, id := range []string{"laravel", "magento", "django", "nextjs", "postgres", "redis", "nginx", "unknown"} {
		if n := lipgloss.Width(devIcon(id, id)); n != 1 {
			t.Errorf("devIcon(%q) measures %d columns, want 1", id, n)
		}
	}
}

// Every rendered sidebar line is the same width, icons or not. This is what
// actually keeps the border straight: if it holds, a font that draws the glyph
// differently shifts the icon inside the row, not the frame around it.
func TestSidebarRowsAreAllTheSameWidth(t *testing.T) {
	m := newModel(t)
	m.w, m.h = 120, 34
	lines := strings.Split(strings.TrimRight(m.sidebar(20), "\n"), "\n")
	if len(lines) < len(m.areas) {
		t.Fatalf("sidebar rendered %d lines for %d areas", len(lines), len(m.areas))
	}
	want := lipgloss.Width(lines[0])
	for i, ln := range lines {
		if got := lipgloss.Width(ln); got != want {
			t.Errorf("sidebar line %d is %d wide, line 0 is %d:\n  %q", i, got, want, ln)
		}
	}
	// And it is the width the layout reserves for it, border included.
	if want != m.sidebarWidth()+1 {
		t.Errorf("sidebar renders %d wide, layout reserves %d + 1 for the border", want, m.sidebarWidth())
	}
}
