package types

// Instance is the public shape returned by `localnet status --json`
// and `GET /api/instances/:name`. It is a projection of
// internal/registry.State + live `docker compose ps` data.
//
// Field selection: anything an external script or the Web UI needs.
// Things that are pure internal bookkeeping (LastHealthCheckAt,
// SchemaVersion of the underlying registry record) stay in
// registry.State and are deliberately omitted here.
type Instance struct {
	// SchemaVersion ties this response to the package SchemaVersion
	// constant so handlers + CLI --json consumers can detect format
	// breaks. Reviewer pin on PR #31 #3:
	// TestAllTopLevelResponses_CarrySchemaVersion enforces that
	// every top-level response struct here has this field.
	SchemaVersion int    `json:"schema_version"`
	Name          string `json:"name"`
	SpliceVersion string `json:"splice_version"`
	Status        string `json:"status"` // "running" | "stopped" | …
	CreatedAt     string `json:"created_at"`
	Uptime        string `json:"uptime,omitempty"` // human-readable; empty if !running

	// Compose / docker wiring — useful for scripts that wrap
	// `docker compose` directly against the project DevKit started.
	ComposeProject  string `json:"compose_project"`
	DockerNetwork   string `json:"docker_network"`
	ContainerPrefix string `json:"container_prefix"`

	// Filesystem locations.
	ProjectDir string `json:"project_dir"`
	DataDir    string `json:"data_dir"`

	// Live state (populated only by status; nil by list).
	Services    []ServiceStatus       `json:"services,omitempty"`
	Endpoints   []Endpoint            `json:"endpoints,omitempty"`
	Parties     []Party               `json:"parties,omitempty"`
	Credentials map[string]Credential `json:"credentials,omitempty"`
}

// InstanceSummary is the row shape returned by `localnet list --json`
// and `GET /api/instances`. Lighter than Instance — no services,
// endpoints, parties, or credentials — so listing 50 instances stays
// cheap.
type InstanceSummary struct {
	Name          string `json:"name"`
	Status        string `json:"status"`
	SpliceVersion string `json:"splice_version"`
	Ports         string `json:"ports"`       // pre-formatted "4441-4487" range
	StartedAgo    string `json:"started_ago"` // "1h 22m ago"; empty if !running
	VolumeSize    string `json:"volume_size,omitempty"`
}

// ListResponse wraps a slice of InstanceSummary so the top-level JSON
// can carry schema_version + an optional "warning" string when the
// registry index is partially unreadable.
type ListResponse struct {
	SchemaVersion int               `json:"schema_version"`
	Instances     []InstanceSummary `json:"instances"`
	Warning       string            `json:"warning,omitempty"`
}
