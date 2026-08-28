package console

import (
	"fmt"
	"strings"
)

// The copy on these screens is written as single-line paragraphs and wrapped
// here, against the width the panel actually has.
//
// It used to be hard-wrapped in the strings themselves, at about 72 columns. A
// hard-wrapped paragraph is only correct at the width it was written for: the
// panel is the terminal minus a 26-column sidebar, so an 80-column terminal
// gives it 52, and every screen's text ran twenty columns past the frame.

func h1(s string) string { return mainTitle.Render(s) + "\n\n" }

// desc is the paragraph under a heading, wrapped to the panel.
func desc(w int, s string) string { return styHead.Width(w).Render(s) + "\n\n" }

// note is a dimmed paragraph, wrapped to the panel.
func note(w int, s string) string { return styDim.Width(w).Render(s) + "\n" }

// cmd is one example command line and what it does. The pair is cut to the
// panel rather than wrapped: half a command on the next line reads as a second
// command.
func cmd(w int, name, what string) string {
	line := name + "  " + what
	if len([]rune(line)) > w {
		return styWord.Render(name) + styDim.Render(" "+trimTo(what, w-len([]rune(name))-1)) + "\n"
	}
	return styWord.Render(name) + styDim.Render("  "+what) + "\n"
}

// trimTo cuts to width in runes, marking the cut. Never negative.
func trimTo(s string, w int) string {
	if w < 2 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	return string(r[:w-1]) + "…"
}

func areaProjects(m *model, w, h int) string {
	return h1("Projects") +
		desc(w, "Your projects on this machine, and everything you do to an existing one.") +
		note(w, "Adopt a project (keel writes a small .keel manifest) to make it managed, then run these against it:") + "\n" +
		cmd(w, "keel adopt <path>", "detect the stack + env, make it keel-managed") +
		cmd(w, "keel db migrate", "database tasks through its env") +
		cmd(w, "keel secrets sync", "create .env + generate app keys") +
		cmd(w, "keel optimize", "scan for secrets, perf and hygiene issues") +
		cmd(w, "keel deploy", "generate production deploy artifacts") + "\n" +
		note(w, "The studio (keel studio) shows these as buttons on each project.")
}

func areaBuild(m *model, w, h int) string {
	return h1("Build a stack") +
		desc(w, "Compose a brand-new project. Pick a framework and keel wires the local dev env, database, backing services and add-ons, then runs the real installers (composer, artisan, uv, npm, ddev), not just an empty folder.") +
		note(w, fmt.Sprintf("Order: Language, Framework, Env, Database, Frontend, Services, Add-ons. (%d recipes)", m.recipeCount)) + "\n" +
		cmd(w, "keel new", "interactive builder, pre-filled from your defaults") +
		cmd(w, "keel new laravel --with filament,redis", "flag-driven") +
		cmd(w, "keel new laravel --dry-run", "show every step, change nothing")
}

func areaGenerate(m *model, w, h int) string {
	return h1("Generate") +
		desc(w, "Scaffold code components inside an existing project.") +
		note(w, "Laravel: events, listeners, controllers, models, via artisan make.") +
		note(w, "Magento: modules, models, observers, blocks, from templates.") + "\n" +
		cmd(w, "keel gen model Order", "generate a component") +
		cmd(w, "keel gen event OrderPlaced", "name it on the command line")
}

func areaData(m *model, w, h int) string {
	return h1("Data") +
		desc(w, "Run database tasks and ad-hoc SQL against a project, through its env, so it works the same under DDEV, Sail, Docker or Local.") +
		cmd(w, "keel db migrate", "run migrations") +
		cmd(w, "keel db seed", "seed data") +
		cmd(w, "keel db status", "show migration status") + "\n" +
		note(w, "The studio adds a SQL console, run through ddev psql or docker compose.")
}

func areaLogs(m *model, w, h int) string {
	return h1("Run & Logs") +
		desc(w, "Start, stop and watch a project's env and build output live.") +
		note(w, "Under the hood these are your env's own commands:") + "\n" +
		cmd(w, "ddev start / restart / logs -f", "DDEV env") +
		cmd(w, "docker compose up -d / logs -f", "Docker env") + "\n" +
		note(w, "The studio streams build and task output into its console panel in real time.")
}

