package token

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// ErrNeedsV2LocalNet signals that the requested action needs the V2
// Splice LocalNet to be up with the splice-test-token-v2 DAR vetted.
// CLI maps to ExitUserError; HTTP handler maps to 412 Precondition
// Failed so the Web UI can render a meaningful remediation.
var ErrNeedsV2LocalNet = errors.New(
	"V2 ledger action not yet wired: bring up a V2 LocalNet first via " +
		"`localnet up --version token-standard-v2 --profile tokens-v2`, " +
		"then re-run")

// ErrUnsupportedOnInstrument signals that the asset implementing the
// instrument doesn't expose a standard mint or burn choice. Amulet is
// the most common case — it doesn't implement BurnMintFactoryV1, and
// the V2 Token Standard has no generic mint/burn interface. Real
// tokens that want these operations either implement BurnMintFactoryV1
// or expose asset-specific choices on the issuer's TokenRules contract.
// CLI maps to ExitUserError; HTTP handler maps to 422 Unprocessable
// Entity so the UI can render "this instrument doesn't support that
// operation — use the asset's wallet UI" instead of a wiring error.
var ErrUnsupportedOnInstrument = errors.New(
	"this instrument doesn't implement the V2 standard's mint/burn surface " +
		"(Amulet has no BurnMintFactoryV1, and V2 defines no generic mint/burn). " +
		"Use the asset's own wallet UI for issuance changes, or create a " +
		"splice-test-token-v2 instrument via `localnet token create` for a token " +
		"that implements asset-specific mint via TokenRules_OfferMint")

// MintOptions / TransferOptions / BurnOptions / BalanceOptions are the
// input shapes for each action. In the orchestration layer (not the CLI
// package) so the HTTP handler can populate them from a JSON body.
type MintOptions struct {
	Instance   string // required
	Instrument string // symbol OR raw instrument id; symbol resolves via ResolveBySymbol
	To         string // recipient party
	Amount     string // decimal string

	// Endpoint, when set, runs the live asset-specific mint
	// (TokenRules_OfferMint) for a splice-test-token-v2 instrument the
	// issuer created on-ledger. Empty Endpoint, or an instrument with
	// no asset-specific mint path (e.g. Amulet), yields
	// ErrUnsupportedOnInstrument.
	Endpoint string
	Role     string
	Insecure bool
}
type TransferOptions struct {
	Instance   string
	Instrument string
	From       string
	To         string
	Amount     string
	NoWait     bool // if true, return the TransferInstruction id without waiting for accept
	Reason     string

	// AutoAccept chains the receiver-side accept onto the
	// transfer when the receiver is locally controlled — the common
	// LocalNet case where you own both parties. The transfer produces a
	// pending TransferInstruction; with AutoAccept the same flow then
	// exercises Accept as the receiver, so a transfer lands in one step.
	// Ignored when NoWait is set (the two are contradictory).
	AutoAccept bool

	// Atomic routes an AutoAccept on-ledger transfer through the wallet's
	// BatchingUtilityV2 so the transfer + receiver-accept commit in ONE
	// all-or-nothing transaction (see batch.go). Default (false) keeps the
	// sequential offer→accept path, which preserves partial-recovery: a
	// committed offer whose accept later fails is retryable, whereas a
	// failed batch rolls back entirely. Only takes effect for on-ledger
	// instruments with AutoAccept set and NoWait clear.
	Atomic bool

	// Live-submit fields. When Endpoint is set, RunTransfer performs
	// the full V2 flow: ACS query → POST /transfer-factory → ledger
	// exercise. Empty Endpoint surfaces the legacy ErrNeedsV2LocalNet
	// stub for callers that haven't been updated to provide one.
	Endpoint    string
	Token       string
	Role        string
	Insecure    bool
	RegistryURL string // off-ledger token registry base URL (asset-specific)
}
type AcceptOptions struct {
	Instance              string
	TransferInstructionID string

	// Party is the receiver acting on the instruction. Optional —
	// empty falls back to the first Act-As party granted to the JWT
	// (correct for single-party participants). Set explicitly when the
	// participant hosts several parties and the receiver isn't the
	// alphabetically-first one.
	Party string

	// Gen is the instruction's token-standard generation when the caller
	// already knows it (e.g. the in-process auto-accept after a transfer).
	// Zero means "derive it" — runAcceptLive inspects the instruction
	// contract rather than guessing from the vetted surfaces, which would
	// misroute a V1 instruction on a participant that also vets V2.
	Gen Generation

	// Same live-submit envelope as TransferOptions.
	Endpoint    string
	Token       string
	Role        string
	Insecure    bool
	RegistryURL string
}
type BurnOptions struct {
	Instance   string
	Instrument string
	From       string // party whose holding is burned
	Amount     string

	// Endpoint, when set, runs the live burn for an on-ledger
	// test-token instrument: a transfer of the holder's tokens to the
	// instrument's special burn account. Amulet / registry-only
	// instruments yield ErrUnsupportedOnInstrument.
	Endpoint string
	Token    string
	Role     string
	Insecure bool
}
type BalanceOptions struct {
	Instance   string
	Party      string // optional; empty = every party visible on the participant
	Instrument string // optional; empty = every instrument

	// Endpoint, Token, Role, Insecure are the live-ACS path. When
	// Endpoint is set, RunBalance ACS-queries the participant for
	// every HoldingInterfaceV2 contract and sums the views per
	// (party, instrument). Empty Endpoint falls back to the
	// registry-derived pseudo-balances.
	//
	// Token is optional: empty triggers per-role JWT auto-issuance
	// via registry.State.Credentials → splice.SignToken (see
	// dialLedger's token resolution chain). Role defaults to
	// "app-user" — pass "sv" or "app-provider" to dial a different
	// participant.
	Endpoint string
	Token    string
	Role     string
	Insecure bool

	// Limit caps result counts on paged read paths (e.g. the activity
	// feed). Zero means the caller's default applies.
	Limit int
}

