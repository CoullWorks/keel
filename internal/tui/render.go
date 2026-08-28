package tui

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/coullworks/keel/internal/art"
	"github.com/coullworks/keel/internal/resolver"
)

// Splash is the keel mascot banner (the anchor + wordmark) for the CLI.
func Splash() string {
	return "\n" + art.Anchor(18) +
		" " + styTitle.Render("keel") + styDim.Render("   a web development studio for any stack") + "\n"
}

// wrapCmd soft-wraps a long shell command at width columns (breaking on spaces)
// so a preview box stays a sane width; continuation lines are indented. Widths
// are counted in runes, not bytes, so a command carrying a multi-byte character
// wraps at the column the terminal actually shows rather than one derived from
// its UTF-8 byte length.
func wrapCmd(s string, width int) string {
	if utf8.RuneCountInString(s) <= width {
		return s
	}
	var b strings.Builder
	line := ""
	for _, w := range strings.Fields(s) {
		switch {
		case line == "":
			line = w
		case utf8.RuneCountInString(line)+1+utf8.RuneCountInString(w) > width:
			b.WriteString(line + "\n    ")
			line = w
		default:
			line += " " + w
		}
	}
	b.WriteString(line)
	return b.String()
}

// RenderPlan returns a styled, boxed view of a resolved plan. steps is the
// execution-ordered command list (from engine.Steps), so the preview matches
// exactly what will run.
func RenderPlan(p *resolver.Plan, steps []string) string {
	var b strings.Builder
	b.WriteString(styTitle.Render("Keel plan") + styDim.Render("  ·  "+p.Framework) + "\n\n")
	for _, r := range p.Recipes {
		b.WriteString(styKind.Render(string(r.Kind)) + styHead.Render(r.Label) + "\n")
	}
	b.WriteString("\n" + styDim.Render("steps, in order") + "\n")
	for _, s := range steps {
		b.WriteString(styAccent.Render("$ ") + wrapCmd(s, 88) + "\n")
	}
	return styPanel.Render(strings.TrimRight(b.String(), "\n")) + "\n"
}

// ToolState is the health of a host tool.
type ToolState int

const (
	ToolMissing ToolState = iota // not on PATH
	ToolWarn                     // present but not usable (e.g. docker installed, daemon down)
	ToolOK                       // present and usable
)

// Tool is a host-tool check result for the doctor view.
type Tool struct {
	Name  string
	State ToolState
	Note  string // e.g. a version, or "installed, daemon not running"
}

// RenderDoctor returns a styled host-tool report as a themed table: one row per
// tool, its status coloured (green ok, yellow check, dim not found) and its
// version or note in the last column.
func RenderDoctor(tools []Tool) string {
	cols := []TableColumn{{Title: "tool", Width: 12}, {Title: "status", Width: 14}, {Title: "notes", Width: 40}}
	rows := make([][]string, len(tools))
	for i, t := range tools {
		var status string
		switch t.State {
		case ToolOK:
			status = "✓ ok"
		case ToolWarn:
			status = "! check"
		default:
			status = "✗ not found"
		}
		rows[i] = []string{t.Name, status, t.Note}
	}
	cell := func(row, col int, _ string) (lipgloss.Style, bool) {
		if col != 1 {
			return lipgloss.Style{}, false
		}
		switch tools[row].State {
		case ToolOK:
			return styOK, true
		case ToolWarn:
			return styWarn, true
		default:
			return styDim, true
		}
	}
	return styTitle.Render("keel doctor") + "\n\n" + RenderTable(cols, rows, cell) + "\n"
}

// RenderHomeSummary is the one-line profile summary shown atop the home menu.
func RenderHomeSummary(framework, env, db, editor string) string {
	return framework + " on " + env + ", " + db + " · editor " + editor
}

// RenderDone is the success line after a build.
//
// url is where the project can be opened, or "" for an environment that
// publishes nothing. It is worth a line of its own: keel's whole job here is to
// hand back a stack that runs, and until this was added it finished by naming
// three commands and never saying where the thing it had just built actually
// was. The value comes from the environment's own site_url, so it carries the
// real port rather than an assumed one.
func RenderDone(dir, url string) string {
	next := styDim.Render("  next: ") +
		styAccent.Render("cd "+dir) + styDim.Render("  ·  ") +
		styAccent.Render("keel secrets sync") + styDim.Render("  ·  ") +
		styAccent.Render("keel db migrate")
	out := "\n" + styOK.Render("✓ built") + " " + styHead.Render("./"+dir) + "\n"
	if url != "" {
		out += styDim.Render("  open: ") + styAccent.Render(url) + "\n"
	}
	return out + next + "\n"
}

