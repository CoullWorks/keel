//go:build !unix

package filelock

// acquire is a no-op on platforms without flock. keel's primary targets are
// Linux, macOS and WSL (all unix); elsewhere concurrent CLI+studio mutation
// degrades to last-writer-wins, which the atomic write still keeps un-corrupted.
func acquire(path string) (func(), error) {
	return func() {}, nil
}
