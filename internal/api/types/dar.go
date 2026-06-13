package types

// DAR wire shapes shared between the CLI (`localnet dar …` --format
// json) and the Web UI handlers (internal/ui/handlers/dar.go). Kept
// here — rather than as anonymous map[string]any literals in each
// surface — so the two cannot drift: a field added for one surface
// lands on both, and the schema-pin tests (schema_shape_test.go +
// schema_pin_test.go) catch any rename/retype.
//
// The business logic that populates these lives in
// internal/localnet/darops; both surfaces call it.

// DARRow is one uploaded DAR on a participant. Mirrors the Canton
// Admin API DarDescription projection. `Vetted` is a tri-state via a
// pointer: nil means "vetting state not queried" (cheap list path),
// non-nil is the resolved per-row state on the targeted participant.
// Surfacing vetting was a proposal-listed `dar list` column
// (docs/original-devkit-proposal.md) that both surfaces previously
// lacked or faked.
type DARRow struct {
	Main        string `json:"main"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	// Vetted reports whether the DAR is vetted on the participant the
	// list was addressed to. nil = not probed (the caller didn't ask
	// for vetting enrichment); the JSON key is omitted in that case so
	// a non-enriched listing stays byte-identical to the old shape.
	Vetted *bool `json:"vetted,omitempty"`
}

// DARListResponse is the GET /api/instances/{name}/dar body and the
// CLI `dar list --format json` output. `Role` records which
// participant the rows describe.
type DARListResponse struct {
	SchemaVersion int      `json:"schema_version"`
	Instance      string   `json:"instance"`
	Role          string   `json:"role"`
	Dars          []DARRow `json:"dars"`
}

// DARVettingRow is one participant's vetting verdict for a single DAR.
// Error is set (and Vetted left false) when that participant couldn't
// be probed — port not recorded, no JWT, or the RPC failed — so the
// UI can render a per-row "unknown" rather than silently claiming
// "unvetted".
type DARVettingRow struct {
	Role   string `json:"role"`
	Vetted bool   `json:"vetted"`
	Error  string `json:"error,omitempty"`
}

// DARVettingResponse is the GET /api/instances/{name}/dar/{id}/vetting
// body and the per-row enrichment source for `dar list --vetting`.
type DARVettingResponse struct {
	SchemaVersion int             `json:"schema_version"`
	Instance      string          `json:"instance"`
	Main          string          `json:"main"`
	Participants  []DARVettingRow `json:"participants"`
}

// DARVettingToggleResponse is the POST
// /api/instances/{name}/dar/{id}/vetting/{role} body.
type DARVettingToggleResponse struct {
	SchemaVersion int    `json:"schema_version"`
	Instance      string `json:"instance"`
	Main          string `json:"main"`
	Role          string `json:"role"`
	Vetted        bool   `json:"vetted"`
}

// DARUploadRoleResult is one participant's outcome from a fan-out
// upload. OK=false carries Error; the aggregate envelope stays 200
// even on partial failure so the caller sees what landed.
type DARUploadRoleResult struct {
	Role   string   `json:"role"`
	OK     bool     `json:"ok"`
	DarIDs []string `json:"dar_ids,omitempty"`
	Count  int      `json:"count"`
	Error  string   `json:"error,omitempty"`
}

// DARUploadResponse is the POST /api/instances/{name}/dar body.
type DARUploadResponse struct {
	SchemaVersion int                   `json:"schema_version"`
	Instance      string                `json:"instance"`
	Results       []DARUploadRoleResult `json:"results"`
	TotalUploaded int                   `json:"total_uploaded"`
}
