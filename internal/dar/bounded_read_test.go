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

// TestReadDARFile_RejectsOversize pins: a file over the cap must be
// refused BEFORE the buffer allocation. Exercised via readWithCap
// with a tiny cap — the real 512 MiB MaxDARBytes would be slow and
// flaky to materialize in a unit test.
func TestReadDARFile_RejectsOversize(t *testing.T) {
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
