package localnet

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMaterializeTokensV2Overlay_WritesExpectedFile pins the materialiser
// contract: a fresh dataDir gets the overlay written under tokens-v2/,
// the returned path is the overlay file, and the contents carry the four
// Canton alpha-protocol flags V2 needs.
func TestMaterializeTokensV2Overlay_WritesExpectedFile(t *testing.T) {
	dataDir := t.TempDir()
	out, err := MaterializeTokensV2Overlay(dataDir, nil)
	if err != nil {
		t.Fatalf("MaterializeTokensV2Overlay: %v", err)
	}

	wantDir := filepath.Join(dataDir, "tokens-v2")
	if !strings.HasPrefix(out, wantDir+string(filepath.Separator)) {
		t.Errorf("overlay path %q is not under %q", out, wantDir)
	}

	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read overlay: %v", err)
	}
	want := []string{
		"alpha-version-support=true",
		"non-standard-config=true",
		"initial-protocol-version=35",
		"ADDITIONAL_CONFIG_TOKEN_STANDARD_V2_DEVNET",
	}
	for _, s := range want {
		if !strings.Contains(string(body), s) {
			t.Errorf("overlay missing %q; got:\n%s", s, body)
		}
	}
}

// TestMaterializeTokensV2Overlay_Idempotent reruns the materialiser on
// the same dataDir twice and confirms the second call returns the same
// path with no error — operators must be able to repeat `up` without
// the materialiser clobbering or panicking.
func TestMaterializeTokensV2Overlay_Idempotent(t *testing.T) {
	dataDir := t.TempDir()
	first, err := MaterializeTokensV2Overlay(dataDir, nil)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	second, err := MaterializeTokensV2Overlay(dataDir, nil)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if first != second {
		t.Errorf("idempotent run returned different paths: %q vs %q", first, second)
	}
}

// TestMaterializeTokensV2Overlay_NoOpWhenBytesMatch pins the
// write-if-unchanged contract: when the destination already has the
// canonical embedded bytes, re-materialising MUST NOT call WriteFile
// — proven by checking the file's mtime is unchanged across calls.
func TestMaterializeTokensV2Overlay_NoOpWhenBytesMatch(t *testing.T) {
	dataDir := t.TempDir()
	out, err := MaterializeTokensV2Overlay(dataDir, nil)
	if err != nil {
		t.Fatalf("first materialise: %v", err)
	}
	stat1, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat after first: %v", err)
	}
	// Backdate the mtime so a re-write would be observable.
	old := stat1.ModTime().Add(-1 * 60 * 60 * 1_000_000_000) // -1h
	if err := os.Chtimes(out, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if _, err := MaterializeTokensV2Overlay(dataDir, nil); err != nil {
		t.Fatalf("second materialise: %v", err)
	}
	stat2, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat after second: %v", err)
	}
	if !stat2.ModTime().Equal(old) {
		t.Errorf("expected mtime unchanged (no write); got %v vs backdated %v",
			stat2.ModTime(), old)
	}
}

// TestMaterializeTokensV2Overlay_PreservesOperatorEdits is the
// regression for the inverted edit-preservation logic: an operator who
// tweaks the overlay in place (the workflow the doc comment promises is
// safe) must keep their edit across a re-materialise, and the drift
// must be reported on the warn writer rather than silently stomped.
func TestMaterializeTokensV2Overlay_PreservesOperatorEdits(t *testing.T) {
	dataDir := t.TempDir()
	out, err := MaterializeTokensV2Overlay(dataDir, nil)
	if err != nil {
		t.Fatalf("first materialise: %v", err)
	}

	edited := []byte("# operator tweak: point at a custom snapshot\n")
	if err := os.WriteFile(out, edited, 0o644); err != nil {
		t.Fatalf("simulate operator edit: %v", err)
	}

	var warn bytes.Buffer
	if _, err := MaterializeTokensV2Overlay(dataDir, &warn); err != nil {
		t.Fatalf("re-materialise over edited overlay: %v", err)
	}

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read after re-materialise: %v", err)
	}
	if !bytes.Equal(got, edited) {
		t.Errorf("operator edit was clobbered:\n got: %q\nwant: %q", got, edited)
	}
	if !strings.Contains(warn.String(), out) {
		t.Errorf("expected a drift notice mentioning %q on the warn writer, got %q", out, warn.String())
	}
}

// TestMaterializeTokensV2Overlay_RejectsEmptyDataDir guards the same
// contract as the observability overlay: an empty dataDir is a caller
// bug, not a silent CWD-relative materialise.
func TestMaterializeTokensV2Overlay_RejectsEmptyDataDir(t *testing.T) {
	_, err := MaterializeTokensV2Overlay("", nil)
	if err == nil {
		t.Fatal("expected error for empty dataDir, got nil")
	}
	if !strings.Contains(err.Error(), "empty dataDir") {
		t.Errorf("error %q should mention empty dataDir", err.Error())
	}
}
