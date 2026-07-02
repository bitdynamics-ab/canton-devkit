package telemetry

import (
	"crypto/rand"
	"encoding/hex"
)

// installID returns this install's anonymous de-duplication token, minting
// and persisting a fresh random one on first use.
//
// The token is a random UUIDv4 stored in the telemetry config file — pure
// CSPRNG output, not derived from any machine attribute (no hostname, MAC,
// serial, or hardware fingerprint). It exists solely so the collector can
// count distinct installs: the collector records only (token, active-date)
// in a separate table and never tags an individual counter with it, so
// usage can never be traced back to a host. Because it lives in the config
// file, a fresh container, VM, or reinstall mints a new token — by design,
// so each environment counts once. It identifies a config-file lifetime,
// never a person or a device.
//
// Gated to non-CI like RecordInstallOnce: ephemeral CI runners would mint
// a new token per job and inflate the unique-install count.
//
// Returns "" in CI or if the CSPRNG is unavailable; callers ship the
// upload without a token in that case.
func installID() string {
	if detectCI() {
		return ""
	}
	c := loadConsent()
	if c.InstallID != "" {
		return c.InstallID
	}
	id := newInstallID()
	if id == "" {
		return ""
	}
	c.InstallID = id
	_ = saveConsent(c) // best-effort; a write miss just re-mints next run
	return id
}

// newInstallID mints a random UUIDv4. Returns "" if the system CSPRNG
// fails (telemetry then ships without a token rather than blocking).
func newInstallID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// ResetInstallID clears the persisted token so the next upload mints a
// fresh one. Lets a user rotate their anonymous install id at will
// (`dpm telemetry reset-id`) — useful if they want to break any linkage
// between past and future uploads.
func ResetInstallID() error {
	c := loadConsent()
	c.InstallID = ""
	return saveConsent(c)
}
