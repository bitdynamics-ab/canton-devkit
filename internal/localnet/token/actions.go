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
		"then re-run; the V2 instrument-creation + transfer submission " +
		"lands incrementally on top of this PR")

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

// MintOptions / TransferOptions / BurnOptions / BalanceOptions are
// the input shapes for each action. Lifted into the orchestration
// layer (not the CLI package) so the HTTP handler can populate them
// directly from a JSON body without re-deriving the flag parsing.
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

	// AutoAccept (BIT-215 #3) chains the receiver-side accept onto the
	// transfer when the receiver is locally controlled — the common
	// LocalNet case where you own both parties. The transfer produces a
	// pending TransferInstruction; with AutoAccept the same flow then
	// exercises Accept as the receiver, so a transfer lands in one step.
	// Ignored when NoWait is set (the two are contradictory).
	AutoAccept bool

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

// maxHoldingsScan caps how many ACS contracts runBalanceLive will
// consume before declaring the result truncated. A real wallet
// rarely holds more than a few thousand contracts; the cap is a
// belt-and-braces guard against a runaway ledger pumping us into
// OOM (the gRPC stream is otherwise unbounded). When the cap fires
// the caller gets truncated=true so the UI can show "showing N of
// many" rather than silently misreporting the balance.
const maxHoldingsScan = 10_000

// BalanceRow is one row of the balance response — instrument + party
// + summed amount across the participant's visible ACS holdings.
type BalanceRow struct {
	InstrumentSymbol string `json:"instrument_symbol,omitempty"`
	InstrumentID     string `json:"instrument_id"`
	Party            string `json:"party"`
	Amount           string `json:"amount"`
}

// RunMint, RunTransfer, RunBurn, RunAccept currently surface
// ErrNeedsV2LocalNet — the ACS + ledger-submission wiring against
// the live V2 splice-test-token-v2 templates lands incrementally.
// Both the CLI and the HTTP handler call into here so the
// "not yet wired" surface is consistent across both surfaces, and
// the upgrade to a working submission is a one-package change.
//
// The functions still validate inputs + resolve symbols via the
// registry so unit-level wiring tests cover the flow. Callers that
// only want validation can pass a nil writer.

