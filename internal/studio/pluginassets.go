package studio

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/coullworks/keel/internal/pluginstore"
)

// handlePluginAsset serves a trusted plugin's built UI bundle (and any assets it
// references) at /plugin-assets/<name>/<path>. The React studio dynamic-imports
// these to MOUNT a plugin's own React component at a page/screen — the no-iframe,
// no-injection replacement for the old own-UI HTML.
//
// It is loopback-only (like /app and /assets), NOT token-guarded, because the
// browser fetches a module/script without a custom header — and a static bundle
// is not a privileged action. The privileged channel stays /api/plugin/call,
// which is token- + trust- + capability-gated. Two hard limits here: the plugin
// must be TRUSTED (running its code is exactly the consent trust grants), and the
// resolved path must stay INSIDE the plugin's own directory (no traversal).
func handlePluginAsset(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/plugin-assets/")
	name, sub, ok := strings.Cut(rest, "/")
	if !ok || name == "" || sub == "" {
		http.NotFound(w, r)
		return
	}
	dir, trusted := pluginDir(name)
	if dir == "" || !trusted {
		http.NotFound(w, r)
		return
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	abs, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(sub)))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Refuse anything that resolves outside the plugin directory.
	if abs != root && !strings.HasPrefix(abs, root+string(os.PathSeparator)) {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	// A plugin's bundle changes when the plugin updates; never cache it, so an
	// upgrade is picked up without a stale module lingering.
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, abs)
}

// pluginDir resolves an installed plugin's directory and whether it is trusted.
func pluginDir(name string) (dir string, trusted bool) {
	list, err := pluginstore.List()
	if err != nil {
		return "", false
	}
	for _, p := range list {
		if p.Name == name {
			return p.Dir, p.Trusted
		}
	}
	return "", false
}
