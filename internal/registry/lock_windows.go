//go:build windows

package registry

// Lock is a no-op on Windows. We accept this trade-off: Windows users
// running two concurrent `localnet up`s against the same name will see
// the docker-compose error directly. Adding real locking would require a
// CreateFileW + LockFileEx dance via x/sys/windows; out of scope until
// someone hits the issue on Windows.
func Lock(name string) (release func(), err error) {
	return func() {}, nil
}
