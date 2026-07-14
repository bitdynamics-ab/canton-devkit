package types

// TokenRef is the registry-cached descriptor of a V2 token instrument
// the user has created on this LocalNet via `localnet token create`.
//
// Subsequent CLI invocations (`token mint`, `transfer`, `burn`,
// `balance`) and the Web UI Tokens screen resolve `--instrument
// <id|symbol>` against this map so the user doesn't have to retype the
// full (admin, id) pair every command.
//
// The map lives in registry.State.Tokens, keyed by symbol — symbols
// are user-chosen short strings and must be unique within an instance.
type TokenRef struct {
	// Name is the human-readable instrument name (e.g. "Retail Token").
	Name string `json:"name"`
	// Symbol is the short identifier (e.g. "RTK"). Map key as well.
	Symbol string `json:"symbol"`
	// Decimals is the number of fractional digits the instrument supports.
	Decimals int `json:"decimals"`
	// InitialSupply is the initial mint amount, as a decimal string.
	// Kept as string (not float / big.Int) because Daml Decimal has a
	// distinct fixed-precision representation we don't want to round.
	InitialSupply string `json:"initial_supply"`
	// IssuerParty is the canonical party ID of the admin/issuer.
	IssuerParty string `json:"issuer_party"`
	// InstrumentID is the V2 InstrumentId.id text — together with the
	// IssuerParty (which is the admin) forms the globally unique
	// `(admin, id)` tuple from `HoldingV2.InstrumentId`.
	InstrumentID string `json:"instrument_id"`
	// CreatedAt is RFC3339 — when this entry was recorded locally.
	CreatedAt string `json:"created_at"`
	// Status reflects on-ledger reality:
	//   "recorded"   — created in registry only; ledger submission deferred
	//                 (e.g. V2 LocalNet not yet up at create time).
	//   "submitted"  — issuer's instrument contract was successfully
	//                 created on the ledger.
	//   "failed:<err>" — last submission attempt errored; instrument is
	//                  still claimed in the local registry.
	Status string `json:"status"`
}

// TokenIdentityResponse is the act-as identity picker payload served by
// GET /api/tokens/identity. AvailableRoles is the full set of roles a
// LocalNet bring-up issues tokens for (UI-ordered, default first);
// CurrentRole echoes the role the request was made under (?role=,
// defaulting to app-user) so the switcher can highlight the active
// identity without a second round-trip. Same shape backs the CLI
// `token identity --format json`.
type TokenIdentityResponse struct {
	SchemaVersion  int      `json:"schema_version"`
	Instance       string   `json:"instance"`
	AvailableRoles []string `json:"available_roles"`
	CurrentRole    string   `json:"current_role"`
}

// TokenCreateRequest is the input shape for both `token create
// --non-interactive` (CLI) and `POST /api/tokens` (Web UI). Same struct
// so the CLI's --json output and the Web UI handler's request body are
// 1:1 identical — mirrors the CLI ↔ Web UI parity rule we follow for
// every other resource.
type TokenCreateRequest struct {
	Name          string `json:"name"`
	Symbol        string `json:"symbol"`
	Decimals      int    `json:"decimals"`
	InitialSupply string `json:"initial_supply"`
	Issuer        string `json:"issuer"`
}

// HoldingSource records WHERE a balance row came from so neither
// surface presents a fabricated number as real on-ledger state.
// The CLI's `token balance` JSON and the Web UI holdings table both
// branch on it.
type HoldingSource string

const (
	// HoldingSourceLedger means the amount was summed from the
	// participant's live Active Contract Set (HoldingViewV2 records).
	// This is the real on-ledger balance.
	HoldingSourceLedger HoldingSource = "ledger"
	// HoldingSourceRegistry means the row is the registry-derived
	// pseudo-balance (issuer holds InitialSupply, everyone else 0) —
	// a local bookkeeping placeholder, NOT on-ledger truth. Surfaced
	// when no live participant endpoint is reachable. UIs MUST label
	// these rows so a user can't mistake them for real holdings.
	HoldingSourceRegistry HoldingSource = "registry"
)

// TokenHolding is one balance row — instrument + party + summed
// amount, plus the Source that produced it. Mirrors
// internal/localnet/token.BalanceRow byte-for-byte (same JSON tags);
// that package keeps its own copy so it doesn't depend on api/types.
// This declaration is the source of truth for the JSON shape — adding
// a field requires updating both.
type TokenHolding struct {
	// InstrumentSymbol is the friendly ticker when the instrument is
	// recorded in the local registry; omitted for an unknown holding.
	InstrumentSymbol string `json:"instrument_symbol,omitempty"`
	// InstrumentID is the V2 InstrumentId.id text.
	InstrumentID string `json:"instrument_id"`
	// Party is the holding owner's canonical party id.
	Party string `json:"party"`
	// Amount is the summed balance as a Daml Decimal string.
	Amount string `json:"amount"`
	// Source distinguishes a live-ACS sum from the registry fallback.
	Source HoldingSource `json:"source"`
}

