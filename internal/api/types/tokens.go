package types

// TokenRef is the registry-cached descriptor of a V2 token instrument
// created via `localnet token create`. Later commands and the Web UI
// resolve `--instrument <id|symbol>` against this map. Lives in
// registry.State.Tokens, keyed by (unique-per-instance) symbol.
type TokenRef struct {
	Name     string `json:"name"`
	Symbol   string `json:"symbol"`
	Decimals int    `json:"decimals"`
	// InitialSupply is the initial mint amount. Kept as a string, not
	// float/big.Int, to preserve Daml Decimal's fixed precision.
	InitialSupply string `json:"initial_supply"`
	// IssuerParty is the canonical party ID of the admin/issuer.
	IssuerParty string `json:"issuer_party"`
	// InstrumentID is the V2 InstrumentId.id text; with IssuerParty (the
	// admin) it forms the globally unique `(admin, id)` tuple.
	InstrumentID string `json:"instrument_id"`
	CreatedAt    string `json:"created_at"` // RFC3339
	// Status: "recorded" (registry-only, ledger submission deferred),
	// "submitted" (instrument created on-ledger), or "failed:<err>".
	Status string `json:"status"`
}

// TokenIdentityResponse is the act-as identity picker payload served by
// GET /api/tokens/identity and the CLI `token identity --format json`.
// CurrentRole echoes ?role= (default app-user) so the switcher can
// highlight the active identity without a second round-trip.
type TokenIdentityResponse struct {
	SchemaVersion  int      `json:"schema_version"`
	Instance       string   `json:"instance"`
	AvailableRoles []string `json:"available_roles"`
	CurrentRole    string   `json:"current_role"`
}

// TokenCreateRequest is the shared input shape for `token create
// --non-interactive` (CLI) and `POST /api/tokens` (Web UI), so both
// surfaces stay 1:1 (CLI ↔ Web UI parity).
type TokenCreateRequest struct {
	Name          string `json:"name"`
	Symbol        string `json:"symbol"`
	Decimals      int    `json:"decimals"`
	InitialSupply string `json:"initial_supply"`
	Issuer        string `json:"issuer"`
}

// HoldingSource records where a balance row came from so neither surface
// presents a fabricated number as real on-ledger state.
type HoldingSource string

const (
	// HoldingSourceLedger: summed from the live ACS (HoldingViewV2) — the
	// real on-ledger balance.
	HoldingSourceLedger HoldingSource = "ledger"
	// HoldingSourceRegistry: the registry-derived pseudo-balance (issuer
	// holds InitialSupply, everyone else 0), surfaced when no live
	// participant is reachable. UIs MUST label these — they are NOT
	// on-ledger truth.
	HoldingSourceRegistry HoldingSource = "registry"
)

// TokenHolding is one balance row (instrument + party + summed amount +
// Source). Mirrors internal/localnet/token.BalanceRow byte-for-byte;
// this declaration is the source of truth for the JSON shape.
type TokenHolding struct {
	// InstrumentSymbol is the friendly ticker; omitted for an unknown holding.
	InstrumentSymbol string        `json:"instrument_symbol,omitempty"`
	InstrumentID     string        `json:"instrument_id"`
	Party            string        `json:"party"`
	Amount           string        `json:"amount"` // Daml Decimal string
	Source           HoldingSource `json:"source"`
}

// TokenHoldingsResponse is the body of GET /api/tokens/{symbol}/holdings
// and the CLI `token balance --format json`. Shared + schema-pinned so
// the two surfaces cannot drift.
type TokenHoldingsResponse struct {
	SchemaVersion int `json:"schema_version"`
	// Source is the response-level provenance (matches every row's
	// Source), lifted here so the UI can render one disclaimer banner
	// without scanning every row.
	Source HoldingSource `json:"source"`
	// Holdings is never null on the wire — an empty live ACS yields [].
	Holdings []TokenHolding `json:"holdings"`
	// Truncated is set when the live ACS scan hit its safety cap and the
	// rows are a partial view.
	Truncated bool `json:"truncated,omitempty"`
}

// --- V2 foundation shapes -------------------------------------------
//
// Shared structs the later V2 feature surfaces (EventLog history,
// allocations/DvP, BatchingUtilityV2) emit on both CLI --json and Web UI
// payloads. Declared here so the surfaces cannot drift; pinned by
// schema_shape_test. None is a top-level response body (no
// SchemaVersion) — a feature wraps them in its own versioned response.

// TokenActivitySource records what on-ledger construct produced a
// TokenActivityEvent.
type TokenActivitySource string

