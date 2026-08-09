package token

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// Pending-transfer listing. A pending offer is an on-ledger
// TransferInstruction the receiver has not yet accepted — and since
// acceptance archives the instruction, every active TransferInstruction
// contract in the ACS IS by definition still pending. So the list is a
// plain ACS scan of the TransferInstruction interface. It follows
// RunListAllocations, with two differences the allocation list doesn't
// need: the TransferInstruction filter is per-generation so it must first
// probe the vetted surfaces, and the scan is bounded (see
// pendingTransfersScanCap). It decodes both V1 and V2 instructions and
// backs both the CLI `token transfers` verb and the GET
// /api/tokens/transfers handler.

// pendingTransfersScanCap bounds the ACS scan so a busy ledger can't OOM us
// through the unbounded gRPC stream. A package var (not the maxHoldingsScan
// const directly) so a test can lower it to exercise the truncated path
// without streaming ten thousand contracts.
var pendingTransfersScanCap = maxHoldingsScan

// ListPendingTransfersOptions is the input for RunListPendingTransfers — an
// ACS scan of the TransferInstruction interface, optionally filtered to one
// party (matched on either the sender or the receiver).
type ListPendingTransfersOptions struct {
	Instance string
	Party    string // empty → every visible pending offer

	Endpoint string
	Token    string
	Role     string
	Insecure bool
}

// RunListPendingTransfers ACS-queries the TransferInstruction interface and
// returns the pending offers as summary rows, most-recently-requested
// first. truncated is true when the scan hit pendingTransfersScanCap and
// the list is partial.
func RunListPendingTransfers(ctx context.Context, opts ListPendingTransfersOptions) (rows []types.TransferSummary, truncated bool, err error) {
	if opts.Endpoint == "" {
		return nil, false, ErrNeedsV2LocalNet
	}
	if opts.Role == "" {
		opts.Role = DefaultRole
	}
	party := ResolveAlias(aliasMapForInstance(opts.Instance), opts.Party)

	conn := LedgerConn{
		Endpoint: opts.Endpoint,
		Token:    opts.Token,
		Insecure: opts.Insecure,
		Instance: opts.Instance,
		Role:     opts.Role,
	}
	// dialLedgerFn (not dialLedger): a pure ACS scan needs only the narrow
	// LedgerClient interface, so tests inject a fakeLedger through this seam.
	client, cleanup, err := dialLedgerFn(ctx, conn)
	if err != nil {
		return nil, false, err
	}
	defer cleanup()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// The TransferInstruction filter is per-generation, so — unlike the
	// allocation filter — it must be built from the participant's vetted
	// surfaces or it would reference a missing package and match nothing.
	surfaces, err := discoverTokenSurfaces(ctx, client)
	if err != nil {
		return nil, false, err
	}
	if !surfaces.Any() {
		return nil, false, ErrNeedsV2LocalNet
	}

	end, err := client.LedgerEnd(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("ledger end: %w", err)
	}
	var parties []string
	if party != "" {
		parties = []string{party}
	}
	stream, err := client.ActiveContracts(ctx, ledger.ActiveContractsRequest{
		ActiveAtOffset: end.Offset,
		EventFormat:    transferInstructionInterfaceFilter(surfaces, parties),
	})
	if err != nil {
		return nil, false, fmt.Errorf("ACS query: %w", err)
	}
	out := make([]types.TransferSummary, 0)
	var scanned int
	for item := range stream {
		if item.Err != nil {
			return nil, false, fmt.Errorf("ACS stream: %w", item.Err)
		}
		// Bound the scan so a busy ledger can't OOM us through the
		// unbounded gRPC stream; the caller surfaces truncated=true.
		scanned++
		if scanned > pendingTransfersScanCap {
			truncated = true
			break
		}
		entry, ok := item.Value.ContractEntry.(*lapiv2.GetActiveContractsResponse_ActiveContract)
		if !ok {
			continue
		}
		created := entry.ActiveContract.GetCreatedEvent()
		if created == nil {
			continue
		}
		tv, ok := extractTransferInstructionView(created.GetInterfaceViews())
		if !ok {
			continue
		}
		// The ACS party filter returns contracts a party is a stakeholder
		// of (both sender and receiver see the instruction); narrow to the
		// party's own legs so `--party bob` means "offers involving bob".
		if party != "" && tv.Sender != party && tv.Receiver != party {
			continue
		}
		out = append(out, types.TransferSummary{
			ContractID:    created.GetContractId(),
			Sender:        tv.Sender,
			Receiver:      tv.Receiver,
			InstrumentID:  tv.InstrumentID,
			Amount:        tv.Amount,
			Generation:    tv.Generation,
			RequestedAt:   tv.RequestedAt,
			ExecuteBefore: tv.ExecuteBefore,
		})
	}
	// Most-recently-requested first; RequestedAt is RFC3339-UTC so the
	// string compare is chronological. ContractID breaks ties (and orders
	// rows with no timestamp) so the result is deterministic.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].RequestedAt != out[j].RequestedAt {
			return out[i].RequestedAt > out[j].RequestedAt
		}
		return out[i].ContractID < out[j].ContractID
	})
	return out, truncated, nil
}

