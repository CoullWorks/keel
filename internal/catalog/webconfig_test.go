package catalog

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

// TestEveryWebServerConfigParses hands each generated web-server config to the
// server that will actually read it and asks whether it is valid.
//
// Nothing else here can answer that. The other guards read the config as text or
// as YAML, which proves the directive is present and says nothing about whether
// nginx or httpd will accept the file - and a web server that will not start is
// a stack with no way in, which is the failure this whole area keeps producing.
// Every one of these files is generated, so one bad edit breaks a config for
// every project built from that framework afterwards.
//
// Docker-gated, because it runs the real servers: set KEEL_WEBCONF=1. It is not
// part of the default `go test ./...`, which stays offline; `make verify` runs
// it where Docker exists.
func TestEveryWebServerConfigParses(t *testing.T) {
	if os.Getenv("KEEL_WEBCONF") != "1" {
		t.Skip("set KEEL_WEBCONF=1 to check the generated configs against real nginx and httpd")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not on PATH")
	}
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}

	checked := 0
	for _, fw := range reg.OfKind(recipe.Framework) {
		for _, env := range reg.ForFramework(fw.ID, recipe.Env) {
			// Only the compose family writes the config files the containers
			// mount. DDEV carries its own web server and its own config layout.
			if env.EnvFamily != "compose" {
				continue
			}
			for _, web := range reg.ForFramework(fw.ID, recipe.Service) {
				if !web.IsWebServer() {
					continue
				}
				ids := []string{fw.ID, env.ID, web.ID}
				if db := defaultDB(reg, fw.ID, env.ID); db != "" {
					ids = append(ids, db)
				}
				plan, err := resolver.Resolve(reg, ids)
				if err != nil {
					continue // not a combination keel offers
				}
				dir := t.TempDir()
				files := engine.RenderedFiles(plan, "proj")
				if err := writeTree(dir, files); err != nil {
					t.Fatal(err)
				}
				name := fw.ID + "/" + env.ID + "/" + web.ID
				switch {
				case hasFile(files, ".keel/nginx/nginx.conf"):
					// --entrypoint, because the nginx image's entrypoint shim
					// rewrites files under /etc/nginx before handing over, and
					// those are mounted read-only here. Its failure is not the
					// config's, and letting it run reported every framework as
					// broken while the configs were fine.
					//
					// The hosts are needed because nginx resolves upstream names
					// when it loads the config, not when it proxies: without the
					// compose network there is no `php` or `app` to find and
					// every config fails on a missing container rather than on
					// anything it says. Apache needs no equivalent - mod_proxy
					// resolves per request.
					args := []string{
						"--entrypoint", "nginx",
						"-v", filepath.Join(dir, ".keel/nginx/nginx.conf") + ":/etc/nginx/nginx.conf:ro",
						"-v", filepath.Join(dir, "docker/nginx/conf.d") + ":/etc/nginx/conf.d:ro",
					}
					for _, h := range upstreamHosts(files) {
						args = append(args, "--add-host", h+":127.0.0.1")
					}
					checkConfig(t, name, "nginx:1.27-alpine", args, []string{"-t"})
					checked++
				case hasFile(files, ".keel/apache/keel-tuning.conf"):
					// The same explicit Include the compose command uses: the
					// official httpd image has no conf.d and no IncludeOptional
					// line, so without this httpd would validate its own stock
					// config and pass while ours was never read.
					checkConfig(t, name, "httpd:2.4-alpine",
						[]string{
							"-v", filepath.Join(dir, ".keel/apache/keel-tuning.conf") + ":/usr/local/apache2/conf/keel-tuning.conf:ro",
							"-v", filepath.Join(dir, "docker/apache/conf.d") + ":/usr/local/apache2/conf.d:ro",
						},
						[]string{"httpd", "-t", "-c", "Include conf/keel-tuning.conf"})
					checked++
				default:
					t.Errorf("%s: the plan includes a web server but writes no config for it, "+
						"so the container would start on the stock image and serve someone "+
						"else's default page", name)
				}
			}
		}
	}
	// A guard that silently checks nothing passes forever. This is the count of
	// framework/env/web-server combinations keel can build, so it moves when the
	// catalogue does and cannot quietly fall to zero.
	if checked == 0 {
		t.Fatal("no web-server config was checked, so this proved nothing")
	}
	t.Logf("parsed %d generated web-server configs with the real servers", checked)
}

func checkConfig(t *testing.T, name, image string, mounts, cmd []string) {
	t.Helper()
	args := append([]string{"run", "--rm"}, mounts...)
	args = append(args, image)
	args = append(args, cmd...)
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Errorf("%s: %s rejected the generated config, so this stack would come up "+
			"with no web server: %v\n%s", name, image, err, strings.TrimSpace(string(out)))
	}
}

func hasFile(files map[string]string, path string) bool {
	_, ok := files[path]
	return ok
}

// upstreamNames matches every place an nginx config names a host that has to
// resolve when the config loads: `server host:port` inside an upstream block,
// `fastcgi_pass host:port`, and `proxy_pass http://host`.
//
// proxy_pass is included because a literal name there is resolved at load time
// too. Leaving it out on the assumption that it always points at an upstream
// block was wrong: the nine Node frameworks proxy straight to `app:3000` with no
// block at all. When it does name a block, the extra host entry is harmless.
var upstreamNames = regexp.MustCompile(
	`(?m)^[\t ]*(?:server|fastcgi_pass)[\t ]+([a-z0-9][a-z0-9_.-]*):\d+|` +
		`proxy_pass[\t ]+https?://([a-z0-9][a-z0-9_.-]*)`)

// upstreamHosts is every container name the generated nginx config expects to
// resolve, deduplicated and in a stable order.
func upstreamHosts(files map[string]string) []string {
	seen := map[string]bool{}
	for path, body := range files {
		if !strings.Contains(path, "nginx") {
			continue
		}
		for _, m := range upstreamNames.FindAllStringSubmatch(body, -1) {
			for _, host := range m[1:] { // one alternative matched, the rest are empty
				// Addresses need no help; names do.
				if host != "" && net.ParseIP(host) == nil {
					seen[host] = true
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// writeTree materialises a rendered plan on disk so a container can mount it.
func writeTree(root string, files map[string]string) error {
	for path, body := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}
