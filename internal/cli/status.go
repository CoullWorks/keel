package cli

// status.go is the terminal counterpart to the studio's overview dashboard: for
// the project in the current directory it prints the framework + env, each env
// service up/down, the database's reachability, and a few cheap per-framework
// stats (migrations/routes). It is deliberately the CHEAP view - file counts and
// a `compose ps`/`ddev describe` read, never a real DB connection or a build -
// so it is fast and safe to run anywhere, and every unknown degrades to a dash
// rather than a guess.
//
// The service + DB-reachability signals reuse the same runtime probing keel
// service uses (services_runtime.go), so status and service can never disagree.
// Database reachability here is "is the DB service container up?" - the honest
// cheap signal from the runtime, not a driver-level ping (that heavier probe
// lives in the studio's Data tab; the CLI keeps this bounded).

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/tui"
	"github.com/spf13/cobra"
)

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the project overview: framework, env, services, database and cheap stats",
		Long: "The terminal version of the studio overview for the project in the current\n" +
			"directory: its framework and environment, each service up or down, whether the\n" +
			"database service is reachable, and a few cheap per-framework stats (migrations,\n" +
			"routes). Everything is a fast file or metadata read - no build, no DB connection\n" +
			"- so an unknown value shows as a dash rather than blocking or guessing.\n",
		Example: "  keel status\n",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd.Context(), cmd.OutOrStdout(), ".")
		},
	}
}

// runStatus prints the overview for dir. It refuses outside a keel project (the
// manifest is the "is this a project" gate), then reports each section, each
// section degrading to a calm line rather than failing the whole command.
func runStatus(ctx context.Context, out io.Writer, dir string) error {
	m, err := engine.ReadManifest(dir)
	if err != nil {
		return manifestErr(err)
	}
	fmt.Fprintf(out, "project:   %s\n", projectName(dir))
	fmt.Fprintf(out, "framework: %s\n", firstNonEmptyStr(m.Framework, "unknown"))
	fmt.Fprintf(out, "env:       %s\n", firstNonEmptyStr(m.Env, "unknown"))

	// Services + DB reachability share one runtime read so they agree.
	env, envErr := projectEnvRecipe(dir)
	fmt.Fprintln(out, "\nservices:")
	if envErr != nil {
		fmt.Fprintf(out, "  %s\n", envErr.Error())
	} else {
		lctx, cancel := context.WithTimeout(ctx, runtimeTimeout)
		res := listServices(lctx, dir, env)
		cancel()
		printServiceRows(out, res)
		fmt.Fprintf(out, "\ndatabase:  %s\n", dbReachability(res, m))
	}

	// Cheap per-framework stats. A framework with no cheap signal prints nothing
	// (the sections above are the live view); a count that cannot be taken is a
	// dash, never a fabricated number.
	if facts := frameworkStats(dir, m.Framework); len(facts) > 0 {
		fmt.Fprintln(out, "\nstats:")
		for _, f := range facts {
			fmt.Fprintf(out, "  %-12s %s\n", f.label, f.value)
		}
	}
	return nil
}

// printServiceRows prints each service up/down (or the listing's calm message
// when there is nothing to draw).
func printServiceRows(out io.Writer, res svcListing) {
	if len(res.Services) == 0 {
		if res.Message != "" {
			fmt.Fprintf(out, "  %s\n", res.Message)
		} else {
			fmt.Fprintln(out, "  none")
		}
		return
	}
	rows := make([]tui.ServiceRow, len(res.Services))
	for i, s := range res.Services {
		state := "down"
		if s.Running {
			state = "up"
		}
		rows[i] = tui.ServiceRow{State: state, Name: s.Name}
	}
	fmt.Fprintln(out, tui.RenderStatusServices(rows))
}

// dbReachability reports the database's reachability as the cheap, honest signal:
// is a database service in this env's service list, and is it up? A native env
// (no services) or a project whose DB is external/managed cannot be probed
// cheaply, so it says so rather than claiming a state. This is deliberately not a
// driver-level ping - that heavier check is the studio Data tab's job.
func dbReachability(res svcListing, m *engine.Manifest) string {
	for _, s := range res.Services {
		if looksLikeDBService(s.Name, s.Kind) {
			if s.Running {
				return "up (" + s.Name + " service running)"
			}
			return "down (" + s.Name + " service is not running)"
		}
	}
	if m.Env != "" && (strings.Contains(m.Env, "local") || len(res.Services) == 0) {
		return "unknown (native or external database - not probed)"
	}
	return "unknown (no database service found in this environment)"
}

// dbServiceNames are the compose/ddev service names a project database usually
// takes. A match by name or image is the cheap "is the DB up?" heuristic.
var dbServiceNames = []string{"db", "database", "postgres", "postgresql", "pgsql", "mysql", "mariadb"}

