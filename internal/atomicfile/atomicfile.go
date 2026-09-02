// Package atomicfile writes a file so a reader never sees a half-written one.
//
// A plain os.WriteFile truncates the target and then streams the new bytes, so a
// crash, a full disk, or a concurrent reader in the window between the two sees a
// truncated, unparseable file. keel's small state files — the project manifest,
// the plugin index, the pack registry, the credentials store — are read by every
// command, and a truncated one of those bricks the project (see engine's
// ErrManifestMalformed path). This package writes to a temp file in the same
// directory and renames it over the target: rename is atomic on a POSIX
// filesystem, so a reader sees either the whole old file or the whole new one,
// never a mix, and a crash mid-write leaves the intact old file plus a stray temp.
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteFile writes data to path atomically with the given permissions. It creates
// the temp file in path's own directory (so the final rename stays on one
// filesystem — a cross-device rename is not atomic and would fail), fsyncs it so
// the bytes are durable before the rename, and cleans up the temp file on any
// error so a failed write leaves nothing behind. The caller is responsible for
// creating the parent directory.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	// The caller supplies the full target path; normalise it and refuse a
	// traversal so both the temp file and the final rename stay at that path.
	p, err := filepath.Abs(path)
	if err != nil || strings.Contains(p, "..") {
		return fmt.Errorf("refusing path outside %q", path)
	}
	path = p
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	// From here, any failure must remove the temp file rather than leave litter.
	cleanup := func() { _ = f.Close(); _ = os.Remove(tmp) }

	if _, err := f.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := f.Chmod(perm); err != nil {
		cleanup()
		return err
	}
	// fsync before rename: without it a crash after the rename but before the
	// data reached disk could leave the renamed file empty on some filesystems.
	if err := f.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("atomic rename %s: %w", path, err)
	}
	return nil
}
