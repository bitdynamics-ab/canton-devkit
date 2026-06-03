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
