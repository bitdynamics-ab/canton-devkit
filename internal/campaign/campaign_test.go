package campaign

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// resetCache clears the process-level memoized code so each test starts clean.
func resetCache(t *testing.T) {
	t.Helper()
	mu.Lock()
	cached = ""
	mu.Unlock()
}

func TestEnabled_OffByDefault(t *testing.T) {
	resetCache(t)
	t.Setenv("DEVKIT_CAMPAIGN", "")
	if Enabled() {
		t.Fatal("campaign must be OFF for a normal build with no env override")
	}
	if got := Code(); got != "" {
		t.Errorf("Code() must be empty when disabled, got %q", got)
	}
}

// TestEnabled_LinkTimeAndTruthyEnv pins both positive triggers (the link-time
// build flag and a truthy env) and — importantly — that a falsey env value
// like "off"/"0" turns it OFF rather than on.
func TestEnabled_LinkTimeAndTruthyEnv(t *testing.T) {
	t.Setenv("DEVKIT_CAMPAIGN", "")
	// Link-time build gate.
	enabled = "on"
	t.Cleanup(func() { enabled = "" })
	if !Enabled() {
		t.Error("link-time enabled=on must turn campaign mode on")
	}
	enabled = ""

	for _, v := range []string{"1", "true", "on", "yes", "TRUE"} {
		t.Setenv("DEVKIT_CAMPAIGN", v)
		if !Enabled() {
			t.Errorf("DEVKIT_CAMPAIGN=%q should enable", v)
		}
	}
	for _, v := range []string{"", "0", "false", "off", "no"} {
		t.Setenv("DEVKIT_CAMPAIGN", v)
		if Enabled() {
			t.Errorf("DEVKIT_CAMPAIGN=%q must NOT enable (falsey)", v)
		}
	}
}

// TestCode_AdoptsExistingValidFile is the stability guarantee: an install keeps
// the SAME code across restarts by reading a pre-existing valid file verbatim
// (not one this process wrote) — never regenerating over it.
func TestCode_AdoptsExistingValidFile(t *testing.T) {
	resetCache(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "campaign-id")
	if err := os.WriteFile(file, []byte("CCT-ABCD2345\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_CAMPAIGN", "on")
	t.Setenv("DEVKIT_CAMPAIGN_FILE", file)

	if got := Code(); got != "CCT-ABCD2345" {
		t.Errorf("Code() must adopt the existing valid file verbatim, got %q want CCT-ABCD2345", got)
	}
}

func TestCode_GeneratesStableWellFormedCode(t *testing.T) {
	resetCache(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "campaign-id")
	t.Setenv("DEVKIT_CAMPAIGN", "on")
	t.Setenv("DEVKIT_CAMPAIGN_FILE", file)

	code := Code()
	if !regexp.MustCompile(`^CCT-[A-Z2-7]{8}$`).MatchString(code) {
		t.Fatalf("code %q is not the expected CCT-<8 base32> shape", code)
	}
	// Persisted to disk...
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("code file not written: %v", err)
	}
	if got := string(b); got != code+"\n" {
		t.Errorf("persisted file = %q, want %q", got, code+"\n")
	}
	// ...and stable across a cold read (new process simulated by clearing cache).
	resetCache(t)
	if again := Code(); again != code {
		t.Errorf("code must be stable per install: first %q, reread %q", code, again)
	}
}

func TestSanitize_RejectsInjectionChars(t *testing.T) {
	// A corrupt/hand-edited file containing markup or URL chars must be treated
	// as absent (→ regenerate) rather than flow into the watermark / share URL.
	resetCache(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "campaign-id")
	if err := os.WriteFile(file, []byte("<script>alert(1)</script>"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_CAMPAIGN", "on")
	t.Setenv("DEVKIT_CAMPAIGN_FILE", file)

	code := Code()
	if !regexp.MustCompile(`^CCT-[A-Z2-7]{8}$`).MatchString(code) {
		t.Errorf("a corrupt code file must be replaced with a fresh valid code, got %q", code)
	}
}
