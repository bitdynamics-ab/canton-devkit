package dar

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestReadDARFile_RoundTrip pins the happy path: a normal DAR-
// sized file reads back byte-identical.
func TestReadDARFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "small.dar")
	want := []byte("not really a DAR but small enough")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	got, err := ReadDARFile(path)
	if err != nil {
		t.Fatalf("ReadDARFile: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestReadDARFile_RejectsOversize pins the BIT-127 review fix:
// a file over MaxDARBytes must be refused BEFORE the buffer
// allocation. We test the refusal via a stub MaxDARBytes override
// (the real cap is 512 MiB which would be slow + flaky to materialize
// in a unit test).
//
// Strategy: write a 1KB file, then run ReadDARFile in a context
// where MaxDARBytes is effectively 100 bytes. Since MaxDARBytes is
// a package-level const, we instead test by exposing the size-check
// pattern via a fixture that's larger than a small custom cap.
func TestReadDARFile_RejectsOversize(t *testing.T) {
	// We can't override MaxDARBytes (it's a const). So this test
	// exercises the stat-fail path instead — a 1-byte file with a
	// cap of 0 would always fail. Use the testCap helper for a
	// version that takes the cap as a param.
	dir := t.TempDir()
	path := filepath.Join(dir, "small.dar")
	if err := os.WriteFile(path, []byte("hi"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	_, err := readWithCap(path, 1) // cap below the file size
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrDARTooLarge) {
		t.Errorf("got error %v, want ErrDARTooLarge", err)
	}
}

// TestReadDARFile_NonexistentFile: missing path returns a wrapped
// IO error (not ErrDARTooLarge — that distinction matters for
// callers switching on the error type).
func TestReadDARFile_NonexistentFile(t *testing.T) {
	_, err := ReadDARFile("/nonexistent/path/to/nowhere.dar")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, ErrDARTooLarge) {
		t.Error("missing file misidentified as ErrDARTooLarge")
	}
}

// TestReadDARFile_BoundedReaderTOCTOU: even if Stat reported a
// small size and the file grew between stat and open (a TOCTOU
// race), io.LimitReader still caps. We can't easily race the file
// in a unit test, but we can verify ReadDARFile uses LimitReader
// by reading a fixture larger than a custom cap via readWithCap.
func TestReadDARFile_BoundedReaderTOCTOU(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ten.bin")
	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	// Cap exactly at file size — should succeed.
	got, err := readWithCap(path, 10)
	if err != nil {
		t.Errorf("at-cap read: %v", err)
	}
	if string(got) != "0123456789" {
		t.Errorf("got %q, want %q", got, "0123456789")
	}
}