// looksLikeDBService reports whether a service is the project database, by its
// name or its image (kind). It is a heuristic for the cheap status view, not an
// authoritative DB resolver.
func looksLikeDBService(name, kind string) bool {
	n, k := strings.ToLower(name), strings.ToLower(kind)
	for _, s := range dbServiceNames {
		if n == s || strings.Contains(n, s) || strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// statFact is one status stat row: a label and its value ("12" or "-").
type statFact struct {
	label string
	value string
}

// frameworkStats returns the cheap per-framework stats for status. Each is a
// bounded file walk or read, never a build. A framework keel has no cheap signal
// for returns no rows. Migrations and routes are the common signals across the
// supported stacks; a count that cannot be taken renders as a dash.
func frameworkStats(dir, framework string) []statFact {
	switch framework {
	case "laravel":
		return []statFact{
			{"migrations", countOrDash(countFilesCLI(filepath.Join(dir, "database", "migrations"), isPHP))},
			{"routes", countOrDash(countMatchesInDir(filepath.Join(dir, "routes"), "Route::"))},
			{"models", countOrDash(countFilesCLI(filepath.Join(dir, "app", "Models"), isPHP))},
		}
	case "django":
		// Every Django URL pattern is path(...) or re_path(...); "re_path(" itself
		// contains "path(", so counting "path(" alone counts both without
		// double-counting a re_path as two.
		return []statFact{
			{"migrations", countOrDash(countFilesCLI(dir, isDjangoMigration))},
			{"routes", countOrDash(countMatchesInDir(dir, "path("))},
		}
	case "fastapi":
		return []statFact{
			{"endpoints", countOrDash(countMatchesInDir(dir, "@app.") + countMatchesInDir(dir, "@router."))},
		}
	case "next", "nextjs":
		return []statFact{
			{"routes", countOrDash(countFilesCLI(filepath.Join(dir, "app"), isNextRoute) + countFilesCLI(filepath.Join(dir, "pages"), isTSXorJSX))},
		}
	case "magento":
		return []statFact{
			{"modules", countOrDash(countFilesCLI(filepath.Join(dir, "app", "code"), isModuleXML))},
		}
	}
	return nil
}

// --- cheap counting primitives (bounded; mirror the studio's overview walk) ---

const maxWalkEntries = 5000

// countFilesCLI counts files under dir whose name satisfies match, bounded and
// skipping heavy vendor dirs. A missing dir is zero, not an error.
func countFilesCLI(dir string, match func(name string) bool) int {
	n, seen := 0, 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipStatDir(d.Name()) && path != dir {
				return filepath.SkipDir
			}
			return nil
		}
		seen++
		if seen > maxWalkEntries {
			return filepath.SkipAll
		}
		if match(d.Name()) {
			n++
		}
		return nil
	})
	return n
}

// countMatchesInDir counts non-overlapping occurrences of sub across the small
// source files under dir (bounded per file and by entry count). It is a coarse
// signal - a routes count from `Route::`, an endpoint count from `@app.` - not a
// compiler, so status labels it as a count.
func countMatchesInDir(dir, sub string) int {
	total, seen := 0, 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipStatDir(d.Name()) && path != dir {
				return filepath.SkipDir
			}
			return nil
		}
		seen++
		if seen > maxWalkEntries {
			return filepath.SkipAll
		}
		if !isSourceFile(d.Name()) {
			return nil
		}
		if body, ok := readBoundedCLI(path); ok {
			total += strings.Count(body, sub)
		}
		return nil
	})
	return total
}

const maxStatFileBytes = 512 * 1024

// readBoundedCLI reads at most maxStatFileBytes of a file so a huge file cannot
// blow up the count. A missing/empty file is (zero-value, false).
func readBoundedCLI(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	buf := make([]byte, maxStatFileBytes)
	n, _ := f.Read(buf)
	if n <= 0 {
		return "", false
	}
	return string(buf[:n]), true
}

// skipStatDir names the heavy directories a stat walk prunes so a count stays
// cheap in a project with a big dependency tree.
func skipStatDir(name string) bool {
	switch name {
	case "node_modules", "vendor", ".git", ".keel", "dist", "build", ".next", "storage", "var", "__pycache__", ".venv", "venv":
		return true
	}
	return false
}

// countOrDash renders a count, showing a dash for zero so an empty or
// unreadable tree reads as "no signal" rather than a hard "0".
func countOrDash(n int) string {
	if n <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d", n)
}

// --- filename predicates -----------------------------------------------------

func isPHP(name string) bool { return strings.HasSuffix(name, ".php") }
func isTSXorJSX(name string) bool {
	return strings.HasSuffix(name, ".tsx") || strings.HasSuffix(name, ".jsx") ||
		strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".js")
}

// isDjangoMigration matches a Django migration file: a numbered file under a
// migrations directory (0001_initial.py, ...), excluding the package marker.
func isDjangoMigration(name string) bool {
	return strings.HasSuffix(name, ".py") && name != "__init__.py" && len(name) > 4 && name[0] >= '0' && name[0] <= '9'
}

// isNextRoute matches an App Router route file (page/route/layout).
func isNextRoute(name string) bool {
	switch name {
	case "page.tsx", "page.jsx", "page.ts", "page.js",
		"route.ts", "route.js", "route.tsx", "route.jsx":
		return true
	}
	return false
}

// isModuleXML matches a Magento module declaration (module.xml).
func isModuleXML(name string) bool { return name == "module.xml" }

// isSourceFile reports whether a filename is a source file worth scanning for a
// coarse match count (keeps the walk off binaries and lock files).
func isSourceFile(name string) bool {
	for _, ext := range []string{".php", ".py", ".ts", ".tsx", ".js", ".jsx"} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}
