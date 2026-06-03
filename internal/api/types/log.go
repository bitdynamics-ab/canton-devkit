package types

import "time"

// LogLine is one streamed line from `localnet logs` (P1-06).
// Both the CLI's bubbletea TUI and the future Web UI logs panel
// consume the same shape — the CLI prints it with term color tokens,
// the Web UI renders it as a virtualized list row.
//
// Time is the line's timestamp in RFC3339Nano if the docker log
// driver supplied one; empty otherwise. Level is best-effort
// — parsed from common log prefixes (INFO/WARN/ERROR/DEBUG/TRACE);
// "" means we couldn't classify.
//
// We keep Time as a string for JSON wire compatibility (Go's
// time.Time JSON encoding is opinionated and varies across
// versions; a stable string is friendlier to non-Go consumers).
// Use ParsedTime() when a real time.Time is needed.
type LogLine struct {
	Time    string `json:"time,omitempty"`
	Service string `json:"service"`
	Level   string `json:"level,omitempty"`
	Message string `json:"message"`
}

// ParsedTime parses l.Time as RFC3339Nano and returns
// (parsed, true) on success. Returns (zero, false) on empty
// input or parse error — never panics. Reviewer pin on PR #31 #11:
// pushes the parse responsibility down so every consumer doesn't
// re-implement it (and the CLI renderer doesn't have to handle
// a runtime parse error mid-render).
func (l LogLine) ParsedTime() (time.Time, bool) {
	if l.Time == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, l.Time)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
