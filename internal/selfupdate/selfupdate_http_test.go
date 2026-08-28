package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"
)

// rtFunc adapts a function to an http.RoundTripper.
type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeHTTP installs a RoundTripper on http.DefaultClient for the duration of the
// test and restores the original afterwards. handler maps a request to a status
// code + body; the selfupdate code uses http.DefaultClient throughout, so this
// intercepts api.github.com / release-download URLs without hitting the network.
func fakeHTTP(t *testing.T, handler func(*http.Request) (int, string)) {
	t.Helper()
	orig := client.Transport
	client.Transport = rtFunc(func(r *http.Request) (*http.Response, error) {
		code, body := handler(r)
		return &http.Response{
			StatusCode: code,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})
	t.Cleanup(func() { client.Transport = orig })
}

func TestLatestTagOK(t *testing.T) {
	fakeHTTP(t, func(r *http.Request) (int, string) {
		if !strings.Contains(r.URL.String(), "/repos/owner/repo/releases/latest") {
			t.Errorf("unexpected URL: %s", r.URL)
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("missing Accept header, got %q", got)
		}
		return 200, `{"tag_name":"v1.4.0"}`
	})
	tag, err := latestTag(context.Background(), "owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v1.4.0" {
		t.Errorf("tag = %q, want v1.4.0", tag)
	}
}

func TestLatestTagErrors(t *testing.T) {
	t.Run("non-200", func(t *testing.T) {
		fakeHTTP(t, func(*http.Request) (int, string) { return 404, `not found` })
		if _, err := latestTag(context.Background(), "o/r"); err == nil {
			t.Fatal("expected error on 404")
		}
	})
	t.Run("bad json", func(t *testing.T) {
		fakeHTTP(t, func(*http.Request) (int, string) { return 200, `{not json` })
		if _, err := latestTag(context.Background(), "o/r"); err == nil {
			t.Fatal("expected error on malformed json")
		}
	})
	t.Run("empty tag", func(t *testing.T) {
		fakeHTTP(t, func(*http.Request) (int, string) { return 200, `{"tag_name":""}` })
		if _, err := latestTag(context.Background(), "o/r"); err == nil {
			t.Fatal("expected error on empty tag")
		}
	})
}

func TestDownload(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		fakeHTTP(t, func(*http.Request) (int, string) { return 200, "binary-bytes" })
		b, err := download(context.Background(), "https://example.test/x", maxBinary)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != "binary-bytes" {
			t.Errorf("download body = %q", b)
		}
	})
	t.Run("non-200", func(t *testing.T) {
		fakeHTTP(t, func(*http.Request) (int, string) { return 500, "boom" })
		if _, err := download(context.Background(), "https://example.test/x", maxBinary); err == nil {
			t.Fatal("expected error on 500")
		}
	})
}

func TestUpdateAlreadyCurrent(t *testing.T) {
	fakeHTTP(t, func(*http.Request) (int, string) { return 200, `{"tag_name":"1.0.0"}` })
	var out strings.Builder
	got, err := Update(context.Background(), "o/r", "1.0.0", &out)
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.0.0" {
		t.Errorf("Update returned %q, want current 1.0.0", got)
	}
	if !strings.Contains(out.String(), "already up to date") {
		t.Errorf("expected 'already up to date' message, got %q", out.String())
	}
}

func TestUpdateLatestTagFails(t *testing.T) {
	fakeHTTP(t, func(*http.Request) (int, string) { return 404, "" })
	var out strings.Builder
	if _, err := Update(context.Background(), "o/r", "1.0.0", &out); err == nil {
		t.Fatal("expected error when the release lookup fails")
	}
}

func TestUpdateChecksumMismatch(t *testing.T) {
	// A newer release is available; the binary downloads but its .sha256 does not
	// match, so Update must abort before touching the running binary.
	asset := AssetName(runtime.GOOS, runtime.GOARCH)
	fakeHTTP(t, func(r *http.Request) (int, string) {
		u := r.URL.String()
		switch {
		case strings.Contains(u, "/releases/latest"):
			return 200, `{"tag_name":"9.9.9"}`
		case strings.HasSuffix(u, asset+".sha256"):
			return 200, "0000000000000000000000000000000000000000000000000000000000000000  " + asset
		case strings.HasSuffix(u, asset):
			return 200, "the-new-binary"
		}
		t.Fatalf("unexpected URL: %s", u)
		return 500, ""
	})
	var out strings.Builder
	_, err := Update(context.Background(), "o/r", "1.0.0", &out)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch error, got %v", err)
	}
}

