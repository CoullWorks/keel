package cli

// env_resolve.go is the terminal counterpart to the studio's Env & Secrets tab
// (GET /api/env): it DISCOVERS and resolves a project's environment variables
// with Next.js precedence and surfaces them with the same house secret rule the
// studio uses, so `keel secrets list` and the studio agree.
//
// Precedence (Next.js order, highest wins):
//   .env.$(NODE_ENV).local  >  .env.local  >  .env.$(NODE_ENV)  >  .env
// The live process env is not merged in as a source (we would leak the caller's
// own shell); a var's provenance names the exact file it resolved from.
//
// Monorepo fallback: a member with no local .env falls back to the workspace
// root's env via project.EffectiveBackend (EnvDir points at the root for an
// inheriting member).
//
// Secret rule: a NEXT_PUBLIC_-prefixed var is public by definition (Next inlines
// it into the client bundle) and shows its value; a name matching a credential
// pattern, or a URL with embedded credentials, is masked - its raw value is
// NEVER printed.
//
// This re-derives the same logic internal/studio/env.go implements over HTTP,
// because the CLI must not import the studio. Worth consolidating into a shared
// low-level package later - see the report.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/coullworks/keel/internal/envfile"
	"github.com/coullworks/keel/internal/project"
)

// resolvedVar is one resolved environment variable. A secret carries no Value
// (it is withheld); Public and Secret are mutually exclusive. File names the
// provenance (the env file, relative to the dir it resolved from), and FromRoot
// says the var was inherited from the monorepo workspace root.
type resolvedVar struct {
	Key      string
	Value    string
	Secret   bool
	Public   bool
	Present  bool // a secret key that has a (withheld) value
	File     string
	FromRoot bool
}

// envListing is the whole resolution: the resolved vars (secrets masked), the
// dir the env came from, whether it came from the workspace root, and a calm
// note when there is no env at all.
type envListing struct {
	Found    bool
	Vars     []resolvedVar
	EnvDir   string
	FromRoot bool
	Note     string
}

// envNodeEnv reports the NODE_ENV precedence resolves against. A project is
// driven in development by default; an explicit NODE_ENV in the caller's shell
// overrides it. Mirrors Next.js, which defaults NODE_ENV to development.
func envNodeEnv() string {
	if v := strings.TrimSpace(os.Getenv("NODE_ENV")); v != "" {
		return v
	}
	return "development"
}

// envFilesInPrecedence lists the env files for a directory, HIGHEST precedence
// first. .env.local is skipped when NODE_ENV=test (Next.js ignores it there).
// Names are relative; the caller joins them onto the directory.
func envFilesInPrecedence(ne string) []string {
	files := []string{".env." + ne + ".local"}
	if ne != "test" {
		files = append(files, ".env.local")
	}
	files = append(files, ".env."+ne, ".env")
	return files
}

// resolveEnvDir reads dir's env files in precedence order and returns the
// winning value for each key plus the file it came from. Highest file wins; a
// lower file only supplies keys not already seen. found is true when at least
// one env file existed and carried a key.
func resolveEnvDir(dir, ne string) (values, from map[string]string, found bool) {
	values = map[string]string{}
	from = map[string]string{}
	for _, name := range envFilesInPrecedence(ne) {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		f, err := envfile.Load(path)
		if err != nil {
			continue
		}
		keys := f.Keys()
		if len(keys) > 0 {
			found = true
		}
		for _, k := range keys {
			if _, seen := values[k]; seen {
				continue
			}
			values[k] = f.Get(k)
			from[k] = name
		}
	}
	return values, from, found
}

// resolveProjectEnv resolves a project's environment for `keel secrets list`,
// including the monorepo root fallback. It classifies each var (public/secret/
// config) with secrets masked, and returns them sorted for a stable surface.
func resolveProjectEnv(dir string) envListing {
	ne := envNodeEnv()
	values, from, found := resolveEnvDir(dir, ne)
	envDir := dir
	fromRoot := false

	// Monorepo fallback: a member with no local env inherits the workspace root's
	// env. EffectiveBackend points EnvDir at the root for an inheriting member and
	// degrades to the member's own dir for a standalone project. Only fall back
	// when the member truly has no local env.
	if !found {
		if be := project.EffectiveBackend(dir); be.EnvDir != "" && filepath.Clean(be.EnvDir) != filepath.Clean(dir) {
			rv, rf, rfound := resolveEnvDir(be.EnvDir, ne)
			if rfound {
				values, from, found = rv, rf, true
				envDir = be.EnvDir
				fromRoot = true
			}
		}
	}

	res := envListing{Found: found, EnvDir: envDir, FromRoot: fromRoot, Vars: []resolvedVar{}}
	if !found {
		res.Note = "no env found - this project inherits its env from the workspace root, the platform injects it (e.g. Vercel), or run: keel secrets sync"
		return res
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		res.Vars = append(res.Vars, classifyVar(k, values[k], from[k], fromRoot))
	}
	return res
}

// classifyVar builds the surfaced var for one (key, value, file) triple applying
// the public/secret rule. A public (NEXT_PUBLIC_) var shows its value. A secret
// var (a credential-named key, or a URL with embedded credentials) is masked -
// no value is set, so the raw secret is never carried. Everything else is
// ordinary config and shows its value.
func classifyVar(key, value, file string, fromRoot bool) resolvedVar {
	if isPublicEnvKey(key) {
		return resolvedVar{Key: key, Value: value, Public: true, File: file, FromRoot: fromRoot}
	}
	if isSecretEnvKey(key) || urlHasCredentials(value) {
		return resolvedVar{Key: key, Secret: true, Present: strings.TrimSpace(value) != "", File: file, FromRoot: fromRoot}
	}
	return resolvedVar{Key: key, Value: value, File: file, FromRoot: fromRoot}
}

// isPublicEnvKey reports whether a var is public by Next.js's definition: a
// NEXT_PUBLIC_ prefix means the value is inlined into the client bundle, so it
// is safe to display in full.
func isPublicEnvKey(key string) bool {
	return strings.HasPrefix(key, "NEXT_PUBLIC_")
}

// isSecretEnvKey decides whether a config key's value must be withheld. It
// matches the credential words keel never surfaces, so a value that names a
// credential is masked, and everything else (host, dbname, config) shows.
func isSecretEnvKey(key string) bool {
	k := strings.ToLower(key)
	for _, s := range []string{"password", "passwd", "key", "secret", "token", "salt", "crypt"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// urlHasCredentials reports whether a value is a URL carrying an inline
// user:password (scheme://user:pass@host). Such a value is a secret however its
// key is named, so it must be masked.
func urlHasCredentials(v string) bool {
	i := strings.Index(v, "://")
	if i < 0 {
		return false
	}
	rest := v[i+3:]
	at := strings.IndexByte(rest, '@')
	if at < 0 {
		return false
	}
	return strings.IndexByte(rest[:at], ':') >= 0
}
