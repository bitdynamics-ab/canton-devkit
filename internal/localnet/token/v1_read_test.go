package token

import (
	"context"
	"testing"

	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
	adminv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2/admin"
)

// --- fixture builders for interface views of each generation ---

func someParty(p string) *lapiv2.Value {
	return &lapiv2.Value{Sum: &lapiv2.Value_Optional{Optional: &lapiv2.Optional{
		Value: &lapiv2.Value{Sum: &lapiv2.Value_Party{Party: p}},
	}}}
}

// v1HoldingView builds a V1 HoldingView InterfaceView — owner is a direct
// Party (the shape that differs from V2).
func v1HoldingView(owner, admin, id, amount string, locked bool) *lapiv2.InterfaceView {
	lock := &lapiv2.Value{Sum: &lapiv2.Value_Optional{Optional: &lapiv2.Optional{}}} // None
	if locked {
		lock = &lapiv2.Value{Sum: &lapiv2.Value_Optional{Optional: &lapiv2.Optional{
			Value: &lapiv2.Value{Sum: &lapiv2.Value_Record{Record: &lapiv2.Record{}}},
		}}}
	}
	return &lapiv2.InterfaceView{
		InterfaceId: &lapiv2.Identifier{ModuleName: "Splice.Api.Token.HoldingV1", EntityName: "Holding"},
		ViewValue: &lapiv2.Record{Fields: []*lapiv2.RecordField{
			{Label: "owner", Value: &lapiv2.Value{Sum: &lapiv2.Value_Party{Party: owner}}},
			{Label: "instrumentId", Value: &lapiv2.Value{Sum: &lapiv2.Value_Record{Record: &lapiv2.Record{Fields: []*lapiv2.RecordField{
				{Label: "admin", Value: &lapiv2.Value{Sum: &lapiv2.Value_Party{Party: admin}}},
				{Label: "id", Value: &lapiv2.Value{Sum: &lapiv2.Value_Text{Text: id}}},
			}}}}},
			{Label: "amount", Value: &lapiv2.Value{Sum: &lapiv2.Value_Numeric{Numeric: amount}}},
			{Label: "lock", Value: lock},
		}},
	}
}

// v2HoldingView builds a V2 HoldingView InterfaceView — owner nested in an
// Account record.
func v2HoldingView(owner, admin, id, amount string) *lapiv2.InterfaceView {
	return &lapiv2.InterfaceView{
		InterfaceId: &lapiv2.Identifier{ModuleName: "Splice.Api.Token.HoldingV2", EntityName: "Holding"},
		ViewValue: &lapiv2.Record{Fields: []*lapiv2.RecordField{
			{Label: "account", Value: &lapiv2.Value{Sum: &lapiv2.Value_Record{Record: &lapiv2.Record{Fields: []*lapiv2.RecordField{
				{Label: "owner", Value: someParty(owner)},
				{Label: "provider", Value: &lapiv2.Value{Sum: &lapiv2.Value_Optional{Optional: &lapiv2.Optional{}}}},
				{Label: "id", Value: &lapiv2.Value{Sum: &lapiv2.Value_Text{Text: "acct"}}},
			}}}}},
			{Label: "instrumentId", Value: &lapiv2.Value{Sum: &lapiv2.Value_Record{Record: &lapiv2.Record{Fields: []*lapiv2.RecordField{
				{Label: "admin", Value: &lapiv2.Value{Sum: &lapiv2.Value_Party{Party: admin}}},
				{Label: "id", Value: &lapiv2.Value{Sum: &lapiv2.Value_Text{Text: id}}},
			}}}}},
			{Label: "amount", Value: &lapiv2.Value{Sum: &lapiv2.Value_Numeric{Numeric: amount}}},
		}},
	}
}

