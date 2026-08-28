package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/coullworks/keel/internal/pluginstore"
	"github.com/coullworks/keel/plugin"
)

// installPluginWithCaps writes a minimal valid plugin source declaring caps and
// installs it into the isolated config dir, returning its name. It mirrors the
// pluginstore test helpers so a cli-level test can exercise trust/grant against a
// real installed plugin (built-ins have no store record and are refused caps).
func installPluginWithCaps(t *testing.T, name string, caps ...string) {
	t.Helper()
	src := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(src, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema: 1\nname: " + name + "\nversion: 1.0.0\n" +
		"description: a test plugin\nauthor: t\nlicense: MIT\n"
	if len(caps) > 0 {
		manifest += "capabilities: ["
		for i, c := range caps {
			if i > 0 {
				manifest += ", "
			}
			manifest += c
		}
		manifest += "]\n"
	}
	if err := os.WriteFile(filepath.Join(src, "config", "register.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := pluginstore.Install(context.Background(), src, ""); err != nil {
		t.Fatalf("install %s: %v", name, err)
	}
}

// Granting a capability is the terminal counterpart to the studio's per-capability
// toggle: `keel plugins grant <name> <cap>` must persist to the store, and
// `ungrant` must revoke it. Before this command a terminal user could trust a
// plugin but never widen or narrow a single power the way the studio can.
func TestPluginsGrantAndUngrantOneCapability(t *testing.T) {
	isolate(t)
	installPluginWithCaps(t, "netter", "net")
	// Trust so the grant is not merely inert (and to exercise the real gate).
	if _, err := runRoot(t, "plugins", "trust", "netter"); err != nil {
		t.Fatalf("trust netter: %v", err)
	}

	out, err := runRoot(t, "plugins", "grant", "netter", "net")
	if err != nil {
		t.Fatalf("grant net: %v", err)
	}
	mustContain(t, out, "granted net for netter")
	if !pluginstore.CapabilityGranted("netter", plugin.CapNet) {
		t.Error("grant did not persist to the store")
	}

	out, err = runRoot(t, "plugins", "ungrant", "netter", "net")
	if err != nil {
		t.Fatalf("ungrant net: %v", err)
	}
	mustContain(t, out, "revoked net for netter")
	if pluginstore.CapabilityGranted("netter", plugin.CapNet) {
		t.Error("ungrant did not revoke the capability in the store")
	}
}

// An unknown capability is refused with a message that names the closed set, so a
// typo cannot silently do nothing (or, worse, appear to widen consent).
func TestPluginsGrantRefusesUnknownCapability(t *testing.T) {
	isolate(t)
	installPluginWithCaps(t, "netter", "net")
	_, err := runRoot(t, "plugins", "grant", "netter", "everything")
	if err == nil {
		t.Fatal("granting an unknown capability should error")
	}
	mustContain(t, err.Error(), "unknown capability", "net, secrets, exec")
}

// Granting a capability on a plugin that is not yet trusted still records the
// grant, but warns that it is inert until the plugin is trusted — otherwise the
// user is left wondering why the action stays refused.
func TestPluginsGrantWarnsWhenNotTrusted(t *testing.T) {
	isolate(t)
	installPluginWithCaps(t, "netter", "net")
	out, err := runRoot(t, "plugins", "grant", "netter", "net")
	if err != nil {
		t.Fatalf("grant net: %v", err)
	}
	mustContain(t, out, "granted net for netter", "not trusted yet", "keel plugins trust netter")
	if !pluginstore.CapabilityGranted("netter", plugin.CapNet) {
		t.Error("grant should persist even when the plugin is untrusted")
	}
}

// Trusting a plugin from the terminal must grant the capabilities its manifest
// declares — the same single informed decision the studio's Trust button makes —
// so a plugin trusted from the CLI can actually run its capability-gated actions.
// This is the parity fix: previously `keel plugins trust` set only the trust flag
// and left every power ungranted.
func TestPluginsTrustGrantsDeclaredCapabilities(t *testing.T) {
	isolate(t)
	installPluginWithCaps(t, "deployer", "net", "secrets")

	out, err := runRoot(t, "plugins", "trust", "deployer")
	if err != nil {
		t.Fatalf("trust deployer: %v", err)
	}
	mustContain(t, out, "deployer trusted", "granted:", "net", "secrets")
	if !pluginstore.CapabilityGranted("deployer", plugin.CapNet) {
		t.Error("trust did not grant the declared net capability")
	}
	if !pluginstore.CapabilityGranted("deployer", plugin.CapSecrets) {
		t.Error("trust did not grant the declared secrets capability")
	}
	// A power the plugin never declared must not be granted by trust.
	if pluginstore.CapabilityGranted("deployer", plugin.CapExec) {
		t.Error("trust granted a capability the plugin never declared")
	}
}

// Untrusting revokes run-code consent but keeps the recorded grants, so
// re-trusting restores the user's earlier choices rather than resetting them —
// matching the studio's untrust semantics.
func TestPluginsUntrustKeepsGrantsInert(t *testing.T) {
	isolate(t)
	installPluginWithCaps(t, "deployer", "net")
	if _, err := runRoot(t, "plugins", "trust", "deployer"); err != nil {
		t.Fatalf("trust: %v", err)
	}
	if _, err := runRoot(t, "plugins", "untrust", "deployer"); err != nil {
		t.Fatalf("untrust: %v", err)
	}
	p, ok := pluginstore.Get("deployer")
	if !ok {
		t.Fatal("plugin vanished after untrust")
	}
	if p.Trusted {
		t.Error("untrust did not clear the trust flag")
	}
	if !p.GrantedCaps[plugin.CapNet] {
		t.Error("untrust dropped the recorded grant instead of keeping it inert")
	}
}
