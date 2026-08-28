package tui

import "github.com/charmbracelet/lipgloss"

// Brand palette (COULLWORKS: dark + orange) and the shared Lip Gloss styles
// every keel screen renders through. No raw prints — everything is styled.
// Orange is the COULLWORKS brand accent — the one colour every keel surface
// shares. It is exported so the bare-`keel` console renders the same brand orange
// from a single source rather than a second copy that could drift out of step.
var Orange = lipgloss.Color("#ff6a2c")

var (
	cOrange = Orange
	cDim    = lipgloss.Color("245")
	cHead   = lipgloss.Color("252")
	cGreen  = lipgloss.Color("#3fb950")
	cYellow = lipgloss.Color("#d9a441")
	cRed    = lipgloss.Color("#e5484d")

	styTitle  = lipgloss.NewStyle().Bold(true).Foreground(cOrange)
	styHead   = lipgloss.NewStyle().Foreground(cHead)
	styDim    = lipgloss.NewStyle().Foreground(cDim)
	styAccent = lipgloss.NewStyle().Foreground(cOrange)
	styOK     = lipgloss.NewStyle().Foreground(cGreen)
	styWarn   = lipgloss.NewStyle().Foreground(cYellow)
	styBad    = lipgloss.NewStyle().Foreground(cRed)
	styKind   = lipgloss.NewStyle().Foreground(cOrange).Width(10)
	styPanel  = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cDim).
			Padding(0, 2)
	// styClip is the base style the wizard clips a line to the terminal width
	// with. Hoisted out of View() so a fresh style is not allocated on every
	// frame; the width is applied per-call via clipTo, which returns a derived
	// style (lipgloss styles are value types, so this does not mutate the base).
	styClip = lipgloss.NewStyle()
)

// clipTo returns styClip bounded to w columns. Lip Gloss styles are values, so
// MaxWidth yields a copy — the hoisted base is never mutated.
func clipTo(w int) lipgloss.Style { return styClip.MaxWidth(w) }
