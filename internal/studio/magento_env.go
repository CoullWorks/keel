package studio

// This file adds the Magento config surface to the studio's Env & Secrets tab.
// Magento keeps its runtime config in app/etc/env.xml — a PHP config array
// serialised as XML: the DB connection (host/dbname/username/password), the
// crypt key, cache and session backends, install date, etc. Some setups (and
// docker-compose flows) also carry a .env alongside it.
//
// The keel house rule for secrets: never leak the credential VALUE — show WHERE
// it lives and THAT it exists. So non-secret config (db host/name/user, cache and
// session backends) shows its value, while secrets (the db password, the crypt
// key, any *key/*password/*secret/*token) show "present" + the file they live in
// (app/etc/env.xml or .env), never the value. A project with no env.xml (not
// installed yet) returns a clear not-installed shape rather than an error.

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/coullworks/keel/internal/envfile"
)

// magentoConfigItem is one surfaced config key: its label, where it lives, and
// EITHER a non-secret value OR a masked-secret indicator (never both). Secret is
// true when the value is withheld; Present says the secret is actually set.
type magentoConfigItem struct {
	Key     string `json:"key"`             // human key, e.g. "db.host"
	File    string `json:"file"`            // where it lives, e.g. "app/etc/env.xml"
	Value   string `json:"value,omitempty"` // the value (non-secrets only)
	Secret  bool   `json:"secret"`          // true = a secret, value withheld
	Present bool   `json:"present"`         // true = the (secret) key has a value
}

// magentoEnvResponse is the shape /api/magento/env returns: whether an env.xml
// exists at all, the grouped config items (db, crypt, backends), and the .env
// keys found alongside. Values are already VALUE-MASKED for secrets before they
// leave the process — the response never carries a password or crypt key.
type magentoEnvResponse struct {
	Installed bool                `json:"installed"`       // app/etc/env.xml exists
	EnvXML    string              `json:"envXml"`          // relative path shown to the user
	Items     []magentoConfigItem `json:"items"`           // parsed env.xml config, secrets masked
	DotEnv    []magentoConfigItem `json:"dotEnv"`          // .env keys (secrets masked)
	Note      string              `json:"note,omitempty"`  // guidance (e.g. "not installed yet")
	Error     string              `json:"error,omitempty"` // populated only on a hard error
}

// magentoEnvXML is the subset of app/etc/env.xml keel reads. Magento's env.xml
// nests with element names (not <item name="…">, which is config.php/di.xml's
// form): <db><connection><default><host>…, <crypt><key>, <cache><frontend>
// <default><backend>, <session><save>. We decode only the config keys we
// surface; everything else in the (large, additive) schema is ignored.
type magentoEnvXML struct {
	XMLName xml.Name `xml:"config"`
	DB      struct {
		Connection struct {
			Default struct {
				Host     string `xml:"host"`
				DBName   string `xml:"dbname"`
				Username string `xml:"username"`
				Password string `xml:"password"`
			} `xml:"default"`
		} `xml:"connection"`
	} `xml:"db"`
	Crypt struct {
		Key string `xml:"key"`
	} `xml:"crypt"`
	Cache struct {
		Frontend struct {
			Default struct {
				Backend string `xml:"backend"`
			} `xml:"default"`
		} `xml:"frontend"`
	} `xml:"cache"`
	Session struct {
		Save string `xml:"save"`
	} `xml:"session"`
}