// RunMint always returns ErrUnsupportedOnInstrument for the V2 alpha:
// Amulet (the only seeded instrument) doesn't implement BurnMintFactoryV1
// and there's no generic V2 mint interface. The asset-specific
// TokenRules_OfferMint choice on splice-test-token-v2 is a future
// follow-up gated on the test-token DAR being uploaded + an instrument
// being created via the wizard.
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
	ref, err := resolveInstrument(opts.Instance, opts.Instrument)
	if err != nil {
		ref = registry.TokenRef{InstrumentID: opts.Instrument}
	}
	// Live mint path: only for instruments created on-ledger via the
	// test-token (status "on-ledger"). Amulet and registry-only
	// instruments have no asset-specific mint, so they keep returning
	// ErrUnsupportedOnInstrument.
	if opts.Endpoint != "" && ref.Status == "on-ledger" {
		if opts.Role == "" {
			opts.Role = "app-user"
		}
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
	if opts.Endpoint == "" {
		emit(out, "transfer", map[string]any{
			"from": opts.From, "to": opts.To, "amount": opts.Amount,
		})
		return ErrNeedsV2LocalNet
	}
	if opts.Role == "" {
		opts.Role = "app-user"
	}
	instructionID, err := runTransferLive(ctx, out, opts)
	if err != nil {
		return err
	}
	emit(out, "transfer complete", map[string]any{
		"transfer_instruction_id": instructionID,
	})

	// Auto-accept chains the receiver-side accept (BIT-215 #3): on
	// LocalNet you own the receiver, so the two-step offer→accept is just
	// ceremony. NoWait opts out (the caller wants the instruction id to
	// hand off). An empty instructionID means the transfer already
	// settled (e.g. self-transfer) — nothing to accept.
	if opts.AutoAccept && !opts.NoWait && instructionID != "" {
		acc := AcceptOptions{
			Instance:              opts.Instance,
			TransferInstructionID: instructionID,
			Party:                 opts.To,
			Endpoint:              opts.Endpoint,
			Token:                 opts.Token,
			Role:                  opts.Role,
			Insecure:              opts.Insecure,
			RegistryURL:           opts.RegistryURL,
		}
		if err := runAcceptLive(ctx, out, acc); err != nil {
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
	if opts.Endpoint == "" {
		emit(out, "transfer accept", map[string]any{
			"transfer_instruction_id": opts.TransferInstructionID,
		})
		return ErrNeedsV2LocalNet
	}
	if opts.Role == "" {
		opts.Role = "app-user"
	}
	return runAcceptLive(ctx, out, opts)
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
	ref, err := resolveInstrument(opts.Instance, opts.Instrument)
	if err != nil {
		ref = registry.TokenRef{InstrumentID: opts.Instrument}
	}
	if opts.Endpoint != "" && ref.Status == "on-ledger" {
		if opts.Role == "" {
			opts.Role = "app-user"
		}
		return runBurnLive(ctx, out, opts, ref)
	}
	emit(out, "burn", map[string]any{
		"instrument": ref, "from": opts.From, "amount": opts.Amount,
	})
	return ErrUnsupportedOnInstrument
}

// RunBalance is the one action that's fully functional today: it
// returns the recorded TokenRefs from registry.State.Tokens as
// pseudo-balances (Amount = InitialSupply when --party matches the
// issuer; otherwise zero). Callers that want the live ACS-derived
// balance need a running V2 LocalNet + the future ledger ACS query —
// that's the BIT-139 follow-up.
//
// When that follow-up lands, the ACS query uses HoldingInterfaceV2
// (see v2_surface.go for the qualified interface id and why V2 rather
// than V1) and sums the HoldingViewV2.amount for every contract whose
// view.account.owner matches --party. The synthetic issuer-only case
// goes away.
//
// Returning *something* here makes the Web UI's holdings table render
// right away on whatever instance the user is browsing, and gives a
// deterministic surface for tests.
func RunBalance(ctx context.Context, out io.Writer, opts BalanceOptions) ([]BalanceRow, bool, error) {
	if opts.Instance == "" {
		return nil, false, errors.New("instance is required")
	}
	opts.Party = ResolveAlias(aliasMapForInstance(opts.Instance), opts.Party)
	// Live-ACS path takes precedence when the caller has dialed a
	// participant. Symbol/admin pairs are joined back to the local
	// state.Tokens registry so the rendered rows can still carry
	// the friendly symbol when one exists.
	if opts.Endpoint != "" {
		return runBalanceLive(ctx, opts)
	}
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
		})
	}
	return rows, false, nil
}

