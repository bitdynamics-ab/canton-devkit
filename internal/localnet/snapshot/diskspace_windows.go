//go:build windows

package snapshot

import (
	"golang.org/x/sys/windows"
)

// availableDiskBytes returns the bytes available on the volume
// containing dir on Windows via GetDiskFreeSpaceEx.
func availableDiskBytes(dir string) (uint64, error) {
	var free uint64
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	if err := windows.GetDiskFreeSpaceEx(p, &free, nil, nil); err != nil {
		return 0, err
	}
	return free, nil
}
