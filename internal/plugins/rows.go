package plugins

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/coullworks/keel/internal/pluginstore"
	"github.com/coullworks/keel/internal/tui"
	"github.com/coullworks/keel/plugin"
)

// Prefix is the naming convention for an external plugin executable: any
// keel-<name> on PATH is invoked as `keel <name>`, git-style.
const Prefix = "keel-"

// Rows is every plugin keel knows about, in one list, however it got there.
//
// There are three ways a plugin can exist and they are easy to confuse, so both
// front ends read this rather than each deciding for itself: the console used to
// list only what was installed under the plugins directory, which meant it said
// "nothing installed yet" on a build with three plugins compiled into it.
func Rows(reg *Registry) []tui.PluginRow {
	var rows []tui.PluginRow

	// Everything installed on disk, indexed so a loaded plugin can say where it
	// came from and a broken one still gets a row.
	onDisk := map[string]pluginstore.Installed{}
	if installed, err := pluginstore.List(); err == nil {
		for _, p := range installed {
			onDisk[p.Name] = p
		}
	}

	loaded := map[string]bool{}
	if reg != nil {
		for _, p := range reg.Plugins() {
			loaded[p.Meta().Name] = true
			rows = append(rows, builtInOrInstalledRow(p, "enabled"))
		}
		// A built-in the user switched off is not in Plugins(), because it never
		// registered — but dropping it from the listing would make it look
		// uninstallable rather than off. It is shown as present-but-disabled so
		// re-enabling it is a visible choice.
		for _, p := range reg.DisabledBuiltins() {
			loaded[p.Meta().Name] = true
			rows = append(rows, builtInOrInstalledRow(p, "disabled"))
		}
	}

	// Installed but not in the registry: switched off, or broken. Both are
	// listed with the reason, because a plugin that silently does nothing reads
	// as a plugin that does nothing on purpose.
	//
	// Whether it is off is what the store says, not whether it loaded: a plugin
	// that is enabled and still failed to register is a different problem, and
	// calling it "disabled" hid both the truth and the reason.
	failures := loadFailures(reg)
	for _, p := range onDisk {
		if loaded[p.Name] {
			continue
		}
		state, problem := "disabled", p.Problem
		switch {
		case problem != "":
			state = "not loaded"
		case p.Enabled:
			state, problem = "not loaded", failures[p.Name]
			if problem == "" {
				problem = "enabled, but keel did not register it"
			}
		}
		rows = append(rows, tui.PluginRow{
			Name: p.Name, Version: p.Meta.Version, Where: "installed", State: state,
			Description: p.Meta.Description, Problem: problem,
		})
	}

	// keel-* executables on PATH: a third way a command can exist, and one
	// people forget they set up.
	for _, name := range Discover() {
		path, _ := exec.LookPath(Prefix + name)
		rows = append(rows, tui.PluginRow{
			Name: name, Where: path, State: "enabled", Adds: "command",
			Description: "external executable on PATH, run as `keel " + name + "`",
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].BuiltIn != rows[j].BuiltIn {
			return rows[i].BuiltIn // built-ins first: they are always there
		}
		return rows[i].Name < rows[j].Name
	})
	return rows
}

// builtInOrInstalledRow builds a row for a plugin the registry loaded (or a
// built-in it deliberately skipped because it was disabled).
//
// A built-in that is a separate COULLWORKS tool carries its own author and
// homepage, so the listing can say whose tool it is rather than implying keel
// wrote it. "installed" rather than a directory: every installed plugin lives in
// the same one, which the caller names once; repeating it per row only truncated
// it into uselessness.
func builtInOrInstalledRow(p plugin.Plugin, state string) tui.PluginRow {
	m := p.Meta()
	builtIn := IsBuiltin(m.Name)
	bundled := builtIn && IsBundled(m.Name)
	// Where answers a different question from Bundled: where the plugin came from
	// (compiled into the binary, or installed on disk), not whether it is a
	// first-party keel feature. Keeping "built-in" here means the separate-tool
	// distinction is carried by Bundled/Author/Homepage, which is the orthogonal,
	// non-lossy way to say it — and it keeps the WHERE column stable.
	where := "installed"
	if builtIn {
		where = "built-in"
	}
	row := tui.PluginRow{
		Name: m.Name, Version: m.Version, Where: where, State: state,
		Adds: AddsSummary(p), Description: m.Description,
		BuiltIn: builtIn, Bundled: bundled,
	}
	// Provenance is only meaningful for a separate tool: a first-party feature's
	// author is keel itself, which says nothing.
	if bundled {
		row.Author, row.Homepage = m.Author, m.Homepage
	}
	return row
}

// loadFailures indexes the registry's problems by plugin name.
//
// Load reports them as "<name>: <what went wrong>", which is right for printing
// them in a list but useless for putting a reason next to the plugin it belongs
// to. Splitting on the first colon is enough, and a problem that does not carry
// a name is simply not attributed to one.
func loadFailures(reg *Registry) map[string]string {
	out := map[string]string{}
	if reg == nil {
		return out
	}
	for _, err := range reg.Problems() {
		name, reason, found := strings.Cut(err.Error(), ": ")
		if !found || strings.Contains(name, " ") {
			continue
		}
		out[name] = reason
	}
	return out
}

// AddsSummary is what a plugin contributes, in a few words: the one thing a
// listing exists to show beyond which plugins are there and whether they are on.
func AddsSummary(p plugin.Plugin) string {
	var kinds []string
	if c, ok := p.(plugin.Commander); ok && len(c.Commands()) > 0 {
		kinds = append(kinds, "command")
	}
	if s, ok := p.(plugin.Screener); ok && len(s.Screens()) > 0 {
		kinds = append(kinds, "screen")
	}
	if s, ok := p.(plugin.Stepper); ok && len(s.Steps()) > 0 {
		kinds = append(kinds, "step")
	}
	// OptionStepper / Actioner / Overviewer enumerate against a ctx + project, so
	// here we report by capability (does the plugin implement the hook) rather than
	// by item count — enough for the "what does this add" summary. option-step is
	// only meaningful alongside steps: the declarative adapter satisfies
	// OptionStepper for every plugin, so a plugin with no steps would otherwise be
	// mislabelled as offering a headless form it does not have.
	if _, ok := p.(plugin.OptionStepper); ok {
		if s, ok := p.(plugin.Stepper); ok && len(s.Steps()) > 0 {
			kinds = append(kinds, "option-step")
		}
	}
	if _, ok := p.(plugin.Actioner); ok {
		kinds = append(kinds, "action")
	}
	if _, ok := p.(plugin.Overviewer); ok {
		kinds = append(kinds, "overview")
	}
	if pg, ok := p.(plugin.Pager); ok && len(pg.Pages()) > 0 {
		kinds = append(kinds, "page")
	}
	if r, ok := p.(plugin.Reciper); ok && len(r.Recipes()) > 0 {
		kinds = append(kinds, "recipes")
	}
	if _, ok := p.(plugin.Installer); ok {
		kinds = append(kinds, "install")
	}
	if _, ok := p.(plugin.Listener); ok {
		kinds = append(kinds, "event")
	}
	if len(kinds) == 0 {
		return "nothing yet"
	}
	return strings.Join(kinds, ", ")
}

// Discover lists the keel-<name> executables on PATH, de-duplicated: the first
// PATH entry wins, as it would when the shell resolves it.
func Discover() []string {
	seen := map[string]bool{}
	var out []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasPrefix(e.Name(), Prefix) {
				continue
			}
			name := strings.TrimPrefix(e.Name(), Prefix)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
