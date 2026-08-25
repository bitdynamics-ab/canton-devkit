//go:build unix

package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Lock acquires an exclusive advisory lock on the per-instance lock file,
// blocking concurrent `localnet up`/`down` against the same instance from
// the same host. The returned release function must be called via defer.
// The Windows counterpart (LockFileEx) lives in lock_windows.go.
//
// The lock file lives at .locks/<name>.lock under the registry root
// (outside the instance data dir) so Delete can RemoveAll the data dir
// while this lock is still held.
func Lock(name string) (release func(), err error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	path := LockPathFor(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir for lock: %w", err)
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", path, err)
	}

	// LOCK_EX | LOCK_NB — fail immediately rather than block, so the
	// CLI prints a clean "another op in progress" message instead of
	// hanging.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, fmt.Errorf("instance %q is busy (another canton-devkit op holds the lock at %s)",
				name, path)
		}
		return nil, fmt.Errorf("flock %s: %w", path, err)
	}

	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
