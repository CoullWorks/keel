package pluginstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/coullworks/keel/plugin"
)

// A live screen runs the plugin's own executable and shows the View it prints as
// JSON — this is what lets an installed plugin (sonar, ai-core) show live data in
// the studio without being compiled into keel. Untrusted plugins are refused.
func TestScreenRendersLiveJSON(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "render.sh")
	body := "#!/bin/sh\n" +
		`echo '{"sections":[{"kind":"stat","title":"Score","items":[{"label":"ChatGPT","value":"76"}]}]}'` + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	a := &adapter{
		mf: manifest{
			Meta:    plugin.Meta{Schema: 1, Name: "t", Version: "1", Description: "d"},
			Screens: []declaredScreen{{ID: "s", Title: "S", Render: []string{"render.sh"}}},
		},
		dir:     dir,
		trusted: true,
	}
	screens := a.Screens()
	if len(screens) != 1 {
		t.Fatalf("want 1 screen, got %d", len(screens))
	}
	v, err := screens[0].Render(context.Background(), plugin.Project{Dir: dir})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(v.Sections) != 1 || v.Sections[0].Title != "Score" {
		t.Fatalf("unexpected view: %+v", v)
	}
	if len(v.Sections[0].Items) != 1 || v.Sections[0].Items[0].Value != "76" {
		t.Fatalf("unexpected items: %+v", v.Sections[0].Items)
	}

	// An untrusted plugin must not have its executable run.
	a.trusted = false
	if _, err := a.Screens()[0].Render(context.Background(), plugin.Project{Dir: dir}); err == nil {
		t.Error("untrusted live render should be refused")
	}
}