// TokenHoldingsResponse is the top-level body of
// `GET /api/tokens/{symbol}/holdings` and the CLI `token balance
// --format json` output. Shared so the two surfaces cannot drift,
// and schema-pinned via schema_pin_test.
type TokenHoldingsResponse struct {
	SchemaVersion int `json:"schema_version"`
	// Source is the response-level provenance: "ledger" when the rows
	// were summed from a reachable participant, "registry" when the
	// live endpoint was missing/unreachable and the rows are the
	// pseudo-balance fallback. Per-row Source matches this — it is
	// lifted to the top level so the UI can render one disclaimer
	// banner without scanning every row.
	Source HoldingSource `json:"source"`
	// Holdings is the (possibly empty) set of balance rows. Never
	// null on the wire — an empty live ACS yields [].
	Holdings []TokenHolding `json:"holdings"`
	// Truncated is true when the live ACS scan hit its safety cap and
	// stopped early, so the rows are a partial view. Omitted (false)
	// on the common complete-scan and registry-fallback paths. The UI
	// renders a "showing N of many" hint when set.
	Truncated bool `json:"truncated,omitempty"`
}

// --- V2 foundation shapes -------------------------------------------
//
// Shared structs the later V2 feature surfaces (EventLog history,
// allocations/DvP, BatchingUtilityV2) emit on BOTH the CLI --json and
// the Web UI REST/SSE payloads. Declared here so the two surfaces
// cannot drift; pinned by schema_shape_test's golden. None of these is
// a top-level response body (no SchemaVersion) — a feature wraps them
// in its own versioned response when it lands.

// TokenActivitySource records what on-ledger construct produced a
// TokenActivityEvent so the UI can label a row's provenance without the
// feature agent inventing its own enum.
type TokenActivitySource string

const (
	// ActivitySourceEventLog means the row was parsed from an
	// EventLog_HoldingsChange event reported by the instrument admin
	// (splice-api-token-transfer-events-v2). This is the authoritative
	// holdings-change history for a V2 instrument.
	ActivitySourceEventLog TokenActivitySource = "event_log"
	// ActivitySourceTransaction means the row was derived from a raw
	// ledger transaction/exercise, not an EventLog event — a fallback
	// used when the instrument admin does not report EventLog events.
	ActivitySourceTransaction TokenActivitySource = "transaction"
)

// TokenActivityEvent is one row of a V2 instrument's activity history —
// a normalized view over an EventLog_HoldingsChange event (or a raw
// transaction fallback). The EventLog history feature emits a stream of
// these; the fields mirror EventLog_HoldingsChange's payload.
type TokenActivityEvent struct {
	// Source is where the row came from (EventLog vs raw transaction).
	Source TokenActivitySource `json:"source"`
	// UpdateID is the ledger update (transaction) id the event belongs to.
	UpdateID string `json:"update_id"`
	// Offset is the ledger offset for ordering/paging the history.
	Offset int64 `json:"offset"`
	// RecordTime is the ledger record time as RFC3339.
	RecordTime string `json:"record_time"`
	// InstrumentID is the V2 InstrumentId.id text the change is for.
	InstrumentID string `json:"instrument_id"`
	// Account is the party whose holdings changed (EventLog `account`).
	Account string `json:"account"`
	// Admin is the instrument admin that reported the change.
	Admin string `json:"admin"`
	// ConsumedHoldingCount is len(inputHoldingCids) — holdings archived.
	ConsumedHoldingCount int `json:"consumed_holding_count"`
	// CreatedHoldingCount is len(outputHoldingCids) — holdings created.
	CreatedHoldingCount int `json:"created_holding_count"`
	// TransferLegs are the transfer legs that caused the change; empty
	// for pure merge/split events.
	TransferLegs []TokenTransferLeg `json:"transfer_legs"`
	// Reason is the free-text change reason from the event metadata
	// (`splice.lfdecentralizedtrust.org/reason`), if present.
	Reason string `json:"reason,omitempty"`
}