// isSecretKey decides whether a config key's value must be withheld. It matches
// the credential words keel never surfaces — password, key, secret, token, salt —
// so a value that names a credential is masked wherever it appears (env.xml or
// .env), and everything else (host, dbname, backend names) shows its value.
func isSecretKey(key string) bool {
	k := strings.ToLower(key)
	for _, s := range []string{"password", "passwd", "key", "secret", "token", "salt", "crypt"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// item builds a surfaced config item for a (key, value) pair, applying the secret
// rule: a secret key never carries its value, only Present (does it have one). A
// non-secret key shows its value. file is where the key lives.
func item(key, value, file string) magentoConfigItem {
	if isSecretKey(key) {
		return magentoConfigItem{Key: key, File: file, Secret: true, Present: strings.TrimSpace(value) != ""}
	}
	return magentoConfigItem{Key: key, File: file, Value: strings.TrimSpace(value)}
}

// parseMagentoEnvXML reads dir/app/etc/env.xml and surfaces the config keys with
// secrets already masked. The returned items are safe to serialise — no password
// or crypt key value is ever included. A parse error yields an item-less result
// with the error set; the caller decides how to present it. The read path is
// confined under dir (Abs + HasPrefix) so it can never escape the project.
func parseMagentoEnvXML(dir string) ([]magentoConfigItem, error) {
	safeBase, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("refusing path outside %q", dir)
	}
	p, err := filepath.Abs(filepath.Join(dir, "app", "etc", "env.xml"))
	if err != nil || !strings.HasPrefix(p, safeBase) {
		return nil, fmt.Errorf("refusing path outside %q", dir)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var cfg magentoEnvXML
	if err := xml.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	const file = "app/etc/env.xml"
	var out []magentoConfigItem

	// DB default connection: host / dbname / username (values) + password (masked).
	def := cfg.DB.Connection.Default
	addIf := func(key, val string) {
		if strings.TrimSpace(val) != "" {
			out = append(out, item(key, val, file))
		}
	}
	addIf("db.host", def.Host)
	addIf("db.dbname", def.DBName)
	addIf("db.username", def.Username)
	// db.password is always surfaced (masked) so the user sees whether one is set.
	if def.Host != "" || def.DBName != "" || def.Username != "" || def.Password != "" {
		out = append(out, item("db.password", def.Password, file))
	}

	// Crypt key: masked, present-only.
	if cfg.Crypt.Key != "" {
		out = append(out, item("crypt.key", cfg.Crypt.Key, file))
	}

	// Cache / session backends: non-secret, show the value so the user sees what
	// backend the install uses (redis, files, db …).
	addIf("cache.backend", cfg.Cache.Frontend.Default.Backend)
	addIf("session.save", cfg.Session.Save)

	return out, nil
}

// readDotEnvKeys surfaces the .env keys alongside env.xml (some Magento/compose
// setups keep credentials there too). Only KEYS + a masked-secret indicator
// leave the process — never a value for a secret key. Non-secret keys show their
// value. A missing .env yields no items.
func readDotEnvKeys(dir string) []magentoConfigItem {
	f, err := envfile.Load(filepath.Join(dir, ".env"))
	if err != nil {
		return nil
	}
	var out []magentoConfigItem
	for _, k := range f.Keys() {
		out = append(out, item(k, f.Get(k), ".env"))
	}
	return out
}

// handleMagentoEnv is GET /api/magento/env?dir= — the Env & Secrets tab's Magento
// reader. It surfaces app/etc/env.xml's config KEYS (db host/name/user + backends
// as values; password + crypt key masked) and the .env keys, with every secret
// VALUE withheld before it leaves the process. A project with no env.xml reports
// installed=false with a clear note rather than an error. Loopback + same-origin +
// token-guarded and projectDir-validated like every /api route.
func handleMagentoEnv(w http.ResponseWriter, r *http.Request) {
	dir, err := projectDir(r.URL.Query().Get("dir"))
	if err != nil {
		writeJSON(w, magentoEnvResponse{Error: err.Error()})
		return
	}
	rel := "app/etc/env.xml"
	resp := magentoEnvResponse{EnvXML: rel, DotEnv: readDotEnvKeys(dir)}

	safeBase, absErr := filepath.Abs(dir)
	envXMLPath, joinErr := filepath.Abs(filepath.Join(dir, "app", "etc", "env.xml"))
	if absErr != nil || joinErr != nil || !strings.HasPrefix(envXMLPath, safeBase) {
		resp.Error = fmt.Sprintf("refusing path outside %q", dir)
		writeJSON(w, resp)
		return
	}
	if _, statErr := os.Stat(envXMLPath); statErr != nil {
		// No env.xml: Magento isn't installed/configured yet. Not an error — the UI
		// shows a clear "not installed yet" message and still lists any .env keys.
		resp.Installed = false
		resp.Note = "no app/etc/env.xml yet. Magento isn't configured in this project (run setup:install to create it)"
		writeJSON(w, resp)
		return
	}

	items, perr := parseMagentoEnvXML(dir)
	if perr != nil {
		resp.Installed = true
		resp.Error = "could not parse app/etc/env.xml: " + perr.Error()
		writeJSON(w, resp)
		return
	}
	resp.Installed = true
	resp.Items = items
	writeJSON(w, resp)
}
