// Package profile stores the engineer's defaults so Keel doesn't ask the
// obvious every time. `keel init` writes it once; every `keel new` pre-fills the
// tree from it. Layered: global Defaults plus per-language Overrides.
package profile

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// NoWebServer is the "webserver" key's opt-out value: you front the app
// yourself. It is a word rather than an empty string because the two states
// have to be told apart, and an empty string cannot do it.
//
// The web server used to be saved into the services list like any other
// service. That made a profile written before the web-server question existed
// indistinguishable from one where the engineer declined - both are just a
// services list with no web server in it. `keel new` read the first as the
// second and built a stack with no ingress: every container up, nothing
// listening, connection refused. Unreachable stacks are exactly what making a
// web server the default was meant to end.
//
// So: key absent or empty means the question was never answered and the
// framework's default applies; NoWebServer is the only way to decline.
const NoWebServer = "none"

// Git is the author identity stamped into generated skeletons and commits.
type Git struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

// Profile is the persisted developer preference set.
type Profile struct {
	Defaults  map[string]string            `yaml:"defaults"`
	Git       Git                          `yaml:"git"`
	Overrides map[string]map[string]string `yaml:"overrides"` // per-language, e.g. overrides.php.db = mysql
}

// Default is the out-of-the-box profile: PHP-first, DDEV over Sail, and
// Postgres-first (the default DB everywhere the stack supports it).
func Default() *Profile {
	return &Profile{
		Defaults: map[string]string{
			"framework":    "laravel",
			"env":          "ddev",
			"database":     "postgres",
			"cache":        "redis",
			"tests":        "pest",
			"editor":       "code", // launcher command: code | pstorm | cursor | zed | nvim | subl
			"license":      "MIT",
			"projects_dir": "",        // where `keel new` creates projects ("" = current directory)
			"hosting":      "compose", // default deploy target: compose | vercel | fly | render | railway | vps
			// Default stack layers (recipe ids). Empty = fall back to each recipe's
			// own default:true. services/addons/extras are comma-separated lists.
			"frontend": "",
			"services": "",
			"addons":   "",
			"extras":   "",
			// External agentic-commerce readiness tool `keel commerce ready` calls.
			"commerce_tool": "",
			// The web server is single-select, so it has its own key rather than
			// living in the services list. Deliberately absent from this map:
			// see NoWebServer for why unset and "none" must mean different
			// things, and why a fresh profile must read as unset.
		},
		Overrides: map[string]map[string]string{},
	}
}

// HostingOptions is the set of default deploy/hosting targets offered at
// onboarding. Keys match `keel deploy` targets.
var HostingOptions = []struct{ Key, Label string }{
	{"compose", "Self-host (Docker Compose + Caddy)"},
	{"vercel", "Vercel (Next.js)"},
	{"fly", "Fly.io"},
	{"render", "Render"},
	{"railway", "Railway"},
	{"vps", "VPS (rsync + compose)"},
}

// Dir is the config directory (honors KEEL_CONFIG_DIR then XDG_CONFIG_HOME).
func Dir() string {
	if x := os.Getenv("KEEL_CONFIG_DIR"); x != "" {
		return x
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "keel")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "keel")
}

// Path is the profile file location.
func Path() string { return filepath.Join(Dir(), "profile.yaml") }

// Exists reports whether a saved profile is present (used for first-run onboarding).
func Exists() bool {
	_, err := os.Stat(Path())
	return err == nil
}

// Load reads the profile, returning Default() if none exists yet.
func Load() (*Profile, error) {
	b, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return nil, err
	}
	var p Profile
	if err := yaml.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	if p.Defaults == nil {
		p.Defaults = map[string]string{}
	}
	if p.Overrides == nil {
		p.Overrides = map[string]map[string]string{}
	}
	return &p, nil
}

// Save writes the profile to disk, creating the config dir if needed.
func (p *Profile) Save() error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	b, err := yaml.Marshal(p)
	if err != nil {
		return err
	}
	return os.WriteFile(Path(), b, 0o644)
}

// Get returns the effective default for key, honoring a per-language override.
func (p *Profile) Get(lang, key string) string {
	if lang != "" {
		if o, ok := p.Overrides[lang]; ok {
			if v, ok := o[key]; ok {
				return v
			}
		}
	}
	return p.Defaults[key]
}
