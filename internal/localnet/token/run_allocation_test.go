package token

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/registry"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// allocationContract builds an ACS response carrying one Allocation
// interface view (EntityName "Allocation") with a nested `allocation`
// AllocationSpecification record holding the summary fields.
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

// fieldValue returns the value of the named field in a record Value, or nil.
func fieldValue(v *lapiv2.Value, label string) *lapiv2.Value {
	rec := v.GetRecord()
	if rec == nil {
		return nil
	}
	for _, f := range rec.Fields {
		if f.Label == label {
			return f.Value
		}
	}
	return nil
}

// TestTestTokenAllocateFactoryArg pins the on-ledger
// AllocationFactory_Allocate choice argument: allocation.admin carries the
// issuer (so it matches the TokenRules factory admin — the fix for
// "Instrument-id must match the factory"), and extraArgs carry the local
// test-token choice context (TokenRules + AccountConfig cids) rather than a
// registry blob.
func TestTestTokenAllocateFactoryArg(t *testing.T) {
	args := registry.AllocationFactoryChoiceArgs{
		Settlement: registry.SettlementInfo{Executors: []string{"exec::1"}, ID: "s-1"},
		Allocation: registry.AllocationSpecification{
			Admin:      "issuer::1220",
			Authorizer: registry.NewOwnedAccount("holder::1220"),
			TransferLegSides: []registry.TransferLegSide{{
				TransferLegID: "leg0", Side: "SenderSide",
				Otherside: registry.NewOwnedAccount("recv::1220"), Amount: "10", InstrumentID: "DEMO",
			}},
		},
		InputHoldingCids: []string{"h1", "h2"},
		Actors:           []string{"holder::1220"},
	}
	arg := testTokenAllocateFactoryArg(args, "tr-1", []string{"cfg-a"})

	// allocation.admin must be the issuer (the TokenRules factory admin).
	spec := fieldValue(arg, "allocation")
	if spec == nil {
		t.Fatal("missing allocation field")
	}
	if got := partyOf(fieldValue(spec, "admin")); got != "issuer::1220" {
		t.Errorf("allocation.admin = %q, want issuer::1220 (must match the TokenRules factory admin)", got)
	}

	// extraArgs.context must be the local test-token context, not a
	// registry blob: tokenRules cid + accountConfigs list present.
	extra := fieldValue(arg, "extraArgs")
	if extra == nil {
		t.Fatal("missing extraArgs field")
	}
	values := contextValuesMap(t, fieldValue(extra, "context"))
	if _, ok := values[tokenRulesContextKey]; !ok {
		t.Errorf("context missing %q; have %v", tokenRulesContextKey, keysOf(values))
	}
	ctor, inner := variantOf(t, values[tokenRulesContextKey])
	if ctor != "AV_ContractId" || contractIDOf(inner) != "tr-1" {
		t.Errorf("tokenRules entry = (%s,%q), want (AV_ContractId, tr-1)", ctor, contractIDOf(inner))
	}
	if _, ok := values[accountConfigsContextKey]; !ok {
		t.Errorf("context missing %q", accountConfigsContextKey)
	}

	// inputHoldingCids round-trip through the choice arg.
	list := fieldValue(arg, "inputHoldingCids").GetList()
	if list == nil || len(list.Elements) != 2 {
		t.Fatalf("inputHoldingCids = %v, want 2 elements", list)
	}
}

// TestAuthorizerAccountFromHoldings pins that the authorizer Account is
// taken from the picked holdings' own account (owner/provider/id) — the
// DAML impl asserts inputHolding.account == allocation.authorizer.
func TestAuthorizerAccountFromHoldings(t *testing.T) {
	t.Run("provider-scoped", func(t *testing.T) {
		picked := []holdingRef{{Owner: "holder::1", Provider: "prov::1", AccountID: "acct-x"}}
		a := authorizerAccountFromHoldings(picked, "holder::1")
		if a.Owner == nil || *a.Owner != "holder::1" {
			t.Errorf("owner = %v, want holder::1", a.Owner)
		}
		if a.Provider == nil || *a.Provider != "prov::1" {
			t.Errorf("provider = %v, want prov::1", a.Provider)
		}
		if a.ID != "acct-x" {
			t.Errorf("id = %q, want acct-x", a.ID)
		}
		if authorizerProvider(picked) != "prov::1" {
			t.Errorf("authorizerProvider = %q, want prov::1", authorizerProvider(picked))
		}
	})
	t.Run("self-custodial", func(t *testing.T) {
		picked := []holdingRef{{Owner: "holder::1", AccountID: ""}}
		a := authorizerAccountFromHoldings(picked, "holder::1")
		if a.Provider != nil {
			t.Errorf("provider = %v, want nil (self-custodial)", a.Provider)
		}
		if authorizerProvider(picked) != "" {
			t.Errorf("authorizerProvider = %q, want empty", authorizerProvider(picked))
		}
	})
	t.Run("empty falls back to from", func(t *testing.T) {
		a := authorizerAccountFromHoldings(nil, "from::1")
		if a.Owner == nil || *a.Owner != "from::1" {
			t.Errorf("owner = %v, want from::1", a.Owner)
		}
	})
}
