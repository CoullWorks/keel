package studio

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// a realistic app/etc/env.xml with a db password and crypt key that must NEVER
// appear in the handler's output.
const magentoEnvXMLFixture = `<?xml version="1.0"?>
<config>
    <db>
        <connection>
            <default>
                <host>db.internal</host>
                <dbname>magento</dbname>
                <username>mage_user</username>
                <password>SUPER_SECRET_DB_PW</password>
            </default>
        </connection>
    </db>
    <crypt>
        <key>CRYPT_KEY_THAT_MUST_NOT_LEAK</key>
    </crypt>
    <cache>
        <frontend>
            <default>
                <backend>Cm_Cache_Backend_Redis</backend>
            </default>
        </frontend>
    </cache>
    <session>
        <save>redis</save>
    </session>
</config>`

// writeMagentoEnvXML lays down app/etc/env.xml under dir.
func writeMagentoEnvXML(t *testing.T, dir, body string) {
	t.Helper()
	p := filepath.Join(dir, "app", "etc", "env.xml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMagentoEnvSurfacesKeysAndMasksSecrets(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "compose", []string{"magento"})
	writeMagentoEnvXML(t, dir, magentoEnvXMLFixture)
	trackProject(t, dir)

	w := httptest.NewRecorder()
	handleMagentoEnv(w, httptest.NewRequest("GET", "http://127.0.0.1/api/magento/env?dir="+dir, nil))

	body := w.Body.String()
	// The load-bearing assertion: NO secret value ever crosses the wire.
	for _, secret := range []string{"SUPER_SECRET_DB_PW", "CRYPT_KEY_THAT_MUST_NOT_LEAK"} {
		if strings.Contains(body, secret) {
			t.Fatalf("secret value leaked in response: %q\n%s", secret, body)
		}
	}
	m := decodeJSON(t, w)
	if m["installed"] != true {
		t.Fatalf("installed should be true, got %v", m["installed"])
	}
	items, ok := m["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("expected parsed config items, got %v", m["items"])
	}
	// Index items by key for assertions.
	byKey := map[string]map[string]any{}
	for _, it := range items {
		row := it.(map[string]any)
		byKey[row["key"].(string)] = row
	}
	// Non-secret values are shown.
	if row := byKey["db.host"]; row == nil || row["value"] != "db.internal" {
		t.Fatalf("db.host should show its value, got %v", row)
	}
	if row := byKey["db.dbname"]; row == nil || row["value"] != "magento" {
		t.Fatalf("db.dbname should show its value, got %v", row)
	}
	if row := byKey["db.username"]; row == nil || row["value"] != "mage_user" {
		t.Fatalf("db.username should show its value, got %v", row)
	}
	if row := byKey["cache.backend"]; row == nil || row["value"] != "Cm_Cache_Backend_Redis" {
		t.Fatalf("cache.backend should show its value, got %v", row)
	}
	if row := byKey["session.save"]; row == nil || row["value"] != "redis" {
		t.Fatalf("session.save should show its value, got %v", row)
	}
	// Secrets are masked: secret=true, present=true, NO value field.
	for _, k := range []string{"db.password", "crypt.key"} {
		row := byKey[k]
		if row == nil {
			t.Fatalf("%s should be surfaced (masked)", k)
		}
		if row["secret"] != true || row["present"] != true {
			t.Fatalf("%s should be secret+present, got %v", k, row)
		}
		if _, hasVal := row["value"]; hasVal {
			t.Fatalf("%s must not carry a value field, got %v", k, row)
		}
		if row["file"] != "app/etc/env.xml" {
			t.Fatalf("%s should point at app/etc/env.xml, got %v", k, row["file"])
		}
	}
}

func TestMagentoEnvNotInstalled(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "compose", []string{"magento"})
	trackProject(t, dir) // no env.xml written

	w := httptest.NewRecorder()
	handleMagentoEnv(w, httptest.NewRequest("GET", "http://127.0.0.1/api/magento/env?dir="+dir, nil))
	m := decodeJSON(t, w)
	if m["installed"] != false {
		t.Fatalf("installed should be false with no env.xml, got %v", m["installed"])
	}
	note, _ := m["note"].(string)
	if !strings.Contains(note, "isn't configured") {
		t.Fatalf("expected a clear not-installed note, got %q", note)
	}
	if _, hasErr := m["error"]; hasErr {
		t.Fatalf("not-installed is not an error, got %v", m["error"])
	}
}

