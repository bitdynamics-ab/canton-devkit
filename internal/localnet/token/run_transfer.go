package token

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/big"
	"sort"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/registry"
	regstate "github.com/bitdynamics-ab/canton-devkit/internal/registry"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// V2 live transfer + accept orchestration. The CLI / handler dispatches
// here when TransferOptions.Endpoint is non-empty (Endpoint is the
// participant ledger gRPC `host:port`; we also need RegistryURL for the
// off-ledger half).
//
// Flow recap (sender):
//
//	1. dial ledger (auto-grants party rights on first dial)
//	2. ACS-query for sender's HoldingV2 contracts of this instrument
//	3. select input holdings greedily (smallest first)
//	4. POST /registry/transfer-instruction/v2/transfer-factory
//	5. exercise TransferFactory_Transfer (with disclosed factory contracts)
//	6. parse SubmitAndWaitForTransaction response — surface the
//	   created TransferInstruction contract id (Offer kind) or the
//	   receiver's new Holding (Direct kind, no instruction created)
//
// Receiver-side RunAccept is the same shape minus the factory call: the
// receiver discovers the TransferInstruction CID via their ACS (the UI
// surfaces it; CLI takes it as a flag), then dials registry +
// /choice-contexts/accept and exercises TransferInstruction_Accept.

// runTransferLive is the live path RunTransfer dispatches to when
// Endpoint is set. Returns the response's resulting TransferInstruction
// CID (or new Holding CID for Direct kind) — caller prints / returns.
func runTransferLive(ctx context.Context, out io.Writer, opts TransferOptions) (string, error) {
	regBaseURL, regHost, err := resolveRegistryURL(opts.Instance, opts.RegistryURL)
	if err != nil {
		return "", err
	}
	ref, err := resolveInstrument(opts.Instance, opts.Instrument)
	if err != nil {
		// Treat unresolved symbol as the raw instrument id — Amulet
		// is on-chain but not always registered via `token create`.
		ref = regstate.TokenRef{InstrumentID: opts.Instrument, IssuerParty: ""}
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
		return "", err
	}
	defer cleanup()

	// Holding picking: query the sender's HoldingV2 set for this
	// instrument, sort small-to-large, take just enough to cover
	// the amount. The participant validates that the picked contracts
	// are still active at submit time; if any get spent between the
	// ACS query and submit, the exercise returns CONTRACT_NOT_FOUND
	// and the caller can retry (out of MVP scope).
	holdings, err := listSenderHoldings(ctx, client, opts.From, ref.InstrumentID)
	if err != nil {
		return "", fmt.Errorf("list sender holdings: %w", err)
	}
	picked, total, err := selectInputHoldings(holdings, opts.Amount)
	if err != nil {
		return "", err
	}
	emit(out, "transfer: selected", map[string]any{
		"input_count": len(picked), "total_input": total, "amount": opts.Amount,
	})

	// Off-ledger factory lookup.
	regCli, err := registry.Dial(registry.DialOptions{
		BaseURL:    regBaseURL,
		HostHeader: regHost,
		Token:      registry.StaticToken(resolveRegistryToken(opts.Token, opts.Instance, opts.Role)),
	})
	if err != nil {
		return "", fmt.Errorf("dial registry: %w", err)
	}
	admin := ref.IssuerParty
	if admin == "" {
		admin = inferAdminFromHoldings(holdings)
	}
	if admin == "" {
		return "", fmt.Errorf("could not determine instrument admin party — pass an instrument with a recorded issuer or rely on at least one holding being present for the sender")
	}
	requestedAt, executeBefore := transferTimes()
	transferArgs := registry.TransferArgs{
		Sender:           registry.NewOwnedAccount(opts.From),
		Receiver:         registry.NewOwnedAccount(opts.To),
		Amount:           opts.Amount,
		InstrumentID:     registry.InstrumentID{Admin: admin, ID: ref.InstrumentID},
		RequestedAt:      requestedAt,
		ExecuteBefore:    executeBefore,
		InputHoldingCids: contractIDsOf(picked),
		Meta:             registry.Metadata{Values: map[string]string{}},
	}
	factoryReq := registry.TransferFactoryRequest{
		ChoiceArguments: registry.TransferFactoryChoiceArgs{
			Transfer: transferArgs,
			Actors:   []string{opts.From},
			ExtraArgs: registry.ExtraArgs{
				Context: registry.Metadata{Values: map[string]string{}},
				Meta:    registry.Metadata{Values: map[string]string{}},
			},
		},
	}
	factoryResp, err := regCli.GetTransferFactory(ctx, factoryReq)
	if err != nil {
		return "", fmt.Errorf("registry transfer-factory: %w", err)
	}
	emit(out, "transfer: factory", map[string]any{
		"factory_id":    factoryResp.FactoryID,
		"transfer_kind": factoryResp.TransferKind,
		"disclosed":     len(factoryResp.DisclosedContractsList()),
	})

	// On-ledger exercise.
	resp, err := exerciseV2TransferFactory(
		ctx, client, opts.From,
		factoryResp.FactoryID, transferArgs, factoryResp,
	)
	if err != nil {
		return "", fmt.Errorf("exercise TransferFactory_Transfer: %w", err)
	}
	instructionID := findCreatedInstructionID(resp)
	emit(out, "transfer: submitted", map[string]any{
		"transfer_instruction_id": instructionID,
		"update_id":               resp.GetTransaction().GetUpdateId(),
	})
	return instructionID, nil
}