// runBalanceLive is the V2-native ACS path: stream every contract
// that implements HoldingInterfaceV2 from the participant at
// Endpoint, parse the V2 Holding view, and sum amounts per (party,
// instrument).
//
// Symbols come from the local registry — when a streamed instrument
// matches a recorded TokenRef.InstrumentID we surface its symbol;
// otherwise the row just carries the raw `(admin, id)` pair so an
// unknown-instrument holding still renders.
//
// Numeric amounts are summed as decimal strings via summary
// concatenation? No — we add them. The participant emits each holding
// as a Daml Decimal (textual, up to 10 fractional digits in V1, 38 in
// V2). Adding them in Go without losing precision means using a big-
// decimal-style approach: split on '.', align scale, add as big.Ints.
// The helper addDecimal does that.
func runBalanceLive(ctx context.Context, opts BalanceOptions) ([]BalanceRow, bool, error) {
	// Cancelling on every return path tears down the gRPC stream
	// pump goroutine so an early break (cap, decode error) doesn't
	// leak it for the lifetime of the request context.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	state, err := registry.Read(opts.Instance)
	if err != nil {
		return nil, false, fmt.Errorf("read instance state: %w", err)
	}
	// Resolve the optional --instrument filter once into the form we
	// match against streamed views. The user can pass a symbol or a
	// raw InstrumentID; both turn into the same (admin, id) pair we
	// compare HoldingViewV2 against.
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
	client, cleanup, err := dialLedger(ctx, conn)
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
		// Canton's wildcard ("FiltersForAnyParty") path requires the
		// JWT to carry the super-reader / ParticipantAdmin claim. The
		// per-role user-tokens we mint only carry CanActAs/CanReadAs
		// on the local parties — so a wildcard query is rejected even
		// after the grant. Enumerate the role's local parties via
		// PartyManagement and submit them in FiltersByParty so Canton
		// gates per-party instead.
		discovered, err := localPartiesForRole(ctx, client, opts.Role)
		if err != nil {
			return nil, false, fmt.Errorf("discover local parties for role %q: %w", opts.Role, err)
		}
		parties = discovered
	}
	if len(parties) == 0 {
		// No parties means an empty FiltersByParty, which Canton
		// rejects. Return an empty balance set — the participant has
		// no parties hosted (or none matching the role prefix) so
		// there are no holdings to report.
		return nil, false, nil
	}
	req := ledger.ActiveContractsRequest{
		ActiveAtOffset: end.Offset,
		EventFormat:    holdingInterfaceFilterV2(parties),
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
			// Bail out and tell the caller we stopped early. cancel()
			// (deferred above) tears down the upstream pump.
			truncated = true
			break
		}
		// ContractEntry is a oneof — only the ActiveContract branch
		// carries the CreatedEvent we need. IncompleteAssigned /
		// IncompleteUnassigned events are mid-reassignment and don't
		// affect the V2 wallet balance.
		entry, ok := item.Value.ContractEntry.(*lapiv2.GetActiveContractsResponse_ActiveContract)
		if !ok || entry.ActiveContract == nil {
			continue
		}
		// CreatedEvent carries the parsed interface views.
		for _, iv := range entry.ActiveContract.GetCreatedEvent().GetInterfaceViews() {
			hv, ok := extractHoldingViewV2(iv)
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
	}

	// Join back to local symbol table for friendly rendering.
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
		})
	}
	return rows, truncated, nil
}

// addDecimal returns a + b for two Daml Decimal strings (e.g. "1.5",
// "1000000"). Empty a is treated as "0". Aligns fractional widths so
// "1.0" + "1" → "2.0" without losing scale. Uses big.Int over an
// aligned representation so we don't depend on a big-decimal library.
func addDecimal(a, b string) (string, error) {
	if a == "" {
		a = "0"
	}
	if b == "" {
		b = "0"
	}
	intA, fracA := splitDecimal(a)
	intB, fracB := splitDecimal(b)
	// Pad fractional parts to the same length.
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

// resolveInstrument turns the user's --instrument string into a full
// TokenRef. The string MAY be the symbol (the common path, looked up
// against state.Tokens) OR the raw InstrumentID (the escape hatch
// used by the HTTP handler with already-resolved IDs).
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
	// Fall back to InstrumentID match in case the caller passed the
	// raw id rather than the symbol (UI does this; CLI primarily
	// uses symbols).
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
// BEFORE the action stubs out. Without it, "abc" or "1.2e5" fell through
// to ErrNeedsV2LocalNet, mislabelling a plain input error as "this needs
// a V2 LocalNet". looksLikeDecimal pins the same digits-and-one-dot
// grammar the create wizard enforces.
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

// validatePartyID + partyIDPattern live in token.go (added by the
// create-wizard PR #82, which merged to main first). The mint/transfer/
// burn actions in this file reuse it; the duplicate copy that used to
// live here was removed on merge to avoid a redeclaration.

// emit writes a human-readable "going to run X with Y" line on the
// caller's writer before the action returns ErrNeedsV2LocalNet, so
// users see what would have happened. JSON-encoded for consistent
// surface across CLI and HTTP.
func emit(out io.Writer, verb string, payload map[string]any) {
	if out == nil {
		return
	}
	_, _ = fmt.Fprintf(out, "Planned %s: ", verb)
	enc := json.NewEncoder(out)
	_ = enc.Encode(payload) // includes trailing newline
}
