package pluginstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/coullworks/keel/plugin"
)

// capturingIO records what a plugin wrote, so a test can assert on output
// without a terminal.
type capturingIO struct{ lines []string }

func (c *capturingIO) Title(s string)            { c.lines = append(c.lines, s) }
func (c *capturingIO) Detail(l, v string)        { c.lines = append(c.lines, l+": "+v) }
func (c *capturingIO) Note(s string)             { c.lines = append(c.lines, s) }
func (c *capturingIO) List(t string, i []string) { c.lines = append(c.lines, t) }
func (c *capturingIO) OK(s string)               { c.lines = append(c.lines, s) }
func (c *capturingIO) Warn(s string)             { c.lines = append(c.lines, s) }
func (c *capturingIO) Bad(s string)              { c.lines = append(c.lines, s) }

// writeDeclared installs a plugin declaring a command, a screen and a step.
func writeDeclared(t *testing.T, name string, runArgv string) {
	t.Helper()
	src := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(src, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho ran-with \"$@\"\n"
	if err := os.WriteFile(filepath.Join(src, "bin", "hello"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `schema: 1
name: ` + name + `
version: 1.0.0
description: declares everything
author: t
license: MIT
commands:
  - name: greet
    summary: say hello
    run: [` + runArgv + `]
screens:
  - id: overview
    title: Overview
    sections:
      - kind: list
        title: Checks
        items:
          - label: one
            value: ok
steps:
  - id: pick
    title: Pick
    help: choose
    multi: true
    order: 60
    options:
      - key: a
        label: Option A
        selected: true
`
	if err := os.WriteFile(filepath.Join(src, "config", "register.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), src, ""); err != nil {
		t.Fatal(err)
	}
}

// TestDeclaredPluginContributes: a plugin installed at runtime has to actually
// add its command, screen and wizard step. Installing something that then
// contributes nothing is the failure mode this whole design exists to avoid.

// TestDeclaredWebUIPageAndCall: a plugin can render its own HTML page (a
// "webview") and answer a bridged call — the two halves of a self-contained
// interactive plugin surface. keel hosts the HTML and proxies the call to the
// plugin's own executable; the work happens in the plugin.
func TestDeclaredWebUIPageAndCall(t *testing.T) {
	withConfigDir(t)
	src := filepath.Join(t.TempDir(), "webby")
	if err := os.MkdirAll(filepath.Join(src, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(src, "bin", "ui"), "#!/bin/sh\nprintf '<h1>hi</h1>'\n", 0o755)
	mustWrite(t, filepath.Join(src, "bin", "call"), "#!/bin/sh\nprintf '{\"echo\":\"%s\"}' \"$1\"\n", 0o755)
	manifest := "schema: 1\nname: webby\nversion: 1.0.0\ndescription: a plugin with an own-UI page and a call\n" +
		"author: t\nlicense: MIT\ncapabilities: [exec]\n" +
		"pages:\n  - id: dash\n    title: Dashboard\n    ui: true\n    render: [\"bin/ui\"]\n" +
		"actions:\n  - id: ping\n    label: Ping\n    needs: exec\n    run: [\"bin/call\"]\n"
	if err := os.WriteFile(filepath.Join(src, "config", "register.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), src, ""); err != nil {
		t.Fatal(err)
	}
	if err := SetTrusted("webby", true); err != nil {
		t.Fatal(err)
	}
	if err := SetCapabilityGranted("webby", plugin.CapExec, true); err != nil {
		t.Fatal(err)
	}

	// The page is an own-HTML surface: HTML true, RenderHTML returns the plugin's HTML.
	loaded, problems := Load()
	if len(problems) > 0 {
		t.Fatalf("problems loading: %v", problems)
	}
	var page plugin.Page
	for _, p := range loaded {
		if p.Meta().Name != "webby" {
			continue
		}
		for _, pg := range p.(plugin.Pager).Pages() {
			page = pg
		}
	}
	if !page.HTML || page.RenderHTML == nil {
		t.Fatalf("expected an own-HTML page, got %+v", page)
	}
	html, err := page.RenderHTML(context.Background())
	if err != nil || html != "<h1>hi</h1>" {
		t.Fatalf("RenderHTML = %q, %v; want the plugin's HTML", html, err)
	}

	// The bridge: Call runs the action in the plugin and returns its JSON.
	out, err := Call(context.Background(), "webby", "ping", map[string]string{"msg": "yo"}, "")
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !strings.Contains(out, "echo") {
		t.Errorf("Call result should be the plugin's JSON, got %q", out)
	}

	// Untrusted, the same call is refused — keel never runs a plugin's code without trust.
	if err := SetTrusted("webby", false); err != nil {
		t.Fatal(err)
	}
	if _, err := Call(context.Background(), "webby", "ping", nil, ""); err == nil {
		t.Error("Call on an untrusted plugin should be refused")
	}
}

// TestDeclaredPluginPage: a discovered plugin can contribute a top-level studio
// page (a nav destination under "Extend"), not only a per-project screen. The
// page renders by running the plugin's own executable — trust-gated like a
// screen — and has no project because a page is not scoped to one.
func TestDeclaredPluginPage(t *testing.T) {
	withConfigDir(t)
	src := filepath.Join(t.TempDir(), "haspage")
	if err := os.MkdirAll(filepath.Join(src, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	pageScript := "#!/bin/sh\ncat <<JSON\n{\"sections\":[{\"kind\":\"text\",\"title\":\"Hello\",\"items\":[{\"label\":\"a\",\"value\":\"b\"}]}]}\nJSON\n"
	if err := os.WriteFile(filepath.Join(src, "bin", "page"), []byte(pageScript), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema: 1\nname: haspage\nversion: 1.0.0\ndescription: a plugin with a global page\n" +
		"author: t\nlicense: MIT\ncapabilities: [exec]\npages:\n  - id: dash\n    title: Dashboard\n    render: [\"bin/page\"]\n"
	if err := os.WriteFile(filepath.Join(src, "config", "register.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), src, ""); err != nil {
		t.Fatal(err)
	}
	if err := SetTrusted("haspage", true); err != nil { // so the page's executable may run
		t.Fatal(err)
	}

	loaded, problems := Load()
	if len(problems) > 0 {
		t.Fatalf("problems loading: %v", problems)
	}
	var pager plugin.Pager
	for _, p := range loaded {
		if p.Meta().Name == "haspage" {
			pg, ok := p.(plugin.Pager)
			if !ok {
				t.Fatal("a plugin declaring a page should satisfy Pager")
			}
			pager = pg
		}
	}
	if pager == nil {
		t.Fatal("haspage did not load")
	}
	pages := pager.Pages()
	if len(pages) != 1 || pages[0].ID != "dash" || pages[0].Title != "Dashboard" {
		t.Fatalf("expected one page dash/Dashboard, got %+v", pages)
	}
	v, err := pages[0].Render(context.Background())
	if err != nil {
		t.Fatalf("render page: %v", err)
	}
	if len(v.Sections) == 0 {
		t.Fatal("the page rendered nothing to draw")
	}
}

func TestDeclaredPluginContributes(t *testing.T) {
	withConfigDir(t)
	writeDeclared(t, "declares", `"bin/hello"`)

	loaded, problems := Load()
	if len(problems) > 0 {
		t.Fatalf("problems loading: %v", problems)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(loaded))
	}
	p := loaded[0]

	c, ok := p.(plugin.Commander)
	if !ok {
		t.Fatal("a manifest declaring commands did not produce a Commander")
	}
	if cmds := c.Commands(); len(cmds) != 1 || cmds[0].Name != "greet" {
		t.Errorf("wrong commands: %+v", cmds)
	}
	s, ok := p.(plugin.Screener)
	if !ok {
		t.Fatal("a manifest declaring screens did not produce a Screener")
	}
	v, err := s.Screens()[0].Render(context.Background(), plugin.Project{})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Sections) != 1 || len(v.Sections[0].Items) != 1 || v.Sections[0].Items[0].Label != "one" {
		t.Errorf("screen did not carry its declared sections: %+v", v)
	}
	st, ok := p.(plugin.Stepper)
	if !ok {
		t.Fatal("a manifest declaring steps did not produce a Stepper")
	}
	opts, err := st.Steps()[0].Options(context.Background(), plugin.Project{})
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 1 || opts[0].Value != "a" || !opts[0].Default {
		t.Errorf("step options did not survive: %+v", opts)
	}
}

// TestDeclaringNothingAddsNothing: a plugin with no declarations must not
// advertise contributions. Command/screen/step presence is reported by content
// (an empty list shows nothing in `keel plugins`); action/overview presence is
// reported by type, so a plain plugin must satisfy neither Actioner nor
// Overviewer.
func TestDeclaringNothingAddsNothing(t *testing.T) {
	withConfigDir(t)
	src := writePlugin(t, t.TempDir(), "plain", "1.0.0")
	if _, err := Install(context.Background(), src, ""); err != nil {
		t.Fatal(err)
	}
	loaded, _ := Load()
	if len(loaded) != 1 {
		t.Fatalf("expected 1, got %d", len(loaded))
	}
	if c, ok := loaded[0].(plugin.Commander); ok && len(c.Commands()) > 0 {
		t.Error("a plugin declaring no commands advertises commands")
	}
	if s, ok := loaded[0].(plugin.Screener); ok && len(s.Screens()) > 0 {
		t.Error("a plugin declaring no screens advertises screens")
	}
	if _, ok := loaded[0].(plugin.Actioner); ok {
		t.Error("a plugin declaring no actions satisfies Actioner")
	}
	if _, ok := loaded[0].(plugin.Overviewer); ok {
		t.Error("a plugin declaring no overview satisfies Overviewer")
	}
}

// TestUntrustedPluginWillNotRun is the security property: a plugin's own
// executable must not run until the user has trusted it, even though installing
// and enabling it were both allowed.
func TestUntrustedPluginWillNotRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture")
	}
	withConfigDir(t)
	writeDeclared(t, "risky", `"bin/hello"`)

	loaded, _ := Load()
	cmd := loaded[0].(plugin.Commander).Commands()[0]
	io := &capturingIO{}
	err := cmd.Run(context.Background(), io, plugin.Project{Dir: t.TempDir()}, nil)
	if !errors.Is(err, ErrUntrusted) {
		t.Fatalf("an untrusted plugin was allowed to run: %v", err)
	}

	// After trusting, the same command runs and its output comes back through IO.
	if err := SetTrusted("risky", true); err != nil {
		t.Fatal(err)
	}
	loaded, _ = Load()
	cmd = loaded[0].(plugin.Commander).Commands()[0]
	io = &capturingIO{}
	if err := cmd.Run(context.Background(), io, plugin.Project{Dir: t.TempDir()}, []string{"x"}); err != nil {
		t.Fatalf("a trusted plugin failed to run: %v", err)
	}
	if len(io.lines) == 0 || io.lines[0] != "ran-with x" {
		t.Errorf("plugin output did not reach IO: %v", io.lines)
	}
}

// TestPluginCannotRunOutsideItsOwnDirectory: the argv comes from a manifest,
// which is a file keel copied from somewhere else. It must not be able to name
// /bin/sh or climb out with "..".
func TestPluginCannotRunOutsideItsOwnDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix paths")
	}
	withConfigDir(t)
	for _, argv := range []string{`"/bin/sh", "-c", "echo pwned"`, `"../../../bin/sh"`} {
		if err := os.RemoveAll(Dir()); err != nil {
			t.Fatal(err)
		}
		writeDeclared(t, "escape", argv)
		if err := SetTrusted("escape", true); err != nil {
			t.Fatal(err)
		}
		loaded, _ := Load()
		cmd := loaded[0].(plugin.Commander).Commands()[0]
		err := cmd.Run(context.Background(), &capturingIO{}, plugin.Project{Dir: t.TempDir()}, nil)
		if err == nil {
			t.Errorf("argv %s was allowed to run outside the plugin directory", argv)
		}
	}
}

// TestResolveRefusesSymlinkEscape: a symlink whose name stays inside the plugin
// dir but whose target is outside passes the textual containment check, so
// resolve must follow the link and refuse it — otherwise a plugin could run a
// file it does not own by pointing a symlink out of its directory. A real file
// inside the dir still resolves, so the guard does not break legitimate layouts.
func TestResolveRefusesSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix symlinks")
	}
	dir := t.TempDir()     // the plugin directory
	outside := t.TempDir() // somewhere the plugin must not reach
	target := filepath.Join(outside, "tool")
	if err := os.WriteFile(target, []byte("#!/bin/sh\necho pwned\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "tool")); err != nil { // name inside, target outside
		t.Fatal(err)
	}
	a := &adapter{dir: dir}
	if _, err := a.resolve("tool"); err == nil {
		t.Fatal("resolve allowed a symlink that escapes the plugin directory")
	}
	real := filepath.Join(dir, "real")
	if err := os.WriteFile(real, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := a.resolve("real"); err != nil {
		t.Fatalf("resolve rejected a legitimate in-dir file: %v", err)
	}
}
