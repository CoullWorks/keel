// Package creds collects the credentials a build needs and writes them where the
// chosen environment reads them.
//
// Three things stay separate on purpose:
//
//   - A recipe declares WHAT it needs (recipe.Credential). Magento says it needs
//     repo.magento.com keys; a third-party pack says it needs its own vendor's.
//   - The user supplies the values once, and may choose to remember them.
//   - The environment decides WHERE they land: DDEV reads Composer auth from
//     .ddev/homeadditions, a compose project from the project root, a native one
//     from the user's own Composer home.
//
// Keeping them apart is what makes this work for any Composer-based stack rather
// than for Magento specifically, which is what the previous hardcoded flow did.
//
// Everything here is a secret. Values are written with file permissions 0600 and
// never passed through a shell: a `composer config --auth` step would put the key
// in the build log, in any screenshot of it, and in the process list for every
// other user on the machine.
package creds

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

// Value is one supplied credential.
type Value struct {
	ID       string `yaml:"id" json:"id"`
	Kind     string `yaml:"kind" json:"kind"` // composer | env
	Username string `yaml:"username,omitempty" json:"username,omitempty"`
	Secret   string `yaml:"secret" json:"secret"` // password, token, or env value
	Auth     string `yaml:"auth,omitempty" json:"auth,omitempty"`
	// Remember stores this value in the user's credential file so the next
	// project does not ask again.
	Remember bool `yaml:"-" json:"remember,omitempty"`
}

// Filled reports whether this value carries anything worth writing.
func (v Value) Filled() bool {
	if strings.TrimSpace(v.Secret) == "" {
		return false
	}
	// http-basic needs both halves; a username alone authenticates nothing.
	if v.Kind == recipe.CredComposer && v.authType() == recipe.AuthHTTPBasic {
		return strings.TrimSpace(v.Username) != ""
	}
	return true
}

func (v Value) authType() string {
	if v.Auth == "" {
		return recipe.AuthHTTPBasic
	}
	return v.Auth
}

