//go:build windows

package registry

import (
	"fmt"
	"os"
)

// Lock is a no-op on Windows. We accept this trade-off: Windows users
// running two concurrent `localnet up`s against the same name will see
// the docker-compose error directly. Adding real locking would require a
// CreateFileW + LockFileEx dance via x/sys/windows; out of scope until
// someone hits the issue on Windows.
//
// We emit a one-line stderr warning so a user who DOES race two ups
// sees something better than a bare compose-side error — they
// understand DevKit isn't protecting against the collision and that
// the behaviour is documented.
func Lock(name string) (release func(), err error) {
	_, _ = fmt.Fprintf(os.Stderr,
		"warning: registry locking is a no-op on Windows; concurrent "+
			"`localnet up --name %s` may race. See docs/limitations.md.\n",
		name)
	return func() {}, nil
}
