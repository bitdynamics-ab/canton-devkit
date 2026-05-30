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

// MintOptions / TransferOptions / BurnOptions / BalanceOptions are
// the input shapes for each action. Lifted into the orchestration
// layer (not the CLI package) so the HTTP handler can populate them
// directly from a JSON body without re-deriving the flag parsing.
type MintOptions struct {
	Instance   string // required
	Instrument string // symbol OR raw instrument id; symbol resolves via ResolveBySymbol
	To         string // recipient party
	Amount     string // decimal string
}
type TransferOptions struct {
	Instance   string
	Instrument string
	From       string
	To         string
	Amount     string
	NoWait     bool // if true, return the TransferInstruction id without waiting for accept
	Reason     string
}
type AcceptOptions struct {
	Instance              string
	TransferInstructionID string
}
type BurnOptions struct {
	Instance   string
	Instrument string
	From       string // party whose holding is burned
	Amount     string
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
}

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

func RunMint(ctx context.Context, out io.Writer, opts MintOptions) error {
	if err := requireFields("mint", opts.Instance, opts.Instrument, opts.To, opts.Amount); err != nil {
		return err
	}
	if err := validatePartyID("--to", opts.To); err != nil {
		return err
	}
	if err := validateAmount("mint", opts.Amount); err != nil {
		return err
	}
	ref, err := resolveInstrument(opts.Instance, opts.Instrument)
	if err != nil {
		return err
	}
	emit(out, "mint", map[string]any{
		"instrument": ref, "to": opts.To, "amount": opts.Amount,
	})
	return ErrNeedsV2LocalNet
}

func RunTransfer(ctx context.Context, out io.Writer, opts TransferOptions) error {
	if err := requireFields("transfer", opts.Instance, opts.Instrument, opts.From, opts.To, opts.Amount); err != nil {
		return err
	}
	if err := validatePartyID("--from", opts.From); err != nil {
		return err
	}
	if err := validatePartyID("--to", opts.To); err != nil {
		return err
	}
	if err := validateAmount("transfer", opts.Amount); err != nil {
		return err
	}
	ref, err := resolveInstrument(opts.Instance, opts.Instrument)
	if err != nil {
		return err
	}
	emit(out, "transfer", map[string]any{
		"instrument": ref, "from": opts.From, "to": opts.To,
		"amount": opts.Amount, "no_wait": opts.NoWait, "reason": opts.Reason,
	})
	return ErrNeedsV2LocalNet
}

func RunAccept(ctx context.Context, out io.Writer, opts AcceptOptions) error {
	if err := requireFields("transfer accept", opts.Instance, opts.TransferInstructionID); err != nil {
		return err
	}
	emit(out, "transfer accept", map[string]any{
		"transfer_instruction_id": opts.TransferInstructionID,
	})
	return ErrNeedsV2LocalNet
}

func RunBurn(ctx context.Context, out io.Writer, opts BurnOptions) error {
	if err := requireFields("burn", opts.Instance, opts.Instrument, opts.From, opts.Amount); err != nil {
		return err
	}
	if err := validatePartyID("--from", opts.From); err != nil {
		return err
	}
	if err := validateAmount("burn", opts.Amount); err != nil {
		return err
	}
	ref, err := resolveInstrument(opts.Instance, opts.Instrument)
	if err != nil {
		return err
	}
	emit(out, "burn", map[string]any{
		"instrument": ref, "from": opts.From, "amount": opts.Amount,
	})
	return ErrNeedsV2LocalNet
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
func RunBalance(ctx context.Context, out io.Writer, opts BalanceOptions) ([]BalanceRow, error) {
	if opts.Instance == "" {
		return nil, errors.New("instance is required")
	}
	// Live-ACS path takes precedence when the caller has dialed a
	// participant. Symbol/admin pairs are joined back to the local
	// state.Tokens registry so the rendered rows can still carry
	// the friendly symbol when one exists.
	if opts.Endpoint != "" {
		return runBalanceLive(ctx, opts)
	}
	state, err := registry.Read(opts.Instance)
	if err != nil {
		return nil, fmt.Errorf("read instance state: %w", err)
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
	return rows, nil
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
func runBalanceLive(ctx context.Context, opts BalanceOptions) ([]BalanceRow, error) {
	state, err := registry.Read(opts.Instance)
	if err != nil {
		return nil, fmt.Errorf("read instance state: %w", err)
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
		return nil, err
	}
	defer cleanup()

	end, err := client.LedgerEnd(ctx)
	if err != nil {
		return nil, fmt.Errorf("ledger end: %w", err)
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
			return nil, fmt.Errorf("discover local parties for role %q: %w", opts.Role, err)
		}
		parties = discovered
	}
	if len(parties) == 0 {
		// No parties means an empty FiltersByParty, which Canton
		// rejects. Return an empty balance set — the participant has
		// no parties hosted (or none matching the role prefix) so
		// there are no holdings to report.
		return nil, nil
	}
	req := ledger.ActiveContractsRequest{
		ActiveAtOffset: end.Offset,
		EventFormat:    holdingInterfaceFilterV2(parties),
	}
	stream, err := client.ActiveContracts(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ACS query: %w", err)
	}

	type bucketKey struct{ admin, id, party string }
	bucket := map[bucketKey]string{}

	for item := range stream {
		if item.Err != nil {
			return nil, fmt.Errorf("ACS stream: %w", item.Err)
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
				return nil, fmt.Errorf("sum holding amounts: %w", err)
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
	return rows, nil
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
