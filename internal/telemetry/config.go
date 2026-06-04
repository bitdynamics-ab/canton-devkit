package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Env knobs (design decision #10 precedence + #10 debug).
const (
	envDoNotTrack = "DO_NOT_TRACK"        // any non-empty, non-"0" value → off
	envToggle     = "DPM_TELEMETRY"       // "on"/"off"
	envDebug      = "DPM_TELEMETRY_DEBUG" // "1" → print JSON to stderr, skip send
)

// consentPath is the persisted on/off choice, written by `telemetry
// on|off`. Lives alongside the weekly files.
func consentPath() string { return filepath.Join(telemetryDir(), "config.json") }

// consent is the persisted state. NO identifier of any kind (design #2).
type consent struct {
	SchemaVersion int    `json:"schema_version"`
	Enabled       *bool  `json:"enabled,omitempty"` // nil = never chosen (use default)
	NoticeShown   bool   `json:"notice_shown"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

func loadConsent() consent {
	b, err := os.ReadFile(consentPath())
	if err != nil {
		return consent{SchemaVersion: SchemaVersion}
	}
	var c consent
	if json.Unmarshal(b, &c) != nil {
		return consent{SchemaVersion: SchemaVersion}
	}
	return c
}

func saveConsent(c consent) error {
	c.SchemaVersion = SchemaVersion
	c.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := os.MkdirAll(telemetryDir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(consentPath(), b, 0o644)
}

// Source describes which rule decided the effective on/off state, for
// `telemetry status`.
type Source string

const (
	SourceDoNotTrack Source = "DO_NOT_TRACK env"
	SourceEnvToggle  Source = "DPM_TELEMETRY env"
	SourceConfig     Source = "config file"
	SourceDefault    Source = "default (opt-out)"
)

// Enabled reports the effective on/off state and which rule decided it.
// Precedence (highest first): DO_NOT_TRACK → DPM_TELEMETRY → config file
// → default ON (opt-out — design decision #1, ratified opt-out).
func Enabled() (bool, Source) {
	if v := os.Getenv(envDoNotTrack); v != "" && v != "0" {
		return false, SourceDoNotTrack
	}
	switch os.Getenv(envToggle) {
	case "on", "1", "true":
		return true, SourceEnvToggle
	case "off", "0", "false":
		return false, SourceEnvToggle
	}
	if c := loadConsent(); c.Enabled != nil {
		return *c.Enabled, SourceConfig
	}
	return true, SourceDefault // opt-out default
}

// IsEnabled is the boolean-only convenience.
func IsEnabled() bool { on, _ := Enabled(); return on }

// SetEnabled persists the user's on/off choice and marks the notice shown
// (an explicit choice supersedes it).
func SetEnabled(on bool) error {
	c := loadConsent()
	c.Enabled = &on
	c.NoticeShown = true
	return saveConsent(c)
}

func debugEnabled() bool { return os.Getenv(envDebug) == "1" }