func areaPacks(m *model, w, h int) string {
	return h1("Packs") +
		desc(w, "Recipe packs are shareable bundles of stacks and add-ons that extend keel's catalogue. Installed packs show up everywhere a built-in recipe does.") +
		styHead.Render(fmt.Sprintf("%d recipes loaded", m.recipeCount)) + styDim.Render("  (built-in + user + packs)") + "\n" +
		styHead.Render(fmt.Sprintf("%d recipe packs installed", m.packCount)) + "\n\n" +
		cmd(w, "keel recipes search", "find community packs on GitHub") +
		cmd(w, "keel recipes add <owner/repo>", "install, validated, never runs its code") +
		cmd(w, "keel new-recipe --pack", "author your own")
}

func areaSettings(m *model, w, h int) string {
	if m.setup {
		title, opts := m.stepOptions()
		var b strings.Builder
		b.WriteString(mainTitle.Render("Set your defaults") +
			styDim.Render(fmt.Sprintf("    step %d of %d", m.setupStep+1, len(setupStepKey))) + "\n\n")
		b.WriteString(styHead.Width(w).Render(title) + "\n\n")
		if m.isTextStep() {
			shown := m.setupText
			if shown == "" {
				shown = " "
			}
			b.WriteString(navBar.Render(" ") + styWord.Render(trimTo(shown, w-3)+"▏") + "\n\n")
			b.WriteString(styDim.Render("type · enter to continue · esc to leave"))
			return b.String()
		}
		if len(opts) == 0 {
			b.WriteString(styDim.Render("(choose a framework first)"))
		}
		for i, o := range opts {
			lbl := o.label
			if lbl == "" {
				lbl = "None"
			}
			lbl = trimTo(lbl, w-5)
			ic := devIcon(o.key, o.label)
			if i == m.setupCur {
				b.WriteString(navBar.Render(" ▸ ") + ic + " " + styWord.Render(lbl) + "\n")
			} else {
				b.WriteString("   " + ic + " " + styHead.Render(lbl) + "\n")
			}
		}
		return b.String()
	}

	d := map[string]string{}
	if m.prof != nil {
		d = m.prof.Defaults
	}
	get := func(k string) string {
		if v := d[k]; v != "" {
			return styWord.Render(trimTo(v, w-12))
		}
		return styDim.Render("unset")
	}
	name := styDim.Render("unset")
	if m.prof != nil && m.prof.Git.Name != "" {
		name = styWord.Render(trimTo(m.prof.Git.Name, w-12))
	}
	folder := get("projects_dir")
	if m.prof == nil || m.prof.Defaults["projects_dir"] == "" {
		folder = styDim.Render("current dir")
	}
	return h1("Settings") +
		desc(w, "Your defaults. keel pre-fills every new project from these, and uses Hosting as the default deploy target. Stored in ~/.config/keel/profile.yaml.") +
		styHead.Render("Name       ") + name + "\n" +
		styHead.Render("Projects   ") + folder + "\n" +
		styHead.Render("Framework  ") + get("framework") + "\n" +
		styHead.Render("Env        ") + get("env") + "\n" +
		styHead.Render("Database   ") + get("database") + "\n" +
		styHead.Render("Editor     ") + get("editor") + "\n" +
		styHead.Render("Hosting    ") + get("hosting") + "\n\n" +
		styHead.Render("Press ") + styWord.Render("enter") + styHead.Render(" to edit your defaults.") + "\n\n" +
		note(w, "♥ Support keel  "+sponsorURL)
}

// colWidths fits two columns into avail, keeping their proportions and never
// going below something readable. A pair of fixed widths is only right at the
// width they were chosen for: 22 and 18 need 40 columns, and an 80-column
// terminal leaves the panel 52 to hold them plus a tag.
func colWidths(avail, a, b int) (int, int) {
	if avail >= a+b {
		return a, b
	}
	if avail < 16 {
		avail = 16
	}
	na := avail * a / (a + b)
	if na < 8 {
		na = 8
	}
	nb := avail - na
	if nb < 6 {
		nb = 6
	}
	return na, nb
}
