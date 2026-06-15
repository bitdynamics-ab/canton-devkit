package dar

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"
)

// zipWithEntry builds an in-memory zip containing a single entry of
// `size` highly-compressible bytes (all zeros), returning the archive
// bytes and the opened *zip.File for that entry. Zeros DEFLATE at
// roughly 1000:1, so this is a faithful miniature of the zip-bomb
// shape: a tiny compressed blob whose declared UncompressedSize64 is
// large. The compressed-file cap in ReadDARFile can't see that ratio;
// readZipEntry's decompressed cap is what catches it.
func zipWithEntry(t *testing.T, name string, size int) ([]byte, *zip.File) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := w.Write(make([]byte, size)); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	raw := buf.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("zip open: %v", err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			return raw, f
		}
	}
	t.Fatalf("entry %q not found in built zip", name)
	return nil, nil
}

// TestReadZipEntry_RejectsOverCapEntry pins the per-entry
// decompressed ceiling: an entry that decompresses past the cap is
// refused with ErrDARBomb even though its compressed footprint is
// trivial. This is the defence the compressed-file cap can't provide.
func TestReadZipEntry_RejectsOverCapEntry(t *testing.T) {
	raw, f := zipWithEntry(t, "bomb.dalf", 4<<20) // 4 MiB of zeros, ~4 KiB on disk
	if int64(len(raw)) >= 4<<20 {
		t.Fatalf("fixture did not compress (raw=%d bytes) — test premise broken", len(raw))
	}
	_, err := readZipEntry(f, 64<<10) // 64 KiB cap, far below 4 MiB
	if err == nil {
		t.Fatal("expected ErrDARBomb for an entry that decompresses past the cap, got nil")
	}
	if !errors.Is(err, ErrDARBomb) {
		t.Errorf("got %v, want ErrDARBomb", err)
	}
}

// TestReadZipEntry_AllowsUnderCapEntry is the symmetric pin: an entry
// within the cap decompresses successfully and returns the exact
// bytes. Guards against the cap being so aggressive it rejects normal
// .dalf files.
func TestReadZipEntry_AllowsUnderCapEntry(t *testing.T) {
	_, f := zipWithEntry(t, "ok.dalf", 4096)
	data, err := readZipEntry(f, 64<<10)
	if err != nil {
		t.Fatalf("under-cap read: %v", err)
	}
	if len(data) != 4096 {
		t.Errorf("got %d bytes, want 4096", len(data))
	}
}

// TestReadZipEntry_AtCapBoundary pins the off-by-one: an entry whose
// decompressed size is exactly the cap is allowed; one byte over is
// rejected. The LimitReader is sized cap+1 precisely so the boundary
// is exact.
func TestReadZipEntry_AtCapBoundary(t *testing.T) {
	_, f := zipWithEntry(t, "edge.dalf", 8192)
	if _, err := readZipEntry(f, 8192); err != nil {
		t.Errorf("exactly-at-cap read should succeed, got %v", err)
	}
	if _, err := readZipEntry(f, 8191); !errors.Is(err, ErrDARBomb) {
		t.Errorf("one-byte-over read should be ErrDARBomb, got %v", err)
	}
}

