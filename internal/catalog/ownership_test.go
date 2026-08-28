package catalog

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/recipe"
)

var userFlag = regexp.MustCompile(`--user\s+\S+`)

// TestContainerCommandsAgreeOnWhoTheyRunAs.
//
// A compose environment bind-mounts the project from the host, so the uid a
// command runs under is the uid that ends up owning every file it writes into
// the user's own working tree.
//
// Which uid that is depends on the image. The Node and Python images name a
// non-root user in the Dockerfile (USER node, USER dev), so their commands need
// no flag. The PHP images name none and run as root, so theirs pass --user
// explicitly.
//
// Either is fine. What is not fine is disagreeing inside one environment, which
// is exactly what happened: `exec` passed --user and `composer` and `artisan`
// did not, so add-ons wrote root-owned files into vendor/, storage/ and
// bootstrap/cache. The app does not run as root, so it could no longer write
// its own cache or log and answered 500 on every request, and the project could
// not be deleted without sudo.
//
// So the rule is agreement: every container command in an environment runs as
// whoever `exec` runs as.
func TestContainerCommandsAgreeOnWhoTheyRunAs(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	inContainer := func(cmd string) bool {
		return strings.Contains(cmd, "docker compose exec") || strings.Contains(cmd, "docker compose run")
	}
	for _, env := range reg.OfKind(recipe.Env) {
		exec, ok := env.Commands["exec"]
		if !ok || !inContainer(exec) {
			continue // not a compose environment
		}
		want := userFlag.FindString(exec)
		for name, cmd := range env.Commands {
			if name == "exec" || !inContainer(cmd) {
				continue
			}
			// No --allow-root exemption. WP-CLI refuses to run as root without
			// that flag, so its presence means the command IS running as root -
			// and `wp plugin install` writes directories into wp-content, which
			// WordPress then cannot update because it does not run as root
			// either. Running WP-CLI as the invoking user removes both the need
			// for the flag and the problem.
			if got := userFlag.FindString(cmd); got != want {
				t.Errorf("%s: %q runs as %q but exec runs as %q - whichever is right, "+
					"the files they write into the bind mount must have one owner:\n  %s",
					env.ID, name, orRoot(got), orRoot(want), cmd)
			}
		}
	}
}

func orRoot(flag string) string {
	if flag == "" {
		return "the image's own user"
	}
	return flag
}

// TestApplicationServicesDoNotRunAsRoot.
//
// Long-running services that boot the application are the other half of the
// ownership problem. keel's own commands were fixed to run as the invoking
// user, but Laravel's queue worker was still started by compose as root, and
// the first line it logged created storage/logs/laravel.log owned by root.
// Everything else in the stack runs as your uid, so nothing could append to
// that file afterwards - `artisan telescope:install` failed reporting a stream
// it could not open, which says nothing about the actual cause.
//
// cron is exempt and has to be: it needs privileges to drop them, and the
// crontab line names the user the job runs as.
func TestApplicationServicesDoNotRunAsRoot(t *testing.T) {
	// Commands that boot the framework, and so write logs and caches.
	bootsTheApp := []string{"artisan", "bin/console", "bin/magento", " wp "}

	eachComposeDoc(t, func(where string, doc composeDoc, _ map[string]string) {
		for name, svc := range doc.Services {
			cmd := fmt.Sprint(svc.Command)
			if strings.Contains(cmd, "cron") {
				continue
			}
			boots := false
			for _, marker := range bootsTheApp {
				if strings.Contains(cmd, marker) {
					boots = true
				}
			}
			if boots && svc.User == "" {
				t.Errorf("%s: the %q service boots the application as root, so the first "+
					"file it writes into the bind mount is one nothing else can write:\n  %s",
					where, name, cmd)
			}
		}
	})
}

