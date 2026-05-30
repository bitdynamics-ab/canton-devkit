package token

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
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
