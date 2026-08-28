package studio

import (
	"regexp"
	"strings"
	"testing"
)

// The hash router is client-side JS, so these are surface tests: they read what
// index.html declares (paired with TestThePageParses, which proves the block
// actually runs under node). They guard the two studio fixes that live in the
// page — the friendly name-slug URL, and the monorepo root-launch Run.

// TestFriendlyProjectSlugRouting asserts the router serialises a focused project
// by a friendly name slug and resolves that slug back to a path, while still
// falling back to the encoded path (so legacy deep links, members, and name
// collisions round-trip). This is the Issue-1 fix.
func TestFriendlyProjectSlugRouting(t *testing.T) {
	// serialise() builds the hash from projectSlug(), not the raw encoded path.
	for _, want := range []string{
		"function projectSlug(",
		`const base="#/p/"+projectSlug();`,
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("index.html should serialise via the name slug; missing %q", want)
		}
	}
	// projectSlug only emits the friendly name for a unique top-level project, and
	// falls back to the encoded path otherwise (member / name collision).
	if !strings.Contains(indexHTML, "PROJECTS.filter(p=>p.name===name).length===1") {
		t.Error("projectSlug must only shorten a UNIQUE top-level project name")
	}
	if !strings.Contains(indexHTML, "return encodeURIComponent(PROJ.path);") {
		t.Error("projectSlug must fall back to the encoded path for non-unique / member cases")
	}

	// applyRoute resolves the #/p/<seg> segment through slugToPath before focusing,
	// so a name slug and an encoded path both land on the right project.
	for _, want := range []string{
		"async function slugToPath(",
		"const path=await slugToPath(parts[1]);",
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("applyRoute should resolve the slug via slugToPath; missing %q", want)
		}
	}
	// slugToPath: unique top-level name → its path; unique member name → its path;
	// otherwise the segment is treated as a path verbatim (round-trips encoded
	// paths + collisions).
	for _, want := range []string{
		"if(tops.length===1)return tops[0].path;",
		"if(!tops.length&&members.length===1)return members[0].path;",
		"return seg;",
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("slugToPath must resolve unique name/member and fall back to path; missing %q", want)
		}
	}

	// The screen route must keep working through the slug base: #/p/<slug>/screen/<id>.
	if !regexp.MustCompile(`base\+"/screen/"\+encodeURIComponent`).MatchString(indexHTML) {
		t.Error("the plugin-screen route must still hang off the slug base (#/p/<slug>/screen/<id>)")
	}
}

// TestMonorepoRootShowsLaunchRun asserts the monorepo ROOT view surfaces the live
// launch command with a Run button that streams the root command through /api/run
// (which admits a root-launch dir without a manifest). This is the Issue-2 fix.
func TestMonorepoRootShowsLaunchRun(t *testing.T) {
	// The root view reads p.launchCommand (the field inspect() sets live).
	if !strings.Contains(indexHTML, "if(p.launchCommand){") {
		t.Error("monorepoBody must show the launch line when p.launchCommand is set")
	}
	if !strings.Contains(indexHTML, "This workspace launches from the root") {
		t.Error("monorepoBody must label the root-launch prominently")
	}
	// The Run button streams keel run dev via /api/run — handleRun admits a
	// root-launch dir (the root itself) without a manifest.
	if !regexp.MustCompile(`stream\("/api/run",\{dir:\$\{dj\},task:"dev"\}\)`).MatchString(indexHTML) {
		t.Error("the root-launch Run must stream /api/run with task:dev (admitted by handleRun without a manifest)")
	}
	// The launch line is prepended to the monorepo body, so it shows above members.
	if !strings.Contains(indexHTML, "return launch+backend+") {
		t.Error("the launch line must render above the shared-backend + members list")
	}
}
