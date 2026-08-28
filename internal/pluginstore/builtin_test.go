package pluginstore

import (
	"testing"
)

// A built-in has no directory to switch off in, so its off switch is recorded in
// plugins.yaml. Disabling one must persist and be readable back through
// DisabledBuiltins, which is what the registry consults at startup.
func TestBuiltinDisableSurvivesReload(t *testing.T) {
	withConfigDir(t)

	// A fresh index disables nothing.
	if d := DisabledBuiltins(); len(d) != 0 {
		t.Fatalf("nothing disabled yet, got %v", d)
	}

	if err := SetBuiltinEnabled("sonar", false); err != nil {
		t.Fatal(err)
	}
	if d := DisabledBuiltins(); !d["sonar"] {
		t.Errorf("disabling sonar did not persist, got %v", d)
	}

	// Re-enabling clears the record: the default state is "on", expressed by the
	// absence of an off switch rather than a record that says on.
	if err := SetBuiltinEnabled("sonar", true); err != nil {
		t.Fatal(err)
	}
	if d := DisabledBuiltins(); d["sonar"] {
		t.Errorf("re-enabling sonar did not clear the record, got %v", d)
	}
}

// A built-in off switch must not be confused with an on-disk plugin: List scans
// directories, so a built-in placeholder record (which has no directory) never
// shows up as an installed plugin.
func TestDisabledBuiltinIsNotListedAsInstalled(t *testing.T) {
	withConfigDir(t)
	if err := SetBuiltinEnabled("ai-core", false); err != nil {
		t.Fatal(err)
	}
	all, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Errorf("a disabled built-in should not appear as an installed plugin, got %+v", all)
	}
}

// Enabling a built-in that was never disabled is a no-op, not an error: the
// caller should not have to check first.
func TestEnableBuiltinThatWasNeverDisabled(t *testing.T) {
	withConfigDir(t)
	if err := SetBuiltinEnabled("sonar", true); err != nil {
		t.Errorf("enabling an already-enabled built-in should be a no-op, got %v", err)
	}
	if d := DisabledBuiltins(); len(d) != 0 {
		t.Errorf("no built-in should be disabled, got %v", d)
	}
}
