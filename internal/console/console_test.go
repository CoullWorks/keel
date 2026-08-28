package console

import (
	"strings"
	"testing"
)

func TestConsoleRenders(t *testing.T) {
	m := New()
	m.w, m.h = 96, 30
	view := m.View()
	if !strings.ContainsAny(view, "█▀▄") {
		t.Fatal("console hero is missing the anchor mascot")
	}
	for _, want := range []string{"keel", "Projects", "Build a stack", "Data", "Packs", "Settings", "Support keel"} {
		if !strings.Contains(view, want) {
			t.Fatalf("console view missing %q", want)
		}
	}
}

// TestConsoleColdStart proves first-run "set your defaults" renders INSIDE the
// frame (hero + sidebar + footer), not as a separate screen.
func TestConsoleColdStart(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir()) // no profile => cold start
	m := New()
	m.w, m.h = 96, 30
	if !m.setup {
		t.Fatal("cold start should activate the set-defaults stepper")
	}
	view := m.View()
	// Onboarding opens on the first step (name), inside the frame.
	for _, want := range []string{"Set your defaults", "Your name", "♥ Support keel"} {
		if !strings.Contains(view, want) {
			t.Fatalf("cold-start view missing %q (stepper must render inside the frame)", want)
		}
	}
	// Stepping to the framework step still renders its options in-frame.
	m.setupStep = 2 // name, projects_dir, framework
	if fv := m.View(); !strings.Contains(fv, "Default framework") {
		t.Fatal("framework step should render its options in-frame")
	}
	if !strings.ContainsAny(view, "█▀▄") {
		t.Fatal("cold-start view missing the anchor mascot")
	}
	t.Logf("\n%s", view)
}
