// Package telemetry collects privacy-preserving, aggregate-only usage
// signals for canton-devkit (Milestone 4 adoption evidence).
//
// Design constraints — these are hard rules, not preferences:
//
//   - ZERO PII BY CONSTRUCTION. The Event struct has no field that can
//     carry an instance name, party id, DAR name/contents, port, JWT,
//     file path, or env var. We record the command PATH (e.g. "localnet
//     token mint") and an exit code — never argument or flag values.
//   - OPT-OUT, but loud + trivial to disable. Enabled by default; a
//     one-time first-run notice is printed; `CANTON_DEVKIT_TELEMETRY=0`,
//     `DO_NOT_TRACK=1`, or `localnet telemetry off` disable it.
//   - NEVER blocks or breaks a command. Recording is a fast local append;
//     network flushes are batched, hard-timeout-bounded, and fail silently.
//   - AUDITABLE. `localnet telemetry status` prints exactly what is/was
//     sent, and docs/telemetry.md documents every field.
//
// The HTTPS endpoint is intentionally empty by default and set at
// release-build time (-ldflags "-X .../telemetry.endpoint=https://…") or
// via CANTON_DEVKIT_TELEMETRY_ENDPOINT. With no endpoint, events are
// spooled locally and nothing leaves the machine.
package telemetry

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// SchemaVersion is the event/config shape version, bumped on incompatible
// changes so the collector can route old payloads.
const SchemaVersion = 1

// endpoint is the collector URL. Empty = spool-only (nothing sent). Set
// at build time via -ldflags, or overridden by the env var below.
var endpoint = ""

// Env knobs.
const (
	envDisable     = "CANTON_DEVKIT_TELEMETRY"          // "0"/"false"/"off" disables
	envDoNotTrack  = "DO_NOT_TRACK"                     // community standard; "1" disables
	envEndpoint    = "CANTON_DEVKIT_TELEMETRY_ENDPOINT" // overrides the build-time endpoint
	envDir         = "CANTON_DEVKIT_TELEMETRY_DIR"      // test seam for the state dir
	configFileName = "telemetry.json"
	spoolFileName  = "telemetry-spool.jsonl"
)

// Config is the persisted consent state. Lives at <dir>/telemetry.json.
type Config struct {
	SchemaVersion int    `json:"schema_version"`
	Enabled       bool   `json:"enabled"`
	InstallID     string `json:"install_id"` // random v4-style id; NOT machine-derived
	NoticeShown   bool   `json:"notice_shown"`
	UpdatedAt     string `json:"updated_at"`
}

// Event is one aggregate-only telemetry record. Add fields here ONLY if
// they cannot identify a user, project, or workload.
type Event struct {
	SchemaVersion  int    `json:"schema_version"`
	InstallID      string `json:"install_id"`
	Event          string `json:"event"`   // "command"
	Command        string `json:"command"` // command PATH only, e.g. "localnet token mint"
	ExitCode       int    `json:"exit_code"`
	DurationBucket string `json:"duration_bucket"` // coarse; never a precise timing
	ToolVersion    string `json:"tool_version"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	Timestamp      string `json:"ts"` // RFC3339
}

// stateDir is <home>/.canton-devkit, or the test override.
func stateDir() string {
	if d := os.Getenv(envDir); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".canton-devkit")
	}
	return filepath.Join(home, ".canton-devkit")
}

func configPath() string { return filepath.Join(stateDir(), configFileName) }
func spoolPath() string  { return filepath.Join(stateDir(), spoolFileName) }

// disabledByEnv reports whether an env var force-disables telemetry,
// independent of the persisted config (highest priority, writes nothing).
func disabledByEnv() bool {
	switch os.Getenv(envDisable) {
	case "0", "false", "off", "no", "FALSE", "OFF":
		return true
	}
	switch os.Getenv(envDoNotTrack) {
	case "1", "true", "yes", "TRUE":
		return true
	}
	return false
}

// LoadConfig reads the persisted consent state, creating a fresh one
// (enabled by default, with a new install id) when none exists. The
// fresh config is NOT written here — first-run handling writes it after
// the notice so a read alone never has side effects.
func LoadConfig() Config {
	b, err := os.ReadFile(configPath())
	if err != nil {
		return Config{SchemaVersion: SchemaVersion, Enabled: true, InstallID: newInstallID()}
	}
	var c Config
	if json.Unmarshal(b, &c) != nil {
		return Config{SchemaVersion: SchemaVersion, Enabled: true, InstallID: newInstallID()}
	}
	if c.InstallID == "" {
		c.InstallID = newInstallID()
	}
	return c
}

func saveConfig(c Config) error {
	c.SchemaVersion = SchemaVersion
	c.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), b, 0o644)
}

// Enabled reports the effective on/off state: env override wins, else the
// persisted config.
func Enabled() bool {
	if disabledByEnv() {
		return false
	}
	return LoadConfig().Enabled
}

// SetEnabled persists the consent choice (the `telemetry on|off` command).
func SetEnabled(on bool) error {
	c := LoadConfig()
	c.Enabled = on
	c.NoticeShown = true // an explicit choice supersedes the notice
	return saveConfig(c)
}

// MaybeFirstRunNotice prints the one-time opt-out notice to w the first
// time telemetry runs, then records that it was shown. No-op when an env
// var has force-disabled telemetry (nothing is collected, so no notice
// is owed) or when the notice was already shown.
func MaybeFirstRunNotice(w io.Writer) {
	if disabledByEnv() {
		return
	}
	c := LoadConfig()
	if c.NoticeShown {
		return
	}
	_, _ = fmt.Fprint(w, firstRunNotice)
	c.NoticeShown = true
	_ = saveConfig(c) // best-effort; a write failure just re-shows next time
}

const firstRunNotice = `
canton-devkit collects anonymous, aggregate usage data (the command you
ran, its exit code, a coarse duration, tool + OS version, and a random
install id) to guide development. It never collects instance names, party
ids, DAR contents, ports, credentials, or file paths.

Opt out anytime:  canton-devkit localnet telemetry off
                  (or set CANTON_DEVKIT_TELEMETRY=0 / DO_NOT_TRACK=1)
See exactly what's sent:  canton-devkit localnet telemetry status

`

// newInstallID returns a random 128-bit hex id (v4-style, not derived
// from any machine identifier).
func newInstallID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Extremely unlikely; fall back to a time-seeded value so we
		// never block. Still not machine-identifying.
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))[:32]
	}
	return hex.EncodeToString(b[:])
}

// durationBucket coarsens a duration so we never record precise timings.
func durationBucket(d time.Duration) string {
	ms := d.Milliseconds()
	switch {
	case ms < 100:
		return "<100ms"
	case ms < 500:
		return "100-500ms"
	case ms < 2000:
		return "500ms-2s"
	case ms < 10000:
		return "2-10s"
	case ms < 60000:
		return "10-60s"
	default:
		return ">60s"
	}
}
