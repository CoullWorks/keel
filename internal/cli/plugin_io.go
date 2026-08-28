package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/coullworks/keel/internal/tui"
	"github.com/coullworks/keel/plugin"
)

// termIO renders a plugin's output through keel's theme, so a plugin's lines
// carry the same colours and alignment as keel's own and cannot print raw text
// into a styled screen.
type termIO struct {
	out io.Writer
	err io.Writer
}

// newIO returns the plugin IO backed by these writers.
func newIO(out, errw io.Writer) plugin.IO { return &termIO{out: out, err: errw} }

func (t *termIO) Title(text string)          { fmt.Fprintln(t.out, tui.RenderTitle(text)) }
func (t *termIO) Detail(label, value string) { fmt.Fprintln(t.out, tui.RenderDetail(label, value)) }
func (t *termIO) Note(text string)           { fmt.Fprintln(t.out, tui.RenderNote(text)) }
func (t *termIO) OK(text string)             { fmt.Fprintln(t.out, tui.RenderOK(text)) }
func (t *termIO) Warn(text string)           { fmt.Fprintln(t.err, tui.RenderWarn(text)) }
func (t *termIO) Bad(text string)            { fmt.Fprintln(t.err, tui.RenderBad(text)) }

func (t *termIO) List(title string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprint(t.out, tui.RenderList(title, items))
}

// captureIO collects a plugin's output instead of drawing it, for the studio
// and for tests. Same interface, so a plugin cannot tell the difference and
// nothing has to be mocked.
type captureIO struct {
	lines []string
	bad   []string
}

func (c *captureIO) Title(text string)          { c.lines = append(c.lines, text) }
func (c *captureIO) Detail(label, value string) { c.lines = append(c.lines, label+": "+value) }
func (c *captureIO) Note(text string)           { c.lines = append(c.lines, text) }
func (c *captureIO) OK(text string)             { c.lines = append(c.lines, text) }
func (c *captureIO) Warn(text string)           { c.bad = append(c.bad, text) }
func (c *captureIO) Bad(text string)            { c.bad = append(c.bad, text) }

func (c *captureIO) List(title string, items []string) {
	c.lines = append(c.lines, title+": "+strings.Join(items, ", "))
}

// Output is everything the plugin wrote, and Problems is what it flagged.
func (c *captureIO) Output() []string   { return c.lines }
func (c *captureIO) Problems() []string { return c.bad }