const (
	// ActivitySourceEventLog: parsed from an EventLog_HoldingsChange event
	// reported by the instrument admin — the authoritative holdings-change
	// history for a V2 instrument.
	ActivitySourceEventLog TokenActivitySource = "event_log"
	// ActivitySourceTransaction: derived from a raw ledger
	// transaction/exercise — a fallback when the admin reports no EventLog
	// events.
	ActivitySourceTransaction TokenActivitySource = "transaction"
)

// TokenActivityEvent is one row of a V2 instrument's activity history — a
// normalized view over an EventLog_HoldingsChange event (or raw
// transaction fallback).
type TokenActivityEvent struct {
	Source TokenActivitySource `json:"source"`
	// UpdateID / Offset / RecordTime locate the event on the ledger.
	UpdateID     string `json:"update_id"`
	Offset       int64  `json:"offset"`
	RecordTime   string `json:"record_time"` // RFC3339
	InstrumentID string `json:"instrument_id"`
	// Account is the party whose holdings changed; Admin reported the change.
	Account string `json:"account"`
	Admin   string `json:"admin"`
	// Consumed/Created counts are len(input/outputHoldingCids).
	ConsumedHoldingCount int `json:"consumed_holding_count"`
	CreatedHoldingCount  int `json:"created_holding_count"`
	// TransferLegs is empty for pure merge/split events.
	TransferLegs []TokenTransferLeg `json:"transfer_legs"`
	// Reason is the free-text change reason from the event metadata.
	Reason string `json:"reason,omitempty"`
}

// TokenTransferLeg mirrors one TransferLegSide from an
// EventLog_HoldingsChange (or an allocation transfer leg).
type TokenTransferLeg struct {
	// TransferLegID correlates the sender and receiver sides of a leg.
	TransferLegID string `json:"transfer_leg_id"`
	// Side is "sender" or "receiver"; Otherside is the opposite party.
	Side         string `json:"side"`
	Otherside    string `json:"otherside"`
	Amount       string `json:"amount"` // Daml Decimal string
	InstrumentID string `json:"instrument_id"`
}

// AllocationStatus is the lifecycle state of a V2 allocation, derived
// from its interface + registry state so both surfaces agree on the label.
type AllocationStatus string

const (
	// Pending: AllocationInstruction created, Allocation not yet finalized.
	AllocationStatusPending AllocationStatus = "pending"
	// Ready: a finalized Allocation exists and can be settled.
	AllocationStatusReady     AllocationStatus = "ready"
	AllocationStatusSettled   AllocationStatus = "settled"
	AllocationStatusCancelled AllocationStatus = "cancelled" // executors cancelled
	AllocationStatusWithdrawn AllocationStatus = "withdrawn" // authorizer withdrew
)

// Allocation is the shared detail view of one V2 allocation for DvP — a
// normalized projection of AllocationView + AllocationSpecification.
type Allocation struct {
	// ContractID is the Allocation (or AllocationInstruction) contract id.
	ContractID string           `json:"contract_id"`
	Status     AllocationStatus `json:"status"`
	// SettlementID (SettlementInfo.id) correlates allocations of one settlement.
	SettlementID string `json:"settlement_id"`
	Admin        string `json:"admin"`
	// Authorizer is the account owner funding the allocation.
	Authorizer string `json:"authorizer"`
	// Executors are the parties responsible for settling the batch.
	Executors []string `json:"executors"`
	// Committed: funds are locked until the deadline, no early withdraw.
	Committed bool `json:"committed"`
	// SettlementDeadline is the optional TTL (RFC3339); empty when unset.
	SettlementDeadline string             `json:"settlement_deadline,omitempty"`
	TransferLegs       []TokenTransferLeg `json:"transfer_legs"`
	CreatedAt          string             `json:"created_at,omitempty"` // RFC3339
}

// AllocationSummary is the compact list-row form of an Allocation.
type AllocationSummary struct {
	ContractID   string           `json:"contract_id"`
	Status       AllocationStatus `json:"status"`
	SettlementID string           `json:"settlement_id"`
	Authorizer   string           `json:"authorizer"`
	LegCount     int              `json:"leg_count"`
	Committed    bool             `json:"committed"`
}

// BatchActionResult is one action's outcome inside a BatchResult,
// mirroring one TokenStandardActionResult (order-preserved).
type BatchActionResult struct {
	// Kind names the token-standard action variant (e.g. "transfer_v2").
	Kind string `json:"kind"`
	// OK is true when the action completed (vs pending or failed).
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// BatchResult is the shared outcome of a BatchingUtility_ExecuteBatch
// exercise — the per-action results in submission order.
type BatchResult struct {
	// UpdateID is the ledger update id of the batch transaction.
	UpdateID string `json:"update_id"`
	// Actions are the per-action results, in submitted order.
	Actions []BatchActionResult `json:"actions"`
	// OK is true when every action succeeded.
	OK bool `json:"ok"`
}
