package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/catalog"
)

// renderDump prints the wizard View at a given state so the layout can be QA'd
// without a live terminal (run: go test ./internal/tui -run Dump -v).
func dump(t *testing.T, name string, m wizardModel) {
	m.enter(m.step)
	view := m.View()
	lines := strings.Split(view, "\n")
	var b strings.Builder
	b.WriteString("\n===== " + name + " (" + itoa(len(lines)) + " lines) =====\n")
	for i, ln := range lines {
		b.WriteString(pad(itoa(i)) + "| " + ln + "\n")
	}
	t.Log(b.String())
}

func TestDumpFramework(t *testing.T) {
	steps := []Step{
		{Title: "Framework", Help: "What are you building?", Options: []Choice{
			{Key: "laravel", Label: "Laravel", Selected: true},
			{Key: "magento", Label: "Magento / Adobe Commerce"},
			{Key: "woocommerce", Label: "WooCommerce"},
		}},
		{Title: "Local dev environment", Help: "x"},
	}
	m := wizardModel{title: "New project", intro: "a web development studio for any stack", steps: steps, sel: make([][]string, len(steps))}
	dump(t, "single-select, cursor on Laravel", m)
}

func TestDumpMultiLongLabels(t *testing.T) {
	steps := []Step{{Title: "Components", Help: "Pick what to add.", Multi: true, Options: []Choice{
		{Key: "module", Label: "Module (registration + module.xml)"},
		{Key: "model", Label: "Model (+ resource model, collection, db_schema)", Selected: true},
		{Key: "block", Label: "Block"},
	}}}
	m := wizardModel{title: "keel gen", intro: "generate code components", steps: steps, sel: make([][]string, 1)}
	dump(t, "multi-select, LONG labels (wrapping check)", m)
}

// TestLanguageGrouping checks the real catalog groups frameworks by language
// (PHP first, Django under Python) and renders the Language step cleanly.
func TestLanguageGrouping(t *testing.T) {
	reg, err := catalog.Registry()
	if err != nil {
		t.Fatal(err)
	}
	langs := reg.Languages()
	if len(langs) < 2 || langs[0] != "php" {
		t.Fatalf("Languages() = %v, want php first", langs)
	}
	var haveDjango bool
	for _, r := range reg.FrameworksForLang("python") {
		if r.ID == "django" {
			haveDjango = true
		}
	}
	if !haveDjango {
		t.Fatalf("python frameworks = %v, want django present", reg.FrameworksForLang("python"))
	}
	label := map[string]string{"php": "PHP", "python": "Python"}
	var opts []Choice
	for i, l := range langs {
		lbl := label[l]
		if lbl == "" {
			lbl = l
		}
		opts = append(opts, Choice{Key: l, Label: lbl, Selected: i == 0})
	}
	m := wizardModel{title: "New project", intro: "compose a stack — keel builds it, wired and ready to run",
		steps: []Step{{Title: "Language", Help: "Which stack family?", Options: opts}}, sel: make([][]string, 1)}
	dump(t, "Language step (grouping)", m)
}

// TestWizardScrollWindow checks a long option list is windowed (not overflowed),
// the cursor stays in view, the scroll hint shows, and the Continue row lands
// where the mouse hit-test expects it (optionsTop + visN + 1).
func TestWizardScrollWindow(t *testing.T) {
	var opts []Choice
	for i := 0; i < 14; i++ {
		opts = append(opts, Choice{Key: fmt.Sprintf("k%d", i), Label: fmt.Sprintf("Option %d", i)})
	}
	m := wizardModel{title: "t", intro: "i",
		steps: []Step{{Title: "Add-ons", Help: "pick", Multi: true, Options: opts}},
		sel:   make([][]string, 1), height: 20, width: 80}
	m.enter(0)
	m.cursor = 10 // deep in the list

	if got := m.maxVisible(); got != 8 { // 20 - optionsTop(8) - 4
		t.Fatalf("maxVisible=%d want 8", got)
	}
	if got := m.offset(); got != 6 { // centred: 10 - 8/2
		t.Fatalf("offset=%d want 6", got)
	}
	view := m.View()
	if !strings.Contains(view, "(7-14 of 14") {
		t.Fatalf("missing scroll hint:\n%s", view)
	}
	if strings.Contains(view, "Option 5") { // just above the window
		t.Fatalf("rendered an option outside the window:\n%s", view)
	}
	if !strings.Contains(view, "Option 6") || !strings.Contains(view, "Option 13") {
		t.Fatalf("window options missing:\n%s", view)
	}
	lines := strings.Split(view, "\n")
	contRow := optionsTop + m.visN() + 1 // where the mouse handler advances
	if contRow >= len(lines) || !strings.Contains(lines[contRow], "Finish") {
		t.Fatalf("Finish row not at %d (mouse mapping would break):\n%s", contRow, view)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

func pad(s string) string {
	for len(s) < 2 {
		s = " " + s
	}
	return s
}
