//go:build windows

package registry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// Lock acquires an exclusive lock on the per-instance lock file, blocking
// concurrent `localnet up`/`down` against the same instance from the same
// host — the Windows counterpart to the Unix flock in lock_unix.go.
//
// We use windows.LockFileEx with LOCKFILE_EXCLUSIVE_LOCK |
// LOCKFILE_FAIL_IMMEDIATELY (the non-blocking variant) so the CLI prints
// a clean "instance is busy" message instead of hanging, matching the
// LOCK_EX|LOCK_NB behaviour on Unix. The OS releases the lock byte-range
// automatically when the handle is closed or the process exits, so there
// is no stale-lock file to recover after a crash.
//
// The lock file lives at .locks/<name>.lock under the registry root
// (outside the instance data dir) so Delete can RemoveAll the data dir
// while this lock is still held — Windows cannot unlink an open file.
func Lock(name string) (release func(), err error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	path := LockPathFor(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir for lock: %w", err)
	}

	h, err := openLockHandle(path)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", path, err)
	}

	var ol windows.Overlapped
	if err := windows.LockFileEx(h,
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, lockBytesLow, lockBytesHigh, &ol); err != nil {
		_ = windows.CloseHandle(h)
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
			return nil, fmt.Errorf("instance %q is busy (another canton-devkit op holds the lock at %s)",
				name, path)
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}

	return func() {
		var ol windows.Overlapped
		_ = windows.UnlockFileEx(h, 0, lockBytesLow, lockBytesHigh, &ol)
		_ = windows.CloseHandle(h)
	}, nil
}

// lockBytesLow / lockBytesHigh describe the byte range to lock. We lock a
// single sentinel byte (range [0,1)) rather than the whole file — the
// file is empty and used only as a lock token, so one byte is enough and
// avoids overflow math on the high dword.
const (
	lockBytesLow  = 1
	lockBytesHigh = 0
)

// openLockHandle opens (creating if needed) the lock file with sharing
// flags that let other processes open the same file — the exclusion is
// enforced by LockFileEx on the byte range, not by the open itself.
func openLockHandle(path string) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	return windows.CreateFile(p,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL,
		0)
}
