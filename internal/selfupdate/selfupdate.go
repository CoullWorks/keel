// Package selfupdate lets keel replace its own binary from the latest GitHub
// release — the baseline every modern Go CLI ships. Asset naming mirrors
// install.sh / the release workflow (keel_<os>_<arch>).
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// repoPattern is the owner/name shape we allow before interpolating repo into a
// GitHub URL. Restricting it to the characters GitHub actually permits closes a
// request-forgery vector: a repo like "x/y/../../evil" or one carrying a query
// string could otherwise redirect the release lookup or the binary download.
var repoPattern = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)

// validateRepo rejects a repo that is not a plain owner/name.
func validateRepo(repo string) error {
	if !repoPattern.MatchString(repo) {
		return fmt.Errorf("invalid repository %q: want owner/name", repo)
	}
	return nil
}

// AssetName is the release asset for a platform, e.g. keel_linux_amd64.
func AssetName(goos, goarch string) string { return "keel_" + goos + "_" + goarch }

// Newer reports whether latest is a newer release than current. A "-dev" current
// build is always considered older (so devs can jump to a real release). Unknown
// formats fall back to a plain string inequality.
func Newer(current, latest string) bool {
	c, l := norm(current), norm(latest)
	if c == l {
		return false
	}
	// A dev build (…-dev suffix) is always older than a real release, so devs can
	// jump to one. Match the suffix, not a bare "dev" substring — "1.2.0-devil" or
	// a repo literally named "…dev…" must not be misread as a dev build.
	if strings.HasSuffix(c, "-dev") {
		return true
	}
	cp, cok := parseVer(c)
	lp, lok := parseVer(l)
	if !cok || !lok {
		return l != c // best effort
	}
	for i := 0; i < 3; i++ {
		if lp[i] != cp[i] {
			return lp[i] > cp[i]
		}
	}
	return false
}

func norm(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }

func parseVer(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.SplitN(v, "-", 2)[0] // drop pre-release suffix
	fields := strings.Split(parts, ".")
	if len(fields) == 0 || len(fields) > 3 {
		return out, false
	}
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

type release struct {
	TagName string `json:"tag_name"`
}

// Update checks repo's latest release and, if newer than currentVersion, replaces
// the running binary. Returns the version installed (or current if up to date).
func Update(ctx context.Context, repo, currentVersion string, out io.Writer) (string, error) {
	if err := validateRepo(repo); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	latest, err := latestTag(ctx, repo)
	if err != nil {
		return "", err
	}
	if !Newer(currentVersion, latest) {
		fmt.Fprintf(out, "already up to date (%s)\n", currentVersion)
		return currentVersion, nil
	}
	fmt.Fprintf(out, "updating %s → %s …\n", currentVersion, latest)

	asset := AssetName(runtime.GOOS, runtime.GOARCH)
	base := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, latest, asset)
	bin, err := download(ctx, base, maxBinary)
	if err != nil {
		return "", err
	}
	if err := verifyChecksum(ctx, base, asset, bin); err != nil {
		return "", err
	}
	if err := replaceSelf(bin); err != nil {
		return "", err
	}
	fmt.Fprintf(out, "✓ updated to %s\n", latest)
	return latest, nil
}

// verifyChecksum requires a published SHA-256 for the asset and requires it to
// match. A missing or unreadable checksum is a hard failure: this code installs
// a downloaded binary over the running one, and skipping the check on a 404 (as
// it once did) means a bad or substituted asset gets installed silently.
//
// The checksum and the binary come from the same host, so this proves integrity,
// not authenticity: it catches truncation, corruption and a swapped asset, but a
// compromised release host could publish a matching pair. Signing the release
// with a key baked in here is the next step.
func verifyChecksum(ctx context.Context, base, asset string, bin []byte) error {
	want, err := download(ctx, base+".sha256", maxChecksum)
	if err != nil {
		return fmt.Errorf("no published checksum for %s, refusing to install: %w", asset, err)
	}
	exp := firstField(string(want))
	if len(exp) != sha256.Size*2 {
		return fmt.Errorf("checksum for %s is malformed, refusing to install", asset)
	}
	got := sha256.Sum256(bin)
	if !strings.EqualFold(exp, hex.EncodeToString(got[:])) {
		return fmt.Errorf("checksum mismatch for %s, refusing to install", asset)
	}
	return nil
}

