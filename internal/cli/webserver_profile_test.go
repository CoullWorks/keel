package cli

import (
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/profile"
)

// A saved default stack must never cost you a way into your own build.
//
// The web server used to be stored in the profile's services list like any
// other service. A profile written before that question existed therefore said
// exactly what a profile whose owner had declined a web server says - nothing -
// and `keel new` read the silence as a decision. On this machine that produced
// an AdonisJS stack where every container came up and nothing was listening:
// curl returned no HTTP status at all. Unreachable stacks are the failure that
// making a web server the default was meant to end, so a stale profile must not
// be able to reintroduce them.
//
// For a self-serving Node framework the reachability default is a PROCESS
// MANAGER (PM2), not a PHP front controller: AdonisJS serves its own traffic, so
// fronting it with Apache/NGINX+a language handler is the wrong ingress. The
// fallback must therefore seed PM2, and must never seed a PHP front controller
// even if a stale profile names one.
//
// Each case runs the real `keel new --dry-run`, which is the path that got it
// wrong, and looks for the ingress in the printed plan.
func TestAProfileWithoutAWebServerStillBuildsSomethingYouCanOpen(t *testing.T) {
	// The profile only drives the build for its own framework, so every case
	// uses the one whose stack this was found on.
	const fw = "adonisjs"
	base := func(extra map[string]string) map[string]string {
		d := map[string]string{
			"framework": fw, "env": fw + "-docker",
			"database": "postgres", "services": "redis",
		}
		for k, v := range extra {
			d[k] = v
		}
		return d
	}

	for _, tc := range []struct {
		name     string
		defaults map[string]string
		want     string // label that must appear in the plan
		absent   string // label that must not
	}{{
		// The bug, exactly: a profile written before the question existed. A
		// self-serving Node app's reachability default is PM2.
		name:     "a profile predating the question takes the default",
		defaults: base(nil),
		want:     "PM2",
	}, {
		name:     "declining is honoured",
		defaults: base(map[string]string{"webserver": profile.NoWebServer}),
		absent:   "PM2",
	}, {
		// A PHP front controller a stale profile still names cannot be used for a
		// self-serving Node framework, so it is dropped and the reachability
		// default (PM2) takes its place. Apache must never reach the plan here.
		name:     "a PHP front controller in the profile is dropped for a self-serving framework",
		defaults: base(map[string]string{"webserver": "apache"}),
		want:     "PM2", absent: "Apache",
	}, {
		// A saved id that this framework has no recipe for is dropped on the way
		// into the plan. The fallback keys off what the plan actually holds, not
		// off whether the profile said something, so the build stays reachable
		// instead of inheriting a web server that does not exist.
		name:     "a saved web server that cannot be used falls back to the default",
		defaults: base(map[string]string{"webserver": "no-such-web-server"}),
		want:     "PM2",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t)
			p := &profile.Profile{Defaults: tc.defaults}
			if err := p.Save(); err != nil {
				t.Fatal(err)
			}

			out, err := runRoot(t, "new", fw, "probe", "--dry-run", "--yes")
			if err != nil {
				t.Fatalf("dry run failed: %v\n%s", err, out)
			}
			if tc.want != "" && !strings.Contains(out, tc.want) {
				t.Errorf("no %s in the plan, so this stack has no way in:\n%s", tc.want, out)
			}
			if tc.absent != "" && strings.Contains(out, tc.absent) {
				t.Errorf("%s is in the plan, and this profile did not ask for it:\n%s", tc.absent, out)
			}
		})
	}
}

// The front-controller path is unchanged for a framework that actually needs
// one: a PHP app has no listener of its own, so a stale profile must still fall
// back to NGINX (or honour an explicit Apache), never to a Node process manager.
// This is the non-self-serving twin of the test above, proving the split did not
// change behaviour for the frameworks it should not touch.
func TestFrontControllerFrameworkStillSeedsAWebServer(t *testing.T) {
	const fw = "laravel"
	base := func(extra map[string]string) map[string]string {
		d := map[string]string{"framework": fw, "env": "sail", "database": "mysql"}
		for k, v := range extra {
			d[k] = v
		}
		return d
	}
	for _, tc := range []struct {
		name     string
		defaults map[string]string
		want     string
		absent   string
	}{{
		name:     "a profile predating the question takes the NGINX default",
		defaults: base(nil),
		want:     "NGINX", absent: "PM2",
	}, {
		name:     "an explicit Apache choice wins over the default",
		defaults: base(map[string]string{"webserver": "apache"}),
		want:     "Apache", absent: "NGINX",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t)
			p := &profile.Profile{Defaults: tc.defaults}
			if err := p.Save(); err != nil {
				t.Fatal(err)
			}
			out, err := runRoot(t, "new", fw, "probe", "--dry-run", "--yes")
			if err != nil {
				t.Fatalf("dry run failed: %v\n%s", err, out)
			}
			if tc.want != "" && !strings.Contains(out, tc.want) {
				t.Errorf("no %s in the plan, so this PHP stack has no front controller:\n%s", tc.want, out)
			}
			if tc.absent != "" && strings.Contains(out, tc.absent) {
				t.Errorf("%s is in the plan, and a PHP framework did not ask for it:\n%s", tc.absent, out)
			}
		})
	}
}