// runAcceptLive is the receiver-side counterpart.
func runAcceptLive(ctx context.Context, out io.Writer, opts AcceptOptions) error {
	regBaseURL, regHost, err := resolveRegistryURL(opts.Instance, opts.RegistryURL)
	if err != nil {
		return err
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
		return err
	}
	defer cleanup()

	regCli, err := registry.Dial(registry.DialOptions{
		BaseURL:    regBaseURL,
		HostHeader: regHost,
		Token:      registry.StaticToken(resolveRegistryToken(opts.Token, opts.Instance, opts.Role)),
	})
	if err != nil {
		return fmt.Errorf("dial registry: %w", err)
	}
	ctxResp, err := regCli.GetAcceptChoiceContext(ctx, opts.TransferInstructionID,
		registry.ChoiceContextRequest{Meta: map[string]string{}})
	if err != nil {
		return fmt.Errorf("registry accept choice-context: %w", err)
	}

	// The receiver acts on the instruction. Use the explicit --party
	// when given (required when the participant hosts multiple
	// parties); otherwise fall back to the first Act-As party granted
	// to the JWT (correct for a single-party participant).
	receiver := opts.Party
	if receiver == "" {
		receiver, err = pickActAsParty(ctx, client)
		if err != nil {
			return fmt.Errorf("resolve receiver party: %w", err)
		}
	}

	resp, err := exerciseV2AcceptInstruction(ctx, client, receiver, opts.TransferInstructionID, ctxResp)
	if err != nil {
		return fmt.Errorf("exercise TransferInstruction_Accept: %w", err)
	}
	emit(out, "accept: submitted", map[string]any{
		"update_id": resp.GetTransaction().GetUpdateId(),
	})
	return nil
}

// --- helpers -------------------------------------------------------

// holdingRef captures the parts of a HoldingV2 contract the transfer
// selection cares about: the CID (to feed into inputHoldingCids) and
// the amount (to pick by).
type holdingRef struct {
	ContractID string
	Owner      string
	Provider   string // account.provider ("" when None)
	AccountID  string // account.id
	Admin      string
	Instrument string
	Amount     string // decimal string, untouched precision
}

func contractIDsOf(h []holdingRef) []string {
	out := make([]string, len(h))
	for i, x := range h {
		out[i] = x.ContractID
	}
	return out
}

// listSenderHoldings runs an ACS query filtered by HoldingInterfaceV2
// and the sender's party, returning every holding the sender owns for
// `instrumentID`. Empty instrumentID returns all holdings (caller
// filters).
func listSenderHoldings(ctx context.Context, client *ledger.Client, sender, instrumentID string) ([]holdingRef, error) {
	// Wrap with cancel so a stream-iter error or an upstream timeout
	// tears down the stream pump goroutine instead of leaking it.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	end, err := client.LedgerEnd(ctx)
	if err != nil {
		return nil, fmt.Errorf("ledger end: %w", err)
	}
	stream, err := client.ActiveContracts(ctx, ledger.ActiveContractsRequest{
		ActiveAtOffset: end.Offset,
		EventFormat:    holdingInterfaceFilterV2([]string{sender}),
	})
	if err != nil {
		return nil, fmt.Errorf("ACS query: %w", err)
	}
	var out []holdingRef
	for item := range stream {
		if item.Err != nil {
			return nil, fmt.Errorf("ACS stream: %w", item.Err)
		}
		entry, ok := item.Value.ContractEntry.(*lapiv2.GetActiveContractsResponse_ActiveContract)
		if !ok {
			continue
		}
		created := entry.ActiveContract.GetCreatedEvent()
		if created == nil {
			continue
		}
		// HoldingV2 is queried as an interface, so the view we want
		// is the first InterfaceView whose interface id matches.
		for _, iv := range created.GetInterfaceViews() {
			view, ok := extractHoldingViewV2(iv)
			if !ok || view.Owner != sender {
				continue
			}
			if instrumentID != "" && view.InstrumentID != instrumentID {
				continue
			}
			out = append(out, holdingRef{
				ContractID: created.GetContractId(),
				Owner:      view.Owner,
				Provider:   view.Provider,
				AccountID:  view.AccountID,
				Admin:      view.Admin,
				Instrument: view.InstrumentID,
				Amount:     view.Amount,
			})
		}
	}
	return out, nil
}

