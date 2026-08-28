package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

// table.go is keel's one real table. Every list command used to hand-space its
// own columns with padTo/%-Ns and truncate overflow with an ellipsis, so
// alignment drifted between commands and long cells were silently cut. This
// routes them all through a single lipgloss/table renderer: a rounded dim border
// (the same frame styPanel uses), an orange header row, and columns that wrap to
// their width instead of truncating - the "typer/rich" look keel was faking.

// TableColumn configures one column: its header and the width its cells wrap to.
// A width of 0 lets lipgloss size the column to its content.
type TableColumn struct {
	Title string
	Width int
}

// CellStyler colours one body cell. row is the 0-based data-row index and col
// the column; it returns the style to paint that cell and true to use it, or the
// zero value and false to fall back to the column default (accent first column,
// dim/head elsewhere). It is how a command paints a red "not found" or a yellow
// "untrusted" without reaching past the shared renderer.
type CellStyler func(row, col int, val string) (lipgloss.Style, bool)

var (
	// styTableHeader is the one header row: bold orange, so the columns read the
	// same in every command.
	styTableHeader = lipgloss.NewStyle().Bold(true).Foreground(cOrange).Padding(0, 1)
	// styTableCell is the default body cell.
	styTableCell = lipgloss.NewStyle().Foreground(cHead).Padding(0, 1)
	// styTableAccent is the default first-column cell: the row's identity (a name,
	// an id) reads in the accent colour, matching the old hand-styled columns.
	styTableAccent = lipgloss.NewStyle().Foreground(cOrange).Padding(0, 1)
)

// RenderTable renders rows as one themed, bordered table. cols carries the
// headers and per-column wrap widths; cell, if non-nil, overrides individual
// body cells' colour. It returns the table with no trailing newline, so a caller
// can print a title above it and a legend below.
func RenderTable(cols []TableColumn, rows [][]string, cell CellStyler) string {
	headers := make([]string, len(cols))
	for i, c := range cols {
		headers[i] = strings.ToUpper(c.Title)
	}
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(cDim)).
		BorderRow(false).
		BorderColumn(true).
		Headers(headers...).
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			st := styTableCell
			switch {
			case row == table.HeaderRow:
				st = styTableHeader
			case cell != nil && row >= 0 && row < len(rows) && col < len(rows[row]):
				if s, ok := cell(row, col, rows[row][col]); ok {
					st = s.Padding(0, 1)
				} else if col == 0 {
					st = styTableAccent
				}
			case col == 0:
				st = styTableAccent
			}
			if col < len(cols) && cols[col].Width > 0 {
				st = st.Width(cols[col].Width)
			}
			return st
		})
	return t.Render()
}