// TestDevImagesRevalidateTheCodeTheyMount.
//
// opcache.validate_timestamps = 0 is right for a production image and wrong for
// a bind-mounted one, and the difference is invisible. With it off, PHP serves
// whatever it compiled when the container started: you edit a controller,
// reload, and get the old page, with nothing in any log to say why. A package
// installed after boot stays invisible too, which is how a freshly installed
// Telescope answered 404 on a route `artisan route:list` could see.
//
// So any framework shipping the production setting must also ship a dev
// override that turns it back on.
func TestDevImagesRevalidateTheCodeTheyMount(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reg.All() {
		off, on := false, false
		for _, f := range r.Files {
			body := strings.ReplaceAll(f.Content, " ", "")
			if strings.Contains(body, "opcache.validate_timestamps=0") {
				off = true
			}
			if strings.Contains(body, "opcache.validate_timestamps=1") {
				on = true
			}
		}
		if off && !on {
			t.Errorf("%s turns opcache timestamp validation off but never back on for the "+
				"bind-mounted dev image, so edits to the mounted code have no effect", r.ID)
		}
	}
}

// TestProjectsAreCreatedAgainstTheirOwnPHP.
//
// `composer create-project` resolves a dependency tree against whatever PHP is
// running it, and writes the result into composer.lock. Run it in a standalone
// composer image and that is the composer image's PHP, not the project's: pick
// an older line in the wizard and the project image is then handed a lock it
// cannot install. The error names whichever dev dependency pulled the newer
// requirement - "pestphp/pest ... symfony/process ... requires php >= 8.4.1" -
// and never mentions that two PHP versions were involved.
//
// So a compose create step must run inside the project's own image.
func TestProjectsAreCreatedAgainstTheirOwnPHP(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reg.All() {
		for env, cmd := range r.Create {
			if !strings.Contains(cmd, "create-project") {
				continue
			}
			// A standalone language image, rather than the stack's own service.
			if strings.Contains(cmd, "docker run") && strings.Contains(cmd, "composer:") {
				t.Errorf("%s: the %q create step resolves dependencies in a standalone composer "+
					"image, whose PHP is not the project's:\n  %s", r.ID, env, cmd)
			}
		}
	}
}

// TestApacheVhostsThatServePHPStateADirectoryIndex.
//
// A vhost that serves a PHP front controller from a DocumentRoot has to say
// which file is the index, because the httpd image ships only
// "DirectoryIndex index.html".
//
// Laravel's did not, and GET / returned 403 Forbidden on a completely healthy
// install. The front-controller rewrite is guarded by
// "RewriteCond %{REQUEST_FILENAME} !-d", so for / - where the request filename
// IS the public/ directory - the rewrite is deliberately skipped; Apache falls
// through to DirectoryIndex, finds no index.html, and Options -Indexes turns
// the directory listing into a refusal. Laravel's own .htaccess does not set it
// either: it assumes a host Apache with mod_php, whose config already lists
// index.php. Serving PHP over FastCGI, nothing does.
//
// Stating it explicitly counts either way: Symfony sets "DirectoryIndex
// disabled" because it rewrites everything to the front controller.
func TestApacheVhostsThatServePHPStateADirectoryIndex(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reg.All() {
		files := append([]recipe.File{}, r.Files...)
		for _, frag := range r.Provision {
			files = append(files, frag.Files...)
		}
		for _, f := range files {
			body := f.Content
			// A real vhost that serves PHP itself, rather than the shared tuning
			// file (which mentions both in its commentary) or a config that
			// proxies everything to an app server.
			if !strings.Contains(body, "<VirtualHost") ||
				!strings.Contains(body, "proxy:fcgi://") ||
				!strings.Contains(body, "DocumentRoot") {
				continue
			}
			// The directive, not the word. Checking for the word passes on the
			// comment that explains the directive - which is exactly how this
			// test first passed with the directive deleted. Apache comments
			// start with #, so an uncommented line is the real thing.
			if !directoryIndex.MatchString(body) {
				t.Errorf("%s: %s serves a PHP front controller from a DocumentRoot but states no "+
					"DirectoryIndex, so GET / is 403 on a working install (the image only knows "+
					"index.html)", r.ID, f.Path)
			}
		}
	}
}

// directoryIndex matches an actual DirectoryIndex directive: start of line,
// optional indentation, no leading #.
var directoryIndex = regexp.MustCompile(`(?m)^[\t ]*DirectoryIndex[\t ]+\S`)
