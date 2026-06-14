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