// Response size caps. A hostile or broken server should not be able to make keel
// read an unbounded body into memory.
const (
	maxRelease  = 1 << 20   // release JSON
	maxChecksum = 1 << 12   // one "<hex>  <name>" line
	maxBinary   = 256 << 20 // keel's own binary, with generous headroom
)

// client carries an explicit timeout: http.DefaultClient has none, so a server
// that accepts the connection and never answers would hang the update forever.
var client = &http.Client{Timeout: 60 * time.Second}

// apiBase is a test seam. Without it the only way to exercise the failure path
// is to ask the real api.github.com about a repository that does not exist,
// which makes a unit test depend on the network being up, on DNS, and on not
// having spent the hour's unauthenticated rate limit - and it failed for
// exactly that reason during a full-suite run.
var apiBase = "https://api.github.com"

func latestTag(ctx context.Context, repo string) (string, error) {
	if err := validateRepo(repo); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", apiBase+"/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", fmt.Errorf("no releases yet (GitHub returned %d)", res.StatusCode)
	}
	var r release
	if err := json.NewDecoder(io.LimitReader(res.Body, maxRelease)).Decode(&r); err != nil {
		return "", err
	}
	if r.TagName == "" {
		return "", fmt.Errorf("latest release has no tag")
	}
	return r.TagName, nil
}

func download(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("download %s: %d", url, res.StatusCode)
	}
	// Read one byte past the cap so an oversized body is an error rather than a
	// silently truncated (and then checksum-failing) download.
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("download %s: larger than the %d byte limit", url, limit)
	}
	return b, nil
}

// replaceSelf swaps the running binary with new content and rolls back on
// failure, so a mid-step error can never leave the user with no binary.
//
// The steps: write the new bytes beside the current binary (same filesystem, so
// the final rename is atomic), move the current binary aside to exe.old, then
// rename the new file into place. If the final step fails, exe.old is restored.
// On success exe.old is removed. Windows cannot rename over or delete a running
// executable, so moving the running binary aside first (which Windows *does*
// allow) is also what makes the swap work there.
func replaceSelf(bin []byte) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Resolve symlinks so we replace the real file, not a link — but only when it
	// succeeds. A failed EvalSymlinks previously blanked exe, turning the writes
	// below into WriteFile(".new") / Rename into "" and corrupting the swap.
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	return swapBinary(exe, bin)
}

// swapBinary replaces the file at exe with bin and rolls back on failure. It is
// separated from replaceSelf (which resolves the running executable's path) so
// the swap-and-rollback logic can be tested against a temp file without touching
// the real binary.
func swapBinary(exe string, bin []byte) error {
	tmp := exe + ".new"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return err
	}

	old := exe + ".old"
	_ = os.Remove(old) // clear any leftover from a previous interrupted update
	if err := os.Rename(exe, old); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("move current binary aside: %w", err)
	}
	if err := os.Rename(tmp, exe); err != nil {
		// Roll back: put the original binary back so the user is never left without one.
		if rbErr := os.Rename(old, exe); rbErr != nil {
			return fmt.Errorf("install failed (%v) and rollback failed (%v); "+
				"the previous binary is at %s", err, rbErr, old)
		}
		os.Remove(tmp)
		return fmt.Errorf("install new binary: %w", err)
	}
	// Success. Remove the old binary; on Windows it may still be locked while the
	// process runs, so a failure here is not fatal — it is left for the OS/next run.
	_ = os.Remove(old)
	return nil
}

func firstField(s string) string {
	for _, f := range strings.Fields(s) {
		return f
	}
	return ""
}
