package token

import (
	"context"
	"errors"
	"testing"
	"time"

	adminv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2/admin"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// pendingTransferContract builds an ACS response carrying one
// TransferInstruction interface view (EntityName "TransferInstruction")
// with a nested `transfer` record. gen selects the sender/receiver shape:
// V2 wraps them in Account records ({owner}), V1 uses a bare Party.
func pendingTransferContract(cid, sender, receiver, instrument, amount string, gen Generation, execBefore time.Time) *lapiv2.GetActiveContractsResponse {
	var senderVal, receiverVal *lapiv2.Value
	module := "Splice.Api.Token.TransferInstructionV2"
	if gen == genV1 {
		module = "Splice.Api.Token.TransferInstructionV1"
		senderVal = partyValue(sender)
		receiverVal = partyValue(receiver)
	} else {
		senderVal = recordValue([]field{{"owner", optionalPartyValue(&sender)}})
		receiverVal = recordValue([]field{{"owner", optionalPartyValue(&receiver)}})
	}
	transfer := recordValue([]field{
		{"sender", senderVal},
		{"receiver", receiverVal},
		{"amount", numericValue(amount)},
		{"instrumentId", recordValue([]field{
			{"admin", partyValue("DSO::1220")},
			{"id", textValue(instrument)},
		})},
		{"requestedAt", timestampValue(execBefore.Add(-time.Hour))},
		{"executeBefore", timestampValue(execBefore)},
	})
	view := recordValue([]field{{"transfer", transfer}})
	return &lapiv2.GetActiveContractsResponse{
		ContractEntry: &lapiv2.GetActiveContractsResponse_ActiveContract{
			ActiveContract: &lapiv2.ActiveContract{
				CreatedEvent: &lapiv2.CreatedEvent{
					ContractId: cid,
					InterfaceViews: []*lapiv2.InterfaceView{{
						InterfaceId: &lapiv2.Identifier{
							ModuleName: module,
							EntityName: "TransferInstruction",
						},
						ViewValue: view.GetRecord(),
					}},
				},
			},
		},
	}
}

// TestRunListPendingTransfers_HappyPath pins the decode of both a V2
// (Account-wrapped parties) and a V1 (bare Party) pending offer.
func TestRunListPendingTransfers_HappyPath(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	exp := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	fake := &fakeLedger{
		ActiveContractsFn: func(ctx context.Context, _ ledger.ActiveContractsRequest) (<-chan ledger.StreamItem[*lapiv2.GetActiveContractsResponse], error) {
			return newStream([]*lapiv2.GetActiveContractsResponse{
				pendingTransferContract("00a", "alice::1220", "bob::1220", "MTK", "100.0", genV2, exp),
				pendingTransferContract("00b", "carol::1220", "dave::1220", "ZHE", "5.0", genV1, exp),
			}, nil), nil
		},
	}
	withFakeDial(t, fake)

	rows, truncated, err := RunListPendingTransfers(context.Background(), ListPendingTransfersOptions{
		Instance: "demo", Role: "app-user", Endpoint: "fake:0", Insecure: true,
	})
	if err != nil {
		t.Fatalf("RunListPendingTransfers: %v", err)
	}
	if truncated {
		t.Errorf("truncated: got true, want false")
	}
	if len(rows) != 2 {
		t.Fatalf("rows: got %d, want 2", len(rows))
	}
	r0 := rows[0]
	if r0.ContractID != "00a" || r0.Sender != "alice::1220" || r0.Receiver != "bob::1220" ||
		r0.InstrumentID != "MTK" || r0.Amount != "100.0" || r0.Generation != "v2" {
		t.Errorf("row0 decode: got %+v", r0)
	}
	if r0.ExecuteBefore != exp.Format(time.RFC3339) {
		t.Errorf("row0 executeBefore: got %q, want %q", r0.ExecuteBefore, exp.Format(time.RFC3339))
	}
	if r0.RequestedAt != exp.Add(-time.Hour).Format(time.RFC3339) {
		t.Errorf("row0 requestedAt: got %q", r0.RequestedAt)
	}
	if rows[1].Generation != "v1" || rows[1].Sender != "carol::1220" || rows[1].Receiver != "dave::1220" {
		t.Errorf("row1 (V1 bare party) decode: got %+v", rows[1])
	}
}

// TestRunListPendingTransfers_PartyFilter pins that --party keeps only
// offers where the party is the sender or the receiver.
func TestRunListPendingTransfers_PartyFilter(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	exp := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	contracts := []*lapiv2.GetActiveContractsResponse{
		pendingTransferContract("00a", "alice::1220", "bob::1220", "MTK", "1.0", genV2, exp),
		pendingTransferContract("00b", "carol::1220", "bob::1220", "ZHE", "2.0", genV2, exp),
		pendingTransferContract("00c", "bob::1220", "erin::1220", "MTK", "3.0", genV2, exp),
	}
	fake := &fakeLedger{
		ActiveContractsFn: func(ctx context.Context, _ ledger.ActiveContractsRequest) (<-chan ledger.StreamItem[*lapiv2.GetActiveContractsResponse], error) {
			return newStream(contracts, nil), nil
		},
	}
	withFakeDial(t, fake)

	rows, _, err := RunListPendingTransfers(context.Background(), ListPendingTransfersOptions{
		Instance: "demo", Role: "app-user", Endpoint: "fake:0", Insecure: true,
		Party: "bob::1220",
	})
	if err != nil {
		t.Fatalf("RunListPendingTransfers: %v", err)
	}
	// bob is receiver of 00a, 00b and sender of 00c → all three survive; a
	// non-party offer would be dropped. Pin the count + that erin's-only
	// leg (as receiver) is reachable via bob-as-sender.
	if len(rows) != 3 {
		t.Fatalf("party filter: got %d rows, want 3 (bob on every leg): %+v", len(rows), rows)
	}
}

// TestRunListPendingTransfers_NoEndpoint returns the V2-gating sentinel so
// the CLI/handler render the bring-up remediation.
func TestRunListPendingTransfers_NoEndpoint(t *testing.T) {
	_, _, err := RunListPendingTransfers(context.Background(), ListPendingTransfersOptions{Instance: "demo"})
	if !errors.Is(err, ErrNeedsV2LocalNet) {
		t.Fatalf("no endpoint: got %v, want ErrNeedsV2LocalNet", err)
	}
}

// TestRunListPendingTransfers_NoSurfaces guards the discoverTokenSurfaces
// probe: with no token package vetted the scan is skipped (the filter would
// match nothing) and the sentinel is returned instead of an empty list.
func TestRunListPendingTransfers_NoSurfaces(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")

	fake := &fakeLedger{
		ListKnownPackagesFn: func(ctx context.Context) (*adminv2.ListKnownPackagesResponse, error) {
			return &adminv2.ListKnownPackagesResponse{}, nil // nothing vetted
		},
	}
	withFakeDial(t, fake)

	_, _, err := RunListPendingTransfers(context.Background(), ListPendingTransfersOptions{
		Instance: "demo", Role: "app-user", Endpoint: "fake:0", Insecure: true,
	})
	if !errors.Is(err, ErrNeedsV2LocalNet) {
		t.Fatalf("no surfaces: got %v, want ErrNeedsV2LocalNet", err)
	}
}

// TestExtractTransferInstructionView_Skips covers the best-effort guards:
// a non-TransferInstruction view and a nil ViewValue both yield ok=false
// rather than a panic or a half-populated row.
func TestExtractTransferInstructionView_Skips(t *testing.T) {
	if _, ok := extractTransferInstructionView(nil); ok {
		t.Errorf("nil views: ok=true, want false")
	}
	// Wrong entity name is skipped.
	other := []*lapiv2.InterfaceView{{
		InterfaceId: &lapiv2.Identifier{ModuleName: "Splice.Api.Token.HoldingV2", EntityName: "Holding"},
		ViewValue:   recordValue([]field{{"transfer", recordValue(nil)}}).GetRecord(),
	}}
	if _, ok := extractTransferInstructionView(other); ok {
		t.Errorf("non-instruction view: ok=true, want false")
	}
	// Right entity but empty transfer → no fields → skipped.
	empty := []*lapiv2.InterfaceView{{
		InterfaceId: &lapiv2.Identifier{ModuleName: "Splice.Api.Token.TransferInstructionV2", EntityName: "TransferInstruction"},
		ViewValue:   recordValue([]field{{"transfer", recordValue(nil)}}).GetRecord(),
	}}
	if _, ok := extractTransferInstructionView(empty); ok {
		t.Errorf("empty transfer: ok=true, want false")
	}
}

// TestMicroTimeOf covers the timestamp reader: a real micros value round
// trips to RFC3339; a non-timestamp value yields "".
func TestMicroTimeOf(t *testing.T) {
	ts := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	if got := microTimeOf(timestampValue(ts)); got != ts.Format(time.RFC3339) {
		t.Errorf("timestamp: got %q, want %q", got, ts.Format(time.RFC3339))
	}
	if got := microTimeOf(textValue("nope")); got != "" {
		t.Errorf("non-timestamp: got %q, want \"\"", got)
	}
	if got := microTimeOf(nil); got != "" {
		t.Errorf("nil: got %q, want \"\"", got)
	}
}
