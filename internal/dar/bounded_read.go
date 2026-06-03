package dar

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// MaxDARBytes caps a single DAR file read at 512 MiB. Realistic
// DARs are 100 KB - 50 MiB (sdk-config + dependencies); 512 MiB is
// 10× headroom above the largest reasonable real-world DAR while
// still small enough to protect against the OOM-on-read class of
// bug PR #37 closed for Zip Slip (BIT-127 review feedback).
//
// Exceeding this returns ErrDARTooLarge — caller surfaces it to
// the user with a friendly remediation pointing at the file size.
const MaxDARBytes int64 = 512 * 1024 * 1024

// ErrDARTooLarge is returned by ReadDARFile when the input
// exceeds MaxDARBytes. Distinct error so callers can switch on
// it and render the size-mismatch remediation instead of a
// generic IO failure.
var ErrDARTooLarge = errors.New("DAR file exceeds maximum size cap")

// ReadDARFile reads path into memory with the MaxDARBytes ceiling.
// Stat-first so we refuse oversize files BEFORE allocating their
// buffer (avoids the foot-gun where a 4 GiB file briefly succeeds
// in os.ReadFile on a 16 GiB host, only to OOM somewhere later).
//
// Replaces every os.ReadFile call site in internal/dar/ and
// internal/cli/localnet/dar/ — single read primitive, single
// place to revisit if the cap ever needs to move.
func ReadDARFile(path string) ([]byte, error) {
	return readWithCap(path, MaxDARBytes)
}

// readWithCap is the parameterised variant ReadDARFile delegates
// to. Exported package-private so tests can pin behavior with a
// tiny cap without materializing a 512 MiB fixture on disk.
func readWithCap(path string, cap int64) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if st.Size() > cap {
		return nil, fmt.Errorf("%w: %s is %d bytes (cap %d)",
			ErrDARTooLarge, path, st.Size(), cap)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	// LimitReader is belt-and-braces: even if Stat reported a
	// smaller size and the file grew between stat and read (TOCTOU),
	// we still cap at cap.
	return io.ReadAll(io.LimitReader(f, cap))
}