// Required lists the credentials a plan declares, deduplicated and in a stable
// order, so the wizard asks for each one once however many recipes want it.
func Required(plan *resolver.Plan) []recipe.Credential {
	seen := map[string]bool{}
	var out []recipe.Credential
	for _, r := range plan.Recipes {
		for _, c := range r.Credentials {
			if seen[c.ID] {
				continue
			}
			seen[c.ID] = true
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		// Required first: those block the build, the rest are offers.
		if out[i].Required != out[j].Required {
			return out[i].Required
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Suggestions is the list of environment variable names offered as completions
// when someone adds one of their own: the common ones below, plus whatever the
// plan's recipes suggest.
func Suggestions(plan *resolver.Plan) []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if n = strings.TrimSpace(n); n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, n := range CommonEnvNames {
		add(n)
	}
	for _, r := range plan.Recipes {
		for _, n := range r.EnvSuggestions {
			add(n)
		}
	}
	sort.Strings(out)
	return out
}

// CommonEnvNames are the keys people paste into a fresh project by hand often
// enough that keel may as well ask. Suggestions only, never a fixed list: any
// name is valid, and this exists to save typing, not to constrain anyone.
var CommonEnvNames = []string{
	// Analytics and tags
	"GOOGLE_ANALYTICS_ID", "GOOGLE_TAG_MANAGER_ID", "GOOGLE_MAPS_API_KEY",
	"PLAUSIBLE_DOMAIN", "POSTHOG_KEY", "MIXPANEL_TOKEN",
	// AI providers
	"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GROQ_API_KEY",
	"HUGGINGFACE_API_KEY", "OPENROUTER_API_KEY",
	// Payments and commerce
	"STRIPE_SECRET_KEY", "STRIPE_PUBLISHABLE_KEY", "STRIPE_WEBHOOK_SECRET",
	"PAYPAL_CLIENT_ID", "PAYPAL_CLIENT_SECRET", "KLARNA_API_KEY",
	// Mail
	"RESEND_API_KEY", "POSTMARK_TOKEN", "SENDGRID_API_KEY", "MAILGUN_API_KEY",
	"MAIL_HOST", "MAIL_USERNAME", "MAIL_PASSWORD",
	// Storage, search, monitoring
	"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_BUCKET", "AWS_REGION",
	"CLOUDFLARE_API_TOKEN", "ALGOLIA_APP_ID", "ALGOLIA_API_KEY",
	"SENTRY_DSN", "NEW_RELIC_LICENSE_KEY",
	// Auth providers
	"GITHUB_CLIENT_ID", "GITHUB_CLIENT_SECRET",
	"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET",
}

// ComposerAuthPath is where this environment's Composer reads auth.json, and
// whether that path sits inside the project.
//
// Getting this wrong is silent: the previous flow always wrote DDEV's path, so
// under compose or a native install it produced a file nothing read and a
// `composer create-project` that failed to authenticate for no visible reason.
func ComposerAuthPath(family, projectDir string) (path string, inProject bool) {
	switch family {
	case recipe.FamilyDDEV:
		// DDEV mounts homeadditions into the web container's home, so Composer
		// finds it without the file being part of the project source.
		return filepath.Join(projectDir, ".ddev", "homeadditions", ".composer", "auth.json"), true
	case recipe.FamilyCompose, recipe.FamilySail:
		// The project directory is the bind mount, so the project root is the
		// only place both the host and the container can see.
		return filepath.Join(projectDir, "auth.json"), true
	default:
		// Native: Composer's own home, shared by every project on this machine.
		return filepath.Join(composerHome(), "auth.json"), false
	}
}

func composerHome() string {
	if d := os.Getenv("COMPOSER_HOME"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "composer")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".composer"
	}
	return filepath.Join(home, ".composer")
}

// authFile is Composer's auth.json shape.
type authFile struct {
	HTTPBasic map[string]basicAuth `json:"http-basic,omitempty"`
	Bearer    map[string]string    `json:"bearer,omitempty"`
}

type basicAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// WriteComposerAuth merges values into the auth.json this environment reads and
// returns the path written, or "" when there was nothing to write.
//
// Merging rather than replacing matters: a native install shares one auth.json
// across every project on the machine, and overwriting it would silently drop
// the credentials for everything else.
func WriteComposerAuth(family, projectDir string, values []Value) (string, error) {
	var use []Value
	for _, v := range values {
		if v.Kind == recipe.CredComposer && v.Filled() {
			use = append(use, v)
		}
	}
	if len(use) == 0 {
		return "", nil
	}
	path, _ := ComposerAuthPath(family, projectDir)
	// Normalise the auth.json target and refuse a traversal: the path is built
	// from projectDir / the Composer home, so a crafted value must not escape to
	// write this secret somewhere unintended.
	pathAbs, err := filepath.Abs(path)
	if err != nil || strings.Contains(pathAbs, "..") {
		return "", fmt.Errorf("refusing path outside %q", path)
	}
	path = pathAbs

	af := authFile{HTTPBasic: map[string]basicAuth{}, Bearer: map[string]string{}}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &af); err != nil {
			return "", fmt.Errorf("%s is not valid JSON, refusing to overwrite it: %w", path, err)
		}
		if af.HTTPBasic == nil {
			af.HTTPBasic = map[string]basicAuth{}
		}
		if af.Bearer == nil {
			af.Bearer = map[string]string{}
		}
	}
	for _, v := range use {
		if v.authType() == recipe.AuthBearer {
			af.Bearer[v.ID] = v.Secret
			continue
		}
		af.HTTPBasic[v.ID] = basicAuth{Username: v.Username, Password: v.Secret}
	}
	if len(af.HTTPBasic) == 0 {
		af.HTTPBasic = nil
	}
	if len(af.Bearer) == 0 {
		af.Bearer = nil
	}
	b, err := json.MarshalIndent(af, "", "    ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	// 0600: this file is the credential.
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// EnvValues returns the env-kind credentials as name/value pairs, ready to merge
// into the project's .env.
func EnvValues(values []Value) map[string]string {
	out := map[string]string{}
	for _, v := range values {
		if v.Kind == recipe.CredEnv && v.Filled() {
			out[v.ID] = v.Secret
		}
	}
	return out
}

// MissingRequired lists the credentials a plan declares as required that were
// not supplied, so a build can stop before running installers that would fail
// halfway through with someone else's error message.
func MissingRequired(plan *resolver.Plan, values []Value) []recipe.Credential {
	have := map[string]bool{}
	for _, v := range values {
		if v.Filled() {
			have[v.ID] = true
		}
	}
	var out []recipe.Credential
	for _, c := range Required(plan) {
		if c.Required && !have[c.ID] {
			out = append(out, c)
		}
	}
	return out
}
