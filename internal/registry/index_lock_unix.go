//go:build unix

package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// withIndexLock blocks (LOCK_EX, *blocking* — unlike per-instance Lock)
// until exclusive access to index.json is granted. Used to make the
// read-modify-write of the index safe across goroutines and across
// canton-devkit processes.
//
// LOCK_EX without LOCK_NB is intentional here: contention on the index
// is brief (a sub-millisecond rewrite), so blocking is the right wait
// pattern. Per-instance Lock() uses NB because that contention reflects
// a real user error (concurrent up on same instance).
func withIndexLock(fn func() error) error {
	if err := os.MkdirAll(Root(), 0o755); err != nil {
		return fmt.Errorf("mkdir registry root: %w", err)
	}
	path := filepath.Join(Root(), ".index.lock")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open index lock: %w", err)
	}
	defer func() { _ = f.Close() }()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("flock index: %w", err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	return fn()
}
