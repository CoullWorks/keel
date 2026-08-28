package brand

import (
	"path/filepath"
	"testing"
)

func TestDetectNoStack(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "readme.md", "nothing here")
	d, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.Found {
		t.Fatalf("Detect should report Found=false for a project with no CSS stack")
	}
}

func TestDetectTailwindV4(t *testing.T) {
	dir := t.TempDir()
	css := `@import "tailwindcss";
@theme {
  --color-brand-500: #5B21B6;
  --color-brand-700: #4c1d95;
  --color-accent-500: #3ab7bf;
  --color-background: #ffffff;
  --color-card-foreground: #0a0a0a;
  --radius: 0.75rem;
  --font-sans: Inter, system-ui;
  --font-mono: JetBrains Mono, monospace;
}
`
	writeFile(t, dir, filepath.Join("src", "app.css"), css)

	d, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.Stack != "tailwind4" || !d.Found {
		t.Fatalf("stack = %q found = %v, want tailwind4/true", d.Stack, d.Found)
	}
	// Hex is normalised to lower-case.
	if d.Roles.Brand[500] != "#5b21b6" {
		t.Fatalf("brand 500 = %q, want #5b21b6 (normalised)", d.Roles.Brand[500])
	}
	if d.Roles.Brand[700] != "#4c1d95" {
		t.Fatalf("brand 700 = %q", d.Roles.Brand[700])
	}
	if d.Roles.Accent[500] != "#3ab7bf" {
		t.Fatalf("accent 500 = %q", d.Roles.Accent[500])
	}
	if d.Surface.Background != "#ffffff" {
		t.Fatalf("surface bg = %q", d.Surface.Background)
	}
	if d.Surface.CardFg != "#0a0a0a" {
		t.Fatalf("card-foreground = %q", d.Surface.CardFg)
	}
	if d.Radius.Base != "0.75rem" {
		t.Fatalf("radius = %q", d.Radius.Base)
	}
	if d.Font.Sans != "Inter, system-ui" {
		t.Fatalf("font sans = %q", d.Font.Sans)
	}
}

func TestDetectTailwindV3(t *testing.T) {
	dir := t.TempDir()
	cfg := `module.exports = {
  theme: {
    extend: {
      colors: {
        brand: { 500: '#5b21b6', 600: "#4c1d95" },
        accent: '#3ab7bf',
        spacing: { 4: '1rem' },
      },
    },
  },
};`
	writeFile(t, dir, "tailwind.config.js", cfg)

	d, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.Stack != "tailwind3" {
		t.Fatalf("stack = %q, want tailwind3", d.Stack)
	}
	if d.Roles.Brand[500] != "#5b21b6" || d.Roles.Brand[600] != "#4c1d95" {
		t.Fatalf("brand stops = %v", d.Roles.Brand)
	}
	// A single-value colour maps to the 500 stop.
	if d.Roles.Accent[500] != "#3ab7bf" {
		t.Fatalf("accent 500 = %q", d.Roles.Accent[500])
	}
	// A non-colour key (spacing) is not misread as a role.
	if len(d.Roles.Neutral) != 0 {
		t.Fatalf("spacing should not populate a colour role")
	}
}

func TestDetectBootstrap(t *testing.T) {
	dir := t.TempDir()
	scss := `$primary: #5B21B6;
$secondary: #3ab7bf;
$success: #22c55e;
@import "bootstrap/scss/bootstrap";`
	writeFile(t, dir, filepath.Join("scss", "app.scss"), scss)

	d, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.Stack != "bootstrap" {
		t.Fatalf("stack = %q, want bootstrap", d.Stack)
	}
	if d.Roles.Brand[500] != "#5b21b6" {
		t.Fatalf("$primary -> brand 500 = %q", d.Roles.Brand[500])
	}
	if d.Roles.Accent[500] != "#3ab7bf" {
		t.Fatalf("$secondary -> accent 500 = %q", d.Roles.Accent[500])
	}
	if d.Roles.Success[500] != "#22c55e" {
		t.Fatalf("$success -> success 500 = %q", d.Roles.Success[500])
	}
}

func TestMergeKeepsDetectedWins(t *testing.T) {
	gen, _ := Generate("#5b21b6", "#3ab7bf")
	// A detected set with one overriding stop and one novel radius.
	det := Detected{
		Roles:  Roles{Brand: Scale{700: "#010203"}},
		Radius: Radius{Base: "1rem"},
		Font:   Font{Sans: "Inter"},
	}
	merged := gen.Merge(det)
	// The detected stop wins.
	if merged.Roles.Brand[700] != "#010203" {
		t.Fatalf("detected 700 should win, got %q", merged.Roles.Brand[700])
	}
	// The un-detected stops fall back to the generated ramp.
	if merged.Roles.Brand[500] != "#5b21b6" {
		t.Fatalf("generated 500 should stand, got %q", merged.Roles.Brand[500])
	}
	// Detected radius/font win; other roles untouched.
	if merged.Radius.Base != "1rem" || merged.Font.Sans != "Inter" {
		t.Fatalf("detected radius/font should win: %+v %+v", merged.Radius, merged.Font)
	}
	if len(merged.Roles.Accent) != len(scaleSteps) {
		t.Fatalf("accent ramp should be untouched by the merge")
	}
}

func TestMergeEmptyDetectedIsNoOp(t *testing.T) {
	gen, _ := Generate("#5b21b6", "")
	merged := gen.Merge(Detected{})
	if merged.Roles.Brand[500] != gen.Roles.Brand[500] {
		t.Fatalf("merging an empty Detected must not change the generated set")
	}
}