func TestMagentoEnvSurfacesDotEnvMasked(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "compose", []string{"magento"})
	// .env alongside, carrying a non-secret and a secret.
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("MYSQL_DATABASE=magento\nMYSQL_PASSWORD=DOTENV_SECRET_PW\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	trackProject(t, dir)

	w := httptest.NewRecorder()
	handleMagentoEnv(w, httptest.NewRequest("GET", "http://127.0.0.1/api/magento/env?dir="+dir, nil))
	body := w.Body.String()
	if strings.Contains(body, "DOTENV_SECRET_PW") {
		t.Fatalf(".env secret value leaked: %s", body)
	}
	m := decodeJSON(t, w)
	dotenv, ok := m["dotEnv"].([]any)
	if !ok || len(dotenv) != 2 {
		t.Fatalf("expected 2 .env keys, got %v", m["dotEnv"])
	}
	byKey := map[string]map[string]any{}
	for _, it := range dotenv {
		row := it.(map[string]any)
		byKey[row["key"].(string)] = row
	}
	if row := byKey["MYSQL_DATABASE"]; row == nil || row["value"] != "magento" {
		t.Fatalf("MYSQL_DATABASE should show its value, got %v", row)
	}
	if row := byKey["MYSQL_PASSWORD"]; row == nil || row["secret"] != true || row["present"] != true {
		t.Fatalf("MYSQL_PASSWORD should be masked secret+present, got %v", row)
	} else if _, hasVal := row["value"]; hasVal {
		t.Fatalf("MYSQL_PASSWORD must not carry a value, got %v", row)
	}
}

func TestMagentoEnvRejectsUntrackedDir(t *testing.T) {
	isolateConfig(t)
	other := t.TempDir() // never tracked
	w := httptest.NewRecorder()
	handleMagentoEnv(w, httptest.NewRequest("GET", "http://127.0.0.1/api/magento/env?dir="+other, nil))
	m := decodeJSON(t, w)
	if _, ok := m["error"]; !ok {
		t.Fatalf("an untracked dir must be refused, got %v", m)
	}
}

func TestMagentoEnvBadXML(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	writeManifest(t, dir, "compose", []string{"magento"})
	writeMagentoEnvXML(t, dir, "<config><db>this is not closed")
	trackProject(t, dir)

	w := httptest.NewRecorder()
	handleMagentoEnv(w, httptest.NewRequest("GET", "http://127.0.0.1/api/magento/env?dir="+dir, nil))
	m := decodeJSON(t, w)
	if m["installed"] != true {
		t.Fatalf("installed should be true (the file exists), got %v", m["installed"])
	}
	if _, ok := m["error"]; !ok {
		t.Fatalf("a malformed env.xml should surface a parse error, got %v", m)
	}
}

func TestIsSecretKey(t *testing.T) {
	secret := []string{"password", "db.password", "crypt.key", "API_SECRET", "auth_token", "MYSQL_PASSWORD", "salt"}
	notSecret := []string{"host", "db.host", "dbname", "username", "cache.backend", "session.save", "MYSQL_DATABASE"}
	for _, k := range secret {
		if !isSecretKey(k) {
			t.Errorf("isSecretKey(%q) = false, want true", k)
		}
	}
	for _, k := range notSecret {
		if isSecretKey(k) {
			t.Errorf("isSecretKey(%q) = true, want false", k)
		}
	}
}
