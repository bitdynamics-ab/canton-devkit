package dar

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
)

// MaxDARBytes caps a single DAR file read at 512 MiB. Realistic
// DARs are 100 KB - 50 MiB (sdk-config + dependencies); 512 MiB is
// 10× headroom above the largest reasonable real-world DAR while
// still small enough to prevent OOM-on-read. Exceeding this returns
// ErrDARTooLarge.
const MaxDARBytes int64 = 512 * 1024 * 1024

// MaxEntryDecompressedBytes caps how many bytes a single zip entry
// (a .dalf, or MANIFEST.MF) may decompress to. The compressed file
// is already bounded by MaxDARBytes, but DEFLATE compresses up to
// ~1032:1, so a few-MiB archive can expand a single entry to many
// GiB and OOM `dar info`/`dar diff` — the exact failure mode the
// compressed cap was built to prevent. The largest real Splice .dalf
// is tens of MiB; 512 MiB is ample headroom while keeping any one
// entry within the same ceiling as the whole compressed archive.
const MaxEntryDecompressedBytes int64 = 512 * 1024 * 1024

// MaxTotalDecompressedBytes caps the sum of all entries decompressed
// from one DAR. A DAR is mostly already-compressed .dalf protobuf
// (which barely shrinks) plus a tiny manifest, so the decompressed
// total tracks the compressed size closely; 2 GiB is generous for
// even a large vendored bundle while still bounding a many-entry
// bomb that stays under the per-entry cap.
const MaxTotalDecompressedBytes int64 = 2 * 1024 * 1024 * 1024

// ErrDARTooLarge is returned by ReadDARFile when the input
// exceeds MaxDARBytes. Distinct error so callers can switch on
// it and render the size-mismatch remediation instead of a
// generic IO failure.
var ErrDARTooLarge = errors.New("DAR file exceeds maximum size cap")

// ErrDARBomb is returned when a zip entry decompresses past the
// per-entry or cumulative decompressed ceiling — a compression-ratio
// (zip-bomb) attack that the compressed-size cap alone can't catch.
// Distinct error so callers can tell "file too big on disk" apart
// from "file lies about how big it expands to".
var ErrDARBomb = errors.New("DAR decompresses past the decompressed-size cap (possible zip bomb)")

// ReadDARFile reads path into memory with the MaxDARBytes ceiling.
// Stat-first so oversize files are refused BEFORE allocating their
// buffer. It is the single read primitive for every DAR read in the
// codebase, so the cap lives in one place.
func ReadDARFile(path string) ([]byte, error) {
	return readWithCap(path, MaxDARBytes)
}

// readWithCap is the parameterised variant ReadDARFile delegates to,
// kept separate so tests can pin behavior with a tiny cap without
// materializing a 512 MiB fixture on disk.
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

// readZipEntry decompresses one zip entry into memory under a hard
// decompressed-size ceiling, defeating the compression-ratio (zip
// bomb) class that the compressed-file cap in ReadDARFile cannot see.
//
// Two layers, mirroring readWithCap's stat-then-LimitReader pattern:
//
//  1. The central-directory UncompressedSize64 is checked up front so
//     a declared-huge entry is rejected before a single byte is
//     decompressed (cheap, no allocation).
//  2. rc is wrapped in io.LimitReader(rc, cap+1) as the TOCTOU
//     belt-and-braces — a lying header that under-reports the size
//     still can't stream more than cap+1 bytes, and reading exactly
//     cap+1 is the tell that the entry overflowed.
//
// Returns ErrDARBomb on either trip so callers surface the bomb
// distinctly from a generic IO error.
func readZipEntry(f *zip.File, cap int64) ([]byte, error) {
	if cap < 0 {
		cap = 0
	}
	if int64(f.UncompressedSize64) > cap {
		return nil, fmt.Errorf("%w: entry %q declares %d bytes (per-entry cap %d)",
			ErrDARBomb, f.Name, f.UncompressedSize64, cap)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, cap+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > cap {
		return nil, fmt.Errorf("%w: entry %q decompressed past the per-entry cap %d",
			ErrDARBomb, f.Name, cap)
	}
	return data, nil
}