// TokenTransferLeg mirrors one TransferLegSide from an
// EventLog_HoldingsChange (or an allocation transfer leg) — enough for
// a UI row without the on-ledger contract-id plumbing.
type TokenTransferLeg struct {
	// TransferLegID correlates the sender and receiver sides of a leg.
	TransferLegID string `json:"transfer_leg_id"`
	// Side is "sender" or "receiver" (EventLog TransferSide).
	Side string `json:"side"`
	// Otherside is the party on the opposite side of the leg.
	Otherside string `json:"otherside"`
	// Amount is the leg amount as a Daml Decimal string.
	Amount string `json:"amount"`
	// InstrumentID is the leg's instrument id text.
	InstrumentID string `json:"instrument_id"`
}

// AllocationStatus is the lifecycle state of a V2 allocation, derived
// from its interface + registry state so both surfaces agree on the
// label shown to the user.
type AllocationStatus string

const (
	// AllocationStatusPending means an AllocationInstruction is created
	// but the Allocation itself is not yet finalized.
	AllocationStatusPending AllocationStatus = "pending"
	// AllocationStatusReady means a finalized Allocation contract exists
	// and can be settled.
	AllocationStatusReady AllocationStatus = "ready"
	// AllocationStatusSettled means the allocation was settled.
	AllocationStatusSettled AllocationStatus = "settled"
	// AllocationStatusCancelled means the executors cancelled it.
	AllocationStatusCancelled AllocationStatus = "cancelled"
	// AllocationStatusWithdrawn means the authorizer withdrew it.
	AllocationStatusWithdrawn AllocationStatus = "withdrawn"
)

// Allocation is the shared detail view of one V2 allocation for DvP —
// a normalized projection of AllocationView + AllocationSpecification
// (splice-api-token-allocation-v2). Both surfaces render this; feature
// wraps it in its own versioned response.
type Allocation struct {
	// ContractID is the on-ledger Allocation (or AllocationInstruction)
	// contract id.
	ContractID string `json:"contract_id"`
	// Status is the lifecycle label.
	Status AllocationStatus `json:"status"`
	// SettlementID is SettlementInfo.id — correlates allocations of the
	// same settlement.
	SettlementID string `json:"settlement_id"`
	// Admin is the instrument admin of the allocated instruments.
	Admin string `json:"admin"`
	// Authorizer is the party authorizing the transfers (the account
	// owner funding the allocation).
	Authorizer string `json:"authorizer"`
	// Executors are the parties responsible for settling the batch.
	Executors []string `json:"executors"`
	// Committed is AllocationSpecification.committed — funds are locked
	// until the settlement deadline and cannot be withdrawn early.
	Committed bool `json:"committed"`
	// SettlementDeadline is the optional TTL as RFC3339; empty when unset.
	SettlementDeadline string `json:"settlement_deadline,omitempty"`
	// TransferLegs are the transfer leg sides this allocation authorizes.
	TransferLegs []TokenTransferLeg `json:"transfer_legs"`
	// CreatedAt is when the allocation was originally created (RFC3339).
	CreatedAt string `json:"created_at,omitempty"`
}

// AllocationSummary is the compact list-row form of an Allocation for
// the allocations table — enough to render a row and link to the detail.
type AllocationSummary struct {
	// ContractID is the on-ledger contract id.
	ContractID string `json:"contract_id"`
	// Status is the lifecycle label.
	Status AllocationStatus `json:"status"`
	// SettlementID correlates allocations of the same settlement.
	SettlementID string `json:"settlement_id"`
	// Authorizer is the funding party.
	Authorizer string `json:"authorizer"`
	// LegCount is the number of transfer legs the allocation authorizes.
	LegCount int `json:"leg_count"`
	// Committed marks a committed (non-withdrawable) allocation.
	Committed bool `json:"committed"`
}

// BatchActionResult is one action's outcome inside a BatchResult,
// mirroring one TokenStandardActionResult from
// BatchingUtility_ExecuteBatchResult.actionResults (order-preserved).
type BatchActionResult struct {
	// Kind names the token-standard action variant (e.g.
	// "transfer_v2", "allocation_accept_v2") so the UI can label the row.
	Kind string `json:"kind"`
	// OK is true when the action produced a completed result (vs pending
	// or failed).
	OK bool `json:"ok"`
	// Detail is an optional human-readable outcome note.
	Detail string `json:"detail,omitempty"`
}

// BatchResult is the shared outcome of a BatchingUtility_ExecuteBatch
// exercise — the per-action results in submission order. Both surfaces
// render this; feature wraps it in its own versioned response.
type BatchResult struct {
	// UpdateID is the ledger update id of the batch transaction.
	UpdateID string `json:"update_id"`
	// Actions are the per-action results, in the same order as the
	// submitted actions (BatchingUtility_ExecuteBatchResult.actionResults).
	Actions []BatchActionResult `json:"actions"`
	// OK is true when every action succeeded.
	OK bool `json:"ok"`
}
