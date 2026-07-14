package token

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// allocationContract builds an ACS response carrying one Allocation
// interface view: the InterfaceId (EntityName "Allocation", so
// extractAllocationView matches) + a nested `allocation`
// AllocationSpecification record with the summary fields.
func allocationContract(cid, authorizer, settlementID string, legCount int, committed bool) *lapiv2.GetActiveContractsResponse {
	legs := make([]*lapiv2.Value, legCount)
	for i := range legs {
		legs[i] = recordValue([]field{{"transferLegId", textValue("leg")}})
	}
	spec := recordValue([]field{
		{"admin", partyValue("DSO::1220")},
		{"authorizer", recordValue([]field{
			{"owner", optionalPartyValue(&authorizer)},
		})},
		{"transferLegSides", &lapiv2.Value{Sum: &lapiv2.Value_List{List: &lapiv2.List{Elements: legs}}}},
		{"committed", boolValue(committed)},
		{"settlement", recordValue([]field{
			{"settlementRef", recordValue([]field{{"id", textValue(settlementID)}})},
		})},
	})
	view := recordValue([]field{{"allocation", spec}})
	return &lapiv2.GetActiveContractsResponse{
		ContractEntry: &lapiv2.GetActiveContractsResponse_ActiveContract{
			ActiveContract: &lapiv2.ActiveContract{
				CreatedEvent: &lapiv2.CreatedEvent{
					ContractId: cid,
					InterfaceViews: []*lapiv2.InterfaceView{{
						InterfaceId: &lapiv2.Identifier{
							ModuleName: "Splice.Api.Token.AllocationV2",
							EntityName: "Allocation",
						},
						ViewValue: view.GetRecord(),
					}},
				},
			},
		},
	}
}

// TestParseSettlementDeadline covers the RFC3339, duration, empty, and
// invalid cases of the --settlement-deadline parser.
func TestParseSettlementDeadline(t *testing.T) {
	if d, err := parseSettlementDeadline(""); err != nil || d != nil {
		t.Errorf("empty: got (%v,%v), want (nil,nil)", d, err)
	}
	if d, err := parseSettlementDeadline("2026-07-01T12:00:00Z"); err != nil || d == nil {
		t.Fatalf("rfc3339: got (%v,%v)", d, err)
	} else if !d.Equal(time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("rfc3339 value: got %v", d)
	}
	before := time.Now().UTC()
	d, err := parseSettlementDeadline("1h")
	if err != nil || d == nil {
		t.Fatalf("duration: got (%v,%v)", d, err)
	}
	if d.Before(before.Add(59 * time.Minute)) {
		t.Errorf("duration value not ~now+1h: %v", d)
	}
	if _, err := parseSettlementDeadline("not-a-time"); err == nil {
		t.Error("invalid: want error, got nil")
	}
}

// TestExtractAllocationView pins the record-walker: a nested `allocation`
// spec yields the authorizer, settlement id, leg count, and committed flag.
func TestExtractAllocationView(t *testing.T) {
	resp := allocationContract("00alloc-1", "alice::1220", "settlement-9", 2, true)
	created := resp.GetContractEntry().(*lapiv2.GetActiveContractsResponse_ActiveContract).
		ActiveContract.GetCreatedEvent()
	av, ok := extractAllocationView(created.GetInterfaceViews())
	if !ok {
		t.Fatal("extractAllocationView: ok=false")
	}
	if av.Authorizer != "alice::1220" {
		t.Errorf("authorizer: got %q", av.Authorizer)
	}
	if av.SettlementID != "settlement-9" {
		t.Errorf("settlementID: got %q", av.SettlementID)
	}
	if av.LegCount != 2 {
		t.Errorf("legCount: got %d, want 2", av.LegCount)
	}
	if !av.Committed {
		t.Error("committed: got false, want true")
	}
}

// TestExtractAllocationView_SkipsNonAllocation pins that a non-Allocation
// interface view (wrong EntityName) is skipped.
func TestExtractAllocationView_SkipsNonAllocation(t *testing.T) {
	view := recordValue([]field{{"allocation", recordValue([]field{
		{"authorizer", recordValue([]field{{"owner", optionalPartyValue(strptr("x::1"))}})},
	})}})
	iv := []*lapiv2.InterfaceView{{
		InterfaceId: &lapiv2.Identifier{EntityName: "TransferInstruction"},
		ViewValue:   view.GetRecord(),
	}}
	if _, ok := extractAllocationView(iv); ok {
		t.Error("want ok=false for non-Allocation entity")
	}
}

