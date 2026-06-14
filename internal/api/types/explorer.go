package types

// Explorer wire shapes — the Active Contract Set, transaction list,
// contract-detail drawer, and per-party transaction-replay views.
//
// These are the single source of truth for BOTH the Web UI Explorer
// handlers (internal/ui/handlers/contracts.go, transactions.go) and
// the CLI `--format json` paths (internal/cli/localnet/contracts.go:
// `contracts ls`, `tx ls`, `tx replay`). Pinning the shapes here lets
// TestSchemaShape_GoldenPins… keep the Go handlers, the CLI JSON, and
// frontend/src/api.ts from drifting with no CI signal.
//
// snake_case JSON tags throughout (matches the rest of this package
// and frontend/src/api.ts). Fields not applicable to a row kind are
// omitempty so the same struct serves create / archive / exercise.

// ContractRow is one Active Contract Set entry. Emitted by the
// Explorer ACS snapshot (`GET .../contracts`) and the CLI
// `contracts ls`. The Web UI fills Payload / CreatedAt / package
// metadata (it renders a typed drawer); the CLI text table reads
// only the id / template / parties columns, but shares the struct so
// the `--format json` output and the HTTP payload can never drift.
type ContractRow struct {
	ContractID     string         `json:"contract_id"`
	TemplateID     string         `json:"template_id"`
	Payload        map[string]any `json:"payload,omitempty"`
	Signatories    []string       `json:"signatories"`
	Observers      []string       `json:"observers"`
	CreatedAt      string         `json:"created_at,omitempty"`
	PackageName    string         `json:"package_name,omitempty"`
	PackageVersion string         `json:"package_version,omitempty"`
}

// ContractsListResponse is the body of `GET .../contracts` and the
// CLI `contracts ls --format json`. LedgerEnd is the offset the
// snapshot was taken at; the frontend threads it into the live SSE
// stream's `since` so the snapshot→stream handoff is one atomic
// offset boundary. Truncated is true when the participant had more
// matching contracts than Limit.
type ContractsListResponse struct {
	SchemaVersion int           `json:"schema_version"`
	Instance      string        `json:"instance"`
	Role          string        `json:"role,omitempty"`
	Parties       []string      `json:"parties,omitempty"`
	LedgerEnd     int64         `json:"ledger_end"`
	Contracts     []ContractRow `json:"contracts"`
	Truncated     bool          `json:"truncated,omitempty"`
	Limit         int           `json:"limit,omitempty"`
}

// ContractDetail is the deep view the Explorer's contract drawer
// renders — the create event's payload + parties and the archive
// event (if any). Backed by EventQueryService.GetEventsByContractId
// projected through the JWT's party set.
type ContractDetail struct {
	ContractID     string         `json:"contract_id"`
	TemplateID     string         `json:"template_id,omitempty"`
	PackageName    string         `json:"package_name,omitempty"`
	Payload        map[string]any `json:"payload,omitempty"`
	Signatories    []string       `json:"signatories"`
	Observers      []string       `json:"observers"`
	CreatedAt      string         `json:"created_at,omitempty"`
	CreatedOffset  int64          `json:"created_offset,omitempty"`
	CreatedTxID    string         `json:"created_update_id,omitempty"`
	Archived       bool           `json:"archived"`
	ArchivedAt     string         `json:"archived_at,omitempty"`
	ArchivedOffset int64          `json:"archived_offset,omitempty"`
	ArchivedTxID   string         `json:"archived_update_id,omitempty"`
}

// ContractDetailResponse is the body of
// `GET .../contracts/{contract_id}`.
type ContractDetailResponse struct {
	SchemaVersion int            `json:"schema_version"`
	Instance      string         `json:"instance"`
	Role          string         `json:"role,omitempty"`
	Contract      ContractDetail `json:"contract"`
}

// TransactionEvent is one create / archive / exercise event projected
// from a transaction. The `kind` is the abbreviated form the Web UI
// has always rendered ("create"/"archive"/"exercise"); the shared
// ledger.ProjectTransactionEvents decoder produces the past-tense
// EventKind, which the handler/CLI map to this on the wire.
type TransactionEvent struct {
	Kind       string   `json:"kind"`
	ContractID string   `json:"contract_id,omitempty"`
	Template   string   `json:"template,omitempty"`
	Witnesses  []string `json:"witnesses,omitempty"`
}

// TransactionRow is the unified projection across the
// GetUpdatesResponse oneof: a transaction, reassignment, or topology
// event. The `kind` discriminator lets the frontend pick its render
// path (table row vs timeline glyph) without re-walking the proto.
type TransactionRow struct {
	Kind         string             `json:"kind"`
	Offset       int64              `json:"offset"`
	UpdateID     string             `json:"update_id,omitempty"`
	WorkflowID   string             `json:"workflow_id,omitempty"`
	CommandID    string             `json:"command_id,omitempty"`
	RecordTime   string             `json:"record_time,omitempty"`
	Synchronizer string             `json:"synchronizer,omitempty"`
	EventCount   int                `json:"event_count,omitempty"`
	Events       []TransactionEvent `json:"events,omitempty"`
}

// TransactionsListResponse is the body of `GET .../transactions` and
// the CLI `tx ls --format json`. ScannedFrom is the exclusive lower
// bound of the offset window inspected; WindowTruncated is true when
// the scan stopped before reaching the window's end (stream cap or
// deadline), so the rows are the newest of a clipped scan rather than
// the complete recent history.
type TransactionsListResponse struct {
	SchemaVersion   int              `json:"schema_version"`
	Instance        string           `json:"instance"`
	Role            string           `json:"role,omitempty"`
	Parties         []string         `json:"parties,omitempty"`
	LedgerEnd       int64            `json:"ledger_end"`
	Transactions    []TransactionRow `json:"transactions"`
	Count           int              `json:"count"`
	ScannedFrom     int64            `json:"scanned_from"`
	WindowTruncated bool             `json:"window_truncated"`
}

// TxReplayEvent is one event of a replayed transaction, projected
// with the LEDGER_EFFECTS shape so exercised choices (not just the
// ACS delta) are visible. This is the per-party visibility projection
// the proposal commits to: querying the same transaction as different
// parties yields different event sets. Emitted by the CLI `tx replay`
// and the Web UI `GET .../transactions/{update_id}/replay`.
type TxReplayEvent struct {
	Kind          string   `json:"kind"` // "created" | "exercised" | "archived"
	NodeID        int32    `json:"node_id"`
	ContractID    string   `json:"contract_id"`
	TemplateID    string   `json:"template_id,omitempty"`
	Choice        string   `json:"choice,omitempty"`         // exercised only
	ActingParties []string `json:"acting_parties,omitempty"` // exercised only
	Consuming     bool     `json:"consuming,omitempty"`      // exercised only
	Signatories   []string `json:"signatories,omitempty"`    // created only
	Observers     []string `json:"observers,omitempty"`      // created only
}

// TxReplayResponse is the body of `tx replay` (CLI `--format json`
// and the Web UI replay endpoint). Parties is the party set the
// projection was taken through; EffectiveAt is RFC3339.
type TxReplayResponse struct {
	SchemaVersion int             `json:"schema_version"`
	Instance      string          `json:"instance"`
	Parties       []string        `json:"parties,omitempty"`
	UpdateID      string          `json:"update_id"`
	Offset        int64           `json:"offset"`
	WorkflowID    string          `json:"workflow_id,omitempty"`
	EffectiveAt   string          `json:"effective_at,omitempty"`
	EventCount    int             `json:"event_count"`
	Events        []TxReplayEvent `json:"events"`
}