// maxHoldingsScan caps how many ACS contracts runBalanceLive consumes
// before declaring the result truncated — a guard against a runaway
// ledger OOM-ing us via the unbounded gRPC stream. On overflow the caller
// gets truncated=true so the UI can show "showing N of many".
const maxHoldingsScan = 10_000

// BalanceRow is one row of the balance response — instrument + party +
// summed amount across the participant's visible ACS holdings.
//
// MIRRORS internal/api/types.TokenHolding byte-for-byte (same JSON tags);
// a local copy so this package doesn't depend on api/types. Adding a
// field requires updating both.
type BalanceRow struct {
	InstrumentSymbol string `json:"instrument_symbol,omitempty"`
	InstrumentID     string `json:"instrument_id"`
	Party            string `json:"party"`
	Amount           string `json:"amount"`
	// Source records whether this row was summed from the live ACS
	// ("ledger") or synthesized from the registry pseudo-balance
	// ("registry"). Every row carries its provenance so neither surface
	// can present the fallback as a real holding.
	Source HoldingSource `json:"source"`
}

// HoldingSource is the provenance of a BalanceRow. Mirrors
// api/types.HoldingSource — the wire values ("ledger"/"registry") must
// match. Defined here too so this package stays free of an api/types
// import (which would create a cycle as shared types grow).
type HoldingSource string

const (
	// SourceLedger — summed from the participant's live ACS.
	SourceLedger HoldingSource = "ledger"
	// SourceRegistry — the registry-derived pseudo-balance fallback
	// (issuer holds InitialSupply, everyone else 0). NOT on-ledger
	// truth; surfaced only when no live participant endpoint is
	// reachable.
	SourceRegistry HoldingSource = "registry"
)

