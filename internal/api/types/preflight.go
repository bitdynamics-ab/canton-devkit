package types

// PreflightReport is the JSON-emitted shape from `localnet doctor
// --json` (P1-05) and the structured progress feed `localnet up`
// streams (P1-03 --json mode). It mirrors the Sections + Steps
// rendered visually by ScreenDoctor / ScreenUp in the JSX mockups.
type PreflightReport struct {
	SchemaVersion int                `json:"schema_version"`
	OK            bool               `json:"ok"`
	Sections      []PreflightSection `json:"sections"`
	Summary       string             `json:"summary,omitempty"`
	// ErrorCode is the stable machine-readable code the frontend
	// switches on when OK=false. Empty when OK=true.
	// Values are from internal/localnet's ErrCode* constants
	// (PORTS_IN_USE / DOCKER_DOWN / DOCKER_NOT_INSTALLED /
	// COMPOSE_V1_OR_MISSING / DOCKER_MEMORY_LOW / DISK_LOW /
	// PREFLIGHT_FAILED).
	ErrorCode string `json:"error_code,omitempty"`
}

// PreflightSection groups related checks under a header (System,
// Resources, Network, Splice). Matches Section in the mockup.
type PreflightSection struct {
	Title  string           `json:"title"`
	Checks []PreflightCheck `json:"checks"`
}

// PreflightCheck is one row inside a section. Result is "pass",
// "warn", or "fail"; renderer maps to ✓ / ⚠ / ✗ glyphs.
// Remediation is an array of one-line tips shown in the friendly
// error block when result == "fail".
type PreflightCheck struct {
	Label       string   `json:"label"`
	Result      string   `json:"result"` // "pass" | "warn" | "fail"
	Detail      string   `json:"detail,omitempty"`
	Elapsed     string   `json:"elapsed,omitempty"`
	Remediation []string `json:"remediation,omitempty"`
}
