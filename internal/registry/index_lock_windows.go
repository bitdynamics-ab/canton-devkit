//go:build windows

package registry

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// withIndexLock serialises the read-modify-write of index.json across
// processes on Windows — the counterpart to the blocking flock in
// index_lock_unix.go. It takes a process-wide cross-process lock on
// .index.lock via windows.LockFileEx (LOCKFILE_EXCLUSIVE_LOCK, *without*
// FAIL_IMMEDIATELY, so it blocks like the Unix LOCK_EX), runs fn, then
// releases.
//
// This replaces the previous sync.Mutex stub, which protected only
// within a single canton-devkit process and let the long-running
// `localnet ui` reconciler (which rewrites index.json every ~15s) race
// concurrent CLI commands and lose entries. golang.org/x/sys/windows is
// already a direct dependency, so the historical "stay zero-dep"
// justification no longer applies.
func withIndexLock(fn func() error) error {
	if err := os.MkdirAll(Root(), 0o755); err != nil {
		return fmt.Errorf("mkdir registry root: %w", err)
	}
	path := filepath.Join(Root(), ".index.lock")

	h, err := openLockHandle(path)
	if err != nil {
		return fmt.Errorf("open index lock: %w", err)
	}
	defer func() { _ = windows.CloseHandle(h) }()

	var ol windows.Overlapped
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK,
		0, lockBytesLow, lockBytesHigh, &ol); err != nil {
		return fmt.Errorf("lock index: %w", err)
	}
	defer func() {
		var ol windows.Overlapped
		_ = windows.UnlockFileEx(h, 0, lockBytesLow, lockBytesHigh, &ol)
	}()

	return fn()
}