// RunMint mints supply of an on-ledger test-token instrument via its
// asset-specific TokenRules_OfferMint choice (requires Endpoint and a
// TokenRef with status "on-ledger"). Everything else — Amulet,
// registry-only instruments, or a missing Endpoint — returns
// ErrUnsupportedOnInstrument: Amulet doesn't implement
// BurnMintFactoryV1 and there's no generic V2 mint interface.
func RunMint(ctx context.Context, out io.Writer, opts MintOptions) error {
	if err := requireFields("mint", opts.Instance, opts.Instrument, opts.To, opts.Amount); err != nil {
		return err
	}
	opts.To = ResolveAlias(aliasMapForInstance(opts.Instance), opts.To)
	if err := validatePartyID("--to", opts.To); err != nil {
		return err
	}
	if err := validateAmount("mint", opts.Amount); err != nil {
		return err
	}
	if opts.Role == "" {
		opts.Role = "app-user"
	}
	// Auto-discover the endpoint from the instance's captured ledger port
	// when none was passed, so a live mint (and its self-mint guard) works
	// flag-free on a running LocalNet. Explicit --endpoint wins.
	if opts.Endpoint == "" {
		opts.Endpoint = ResolveLedgerEndpoint(opts.Instance, opts.Role)
	}
	ref := instrumentRefOrRaw(opts.Instance, opts.Instrument)
	// Live mint only for on-ledger test-token instruments. Amulet and
	// registry-only instruments have no asset-specific mint.
	if opts.Endpoint != "" && ref.Status == "on-ledger" {
		return runMintLive(ctx, out, opts, ref)
	}
	emit(out, "mint", map[string]any{
		"instrument": ref, "to": opts.To, "amount": opts.Amount,
	})
	return ErrUnsupportedOnInstrument
}

// RunTransfer dispatches: if opts.Endpoint is set, run the live V2
// transfer flow (ACS → registry → exercise). Otherwise surface
// ErrNeedsV2LocalNet so callers that haven't been updated to thread
// through the endpoint get a clear remediation.
func RunTransfer(ctx context.Context, out io.Writer, opts TransferOptions) error {
	if err := requireFields("transfer", opts.Instance, opts.Instrument, opts.From, opts.To, opts.Amount); err != nil {
		return err
	}
	aliases := aliasMapForInstance(opts.Instance)
	opts.From = ResolveAlias(aliases, opts.From)
	opts.To = ResolveAlias(aliases, opts.To)
	if err := validatePartyID("--from", opts.From); err != nil {
		return err
	}
	if err := validatePartyID("--to", opts.To); err != nil {
		return err
	}
	if err := validateAmount("transfer", opts.Amount); err != nil {
		return err
	}
	if opts.Role == "" {
		opts.Role = "app-user"
	}
	// Auto-discover the endpoint from the instance's captured ledger port
	// when none was passed. Explicit --endpoint wins; if still empty (no
	// live participant) fall through to the ErrNeedsV2LocalNet remediation.
	if opts.Endpoint == "" {
		opts.Endpoint = ResolveLedgerEndpoint(opts.Instance, opts.Role)
	}
	if opts.Endpoint == "" {
		emit(out, "transfer", map[string]any{
			"from": opts.From, "to": opts.To, "amount": opts.Amount,
		})
		return ErrNeedsV2LocalNet
	}

	// On-ledger instruments transfer by exercising the issuer's TokenRules
	// contract (which IS the V2 TransferFactory, admin = issuer) directly.
	// The off-ledger path targets the scan registry's Amulet factory
	// (admin = DSO), which rejects an issuer-administered instrument with
	// an admin-mismatch assertion.
	ref := instrumentRefOrRaw(opts.Instance, opts.Instrument)
	if ref.Status == "on-ledger" {
		instructionID, err := runTransferOnLedgerFn(ctx, out, opts, ref)
		if err != nil {
			return err
		}
		emit(out, "transfer complete", map[string]any{
			"transfer_instruction_id": instructionID,
		})
		return nil
	}
	return runTransferOffLedgerFn(ctx, out, opts)
}

