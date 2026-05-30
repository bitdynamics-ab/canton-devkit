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
		registry.ChoiceContextRequest{Meta: registry.Metadata{Values: map[string]string{}}})
	if err != nil {
		return fmt.Errorf("registry accept choice-context: %w", err)
	}

	// We don't know the receiver party off-the-bat — the JWT user's
	// granted parties tell us who we can act as. Use the first granted
	// party (the dialer auto-granted the role's local party set, so
	// this resolves to the local party that owns the instruction).
	receiver, err := pickActAsParty(ctx, client)
	if err != nil {
		return fmt.Errorf("resolve receiver party: %w", err)
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
func selectInputHoldings(holdings []holdingRef, amount string) ([]holdingRef, string, error) {
	target, ok := new(big.Float).SetString(amount)
	if !ok {
		return nil, "", fmt.Errorf("amount %q is not a valid decimal", amount)
	}
	indexed := make([]holdingRef, len(holdings))
	copy(indexed, holdings)
	sort.Slice(indexed, func(i, j int) bool {
		a, _ := new(big.Float).SetString(indexed[i].Amount)
		b, _ := new(big.Float).SetString(indexed[j].Amount)
		return a.Cmp(b) < 0
	})
	sum := new(big.Float).SetFloat64(0)
	var picked []holdingRef
	for _, h := range indexed {
		a, _ := new(big.Float).SetString(h.Amount)
		sum.Add(sum, a)
		picked = append(picked, h)
		if sum.Cmp(target) >= 0 {
			return picked, sum.Text('f', 10), nil
		}
	}
	return nil, "", fmt.Errorf("sender has %s total but transfer needs %s", sum.Text('f', 10), amount)
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
// `executeBefore` gives the participant 5 minutes to commit before the
// registry considers the request stale.
func transferTimes() (time.Time, time.Time) {
	now := time.Now().UTC()
	return now, now.Add(5 * time.Minute)
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
