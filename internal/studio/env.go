package studio

// This file adds the Env & Secrets tab's core reader: GET /api/env, which
// DISCOVERS and resolves a project's environment variables with Next.js
// precedence and surfaces them with the studio's house secret rule.
//
// Why this exists: the Env & Secrets tab used to be action-only (Sync .env /
// Generate a key) and never listed the project's real variables, so on a Next.js
// app (.env.local), a monorepo member (env at the workspace root) or any stack
// the developer saw nothing. This reader closes that gap.
//
// Precedence (Next.js order, highest wins) — see the monorepo-run-env research:
//   .env.$(NODE_ENV).local  >  .env.local  >  .env.$(NODE_ENV)  >  .env
// The live process env is not merged in as a source (the studio would leak its
// own shell), but a var's provenance names the exact file it resolved from.
//
// Monorepo fallback: a member with no local .env falls back to the workspace
// root's env, resolved via project.EffectiveBackend (the member inherits the
// root's env dir). Each var records whether it came from the root or is local.
//
// Secret rule (mirrors magento_env.go and the credentialDTO boundary): a
// NEXT_PUBLIC_-prefixed var is public by definition (Next inlines it into the
// client bundle) and shows its value; anything matching a secret pattern, or a
// URL with embedded credentials, is masked to "••• present" server-side and its
// value NEVER crosses the wire.

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/coullworks/keel/internal/envfile"
	"github.com/coullworks/keel/internal/project"
)

// envVar is one resolved environment variable in the studio's stable JSON shape.
// A secret carries no Value (masked before it leaves the process); Public and
// Secret are mutually exclusive classifications. File names the provenance (the
// env file the value resolved from, relative to Dir), and FromRoot says the var
// was inherited from the monorepo workspace root rather than the member itself.
type envVar struct {
	Key      string `json:"key"`
	Value    string `json:"value,omitempty"` // omitted for secrets
	Secret   bool   `json:"secret"`          // true = value withheld
	Public   bool   `json:"public"`          // true = NEXT_PUBLIC_ (shown in full)
	Present  bool   `json:"present"`         // true = the (secret) key has a value
	File     string `json:"file"`            // provenance, e.g. ".env.local"
	FromRoot bool   `json:"fromRoot"`        // inherited from the workspace root
}

// envResponse is the shape GET /api/env returns. Vars are secret-masked before
// serialisation; the response never carries a raw secret value. RootDir/EnvDir
// let the UI say "inherited from the workspace root". A project with no env at
// all reports Found=false with a calm note rather than an error.
type envResponse struct {
	Found    bool     `json:"found"`            // any env file resolved at all
	Vars     []envVar `json:"vars"`             // resolved, secrets masked
	EnvDir   string   `json:"envDir,omitempty"` // dir the env resolved from (member or root)
	FromRoot bool     `json:"fromRoot"`         // env came from the workspace root
	Note     string   `json:"note,omitempty"`   // guidance (e.g. no env found)
	Error    string   `json:"error,omitempty"`  // populated only on a hard error
}

// nodeEnv reports the NODE_ENV the studio resolves precedence against. The
// studio drives a developer's project, so development is the right default; an
// explicit NODE_ENV in the studio's own shell overrides it. This mirrors
// Next.js, which defaults NODE_ENV to development for `next dev`.
func nodeEnv() string {
	if v := strings.TrimSpace(os.Getenv("NODE_ENV")); v != "" {
		return v
	}
	return "development"
}

// envFilesInPrecedence lists the env files for a directory, HIGHEST precedence
// first, per Next.js order. .env.local is skipped when NODE_ENV=test (Next.js
// ignores it there so tests get stable defaults). The returned names are
// relative — the caller joins them onto the directory and attributes provenance.
func envFilesInPrecedence(ne string) []string {
	files := []string{".env." + ne + ".local"}
	if ne != "test" {
		files = append(files, ".env.local")
	}
	files = append(files, ".env."+ne, ".env")
	return files
}

