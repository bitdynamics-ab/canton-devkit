// Package campaign backs the campaign build: a stable per-install code the Web
// UI stamps on every screen. OFF by default; the campaign build enables it at
// link time:
//
//	-ldflags "-X github.com/bitdynamics-ab/canton-devkit/internal/campaign.enabled=on"
package campaign

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// enabled is set to "on" at link time for the campaign build.
var enabled = ""

func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DEVKIT_CAMPAIGN"))) {
	case "1", "true", "on", "yes", "y":
		return true
	case "0", "false", "off", "no", "n":
		return false
	}
	return enabled == "on"
}

var (
	mu         sync.Mutex
	cachedPath string
	cached     string
	cachedErr  error
)

// Code returns this install's stable code, minting and persisting one on first
// use. "" when the campaign build is not enabled.
func Code() string {
	code, _ := Lookup()
	return code
}

func Lookup() (string, error) {
	if !Enabled() {
		return "", nil
	}
	path := codePath()
	mu.Lock()
	defer mu.Unlock()
	if cached == "" || cachedPath != path {
		cached, cachedErr = loadOrCreate(path)
		cachedPath = path
		if cachedErr != nil {
			cachedErr = fmt.Errorf("campaign code not saved to %s, it will change on restart: %w", path, cachedErr)
		}
	}
	return cached, cachedErr
}

const maxAttempts = 4

var errUnsettled = errors.New("code file kept changing")

func loadOrCreate(path string) (string, error) {
	for range maxAttempts {
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			if code := sanitize(string(b)); code != "" {
				return code, nil
			}
		case !errors.Is(err, fs.ErrNotExist):
			return newCode(), err
		default:
			code := newCode()
			if err := createExclusive(path, code); !errors.Is(err, fs.ErrExist) {
				return code, err
			}
			if !isSymlink(path) {
				continue
			}
		}
		code, err := takeInvalid(path)
		if err != nil {
			return newCode(), err
		}
		if code != "" {
			return code, nil
		}
	}
	return newCode(), errUnsettled
}

func isSymlink(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode()&fs.ModeSymlink != 0
}

func takeInvalid(path string) (string, error) {
	aside := path + ".invalid-" + strings.TrimPrefix(newCode(), "CCT-")
	if err := os.Rename(path, aside); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	defer func() { _ = os.Remove(aside) }()
	b, err := os.ReadFile(aside)
	if err != nil {
		return "", nil
	}
	code := sanitize(string(b))
	if code == "" || os.Link(aside, path) != nil {
		return "", nil
	}
	return code, nil
}

func createExclusive(path, code string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".campaign-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	_, werr := tmp.WriteString(code + "\n")
	if err := errors.Join(werr, tmp.Close()); err != nil {
		return err
	}
	err = os.Link(tmpName, path)
	if err == nil || errors.Is(err, fs.ErrExist) {
		return err
	}
	return os.Rename(tmpName, path)
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
