package console

import (
	"regexp"
	"strings"
	"testing"
)

var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// plain strips colour so a line's real width can be measured.
func plain(s string) string { return ansiSeq.ReplaceAllString(s, "") }

// No screen draws past the panel it is given, at any terminal width.
//
// Every screen's copy used to be hard-wrapped in the string itself, at about 72
// columns, and the tables used fixed column widths chosen for a wide terminal.
// A hard-wrapped paragraph is only correct at the width it was written for: the
// panel is the terminal minus a 26-column sidebar, so an 80-column terminal
// leaves it 52, and text ran twenty columns into the frame on every screen.
// The build wizard was worse: it named all seventeen steps on one line, about
// two hundred columns, which overflows every terminal there is.
func TestNoScreenDrawsPastThePanel(t *testing.T) {
	areas := []string{"projects", "build", "generate", "data", "logs", "packs", "plugins", "settings"}
	for _, total := range []int{80, 100, 120, 160} {
		for _, area := range areas {
			m := newModel(t)
			seedProject(t, "shop")
			m.reload()
			m.w, m.h = total, 34
			m.nav = m.indexOf(area)
			panelW := total - m.sidebarWidth() - 2

			// The overview, the screen it opens, and one level deeper: a project
			// picked, a task chosen, a wizard step answered.
			for _, stage := range []string{"overview", "open", "deep", "deeper"} {
				body := m.cur().render(&m, panelW, 24)
				if m.flow != nil {
					body = m.flow.view(panelW)
				}
				for _, ln := range strings.Split(plain(body), "\n") {
					if n := len([]rune(ln)); n > panelW {
						t.Errorf("%s at %d cols (panel %d): %s stage draws %d wide\n  %q",
							area, total, panelW, stage, n, strings.TrimRight(ln, " "))
					}
				}
				m = send(m, fkey("enter"))
			}
		}
	}
}

// The whole frame fits the terminal too, hero and footer included.
func TestFrameFitsTheTerminal(t *testing.T) {
	for _, total := range []int{80, 100, 160} {
		m := newModel(t)
		m.w, m.h = total, 34
		for _, ln := range strings.Split(plain(m.View()), "\n") {
			// The hero art is drawn beside the wordmark and lipgloss pads the
			// join, so allow the frame its own width and no more.
			if n := len([]rune(ln)); n > total {
				t.Errorf("at %d cols the frame draws a %d-wide line:\n  %q", total, n, strings.TrimRight(ln, " "))
			}
		}
	}
}

// A paragraph reflows to the panel rather than keeping the newlines it was
// written with.
func TestCopyReflows(t *testing.T) {
	long := "one two three four five six seven eight nine ten eleven twelve"
	narrow := plain(desc(20, long))
	for _, ln := range strings.Split(strings.TrimRight(narrow, "\n"), "\n") {
		if len([]rune(ln)) > 20 {
			t.Errorf("desc did not wrap to 20: %q", ln)
		}
	}
	if !strings.Contains(narrow, "\n") {
		t.Error("desc produced one long line instead of wrapping")
	}
	// And it does not wrap what already fits.
	if n := strings.Count(strings.TrimRight(plain(desc(80, long)), "\n"), "\n"); n != 0 {
		t.Errorf("desc broke a line that fits, %d breaks", n)
	}
}

// colWidths keeps two columns inside the space available.
func TestColWidths(t *testing.T) {
	if a, b := colWidths(40, 22, 18); a != 22 || b != 18 {
		t.Errorf("with room to spare colWidths = (%d,%d), want (22,18)", a, b)
	}
	for _, avail := range []int{40, 30, 20, 12, 0} {
		a, b := colWidths(avail, 22, 18)
		if a < 8 || b < 6 {
			t.Errorf("colWidths(%d) = (%d,%d), below the readable floor", avail, a, b)
		}
		if avail >= 16 && a+b > avail {
			t.Errorf("colWidths(%d) = (%d,%d), which does not fit", avail, a, b)
		}
	}
}