// runTransferOffLedger is the off-ledger (Amulet / external-registry)
// transfer flow: POST to the asset's scan registry for a factory + choice
// context, exercise it on-ledger, then optionally chain the receiver-side
// accept. Split out so tests can pin it via the runTransferOffLedgerFn
// seam.
func runTransferOffLedger(ctx context.Context, out io.Writer, opts TransferOptions) error {
	instructionID, gen, err := runTransferLive(ctx, out, opts)
	if err != nil {
		return err
	}
	emit(out, "transfer complete", map[string]any{
		"transfer_instruction_id": instructionID,
	})

	// Auto-accept chains the receiver-side accept (on LocalNet you own the
	// receiver). NoWait opts out; an empty instructionID means the
	// transfer already settled — nothing to accept.
	if opts.AutoAccept && !opts.NoWait && instructionID != "" {
		acc := AcceptOptions{
			Instance:              opts.Instance,
			TransferInstructionID: instructionID,
			Party:                 opts.To,
			// Thread the transfer's generation so the accept routes the
			// same way — never re-derived from surfaces, which would
			// misroute a V1 instruction on a dual-surface participant.
			Gen:         gen,
			Endpoint:    opts.Endpoint,
			Token:       opts.Token,
			Role:        opts.Role,
			Insecure:    opts.Insecure,
			RegistryURL: opts.RegistryURL,
		}
		if err := RunAccept(ctx, out, acc); err != nil {
			return fmt.Errorf("auto-accept transfer %s: %w", instructionID, err)
		}
		emit(out, "transfer accepted", map[string]any{
			"transfer_instruction_id": instructionID,
			"receiver":                opts.To,
		})
	}
	return nil
}

func RunAccept(ctx context.Context, out io.Writer, opts AcceptOptions) error {
	if err := requireFields("transfer accept", opts.Instance, opts.TransferInstructionID); err != nil {
		return err
	}
	// Resolve a --party alias to its full party id: both the on-ledger
	// detection (which puts the receiver in an ACS party filter) and the
	// off-ledger path need a real party id, not an alias.
	opts.Party = ResolveAlias(aliasMapForInstance(opts.Instance), opts.Party)
	if opts.Role == "" {
		opts.Role = "app-user"
	}
	// Auto-discover the endpoint from the instance's captured ledger port
	// when none was passed. Explicit --endpoint wins; if still empty fall
	// through to the ErrNeedsV2LocalNet remediation.
	if opts.Endpoint == "" {
		opts.Endpoint = ResolveLedgerEndpoint(opts.Instance, opts.Role)
	}
	if opts.Endpoint == "" {
		emit(out, "transfer accept", map[string]any{
			"transfer_instruction_id": opts.TransferInstructionID,
		})
		return ErrNeedsV2LocalNet
	}
	// An issuer-administered test-token offer is accepted on-ledger; only
	// Amulet / external-registry instructions go through the off-ledger
	// choice-context endpoint (runAcceptOnLedgerIfTestToken reports
	// handled=false for those).
	if handled, err := runAcceptOnLedgerFn(ctx, out, opts); handled || err != nil {
		return err
	}
	return runAcceptOffLedgerFn(ctx, out, opts)
}

// RunBurn burns supply of an on-ledger test-token instrument by
// transferring the holder's tokens to the instrument's special burn
// account. Amulet / registry-only instruments have no such path and
// yield ErrUnsupportedOnInstrument.
func RunBurn(ctx context.Context, out io.Writer, opts BurnOptions) error {
	if err := requireFields("burn", opts.Instance, opts.Instrument, opts.From, opts.Amount); err != nil {
		return err
	}
	opts.From = ResolveAlias(aliasMapForInstance(opts.Instance), opts.From)
	if err := validatePartyID("--from", opts.From); err != nil {
		return err
	}
	if err := validateAmount("burn", opts.Amount); err != nil {
		return err
	}
	if opts.Role == "" {
		opts.Role = "app-user"
	}
	// Auto-discover the endpoint from the instance's captured ledger port
	// when none was passed. Explicit --endpoint wins.
	if opts.Endpoint == "" {
		opts.Endpoint = ResolveLedgerEndpoint(opts.Instance, opts.Role)
	}
	ref := instrumentRefOrRaw(opts.Instance, opts.Instrument)
	if opts.Endpoint != "" && ref.Status == "on-ledger" {
		return runBurnLive(ctx, out, opts, ref)
	}
	emit(out, "burn", map[string]any{
		"instrument": ref, "from": opts.From, "amount": opts.Amount,
	})
	return ErrUnsupportedOnInstrument
}

