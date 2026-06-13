//go:build windows

package localnet

// lockSharedStack is a no-op on Windows, matching registry.Lock's
// platform split. Cross-platform robust file locking would require
// golang.org/x/sys/windows; the project keeps the shared-stack lock
// zero-dep, and on Windows LocalNet runs under Docker Desktop where
// concurrent multi-instance up/down (the race this lock guards on Unix)
// is rare in dev use. See shared_lock_unix.go for the real lock.
func lockSharedStack() (release func(), err error) {
	return func() {}, nil
}
