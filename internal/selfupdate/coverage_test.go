package selfupdate

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// failHTTP installs a RoundTripper that returns a transport error (as if the
// network were unreachable), so the http.DefaultClient.Do error branches are hit.
func failHTTP(t *testing.T) {
	t.Helper()
	orig := client.Transport
	client.Transport = rtFunc(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("network down")
	})
	t.Cleanup(func() { client.Transport = orig })
}

// TestNewerEqualParsedVersions covers the final `return false` in Newer: two
// versions that differ as strings after norm ("1.2" vs "1.2.0") but parse to the
// same [3]int, so no component differs and it is not newer.
func TestNewerEqualParsedVersions(t *testing.T) {
	if Newer("1.2", "1.2.0") {
		t.Error(`Newer("1.2","1.2.0") should be false — same version, different spelling`)
	}
	if Newer("1.2.0", "1.2") {
		t.Error(`Newer("1.2.0","1.2") should be false`)
	}
}

func TestLatestTagRequestError(t *testing.T) {
	failHTTP(t)
	if _, err := latestTag(context.Background(), "o/r"); err == nil {
		t.Fatal("expected a transport error from latestTag")
	}
}

func TestDownloadRequestError(t *testing.T) {
	failHTTP(t)
	if _, err := download(context.Background(), "https://example.test/x", maxBinary); err == nil {
		t.Fatal("expected a transport error from download")
	}
}

// TestUpdateBubblesTransportError proves Update surfaces a network failure from
// the release lookup rather than swallowing it.
func TestUpdateBubblesTransportError(t *testing.T) {
	failHTTP(t)
	if _, err := Update(context.Background(), "o/r", "1.0.0", &nopWriter{}); err == nil {
		t.Fatal("expected Update to return the transport error")
	}
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestValidateRepo(t *testing.T) {
	cases := []struct {
		repo string
		ok   bool
	}{
		{"owner/repo", true},
		{"o/r", true},
		{"coullworks/keel", true},
		{"owner/repo.git", true},
		{"", false},
		{"owner", false},                    // no slash
		{"owner/repo/extra", false},         // too many parts
		{"owner/repo?ref=evil", false},      // query string
		{"../../etc/passwd", false},         // path traversal shape
		{"owner/repo/../../../evil", false}, // traversal
		{"owner /repo", false},              // space
		{"https://evil.com/owner/repo", false},
	}
	for _, c := range cases {
		err := validateRepo(c.repo)
		if c.ok && err != nil {
			t.Errorf("validateRepo(%q) = %v, want ok", c.repo, err)
		}
		if !c.ok && err == nil {
			t.Errorf("validateRepo(%q) = nil, want rejected", c.repo)
		}
	}
}

// TestUpdateRejectsBadRepo proves Update validates the repo before any network
// call (the failing transport would otherwise mask the point).
func TestUpdateRejectsBadRepo(t *testing.T) {
	if _, err := Update(context.Background(), "not-a-repo", "1.0.0", &nopWriter{}); err == nil {
		t.Fatal("Update should reject an invalid repo shape")
	}
}

func TestSwapBinary_ReplacesAndRemovesOld(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "keel")
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := swapBinary(exe, []byte("NEW")); err != nil {
		t.Fatalf("swapBinary: %v", err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "NEW" {
		t.Errorf("binary content = %q, want NEW", got)
	}
	// The .old and .new scratch files must not linger on success.
	if _, err := os.Stat(exe + ".old"); !os.IsNotExist(err) {
		t.Errorf("%s.old should be gone after a successful swap", exe)
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Errorf("%s.new should be gone after a successful swap", exe)
	}
}

// TestSwapBinary_FailsLeavingOriginalIntact drives the failure path: writing the
// new binary into a read-only directory fails before the current binary is
// touched, so the original is left intact (never a moment with no binary).
func TestSwapBinary_FailsLeavingOriginalIntact(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root ignores directory write permissions")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "keel")
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil { // read+execute, no write
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if err := swapBinary(exe, []byte("NEW")); err == nil {
		t.Fatal("swapBinary should error when it cannot write the new binary")
	}
	// Restore write so we can read back and clean up.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("original binary should be intact: %v", err)
	}
	if string(got) != "OLD" {
		t.Errorf("original content = %q, want OLD (unchanged)", got)
	}
}

// TestSwapBinary_AsideMoveFailureKeepsOriginal covers the "move current binary
// aside" error branch: a pre-existing NON-EMPTY directory at exe.old makes
// os.Rename(exe, exe.old) fail, so the swap aborts. The original binary and its
// content must be untouched, and the tmp scratch file cleaned up.
func TestSwapBinary_AsideMoveFailureKeepsOriginal(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "keel")
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A non-empty directory occupies the aside target; renaming a file onto it fails.
	if err := os.MkdirAll(filepath.Join(exe+".old", "occupied"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := swapBinary(exe, []byte("NEW")); err == nil {
		t.Fatal("swapBinary should error when the aside-move fails")
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("original binary should be intact: %v", err)
	}
	if string(got) != "OLD" {
		t.Errorf("original content = %q, want OLD (unchanged)", got)
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Errorf("tmp %s.new should be cleaned up after the failed aside-move", exe)
	}
}