// selectInputHoldings picks the smallest holdings that sum to at least
// `amount`. Greedy small-first keeps individually-large holdings free
// for bigger future transfers — a common heuristic for token wallets.
// Returns the picked holdings + sum, or an error if total < amount.
//
// Arithmetic uses math/big.Rat (not big.Float) so 18-decimal-place
// Daml Decimal values round-trip exactly: big.Float uses binary
// mantissa and silently loses precision past ~15 decimal digits,
// which corrupts cash amounts. Rat parses decimal strings exactly
// via SetString and formats back via FloatString(decimals).
func selectInputHoldings(holdings []holdingRef, amount string) ([]holdingRef, string, error) {
	target, ok := new(big.Rat).SetString(amount)
	if !ok {
		return nil, "", fmt.Errorf("amount %q is not a valid decimal", amount)
	}
	indexed := make([]holdingRef, len(holdings))
	copy(indexed, holdings)
	sort.Slice(indexed, func(i, j int) bool {
		a, _ := new(big.Rat).SetString(indexed[i].Amount)
		b, _ := new(big.Rat).SetString(indexed[j].Amount)
		return a.Cmp(b) < 0
	})
	sum := new(big.Rat)
	var picked []holdingRef
	for _, h := range indexed {
		a, ok := new(big.Rat).SetString(h.Amount)
		if !ok {
			return nil, "", fmt.Errorf("holding amount %q is not a valid decimal", h.Amount)
		}
		sum.Add(sum, a)
		picked = append(picked, h)
		if sum.Cmp(target) >= 0 {
			return picked, formatDecimalRat(sum, amount), nil
		}
	}
	return nil, "", fmt.Errorf("sender has %s total but transfer needs %s",
		formatDecimalRat(sum, amount), amount)
}

// formatDecimalRat renders r as a decimal string with enough
// fractional digits to round-trip the input — derived from the
// reference string `like` (typically the user-supplied amount).
// Daml Decimal allows up to 38 fractional digits in V2; we cap at
// the larger of (input scale, 10) to keep output stable.
func formatDecimalRat(r *big.Rat, like string) string {
	scale := decimalScale(like)
	if scale < 10 {
		scale = 10
	}
	return r.FloatString(scale)
}

// decimalScale returns the number of digits after the decimal point
// in s, or 0 if s has no '.' character.
func decimalScale(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			return len(s) - i - 1
		}
	}
	return 0
}

// inferAdminFromHoldings reads the admin off a holding view. All
// holdings of one instrument share the same admin; pick the first.
func inferAdminFromHoldings(h []holdingRef) string {
	for _, x := range h {
		if x.Admin != "" {
			return x.Admin
		}
	}
	return ""
}

// pickActAsParty returns the first ActAs party granted to the JWT's
// user — for receiver flows where the caller doesn't pass the party
// explicitly. The auto-grant in dialLedger guarantees at least one
// per-role local party is granted on a freshly-dialed connection.
func pickActAsParty(ctx context.Context, client *ledger.Client) (string, error) {
	parties, err := client.ResolveActAndReadParties(ctx)
	if err != nil {
		return "", err
	}
	if len(parties) == 0 {
		return "", errors.New("no ActAs party available on this JWT")
	}
	return parties[0], nil
}

// transferTimes returns sensible defaults for requestedAt / executeBefore.
// The registry rejects requests whose `requestedAt` differs from
// participant clock by more than ~30s, so we use real wall clock here
// — context.Now() would also work but pulling clock from time.Now keeps
// the helper simple for the CLI / handler paths.
//
// `executeBefore` gives a 24-hour window (matching the upstream Splice
// CLI) before the transfer is considered stale. For an Offer-kind
// transfer this is the window in which the receiver must accept the
// resulting TransferInstruction — 5 minutes is too tight for a
// human-driven accept, so we use the upstream default.
func transferTimes() (time.Time, time.Time) {
	now := time.Now().UTC()
	return now, now.Add(24 * time.Hour)
}

// findCreatedInstructionID scans the SubmitAndWaitForTransaction
// response for a created event whose template / interface implements
// TransferInstructionV2. Returns "" when the transfer was Direct kind
// (no instruction created — the receiver's Holding shows up instead).
func findCreatedInstructionID(resp *lapiv2.SubmitAndWaitForTransactionResponse) string {
	tx := resp.GetTransaction()
	if tx == nil {
		return ""
	}
	for _, ev := range tx.GetEvents() {
		created := ev.GetCreated()
		if created == nil {
			continue
		}
		for _, iv := range created.GetInterfaceViews() {
			id := iv.GetInterfaceId()
			if id == nil {
				continue
			}
			// Module + entity match is enough — the package id is
			// the alpha snapshot hash and rotates weekly.
			if id.GetModuleName() == "Splice.Api.Token.TransferInstructionV2" &&
				id.GetEntityName() == "TransferInstruction" {
				return created.GetContractId()
			}
		}
	}
	return ""
}