// RenderSteps is a styled, boxed list of shell commands (used by `keel gen`).
func RenderSteps(title string, steps []string) string {
	var b strings.Builder
	b.WriteString(styTitle.Render(title) + "\n\n")
	for _, s := range steps {
		b.WriteString(styAccent.Render("$ ") + wrapCmd(s, 88) + "\n")
	}
	return styPanel.Render(strings.TrimRight(b.String(), "\n")) + "\n"
}

// RecipeRow is one row in the `keel recipes list` output.
// RecipeRow is one row of the recipe listing. The json tags are a contract:
// `keel recipes list --json` is what editors and scripts read, so the field
// names are lowercase and stable.
type RecipeRow struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Label   string `json:"label"`
	Source  string `json:"source"`
	Trusted bool   `json:"trusted"`
	// Scope is what the third column shows: a framework.s language, and for
	// everything else the frameworks it applies to. Added rather than renaming
	// anything: `keel recipes list --json` is a contract.
	Scope   string `json:"scope,omitempty"`
	Default bool   `json:"default,omitempty"`
}

// kindOrder is the order recipe kinds are shown in: the shape of a stack, from
// the thing you pick first to the things layered on it. It matches the resolver's
// own ordering, so the listing reads the way a build is composed.
var kindOrder = []string{
	"framework", "env", "db", "service", "config",
	"addon", "frontend", "starter", "extra", "generator",
}

