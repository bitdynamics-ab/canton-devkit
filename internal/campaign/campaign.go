// Package campaign backs the campaign build: a stable per-install code the Web
// UI stamps on every screen. OFF by default; the campaign build enables it at
// link time:
//
//	-ldflags "-X github.com/bitdynamics-ab/canton-devkit/internal/campaign.enabled=on"
//
// DEVKIT_CAMPAIGN forces it on locally.
package campaign

import (
	"crypto/rand"
	"encoding/base32"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// enabled is set to "on" at link time for the campaign build.
var enabled = ""

// Enabled reports whether this is a campaign build, or a truthy DEVKIT_CAMPAIGN
// (1/true/on/yes) forces it on. Off by default.
func Enabled() bool {
	return enabled == "on" || envTruthy(os.Getenv("DEVKIT_CAMPAIGN"))
}

func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "on", "yes", "y":
		return true
	default:
		return false
	}
}

var (
	mu     sync.Mutex
	cached string
)

// Code returns this install's stable code, minting and persisting one on first
// use. "" when the campaign build is not enabled.
func Code() string {
	if !Enabled() {
		return ""
	}
	mu.Lock()
	defer mu.Unlock()
	if cached != "" {
		return cached
	}
	path := codePath()
	if b, err := os.ReadFile(path); err == nil {
		if c := sanitize(string(b)); c != "" {
			cached = c
			return cached
		}
	}
	cached = newCode()
	// Best-effort: an un-writable home still yields a code for this process.
	_ = writeAtomic(path, cached)
	return cached
}

// newCode is "CCT-" + 40 bits of crypto-random base32 (8 chars, A–Z2–7).
func newCode() string {
	var b [5]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand should never fail; if it does, a constant beats a panic.
		return "CCT-XXXXXXXX"
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])
	return "CCT-" + strings.ToUpper(enc)
}

// codeRe is the one canonical code shape newCode emits.
var codeRe = regexp.MustCompile(`^CCT-[A-Z2-7]{8}$`)

// sanitize returns the trimmed value only when it matches the canonical code
// format, else "". Validating the whole shape (not just the alphabet) means a
// garbled, oversized, or hand-edited file is treated as absent and self-heals
// to a fresh code, and nothing unexpected can flow into the watermark, the
// /api/version payload, or the share URL.
func sanitize(s string) string {
	s = strings.TrimSpace(s)
	if codeRe.MatchString(s) {
		return s
	}
	return ""
}

// codePath is the file holding this install's code:
// ~/.canton-devkit/campaign-id. DEVKIT_CAMPAIGN_FILE overrides it (tests).
func codePath() string {
	if p := os.Getenv("DEVKIT_CAMPAIGN_FILE"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".canton-devkit", "campaign-id")
	}
	return filepath.Join(home, ".canton-devkit", "campaign-id")
}

// writeAtomic writes via a temp file + rename so a crash mid-write never leaves
// a half-written code file.
func writeAtomic(path, data string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".campaign-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(data + "\n"); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