// transferInstructionView is the structured form of a pending
// TransferInstruction InterfaceView — just the fields the offers list
// needs. Sender/Receiver are the transfer's account owner parties.
type transferInstructionView struct {
	Sender        string
	Receiver      string
	InstrumentID  string
	Amount        string
	Generation    string // "v1" | "v2"
	RequestedAt   string // RFC3339
	ExecuteBefore string // RFC3339
}

// extractTransferInstructionView walks a TransferInstruction InterfaceView
// and pulls the summary fields out of its nested `transfer` record.
// Best-effort: an unparseable view is skipped (ok=false) so one malformed
// contract never fails the whole scan. Mirrors extractAllocationView.
func extractTransferInstructionView(views []*lapiv2.InterfaceView) (transferInstructionView, bool) {
	for _, iv := range views {
		id := iv.GetInterfaceId()
		if id == nil || id.GetEntityName() != "TransferInstruction" {
			continue
		}
		if iv.ViewValue == nil {
			continue
		}
		out := transferInstructionView{Generation: "v1"}
		if g, ok := interfaceGeneration(id); ok && g == genV2 {
			out.Generation = "v2"
		}
		for _, f := range iv.ViewValue.Fields {
			if f.Label != "transfer" {
				continue
			}
			if rec := recordOf(f.Value); rec != nil {
				walkTransferFields(rec.Fields, &out)
			}
		}
		if out.Sender == "" && out.Receiver == "" && out.Amount == "" {
			continue
		}
		return out, true
	}
	return transferInstructionView{}, false
}

// walkTransferFields reads the CIP-0112 Transfer record (sender, receiver,
// amount, instrumentId, requestedAt, executeBefore). Sender/receiver are a
// V2 Account record (owner is the party we want) or a bare V1 Party.
func walkTransferFields(fields []*lapiv2.RecordField, out *transferInstructionView) {
	for _, f := range fields {
		switch f.Label {
		case "sender":
			out.Sender = transferPartyOf(f.Value)
		case "receiver":
			out.Receiver = transferPartyOf(f.Value)
		case "amount":
			out.Amount = numericOf(f.Value)
		case "instrumentId":
			if rec := recordOf(f.Value); rec != nil {
				for _, af := range rec.Fields {
					if af.Label == "id" {
						out.InstrumentID = textOf(af.Value)
					}
				}
			}
		case "requestedAt":
			out.RequestedAt = microTimeOf(f.Value)
		case "executeBefore":
			out.ExecuteBefore = microTimeOf(f.Value)
		}
	}
}

// transferPartyOf reads a transfer leg's party: the owner of a V2 Account
// record, or a bare V1 Party. Returns "" when neither shape matches.
func transferPartyOf(v *lapiv2.Value) string {
	if rec := recordOf(v); rec != nil {
		for _, af := range rec.Fields {
			if af.Label == "owner" {
				return optionalPartyOf(af.Value)
			}
		}
		return ""
	}
	return partyOf(v)
}

// microTimeOf renders a Daml Time value (int64 microseconds since the Unix
// epoch) as an RFC3339 string; "" when the value isn't a timestamp.
func microTimeOf(v *lapiv2.Value) string {
	if v == nil {
		return ""
	}
	if ts, ok := v.Sum.(*lapiv2.Value_Timestamp); ok {
		return time.UnixMicro(ts.Timestamp).UTC().Format(time.RFC3339)
	}
	return ""
}