// RunBalance returns per-(party, instrument) balances. When a live
// participant endpoint is available (explicit or auto-discovered from
// the registry) it sums the ACS holdings; otherwise it falls back to
// registry-derived pseudo-balances (Amount = InitialSupply when
// --party matches the issuer, zero otherwise), tagged SourceRegistry
// so no caller can present them as on-ledger truth. The bool result
// reports whether the live scan was truncated at maxHoldingsScan.
func RunBalance(ctx context.Context, out io.Writer, opts BalanceOptions) ([]BalanceRow, bool, error) {
	if opts.Instance == "" {
		return nil, false, errors.New("instance is required")
	}
	opts.Party = ResolveAlias(aliasMapForInstance(opts.Instance), opts.Party)
	// Auto-discover the endpoint from the registry when none was passed,
	// so both surfaces get the live ACS by default. Explicit wins.
	if opts.Endpoint == "" {
		opts.Endpoint = ResolveLedgerEndpoint(opts.Instance, opts.Role)
	}
	if opts.Endpoint != "" {
		return runBalanceLive(ctx, opts)
	}
	// Registry fallback: no live participant. Rows are pseudo-balances
	// (issuer holds InitialSupply, everyone else 0), tagged SourceRegistry
	// so the caller can flag them as not-on-ledger.
	state, err := registry.Read(opts.Instance)
	if err != nil {
		return nil, false, fmt.Errorf("read instance state: %w", err)
	}
	rows := make([]BalanceRow, 0, len(state.Tokens))
	for _, t := range state.Tokens {
		if opts.Instrument != "" && opts.Instrument != t.Symbol && opts.Instrument != t.InstrumentID {
			continue
		}
		party := opts.Party
		if party == "" {
			party = t.IssuerParty
		}
		amount := "0"
		if party == t.IssuerParty {
			amount = t.InitialSupply
		}
		rows = append(rows, BalanceRow{
			InstrumentSymbol: t.Symbol,
			InstrumentID:     t.InstrumentID,
			Party:            party,
			Amount:           amount,
			Source:           SourceRegistry,
		})
	}
	return rows, false, nil
}

// BalanceSource reports which path RunBalance will take for opts —
// "ledger" when a participant endpoint is available (explicit or
// auto-discovered), "registry" otherwise. Callers set the response-level
// provenance from it. Pure: endpoint availability, not reachability,
// decides the path — matching RunBalance's own branch.
func BalanceSource(opts BalanceOptions) HoldingSource {
	if opts.Endpoint != "" || ResolveLedgerEndpoint(opts.Instance, opts.Role) != "" {
		return SourceLedger
	}
	return SourceRegistry
}