// TestReadZipEntry_RejectsCumulativeOverflow pins the CUMULATIVE
// decompressed cap, the second half of the zip-bomb defence that the
// per-entry cap alone misses: many entries that each fit under
// MaxEntryDecompressedBytes but together exceed MaxTotalDecompressedBytes.
//
// decodeDARBytes walks entries debiting a `remaining` budget seeded at
// MaxTotalDecompressedBytes and clamps each entry's ceiling to
// entryCap(remaining). This test drives that exact budget/clamp/read
// loop over a fixture whose entries individually clear the per-entry
// cap but sum past the total, and asserts the entry that overruns the
// running budget is rejected with ErrDARBomb — never silently admitted
// just because it would have fit the per-entry cap in isolation.
func TestReadZipEntry_RejectsCumulativeOverflow(t *testing.T) {
	// Each entry decompresses to entrySize; entrySize is comfortably
	// under the per-entry cap, but two entries already blow the total
	// budget we run the loop with.
	const entrySize = 6 << 20 // 6 MiB of zeros, ~6 KiB on disk
	if int64(entrySize) >= MaxEntryDecompressedBytes {
		t.Fatalf("entrySize %d must stay under the per-entry cap %d — test premise broken",
			entrySize, MaxEntryDecompressedBytes)
	}

	// Run the cumulative loop with a small total budget so two
	// under-per-entry-cap entries already exhaust it. The clamping
	// logic (entryCap) and the rejection (readZipEntry) are the real
	// production symbols decodeDARBytes uses; only the seed budget is
	// shrunk so we needn't materialise a 2 GiB fixture on disk.
	totalBudget := int64(entrySize + entrySize/2) // first entry fits, second overruns

	_, first := zipWithEntry(t, "first.dalf", entrySize)
	_, second := zipWithEntry(t, "second.dalf", entrySize)

	remaining := totalBudget

	// First entry: fits the clamped budget, succeeds, debits the total.
	got, err := readZipEntry(first, entryCap(remaining))
	if err != nil {
		t.Fatalf("first entry should fit the cumulative budget, got %v", err)
	}
	remaining -= int64(len(got))
	if remaining >= int64(entrySize) {
		t.Fatalf("budget %d still admits a full second entry — fixture sizing broken", remaining)
	}

	// Second entry: individually under the per-entry cap, but the
	// running budget is now spent, so entryCap clamps below its size
	// and readZipEntry rejects it as a bomb.
	if _, err := readZipEntry(second, entryCap(remaining)); !errors.Is(err, ErrDARBomb) {
		t.Errorf("second entry overruns the cumulative budget; got %v, want ErrDARBomb", err)
	}
}

// TestEntryCap_ClampsToRemainingBudget pins the clamp helper directly:
// entryCap never exceeds the per-entry cap, tracks a dwindling budget
// once it drops below that cap, and never returns negative even if the
// budget has gone past zero (a debit that overshot). This is the gate
// that converts a spent cumulative budget into a per-entry rejection.
func TestEntryCap_ClampsToRemainingBudget(t *testing.T) {
	if got := entryCap(MaxTotalDecompressedBytes); got != MaxEntryDecompressedBytes {
		t.Errorf("ample budget should clamp to the per-entry cap %d, got %d",
			MaxEntryDecompressedBytes, got)
	}
	if got := entryCap(1024); got != 1024 {
		t.Errorf("budget below the per-entry cap should clamp to the budget, got %d", got)
	}
	if got := entryCap(0); got != 0 {
		t.Errorf("exhausted budget should clamp to 0, got %d", got)
	}
	if got := entryCap(-1); got != 0 {
		t.Errorf("overshot budget must never go negative, got %d", got)
	}
}

// TestParseManifest_RejectsTooManyLines guards the manifest logical-
// line cap. A MANIFEST.MF made of millions of short header lines would
// otherwise grow the in-memory slice without bound (the scanner buffer
// only caps a single token). Builds a manifest just over the cap and
// asserts it is refused.
func TestParseManifest_RejectsTooManyLines(t *testing.T) {
	var b strings.Builder
	// A valid leading header so we don't trip the missing-Name check
	// first; then flood with distinct non-continuation lines.
	b.WriteString("Manifest-Version: 1.0\n")
	for i := 0; i < maxManifestLogicalLines+10; i++ {
		b.WriteString("X-Pad-")
		b.WriteString(itoa(i))
		b.WriteString(": y\n")
	}
	_, err := parseManifest(strings.NewReader(b.String()))
	if err == nil {
		t.Fatal("expected error for manifest exceeding the logical-line cap, got nil")
	}
	if !strings.Contains(err.Error(), "header lines") {
		t.Errorf("error %q should mention the header-line cap", err)
	}
}
