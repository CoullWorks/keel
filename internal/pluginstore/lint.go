package pluginstore

import (
	"fmt"
	"os"
	"path/filepath"
)

// Lint checks a plugin directory against keel's plugin standard and returns the
// problems it finds — an empty slice when the plugin conforms. It runs none of
// the plugin's code: it reads config/register.yaml and inspects the files the
// manifest names, so it is safe to point at an untrusted plugin, and it is what a
// plugin's CI runs to gate a release.
//
// Always enforced (a plugin that fails these is broken, not merely unpolished):
//   - the manifest validates (schema, name, version, description, author, license);
//   - every executable a declaration names (a command's run, a screen's render, a
//     step's apply/optionsRender, an action's run, the overview) is present and
//     resolves inside the plugin directory. A declared-but-missing executable is a
//     plugin that fails the instant it is used.
//
// strict adds the release bar a shareable plugin must clear:
//   - each declared executable is actually executable (chmod +x);
//   - the plugin ships a LICENSE and a README.
func Lint(dir string, strict bool) []string {
	mf, err := readManifest(dir)
	if err != nil {
		return []string{err.Error()}
	}
	var probs []string
	a := &adapter{mf: mf, dir: dir}
	check := func(what string, argv []string) {
		if len(argv) == 0 {
			return
		}
		abs, err := a.resolve(argv[0])
		if err != nil {
			probs = append(probs, fmt.Sprintf("%s names %q: %v", what, argv[0], err))
			return
		}
		if strict {
			if fi, err := os.Stat(abs); err == nil && fi.Mode()&0o111 == 0 {
				probs = append(probs, fmt.Sprintf("%s: %s is not executable (chmod +x it)", what, argv[0]))
			}
		}
	}
	for _, c := range mf.Commands {
		check("command "+c.Name, c.Run)
	}
	for _, s := range mf.Screens {
		check("screen "+s.ID, s.Render)
	}
	for _, s := range mf.Steps {
		check("step "+s.ID, s.Apply)
		check("step "+s.ID+" options", s.OptionsRender)
	}
	for _, ac := range mf.Actions {
		check("action "+ac.ID, ac.Run)
	}
	for _, pg := range mf.Pages {
		check("page "+pg.ID, pg.Render)
	}
	check("overview", mf.Overview)

	if strict {
		if !hasAny(dir, "LICENSE", "LICENSE.md", "LICENSE.txt") {
			probs = append(probs, "no LICENSE file (a shareable plugin needs one)")
		}
		if !hasAny(dir, "README.md", "README") {
			probs = append(probs, "no README (document what it adds and any capabilities it needs)")
		}
	}
	return probs
}

// hasAny reports whether any of the named files exists in dir.
func hasAny(dir string, names ...string) bool {
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			return true
		}
	}
	return false
}