// resolveEnv reads dir's env files in precedence order and returns the winning
// value for each key plus the file it came from. Highest-precedence file wins;
// a lower file only supplies keys not already seen. found is true when at least
// one env file existed and carried a key. The returned maps are keyed by var
// name in no particular order — the caller sorts for a stable surface.
func resolveEnv(dir, ne string) (values map[string]string, from map[string]string, found bool) {
	values = map[string]string{}
	from = map[string]string{}
	safeBase, baseErr := filepath.Abs(dir)
	for _, name := range envFilesInPrecedence(ne) {
		path, absErr := filepath.Abs(filepath.Join(dir, name))
		if baseErr != nil || absErr != nil || !strings.HasPrefix(path, safeBase) {
			continue // refuse a path that would escape the project dir
		}
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
				continue // a higher-precedence file already won this key
			}
			values[k] = f.Get(k)
			from[k] = name
		}
	}
	return values, from, found
}

// isPublicKey reports whether a var is public by Next.js's definition: a
// NEXT_PUBLIC_ prefix means the value is inlined into the client bundle, so it
// is safe to display in full.
func isPublicKey(key string) bool {
	return strings.HasPrefix(key, "NEXT_PUBLIC_")
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
	authority := rest[:at]
	// A credential-bearing authority contains user[:pass]; a bare host before an
	// '@' is unusual, but a ':' before the '@' is the password marker we mask on.
	return strings.IndexByte(authority, ':') >= 0
}

// classifyEnv builds the surfaced envVar for one (key, value, file) triple,
// applying the public/secret rule. A public (NEXT_PUBLIC_) var shows its value.
// A secret var — a name matching a credential pattern, or a URL with embedded
// credentials — is masked: Secret=true, Present says whether it has a value, and
// NO value field is set, so the raw secret never leaves the process. Everything
// else is ordinary config and shows its value.
func classifyEnv(key, value, file string, fromRoot bool) envVar {
	if isPublicKey(key) {
		return envVar{Key: key, Value: value, Public: true, File: file, FromRoot: fromRoot}
	}
	if isSecretKey(key) || urlHasCredentials(value) {
		return envVar{Key: key, Secret: true, Present: strings.TrimSpace(value) != "", File: file, FromRoot: fromRoot}
	}
	return envVar{Key: key, Value: value, File: file, FromRoot: fromRoot}
}

// handleEnv is GET /api/env?dir= — the Env & Secrets tab's environment reader.
// It discovers and resolves the project's env with Next.js precedence
// (.env.$(NODE_ENV).local > .env.local > .env.$(NODE_ENV) > .env), falling back
// to the monorepo workspace root when a member has no local env, and returns
// each variable with its provenance and a public/secret classification. Secret
// values are masked before serialisation — a password, token or credential-URL
// value NEVER crosses the wire. A project with no env reports found=false with a
// calm note. Loopback + same-origin + token-guarded and projectDir-validated
// like every /api route.
func handleEnv(w http.ResponseWriter, r *http.Request) {
	dir, err := projectDir(r.URL.Query().Get("dir"))
	if err != nil {
		writeJSON(w, envResponse{Error: err.Error()})
		return
	}
	ne := nodeEnv()

	// First try the project's own dir.
	values, from, found := resolveEnv(dir, ne)
	envDir := dir
	fromRoot := false

	// Monorepo fallback: a member with no local env inherits the workspace root's
	// env. EffectiveBackend points EnvDir at the root for an inheriting member (it
	// degrades to the member's own dir for a standalone project). Only fall back
	// when the member truly has no local env, so a member that overrides a var
	// locally keeps its own value.
	if !found {
		if be := project.EffectiveBackend(dir); be.EnvDir != "" && filepath.Clean(be.EnvDir) != filepath.Clean(dir) {
			rv, rf, rfound := resolveEnv(be.EnvDir, ne)
			if rfound {
				values, from, found = rv, rf, true
				envDir = be.EnvDir
				fromRoot = true
			}
		}
	}

	resp := envResponse{Found: found, EnvDir: envDir, FromRoot: fromRoot, Vars: []envVar{}}
	if !found {
		resp.Note = "no env found. This project inherits its env from the workspace root or the platform injects it (e.g. Vercel), or run Sync .env below to create one"
		writeJSON(w, resp)
		return
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys) // map iteration order is not stable — sort for a fixed surface
	for _, k := range keys {
		resp.Vars = append(resp.Vars, classifyEnv(k, values[k], from[k], fromRoot))
	}
	writeJSON(w, resp)
}
