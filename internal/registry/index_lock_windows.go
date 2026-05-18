//go:build windows

package registry

import "sync"

// withIndexLock falls back to a process-local mutex on Windows. Multi-
// process safety on Windows would need x/sys/windows LockFileEx; we
// don't ship that dep. Within a single canton-devkit process, this
// preserves the read-modify-write invariant.
var winIndexMu sync.Mutex

func withIndexLock(fn func() error) error {
	winIndexMu.Lock()
	defer winIndexMu.Unlock()
	return fn()
}