// RenderRecipes prints recipes grouped by kind, with a count and column headers
// per group.
//
// It used to print all of them in one block sorted by source, which with a
// single built-in catalogue meant 235 undifferentiated lines: no headings, no
// counts, and the kind buried mid-line as the second column. Nobody reads that
// to find out which databases keel offers.
//
// Grouping by kind answers the question people actually arrive with - what
// frameworks are there, what can I put under them - and the third column carries
// the scope that matters for that kind: a framework's language, everything
// else's applies-to.
func RenderRecipes(rows []RecipeRow) string {
	byKind := map[string][]RecipeRow{}
	for _, r := range rows {
		byKind[r.Kind] = append(byKind[r.Kind], r)
	}
	// Any kind the list above does not name still gets shown, after the known
	// ones, so a pack that invents a kind is never silently dropped.
	seen := map[string]bool{}
	order := make([]string, 0, len(byKind))
	for _, k := range kindOrder {
		if len(byKind[k]) > 0 {
			order, seen[k] = append(order, k), true
		}
	}
	rest := make([]string, 0, len(byKind))
	for k := range byKind {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	order = append(order, rest...)

	var b strings.Builder
	b.WriteString(styTitle.Render("keel recipes") + styDim.Render(fmt.Sprintf("  ·  %d in %d kinds", len(rows), len(order))) + "\n")

	for _, kind := range order {
		group := byKind[kind]
		sort.SliceStable(group, func(i, j int) bool { return group[i].ID < group[j].ID })

		scope := "applies to"
		if kind == "framework" {
			scope = "language"
		}
		b.WriteString("\n" + styHead.Render(strings.ToUpper(kind)) +
			styDim.Render(fmt.Sprintf("  %d", len(group))) + "\n")

		// One themed table per kind group: a leading mark column carries the
		// security signal (untrusted beats default - where a recipe came from is
		// a security question, and a pack's recipes run shell commands on your
		// machine), then id, the kind's scope, and the name (wrapped, not cut).
		cols := []TableColumn{{Title: " ", Width: 2}, {Title: "id", Width: 24}, {Title: scope, Width: 16}, {Title: "name", Width: 34}}
		data := make([][]string, len(group))
		for i, r := range group {
			mark := ""
			switch {
			case !r.Trusted:
				mark = "⚠"
			case r.Default:
				mark = "•" // seeded unless you choose otherwise
			}
			data[i] = []string{mark, r.ID, r.Scope, r.Label}
		}
		cell := func(row, col int, _ string) (lipgloss.Style, bool) {
			if col == 0 {
				switch {
				case !group[row].Trusted:
					return styWarn, true
				case group[row].Default:
					return styOK, true
				}
			}
			return lipgloss.Style{}, false
		}
		b.WriteString(RenderTable(cols, data, cell) + "\n")
	}

	// Sources and their trust, once at the end rather than as a column: with a
	// single built-in catalogue a per-row source repeats 235 times and says
	// nothing, but the moment a pack is added its trust has to be visible.
	sources := map[string]bool{}
	var names []string
	for _, r := range rows {
		if _, seen := sources[r.Source]; !seen {
			names = append(names, r.Source)
		}
		sources[r.Source] = sources[r.Source] || !r.Trusted
	}
	sort.Strings(names)
	b.WriteString("\n" + styDim.Render("  •  seeded by default") + "\n")
	for _, s := range names {
		mark := styOK.Render("✓ trusted")
		if sources[s] {
			mark = styWarn.Render("⚠ untrusted  (its recipes run commands on your machine)")
		}
		b.WriteString(styDim.Render("  "+padTo(s, 22)) + mark + "\n")
	}
	// No outer panel: each kind group is already its own bordered table, so the
	// legend follows them as a plain trust key rather than a second box round the
	// lot.
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// truncate keeps a column a column. An over-long value ends in … so it is
// obvious something was cut rather than looking like the whole value.
//
// Exported because the console renders the same tables and must cut them the
// same way.
//
// Counted in runes, not bytes. The ellipsis is three bytes, so a byte-counted
// version produced a value that padTo then thought was already wide enough and
// left unpadded - which is how "django, magent…MariaDB 11.8 LTS" ran two columns
// into each other.
func Truncate(s string, w int) string {
	if utf8.RuneCountInString(s) <= w {
		return s
	}
	if w <= 1 {
		return "…"
	}
	out := []rune(s)[:w-1]
	return string(out) + "…"
}

// PluginRow is one row of `keel plugins list`.
//
// A bundled plugin would be a separate tool that ships inside the keel binary but
// is authored and maintained on its own — not a first-party keel feature. keel
// ships zero built-ins today (sonar, ai-core and the example are their own repos,
// discovered like any plugin), so no row is bundled in practice; the field
// remains for a future first-party bundled tool. Author and Homepage carry such a
// tool's provenance so both front ends can say whose tool it is; Bundled
// distinguishes it from a genuine part of keel. The json tags are a contract: the
// studio reads these.
type PluginRow struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Where       string `json:"where"` // "built-in" (compiled in), or "installed" (on disk)
	State       string `json:"state"` // enabled | disabled | not loaded
	Adds        string `json:"adds"`  // command, screen, step, event
	Description string `json:"description"`
	// BuiltIn means the plugin is compiled into this keel binary (whether a
	// first-party feature or a bundled separate tool). Bundled narrows that: a
	// separate COULLWORKS tool shipped with keel, not a first-party keel feature.
	BuiltIn  bool   `json:"builtIn"`
	Bundled  bool   `json:"bundled"`
	Author   string `json:"author,omitempty"`
	Homepage string `json:"homepage,omitempty"`
	Problem  string `json:"problem,omitempty"`
}

// RenderPlugins prints plugins as one table, with where each came from and
// whether it is actually doing anything.
//
// The two used to be printed in different shapes - built-ins as a paragraph
// each, installed ones as a trailing list - so there was no way to see at a
// glance what was available versus installed, or which of them were switched
// off. One table with a WHERE column says both.
func RenderPlugins(rows []PluginRow, installedDir string) string {
	// Count what is actually listed: a compiled-in plugin is "built-in" and
	// everything else came from the install dir. keel ships zero built-ins, so in a
	// stock build the built-in count is 0 and every row is installed; the count
	// still keys off BuiltIn so a future first-party plugin would be counted
	// correctly.
	builtIn, installed, bundledCount := 0, 0, 0
	for _, r := range rows {
		if r.BuiltIn {
			builtIn++
		} else {
			installed++
		}
		if r.Bundled {
			bundledCount++
		}
	}

	// One themed table. The per-plugin extras (description, a bundled tool's
	// provenance, a load problem) fold into a wrapping details column rather than
	// trailing sub-lines, so the whole listing is one aligned grid.
	cols := []TableColumn{
		{Title: "name", Width: 14}, {Title: "version", Width: 9}, {Title: "where", Width: 12},
		{Title: "state", Width: 13}, {Title: "adds", Width: 10}, {Title: "details", Width: 44},
	}
	data := make([][]string, len(rows))
	for i, r := range rows {
		details := r.Description
		// A bundled plugin is a separate tool that happens to ship inside keel;
		// naming its author and homepage keeps that honest rather than letting it
		// read as a first-party keel feature.
		if r.Bundled && (r.Author != "" || r.Homepage != "") {
			by := "separate tool"
			if r.Author != "" {
				by += " by " + r.Author
			}
			if r.Homepage != "" {
				by += "  ·  " + r.Homepage
			}
			details = strings.TrimSpace(details + "\n" + by)
		}
		if r.Problem != "" {
			details = strings.TrimSpace(details + "\n" + r.Problem)
		}
		data[i] = []string{r.Name, r.Version, r.Where, r.State, r.Adds, details}
	}
	cell := func(row, col int, _ string) (lipgloss.Style, bool) {
		switch col {
		case 3: // state
			switch rows[row].State {
			case "disabled":
				return styDim, true
			case "not loaded":
				return styBad, true
			default:
				return styOK, true
			}
		case 5: // details: a load problem reads red, everything else dim
			if rows[row].Problem != "" && rows[row].Description == "" {
				return styBad, true
			}
			return styDim, true
		}
		return lipgloss.Style{}, false
	}

	var b strings.Builder
	b.WriteString(styTitle.Render("keel plugins") +
		styDim.Render(fmt.Sprintf("  ·  %d built-in, %d installed", builtIn, installed)) + "\n\n")
	b.WriteString(RenderTable(cols, data, cell) + "\n")
	// Only explain "bundled" when a bundled tool is actually listed. keel ships
	// zero built-ins, so a stock build has none and the line would be noise.
	if bundledCount > 0 {
		b.WriteString("\n" + styDim.Render("  bundled plugins are separate COULLWORKS tools shipped with keel") + "\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteString(styDim.Render("  installed plugins live in "+installedDir) + "\n")
	b.WriteString(styDim.Render("  add one with  ") + styAccent.Render("keel plugins add <path|owner/repo>") + "\n")
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// FreshnessRow is one row of `keel recipes freshness`: a recipe, its status
// ("fresh" / "review-due (Nd)" / "no date") and any version pins, already
// formatted for display by the caller (the CLI owns the classification).
type FreshnessRow struct {
	ID      string
	Kind    string
	Updated string // "-" when the recipe carries no date
	Status  string // "ok", "review-due (Nd)" or "no date"
	Pins    string // "k=v  k=v", or "" when the recipe pins nothing
}

// RenderFreshness prints the freshness report as one themed table: the status
// column is coloured (red review-due, yellow no-date, dim ok), and a pins column
// is shown only when some recipe carries version pins, so a catalogue with none
// is not padded with an empty column. The caller keeps its own summary line.
func RenderFreshness(rows []FreshnessRow) string {
	anyPins := false
	for _, r := range rows {
		if r.Pins != "" {
			anyPins = true
			break
		}
	}
	cols := []TableColumn{
		{Title: "id", Width: 24}, {Title: "kind", Width: 12},
		{Title: "updated", Width: 12}, {Title: "status", Width: 18},
	}
	if anyPins {
		cols = append(cols, TableColumn{Title: "pins", Width: 30})
	}
	data := make([][]string, len(rows))
	for i, r := range rows {
		row := []string{r.ID, r.Kind, r.Updated, r.Status}
		if anyPins {
			row = append(row, r.Pins)
		}
		data[i] = row
	}
	cell := func(row, col int, _ string) (lipgloss.Style, bool) {
		if col != 3 {
			return lipgloss.Style{}, false
		}
		switch {
		case strings.HasPrefix(rows[row].Status, "review-due"):
			return styBad, true
		case rows[row].Status == "no date":
			return styWarn, true
		default:
			return styDim, true
		}
	}
	return RenderTable(cols, data, cell)
}

// CredentialRow is one row of `keel secrets credentials`: the credential's kind
// ("composer" / "env"), its id, and a detail that is never the secret itself (a
// username, "(token)", or a masked tail).
type CredentialRow struct {
	Kind   string
	ID     string
	Detail string
}

// RenderCredentials prints the remembered credentials as one themed table. It
// never carries a secret - the caller masks the detail column first. The caller
// prints the store path above and the count/remove hint below.
func RenderCredentials(rows []CredentialRow) string {
	cols := []TableColumn{
		{Title: "type", Width: 10}, {Title: "id", Width: 30}, {Title: "detail", Width: 24},
	}
	data := make([][]string, len(rows))
	for i, r := range rows {
		data[i] = []string{r.Kind, r.ID, r.Detail}
	}
	return RenderTable(cols, data, nil)
}

// ProxyRow is one row of `keel proxy status`: the project name, the URL the
// proxy reaches it at, and the pid of the process holding the port.
type ProxyRow struct {
	Name string
	URL  string
	PID  int
}

// RenderProxyStatus prints the reachable projects as one themed table (name,
// URL, pid). The caller keeps its own empty-state message.
func RenderProxyStatus(rows []ProxyRow) string {
	cols := []TableColumn{
		{Title: "name", Width: 24}, {Title: "url", Width: 40}, {Title: "pid", Width: 8},
	}
	data := make([][]string, len(rows))
	for i, r := range rows {
		data[i] = []string{r.Name, r.URL, fmt.Sprintf("%d", r.PID)}
	}
	return RenderTable(cols, data, nil)
}

// FindingRow is one row of `keel optimize`: a severity mark, a location, the
// rule name and the message. Sev carries the severity so the mark can be
// coloured without the renderer parsing the glyph.
type FindingRow struct {
	Mark     string // "✗" / "!" / "·"
	Sev      string // "error" / "warn" / "info"
	Location string // "file:line", "file", or "(repo)"
	Rule     string
	Message  string
}

// FindingGroup is one category's findings under its title ("Security", ...).
type FindingGroup struct {
	Title string
	Rows  []FindingRow
}

// RenderFindings prints the optimize findings as one themed table per category,
// each under its uppercased title, with the severity mark coloured (red error,
// yellow warn, dim info). The caller keeps its own summary/clean line.
func RenderFindings(groups []FindingGroup) string {
	cols := []TableColumn{
		{Title: " ", Width: 2}, {Title: "location", Width: 24},
		{Title: "rule", Width: 18}, {Title: "message", Width: 46},
	}
	var b strings.Builder
	for _, g := range groups {
		rows := g.Rows
		data := make([][]string, len(rows))
		for i, r := range rows {
			data[i] = []string{r.Mark, r.Location, r.Rule, r.Message}
		}
		cell := func(row, col int, _ string) (lipgloss.Style, bool) {
			if col != 0 {
				return lipgloss.Style{}, false
			}
			switch rows[row].Sev {
			case "error":
				return styBad, true
			case "warn":
				return styWarn, true
			default:
				return styDim, true
			}
		}
		b.WriteString("\n" + styTitle.Render(strings.ToUpper(g.Title)) + "\n")
		b.WriteString(RenderTable(cols, data, cell) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// ServiceRow is one row of `keel service`: a service's up/down state, its name,
// and a short kind label (the image or ddev type, may be empty).
type ServiceRow struct {
	State string // "up" / "down"
	Name  string
	Kind  string
}

// RenderServices prints the env services as one themed table with the state
// coloured (green up, dim down). A kind column is shown only when some service
// carries one, so a bare listing is not padded with an empty column. The caller
// prints the "services (family):" title above and any hint below.
func RenderServices(rows []ServiceRow) string {
	anyKind := false
	for _, r := range rows {
		if r.Kind != "" {
			anyKind = true
			break
		}
	}
	cols := []TableColumn{{Title: "state", Width: 7}, {Title: "name", Width: 18}}
	if anyKind {
		cols = append(cols, TableColumn{Title: "kind", Width: 24})
	}
	data := make([][]string, len(rows))
	for i, r := range rows {
		row := []string{r.State, r.Name}
		if anyKind {
			row = append(row, r.Kind)
		}
		data[i] = row
	}
	cell := serviceStateCell(rows)
	return RenderTable(cols, data, cell)
}

// RenderStatusServices prints the status view's services block: state + name
// only (status keeps it terse; the kind lives in `keel service`). State is
// coloured the same way, so status and service read alike.
func RenderStatusServices(rows []ServiceRow) string {
	cols := []TableColumn{{Title: "state", Width: 7}, {Title: "name", Width: 24}}
	data := make([][]string, len(rows))
	for i, r := range rows {
		data[i] = []string{r.State, r.Name}
	}
	return RenderTable(cols, data, serviceStateCell(rows))
}

// serviceStateCell colours the state column: green up, dim down.
func serviceStateCell(rows []ServiceRow) CellStyler {
	return func(row, col int, _ string) (lipgloss.Style, bool) {
		if col != 0 {
			return lipgloss.Style{}, false
		}
		if rows[row].State == "up" {
			return styOK, true
		}
		return styDim, true
	}
}

// RenderPacks prints the installed recipe packs as one themed table: name,
// version, commit and trust. Trust is coloured (green trusted, yellow
// untrusted). The caller keeps its own empty-state message.
func RenderPacks(rows []PackRow) string {
	cols := []TableColumn{
		{Title: "name", Width: 20}, {Title: "version", Width: 12},
		{Title: "commit", Width: 14}, {Title: "trust", Width: 12},
	}
	data := make([][]string, len(rows))
	for i, r := range rows {
		trust := "untrusted"
		if r.Trusted {
			trust = "trusted"
		}
		data[i] = []string{r.Name, r.Version, r.Commit, trust}
	}
	cell := func(row, col int, _ string) (lipgloss.Style, bool) {
		if col != 3 {
			return lipgloss.Style{}, false
		}
		if rows[row].Trusted {
			return styOK, true
		}
		return styWarn, true
	}
	return RenderTable(cols, data, cell)
}

// PackRow is one row of `keel recipes list --packs`: an installed pack's name,
// version, commit and trust.
type PackRow struct {
	Name    string
	Version string
	Commit  string
	Trusted bool
}

// RenderFiles is a styled, boxed list of file paths (used by the Magento generator).
func RenderFiles(title string, paths []string) string {
	var b strings.Builder
	b.WriteString(styTitle.Render(title) + "\n\n")
	for _, p := range paths {
		b.WriteString(styAccent.Render("✎ ") + styHead.Render(p) + "\n")
	}
	return styPanel.Render(strings.TrimRight(b.String(), "\n")) + "\n"
}

// The helpers below are what plugins render through. A plugin never touches
// lipgloss: it calls these, so every plugin's output carries the same theme and
// nothing can print raw text into a styled screen.

// RenderTitle is a section heading inside a command's output.
func RenderTitle(text string) string { return styTitle.Render(text) }

// RenderDetail is one label and value, aligned so a run of them lines up.
//
// The gap is always at least one space. Padding to a fixed width alone let a
// label longer than the column run straight into its value, which is how
// "ChatGPT / Copilot (Bing)48/100" got printed.
func RenderDetail(label, value string) string {
	return "  " + styDim.Render(padTo(label, 12)+" ") + styHead.Render(value)
}

// RenderList is a titled list of lines, for findings, notes or anything that is
// not a file. RenderFiles carries a write glyph and means something specific.
func RenderList(title string, items []string) string {
	var b strings.Builder
	b.WriteString(styTitle.Render(title) + "\n\n")
	for _, it := range items {
		b.WriteString(styAccent.Render("- ") + styHead.Render(it) + "\n")
	}
	return styPanel.Render(strings.TrimRight(b.String(), "\n")) + "\n"
}

// RenderNote is an ordinary line of output.
func RenderNote(text string) string { return "  " + styHead.Render(text) }

// RenderOK, RenderWarn and RenderBad carry the same colours as doctor and the
// build summary, so a plugin's success looks like keel's success.
func RenderOK(text string) string   { return "  " + styOK.Render("✓ ") + styHead.Render(text) }
func RenderWarn(text string) string { return "  " + styWarn.Render("! ") + styHead.Render(text) }
func RenderBad(text string) string  { return "  " + styBad.Render("✗ ") + styHead.Render(text) }

// padTo is local to these helpers: the wizard already has its own pad.
// padTo widens a value to a column, counted in runes so a value carrying an
// ellipsis or any other multi-byte character still gets its separating space.
func padTo(s string, w int) string {
	for n := utf8.RuneCountInString(s); n < w; n++ {
		s += " "
	}
	return s
}
