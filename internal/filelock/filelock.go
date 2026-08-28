// Package filelock provides a cross-process advisory lock so two keel processes
// (e.g. the CLI and a running studio, both explicitly supported) cannot lose each
// other's updates to a shared state file. Atomic writes stop a half-written file;
// this stops a lost update — the classic read-modify-write race where two writers
// read the same base and the second save silently discards the first's change.
//
// On Unix it uses flock(2), which the kernel releases automatically when the
// holding process exits, so there is no stale-lock problem even on a crash. On a
// platform without flock it degrades to a no-op (the operation still runs, falling
// back to last-writer-wins) rather than failing — a lock is a safety net, never a
// gate that could brick a command.
package filelock

// With runs fn while holding an exclusive advisory lock on lockPath (created if
// absent). If the lock cannot be acquired for any reason, fn still runs unlocked —
// serialization is best-effort and must never prevent the underlying operation.
func With(lockPath string, fn func() error) error {
	unlock, err := acquire(lockPath)
	if err != nil {
		return fn() // degrade to unlocked rather than block the operation
	}
	defer unlock()
	return fn()
}