// TestRunListAllocations_HappyPath feeds two Allocation contracts through
// the fake ledger and asserts the summaries + the ready status.
func TestRunListAllocations_HappyPath(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")

	fake := &fakeLedger{
		ActiveContractsFn: func(ctx context.Context, _ ledger.ActiveContractsRequest) (<-chan ledger.StreamItem[*lapiv2.GetActiveContractsResponse], error) {
			return newStream([]*lapiv2.GetActiveContractsResponse{
				allocationContract("00a", "alice::1220", "s1", 1, false),
				allocationContract("00b", "bob::1220", "s2", 3, true),
			}, nil), nil
		},
	}
	withFakeDial(t, fake)

	rows, err := RunListAllocations(context.Background(), ListAllocationsOptions{
		Instance: "demo", Role: "app-user", Endpoint: "fake:0", Insecure: true,
	})
	if err != nil {
		t.Fatalf("RunListAllocations: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows: got %d, want 2", len(rows))
	}
	if rows[0].ContractID != "00a" || rows[0].Status != types.AllocationStatusReady {
		t.Errorf("row0: got %+v", rows[0])
	}
	if rows[1].LegCount != 3 || !rows[1].Committed {
		t.Errorf("row1: got %+v", rows[1])
	}
}

// TestRunListAllocations_PartyFilter pins the --party filter: only the
// matching authorizer's allocation survives.
func TestRunListAllocations_PartyFilter(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")

	fake := &fakeLedger{
		ActiveContractsFn: func(ctx context.Context, _ ledger.ActiveContractsRequest) (<-chan ledger.StreamItem[*lapiv2.GetActiveContractsResponse], error) {
			return newStream([]*lapiv2.GetActiveContractsResponse{
				allocationContract("00a", "alice::1220", "s1", 1, false),
				allocationContract("00b", "bob::1220", "s2", 1, false),
			}, nil), nil
		},
	}
	withFakeDial(t, fake)

	rows, err := RunListAllocations(context.Background(), ListAllocationsOptions{
		Instance: "demo", Role: "app-user", Endpoint: "fake:0", Insecure: true,
		Party: "bob::1220",
	})
	if err != nil {
		t.Fatalf("RunListAllocations: %v", err)
	}
	if len(rows) != 1 || rows[0].Authorizer != "bob::1220" {
		t.Fatalf("party filter: got %+v", rows)
	}
}

// TestRunListAllocations_NoEndpoint pins the not-wired stub.
func TestRunListAllocations_NoEndpoint(t *testing.T) {
	_, err := RunListAllocations(context.Background(), ListAllocationsOptions{Instance: "demo"})
	if !errors.Is(err, ErrNeedsV2LocalNet) {
		t.Errorf("want ErrNeedsV2LocalNet, got %v", err)
	}
}

// TestRunAllocate_NoEndpoint pins the not-wired stub for the write path
// (validation passes; the empty endpoint short-circuits to the stub).
func TestRunAllocate_NoEndpoint(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	_, err := RunAllocate(context.Background(), nil, AllocationOptions{
		Instance: "demo", Instrument: "RTK",
		From: "alice::1220", To: "bob::1220", Amount: "10",
	})
	if !errors.Is(err, ErrNeedsV2LocalNet) {
		t.Errorf("want ErrNeedsV2LocalNet, got %v", err)
	}
}

// TestRunAllocate_RejectsBadAmount pins that a non-decimal amount is a
// plain input error, never mislabelled as the not-wired stub.
func TestRunAllocate_RejectsBadAmount(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	_, err := RunAllocate(context.Background(), nil, AllocationOptions{
		Instance: "demo", Instrument: "RTK",
		From: "alice::1220", To: "bob::1220", Amount: "abc",
		Endpoint: "fake:0",
	})
	if err == nil || errors.Is(err, ErrNeedsV2LocalNet) {
		t.Errorf("want a decimal-validation error, got %v", err)
	}
}

func strptr(s string) *string { return &s }