func TestUpdateDownloadFails(t *testing.T) {
	// Newer release, but the asset download 404s → Update returns that error and
	// never reaches replaceSelf (so the test binary is untouched).
	fakeHTTP(t, func(r *http.Request) (int, string) {
		if strings.Contains(r.URL.String(), "/releases/latest") {
			return 200, `{"tag_name":"9.9.9"}`
		}
		return 404, "no such asset"
	})
	var out strings.Builder
	if _, err := Update(context.Background(), "o/r", "1.0.0", &out); err == nil {
		t.Fatal("expected download error")
	}
}

// TestUpdateWithoutChecksumRefusesToInstall is the regression test for a
// silently unverified install: a missing .sha256 asset used to skip verification
// entirely and write the downloaded bytes over the running binary. It must now
// fail closed, without ever reaching replaceSelf.
func TestUpdateWithoutChecksumRefusesToInstall(t *testing.T) {
	asset := AssetName(runtime.GOOS, runtime.GOARCH)
	for _, tc := range []struct {
		name, checksum string
		code           int
	}{
		{"missing checksum asset", "no such asset", 404},
		{"empty checksum", "", 200},
		{"truncated digest", "abc123  " + asset, 200},
		{"not a digest at all", "unavailable  " + asset, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeHTTP(t, func(r *http.Request) (int, string) {
				u := r.URL.String()
				switch {
				case strings.Contains(u, "/releases/latest"):
					return 200, `{"tag_name":"9.9.9"}`
				case strings.HasSuffix(u, asset+".sha256"):
					return tc.code, tc.checksum
				case strings.HasSuffix(u, asset):
					return 200, "the-new-binary"
				}
				t.Fatalf("unexpected URL: %s", u)
				return 500, ""
			})
			var out strings.Builder
			_, err := Update(context.Background(), "o/r", "1.0.0", &out)
			if err == nil {
				t.Fatal("an unverifiable binary must not be installed")
			}
			if !strings.Contains(err.Error(), "refusing to install") {
				t.Fatalf("error should say the install was refused, got %v", err)
			}
			if strings.Contains(out.String(), "✓ updated") {
				t.Fatalf("nothing should be reported as updated: %q", out.String())
			}
		})
	}
}

// An oversized body is refused rather than read into memory unbounded.
func TestDownloadRefusesOversizedBody(t *testing.T) {
	fakeHTTP(t, func(*http.Request) (int, string) { return 200, strings.Repeat("x", 64) })
	if _, err := download(context.Background(), "https://example.test/x", 16); err == nil {
		t.Fatal("a body past the limit should be an error, not a silent truncation")
	}
	// Exactly at the limit is fine.
	if _, err := download(context.Background(), "https://example.test/x", 64); err != nil {
		t.Fatalf("a body at the limit should be accepted: %v", err)
	}
}

func TestFirstField(t *testing.T) {
	cases := map[string]string{
		"abc  def":            "abc",
		"  leading trailing ": "leading",
		"only":                "only",
		"":                    "",
		"   ":                 "",
	}
	for in, want := range cases {
		if got := firstField(in); got != want {
			t.Errorf("firstField(%q) = %q, want %q", in, got, want)
		}
	}
}

// sha helper documents how a real .sha256 line would be built (kept for clarity;
// asserts the checksum encoding the code compares against).
func TestChecksumEncodingShape(t *testing.T) {
	sum := sha256.Sum256([]byte("hello"))
	line := hex.EncodeToString(sum[:]) + "  keel_linux_amd64"
	if firstField(line) != hex.EncodeToString(sum[:]) {
		t.Errorf("firstField should extract the hex digest from a sha256sum line")
	}
}
