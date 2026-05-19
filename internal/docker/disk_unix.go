//go:build unix

package docker

import (
	"os"
	"path/filepath"
	"syscall"
)

// availableDiskBytes returns the bytes available to the unprivileged user on
// the filesystem holding path. If path does not yet exist, the nearest
// existing ancestor is used so the caller doesn't have to mkdir first.
func availableDiskBytes(path string) (uint64, error) {
	target := path
	for {
		if _, err := os.Stat(target); err == nil {
			break
		}
		parent := filepath.Dir(target)
		if parent == target {
			break
		}
		target = parent
	}

	var stat syscall.Statfs_t
	if err := syscall.Statfs(target, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}