// runBalanceLive streams every vetted-generation Holding from the
// participant, parses the view, and sums amounts per (party, instrument).
// Symbols come from the local registry when a streamed instrument matches
// a recorded TokenRef; otherwise the row carries the raw `(admin, id)`
// pair. addDecimal sums the textual Daml Decimals without precision loss.
func runBalanceLive(ctx context.Context, opts BalanceOptions) ([]BalanceRow, bool, error) {
	// Cancel on every return path so an early break (cap, decode error)
	// doesn't leak the gRPC stream pump goroutine.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	state, err := registry.Read(opts.Instance)
	if err != nil {
		return nil, false, fmt.Errorf("read instance state: %w", err)
	}
	// Resolve the optional --instrument filter (symbol or raw
	// InstrumentID) into the (admin, id) pair we match streamed views on.
	var wantAdmin, wantID string
	if opts.Instrument != "" {
		if ref, ok := state.Tokens[opts.Instrument]; ok {
			wantAdmin, wantID = ref.IssuerParty, ref.InstrumentID
		} else {
			wantID = opts.Instrument
		}
	}

	conn := LedgerConn{
		Endpoint: opts.Endpoint,
		Token:    opts.Token,
		Insecure: opts.Insecure,
		Instance: opts.Instance,
		Role:     opts.Role,
	}
	client, cleanup, err := dialLedgerFn(ctx, conn)
	if err != nil {
		return nil, false, err
	}
	defer cleanup()

	end, err := client.LedgerEnd(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("ledger end: %w", err)
	}

	var parties []string
	if opts.Party != "" {
		parties = []string{opts.Party}
	} else {
		// Canton's wildcard (FiltersForAnyParty) path needs a super-reader
		// / ParticipantAdmin claim the per-role tokens don't carry, so
		// enumerate the role's local parties and submit them in
		// FiltersByParty for per-party gating instead.
		discovered, err := localPartiesForRole(ctx, client, opts.Role)
		if err != nil {
			return nil, false, fmt.Errorf("discover local parties for role %q: %w", opts.Role, err)
		}
		parties = discovered
	}
	if len(parties) == 0 {
		// No parties → empty FiltersByParty (which Canton rejects), and
		// nothing to report anyway.
		return nil, false, nil
	}
	// Query every vetted holding surface so a V1 token like Amulet is
	// summed on a stable release, not just V2.
	surfaces, err := discoverTokenSurfaces(ctx, client)
	if err != nil {
		return nil, false, err
	}
	if !surfaces.Any() {
		return nil, false, nil
	}
	req := ledger.ActiveContractsRequest{
		ActiveAtOffset: end.Offset,
		EventFormat:    holdingInterfaceFilter(surfaces, parties),
	}
	stream, err := client.ActiveContracts(ctx, req)
	if err != nil {
		return nil, false, fmt.Errorf("ACS query: %w", err)
	}

	type bucketKey struct{ admin, id, party string }
	bucket := map[bucketKey]string{}

	var scanned int
	var truncated bool
	for item := range stream {
		if item.Err != nil {
			return nil, false, fmt.Errorf("ACS stream: %w", item.Err)
		}
		scanned++
		if scanned > maxHoldingsScan {
			truncated = true
			break
		}
		// ContractEntry is a oneof — only the ActiveContract branch carries
		// the CreatedEvent we need; IncompleteAssigned/Unassigned events
		// are mid-reassignment and don't affect the balance.
		entry, ok := item.Value.ContractEntry.(*lapiv2.GetActiveContractsResponse_ActiveContract)
		if !ok || entry.ActiveContract == nil {
			continue
		}
		// One holding per contract (prefers the richer V2 view) — never
		// double-counted.
		hv, ok := extractBestHoldingView(entry.ActiveContract.GetCreatedEvent().GetInterfaceViews())
		if !ok || hv.Owner == "" {
			continue
		}
		if wantID != "" && hv.InstrumentID != wantID {
			continue
		}
		if wantAdmin != "" && hv.Admin != wantAdmin {
			continue
		}
		k := bucketKey{admin: hv.Admin, id: hv.InstrumentID, party: hv.Owner}
		sum, err := addDecimal(bucket[k], hv.Amount)
		if err != nil {
			return nil, false, fmt.Errorf("sum holding amounts: %w", err)
		}
		bucket[k] = sum
	}

	// Join back to the local symbol table for friendly rendering.
	symByID := make(map[string]string, len(state.Tokens))
	for _, t := range state.Tokens {
		symByID[t.IssuerParty+"\x00"+t.InstrumentID] = t.Symbol
	}
	rows := make([]BalanceRow, 0, len(bucket))
	for k, amount := range bucket {
		rows = append(rows, BalanceRow{
			InstrumentSymbol: symByID[k.admin+"\x00"+k.id],
			InstrumentID:     k.id,
			Party:            k.party,
			Amount:           amount,
			Source:           SourceLedger,
		})
	}
	return rows, truncated, nil
}

