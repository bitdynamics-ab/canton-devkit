//go:build !windows

package localnet

import (
	"golang.org/x/sys/unix"
)

// availableDiskBytes returns the bytes available on the filesystem
// containing dir. Used by RunRestore's preflight (PR #37 #7a) to
// refuse a restore that would fill the disk. statfs is the standard
// POSIX path; any error here is treated as "preflight unavailable"
// by the caller, which proceeds without the gate.
func availableDiskBytes(dir string) (uint64, error) {
	var s unix.Statfs_t
	if err := unix.Statfs(dir, &s); err != nil {
		return 0, err
	}
	// Bavail (blocks available to unprivileged) × block size.
	return uint64(s.Bavail) * uint64(s.Bsize), nil
}
