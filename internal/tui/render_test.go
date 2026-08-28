package tui

import (
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

func TestSplash(t *testing.T) {
	s := Splash()
	if !strings.Contains(s, "keel") {
		t.Fatalf("splash missing wordmark:\n%s", s)
	}
	if !strings.Contains(s, "web development studio") {
		t.Fatalf("splash missing tagline:\n%s", s)
	}
	// The anchor mascot uses block-drawing glyphs.
	if !strings.ContainsAny(s, "█▀▄") {
		t.Fatalf("splash missing anchor mascot:\n%s", s)
	}
}

func TestWrapCmd(t *testing.T) {
	// Short command: returned unchanged, no wrapping.
	if got := wrapCmd("composer install", 88); got != "composer install" {
		t.Fatalf("short cmd changed: %q", got)
	}
	// A single word longer than width still returns intact (nothing to break on).
	long := strings.Repeat("x", 40)
	if got := wrapCmd(long, 10); got != long {
		t.Fatalf("unbreakable word changed: %q", got)
	}
	// A long multi-word command wraps onto indented continuation lines.
	cmd := "docker compose run --rm app php bin/magento setup:install --admin-user=admin --admin-password=secret"
	got := wrapCmd(cmd, 30)
	if !strings.Contains(got, "\n    ") {
		t.Fatalf("expected wrapped continuation lines:\n%s", got)
	}
	// Every wrapped physical line stays within (roughly) the width budget.
	for _, ln := range strings.Split(got, "\n") {
		if len(strings.TrimSpace(ln)) > 40 {
			t.Fatalf("wrapped line too long (%d): %q", len(ln), ln)
		}
	}
}

func TestRenderPlan(t *testing.T) {
	p := &resolver.Plan{
		Framework: "laravel",
		Recipes: []recipe.Recipe{
			{ID: "laravel", Kind: recipe.Framework, Label: "Laravel"},
			{ID: "ddev", Kind: recipe.Env, Label: "DDEV"},
		},
	}
	steps := []string{"composer create-project laravel/laravel app", "ddev start"}
	out := RenderPlan(p, steps)
	for _, want := range []string{"Keel plan", "laravel", "Laravel", "DDEV", "steps, in order", "composer create-project", "ddev start"} {
		if !strings.Contains(out, want) {
			t.Fatalf("RenderPlan missing %q:\n%s", want, out)
		}
	}
}

func TestRenderPlanWrapsLongStep(t *testing.T) {
	long := "docker compose run --rm app " + strings.Repeat("verylongflag ", 12)
	out := RenderPlan(&resolver.Plan{Framework: "x"}, []string{long})
	// wrapCmd breaks the command onto indented continuation lines, but the whole
	// panel is then re-rendered inside a lipgloss border box, so the raw "\n    "
	// indent no longer survives verbatim. Assert the wrap by structure instead:
	// the single "$ " prompt line splits into several physical lines, and at
	// least one continuation line is an indented "verylongflag …" with no prompt.
	lines := strings.Split(out, "\n")
	prompts := 0
	continued := false
	for _, ln := range lines {
		if strings.Contains(ln, "$ docker compose") {
			prompts++
		}
		// A continuation line: carries the wrapped flags but not the "$ " prompt.
		if strings.Contains(ln, "verylongflag") && !strings.Contains(ln, "$ ") {
			continued = true
		}
	}
	if prompts != 1 {
		t.Fatalf("expected exactly one prompt line, got %d:\n%s", prompts, out)
	}
	if !continued {
		t.Fatalf("long step should wrap onto continuation lines:\n%s", out)
	}
}

func TestRenderDoctor(t *testing.T) {
	tools := []Tool{
		{Name: "docker", State: ToolOK, Note: "27.0"},
		{Name: "ddev", State: ToolWarn, Note: "installed, daemon down"},
		{Name: "composer", State: ToolMissing},
	}
	out := RenderDoctor(tools)
	for _, want := range []string{"keel doctor", "docker", "ok", "ddev", "check", "composer", "not found", "27.0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("RenderDoctor missing %q:\n%s", want, out)
		}
	}
}

func TestRenderHomeSummary(t *testing.T) {
	got := RenderHomeSummary("laravel", "ddev", "postgres", "code")
	want := "laravel on ddev, postgres · editor code"
	if got != want {
		t.Fatalf("RenderHomeSummary = %q want %q", got, want)
	}
}

func TestRenderDone(t *testing.T) {
	out := RenderDone("shop", "http://localhost:8080")
	for _, want := range []string{"built", "./shop", "cd shop", "keel secrets sync", "keel db migrate",
		// Where to open it: the point of the whole build, and absent until it
		// was added.
		"open:", "http://localhost:8080"} {
		if !strings.Contains(out, want) {
			t.Fatalf("RenderDone missing %q:\n%s", want, out)
		}
	}
}

// An environment that publishes nothing (a native one, say) has no URL, and the
// line is left out rather than printed empty.
func TestRenderDoneWithoutAURL(t *testing.T) {
	out := RenderDone("shop", "")
	if strings.Contains(out, "open:") {
		t.Fatalf("RenderDone offered somewhere to open with no URL to give:\n%s", out)
	}
	if !strings.Contains(out, "cd shop") {
		t.Fatalf("RenderDone lost its next steps:\n%s", out)
	}
}

func TestRenderSteps(t *testing.T) {
	out := RenderSteps("keel gen", []string{"php artisan make:model Order", "php artisan make:event Paid"})
	for _, want := range []string{"keel gen", "make:model Order", "make:event Paid"} {
		if !strings.Contains(out, want) {
			t.Fatalf("RenderSteps missing %q:\n%s", want, out)
		}
	}
}

func TestRenderRecipes(t *testing.T) {
	rows := []RecipeRow{
		{ID: "laravel", Kind: "framework", Label: "Laravel", Source: "built-in", Trusted: true},
		{ID: "ddev", Kind: "env", Label: "DDEV", Source: "built-in", Trusted: true},
		{ID: "custom", Kind: "addon", Label: "Custom", Source: "user pack", Trusted: false},
	}
	out := RenderRecipes(rows)
	// Grouped by kind, because that is the question people arrive with - what
	// frameworks are there, what can go under them - and 235 recipes in one
	// block sorted by source answered none of it.
	for _, want := range []string{
		"keel recipes", "FRAMEWORK", "ENV", "ADDON",
		"laravel", "Laravel", "custom",
		// Where a recipe came from is a security fact, so it survives the
		// regrouping: sources and their trust are listed once at the end.
		"built-in", "trusted", "user pack", "untrusted",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("RenderRecipes missing %q:\n%s", want, out)
		}
	}
	// One line per source, not one per row.
	if n := strings.Count(out, "built-in"); n != 1 {
		t.Fatalf("built-in listed %d times, want once in the source legend:\n%s", n, out)
	}
}

func TestRenderFiles(t *testing.T) {
	out := RenderFiles("wrote", []string{"app/Model.php", "etc/module.xml"})
	for _, want := range []string{"wrote", "app/Model.php", "etc/module.xml"} {
		if !strings.Contains(out, want) {
			t.Fatalf("RenderFiles missing %q:\n%s", want, out)
		}
	}
}
