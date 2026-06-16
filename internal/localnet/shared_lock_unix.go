//go:build unix

package localnet

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockSharedStack acquires an exclusive advisory lock on the shared
// observability stack so the register+ensure and deregister+teardown
// critical sections serialize ACROSS instances. The per-instance
// registry.Lock does NOT cover this: the shared stack is one host-level
// resource, so without a dedicated lock a `up` of instance C could
// register + EnsureSharedStack at the same moment the last other
// instance's `down` observes a zero refcount and tears the stack down,
// leaving C registered against a stopped stack.
//
// Unlike registry.Lock (LOCK_NB, fail-fast) this lock BLOCKS (LOCK_EX):
// the critical sections are short (a target-file write + an idempotent
// compose up/stop) and a concurrent up/down of a DIFFERENT instance
// should wait its turn rather than error out. flock is released by the
// OS on fd close or process exit, so a crashed holder never wedges it.
//
// On Windows this is a no-op (see shared_lock_windows.go), matching
// registry.Lock's platform split — LocalNet on Windows runs under Docker
// Desktop where concurrent multi-instance up/down is rare in dev use.
func lockSharedStack() (release func(), err error) {
	if err := os.MkdirAll(SharedObservabilityRoot(), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir shared observability root: %w", err)
	}
	path := filepath.Join(SharedObservabilityRoot(), ".lock")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open shared stack lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("flock shared stack lock %s: %w", path, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
