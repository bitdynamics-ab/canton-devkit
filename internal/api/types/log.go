package types

// LogLine is one streamed line from `localnet logs` (P1-06).
// Both the CLI's bubbletea TUI and the future Web UI logs panel
// consume the same shape — the CLI prints it with term color tokens,
// the Web UI renders it as a virtualized list row.
//
// Time is the line's timestamp in RFC3339Nano if the docker log
// driver supplied one; empty otherwise. Level is best-effort
// — parsed from common log prefixes (INFO/WARN/ERROR/DEBUG/TRACE);
// "" means we couldn't classify.
type LogLine struct {
	Time    string `json:"time,omitempty"`
	Service string `json:"service"`
	Level   string `json:"level,omitempty"`
	Message string `json:"message"`
}
