//go:build unix

package filelock

import (
	"os"
	"syscall"
)

// acquire opens (creating if needed) the lock file and takes an exclusive flock,
// blocking until it is available. The returned func releases the lock and closes
// the file. flock is associated with the open file description and is dropped by
// the kernel when the process exits, so a crash never leaves a stale lock.
func acquire(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
