package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/coullworks/keel/internal/profile"
)

// checkEvery is how often keel asks GitHub what the latest release is. A day is
// the same order as apt, brew and gh: often enough that a release is noticed
// within a working day, rare enough that it is not a request per command.
const checkEvery = 24 * time.Hour

// DisableEnv turns the whole thing off. Named so it reads in a Dockerfile or a
// CI job, where a network call on every invocation is pure cost.
const DisableEnv = "KEEL_NO_UPDATE_CHECK"

// cache is the record of the last check.
type cache struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
}

func cachePath() string { return filepath.Join(profile.Dir(), "update-check.json") }

// Notice returns a one-line upgrade message, or "" when there is nothing to say.
//
// It never blocks and never fails a command. The rule is: answer from the cache
// now, refresh it in the background for next time. A first run therefore says
// nothing, which is the right trade - a version notice is not worth making
// every command wait on a network round trip, and a broken or slow network must
// not be able to delay `keel new` at all.
//
// This is why the refresh writes the cache rather than returning: whatever it
// learns is for the next invocation.
func Notice(repo, current string) string {
	if os.Getenv(DisableEnv) != "" {
		return ""
	}
	c, err := readCache()
	stale := err != nil || time.Since(c.CheckedAt) > checkEvery
	if stale {
		go refresh(repo) // for next time; this run says whatever the cache already knew
	}
	if c.Latest == "" || !Newer(current, c.Latest) {
		return ""
	}
	return fmt.Sprintf("keel %s is available (you have %s). Run `keel self-update`.", c.Latest, current)
}

// refresh asks for the latest tag and records it. Every failure is silent on
// purpose: not knowing the latest version is not a problem worth telling anyone
// about, and this runs while the user is doing something else.
func refresh(repo string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	latest, err := latestTag(ctx, repo)
	if err != nil {
		// Still record the attempt, so a machine with no network does not retry
		// on every single command.
		_ = writeCache(cache{CheckedAt: time.Now()})
		return
	}
	_ = writeCache(cache{CheckedAt: time.Now(), Latest: latest})
}

func readCache() (cache, error) {
	var c cache
	b, err := os.ReadFile(cachePath())
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	return c, nil
}

// writeCache writes the cache atomically: a plain WriteFile from the
// fire-and-forget refresh goroutine could interleave with a concurrent read (or
// another refresh) and leave a half-written, unparseable file. Writing to a
// unique temp file and renaming into place means a reader always sees either the
// old file or the new one, never a partial one.
func writeCache(c cache) error {
	dir := filepath.Dir(cachePath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "update-check-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, cachePath()); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// ErrNoCache reports that no check has been recorded yet.
var ErrNoCache = errors.New("no update check recorded")
