package selfupdate

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// isolate points the cache at a temp dir so a test never reads or writes the
// developer's real ~/.config/keel.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("KEEL_CONFIG_DIR", dir)
	// No test may reach the network. Anything that would is a bug in the test.
	t.Setenv("KEEL_NO_UPDATE_CHECK", "")
	return dir
}

func writeCacheFor(t *testing.T, latest string, age time.Duration) {
	t.Helper()
	b, _ := json.Marshal(cache{CheckedAt: time.Now().Add(-age), Latest: latest})
	if err := os.WriteFile(cachePath(), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestNoticeReportsANewerRelease is the whole point: a fresh cache naming a
// newer version produces a line telling you how to get it.
func TestNoticeReportsANewerRelease(t *testing.T) {
	isolate(t)
	writeCacheFor(t, "v1.4.0", time.Hour)
	msg := Notice("coullworks/keel", "1.2.0")
	if msg == "" {
		t.Fatal("a newer cached release produced no notice")
	}
	for _, want := range []string{"1.4.0", "1.2.0", "self-update"} {
		if !contains(msg, want) {
			t.Errorf("notice %q does not mention %q", msg, want)
		}
	}
}

// TestNoticeSaysNothingWhenCurrent: being up to date must be silent. A line on
// every command that only ever says "you are fine" is noise people learn to
// ignore, which is how they miss the one that matters.
func TestNoticeSaysNothingWhenCurrent(t *testing.T) {
	isolate(t)
	writeCacheFor(t, "v1.2.0", time.Hour)
	if msg := Notice("coullworks/keel", "1.2.0"); msg != "" {
		t.Errorf("expected silence when up to date, got %q", msg)
	}
}

// TestNoticeIsSilentOnAFirstRun: with no cache there is nothing to say, and the
// check must not block to find out. The refresh happens in the background and
// the answer arrives on a later run.
func TestNoticeIsSilentOnAFirstRun(t *testing.T) {
	isolate(t)
	t.Setenv("KEEL_NO_UPDATE_CHECK", "1") // also stops the background refresh reaching the network
	if msg := Notice("coullworks/keel", "1.2.0"); msg != "" {
		t.Errorf("a first run should say nothing, got %q", msg)
	}
}

// TestNoticeRespectsTheOptOut: a Dockerfile or CI job sets this, and it must
// suppress both the message and the background request.
func TestNoticeRespectsTheOptOut(t *testing.T) {
	isolate(t)
	writeCacheFor(t, "v9.9.9", time.Hour)
	t.Setenv(DisableEnv, "1")
	if msg := Notice("coullworks/keel", "1.2.0"); msg != "" {
		t.Errorf("%s must suppress the notice, got %q", DisableEnv, msg)
	}
}

// TestNoticeReturnsPromptly is the property that matters most: this runs on
// every single command, so it must answer from disk rather than wait on a
// network round trip. A slow or blackholed connection must not be able to delay
// `keel new` at all.
func TestNoticeReturnsPromptly(t *testing.T) {
	isolate(t)
	writeCacheFor(t, "v1.4.0", 48*time.Hour) // stale, so it also triggers a refresh
	start := time.Now()
	_ = Notice("coullworks/keel", "1.2.0")
	if d := time.Since(start); d > 250*time.Millisecond {
		t.Fatalf("Notice took %v; it must answer from the cache and refresh in the background", d)
	}
}

// TestAFailedRefreshStillRecordsTheAttempt: a machine with no network must not
// fire a request on every command. Recording the attempt is what rate-limits it
// to once a day whether or not the check succeeded.
func TestAFailedRefreshStillRecordsTheAttempt(t *testing.T) {
	isolate(t)
	// A server that refuses, rather than the real GitHub: the point of the test
	// is what keel records when the lookup fails, and reaching the internet to
	// find that out makes the test fail on a train.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	refresh("coullworks/keel")
	c, err := readCache()
	if err != nil {
		t.Fatalf("a failed refresh wrote no cache: %v", err)
	}
	if c.CheckedAt.IsZero() {
		t.Error("a failed refresh did not record when it tried")
	}
}

// TestCacheLivesUnderTheConfigDir keeps the file where the rest of keel's state
// is, so removing the config directory removes this too.
func TestCacheLivesUnderTheConfigDir(t *testing.T) {
	dir := isolate(t)
	if got := cachePath(); filepath.Dir(got) != dir {
		t.Errorf("cache path %q is not under the config dir %q", got, dir)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