// addDecimal returns a + b for two Daml Decimal strings. Empty is treated
// as "0". Aligns fractional widths ("1.0" + "1" → "2.0") and adds as
// big.Ints so we don't depend on a big-decimal library.
func addDecimal(a, b string) (string, error) {
	if a == "" {
		a = "0"
	}
	if b == "" {
		b = "0"
	}
	intA, fracA := splitDecimal(a)
	intB, fracB := splitDecimal(b)
	if len(fracA) < len(fracB) {
		fracA = fracA + strings.Repeat("0", len(fracB)-len(fracA))
	} else if len(fracB) < len(fracA) {
		fracB = fracB + strings.Repeat("0", len(fracA)-len(fracB))
	}
	scale := len(fracA)
	aBig := new(big.Int)
	if _, ok := aBig.SetString(intA+fracA, 10); !ok {
		return "", fmt.Errorf("invalid decimal %q", a)
	}
	bBig := new(big.Int)
	if _, ok := bBig.SetString(intB+fracB, 10); !ok {
		return "", fmt.Errorf("invalid decimal %q", b)
	}
	sum := new(big.Int).Add(aBig, bBig).String()
	if scale == 0 {
		return sum, nil
	}
	if len(sum) <= scale {
		sum = strings.Repeat("0", scale+1-len(sum)) + sum
	}
	return sum[:len(sum)-scale] + "." + sum[len(sum)-scale:], nil
}

func splitDecimal(s string) (intPart, fracPart string) {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// instrumentRefOrRaw resolves the --instrument string to a recorded
// TokenRef, falling back to a bare ref carrying the string as the
// InstrumentID (Amulet and other unregistered instruments have no
// `token create` record).
func instrumentRefOrRaw(instance, ident string) registry.TokenRef {
	ref, err := resolveInstrument(instance, ident)
	if err != nil {
		return registry.TokenRef{InstrumentID: ident}
	}
	return ref
}

// resolveInstrument turns the --instrument string into a full TokenRef.
// It may be the symbol (the common path) or the raw InstrumentID (the
// escape hatch the HTTP handler uses with already-resolved IDs).
func resolveInstrument(instance, ident string) (registry.TokenRef, error) {
	if ident == "" {
		return registry.TokenRef{}, errors.New("--instrument is required")
	}
	state, err := registry.Read(instance)
	if err != nil {
		return registry.TokenRef{}, fmt.Errorf("read instance state: %w", err)
	}
	if t, ok := state.Tokens[ident]; ok {
		return t, nil
	}
	// Fall back to InstrumentID match when the caller passed the raw id.
	for _, t := range state.Tokens {
		if t.InstrumentID == ident {
			return t, nil
		}
	}
	return registry.TokenRef{}, fmt.Errorf(
		"no instrument %q on instance %q (run `localnet token create` first)",
		ident, instance)
}

// requireFields surfaces the same "field X is required" wording as
// the create wizard so every error in the token surface looks alike.
func requireFields(verb string, fields ...string) error {
	for i, v := range fields {
		if v == "" {
			return fmt.Errorf("%s: field at position %d is required", verb, i)
		}
	}
	return nil
}

// validateAmount rejects an --amount that isn't a positive Daml decimal
// before any action logic, so a plain input error is never mislabelled as
// ErrNeedsV2LocalNet.
func validateAmount(verb, amount string) error {
	if !looksLikeDecimal(amount) {
		return fmt.Errorf(
			"%s: --amount %q is not a valid decimal (digits and at most one '.', no sign or exponent)",
			verb, amount)
	}
	if isZeroDecimal(amount) {
		return fmt.Errorf("%s: --amount must be greater than zero", verb)
	}
	return nil
}

// isZeroDecimal reports whether a looksLikeDecimal-valid string is zero
// (only 0s and an optional dot). Cheap and allocation-free.
func isZeroDecimal(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= '1' && s[i] <= '9' {
			return false
		}
	}
	return true
}

// emit writes a "<verb>: {json}" progress line, JSON-encoded for a
// consistent surface across CLI and HTTP.
func emit(out io.Writer, verb string, payload map[string]any) {
	if out == nil {
		return
	}
	_, _ = fmt.Fprintf(out, "%s: ", verb)
	enc := json.NewEncoder(out)
	_ = enc.Encode(payload) // includes trailing newline
}