func TestDiscoverTokenSurfaces(t *testing.T) {
	pkgs := func(names ...string) *adminv2.ListKnownPackagesResponse {
		var d []*adminv2.PackageDetails
		for _, n := range names {
			d = append(d, &adminv2.PackageDetails{Name: n})
		}
		return &adminv2.ListKnownPackagesResponse{PackageDetails: d}
	}
	cases := []struct {
		name           string
		names          []string
		wantV1, wantV2 bool
	}{
		{"both", []string{"splice-api-token-holding-v1", "splice-api-token-holding-v2", "amulet"}, true, true},
		{"v1 only", []string{"splice-api-token-holding-v1", "amulet"}, true, false},
		{"v2 only", []string{"splice-api-token-holding-v2"}, false, true},
		{"neither", []string{"some-other-pkg"}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeLedger{ListKnownPackagesFn: func(context.Context) (*adminv2.ListKnownPackagesResponse, error) {
				return pkgs(c.names...), nil
			}}
			s, err := discoverTokenSurfaces(context.Background(), f)
			if err != nil {
				t.Fatal(err)
			}
			if s.HasV1 != c.wantV1 || s.HasV2 != c.wantV2 {
				t.Errorf("got {V1:%v V2:%v}, want {V1:%v V2:%v}", s.HasV1, s.HasV2, c.wantV1, c.wantV2)
			}
			if s.Any() != (c.wantV1 || c.wantV2) {
				t.Errorf("Any() = %v", s.Any())
			}
		})
	}
}

func TestExtractHoldingViewV1(t *testing.T) {
	hv, ok := extractHoldingViewV1(v1HoldingView("alice::pid", "dso::pid", "Amulet", "123.45", false))
	if !ok {
		t.Fatal("extract failed")
	}
	if hv.Generation != genV1 {
		t.Errorf("generation = %v, want genV1", hv.Generation)
	}
	if hv.Owner != "alice::pid" || hv.Admin != "dso::pid" || hv.InstrumentID != "Amulet" || hv.Amount != "123.45" {
		t.Errorf("fields wrong: %+v", hv)
	}
	if hv.Locked {
		t.Error("should not be locked")
	}
	if locked, _ := extractHoldingViewV1(v1HoldingView("a", "b", "X", "1", true)); !locked.Locked {
		t.Error("Some(lock) should set Locked")
	}
}

func TestExtractBestHoldingView_PrefersV2AndFallsBack(t *testing.T) {
	v1 := v1HoldingView("alice::pid", "dso::pid", "Amulet", "10", false)
	v2 := v2HoldingView("alice::pid", "dso::pid", "Amulet", "10")

	// A contract implementing both → prefer the richer V2 view (counted once).
	if hv, ok := extractBestHoldingView([]*lapiv2.InterfaceView{v1, v2}); !ok || hv.Generation != genV2 {
		t.Errorf("both-implementing contract should yield genV2, got %v (ok=%v)", hv.Generation, ok)
	}
	// A V1-only contract (Amulet on 0.6.4) → the V1 view.
	if hv, ok := extractBestHoldingView([]*lapiv2.InterfaceView{v1}); !ok || hv.Generation != genV1 || hv.Owner != "alice::pid" {
		t.Errorf("V1-only contract should yield genV1, got %+v (ok=%v)", hv, ok)
	}
}

func TestInstrumentsFromHoldings_MixedGenerationsTagged(t *testing.T) {
	// A participant with both a V1 token (Amulet) and a V2 token (MYT):
	// both instruments must appear, each tagged with its generation.
	holdings := []HoldingContract{
		{ContractID: "c1", Party: "p1", Admin: "dso", InstrumentID: "Amulet", Amount: "10", Gen: genV1},
		{ContractID: "c2", Party: "p2", Admin: "issuer", InstrumentID: "MYT", Amount: "5", Gen: genV2},
	}
	instr := instrumentsFromHoldings(holdings, "")
	if len(instr) != 2 {
		t.Fatalf("want 2 instruments (Amulet, MYT), got %d", len(instr))
	}
	bySym := map[string]InstrumentRef{}
	for _, i := range instr {
		bySym[i.Symbol] = i
	}
	if bySym["Amulet"].Generation != "v1" || bySym["Amulet"].Standard != "Splice Amulet" {
		t.Errorf("Amulet: gen=%q std=%q", bySym["Amulet"].Generation, bySym["Amulet"].Standard)
	}
	if bySym["MYT"].Generation != "v2" || bySym["MYT"].Standard != "Token Standard V2 (CIP-0112)" {
		t.Errorf("MYT: gen=%q std=%q", bySym["MYT"].Generation, bySym["MYT"].Standard)
	}
}
