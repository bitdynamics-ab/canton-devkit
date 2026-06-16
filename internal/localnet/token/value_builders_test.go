package token

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/registry"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// TestBuildTransferRecord_Shape pins the field labels + order + inner
// types of the TransferFactory_Transfer choice argument. The participant
// matches by label so order is informational, but we keep it aligned
// with the upstream .daml signature so a future reader can diff. Inner
// type assertions catch the easy-to-miss "wrapped Party in Text" class
// of bug — the participant rejects choice args whose types don't
// match the declared signature.
func TestBuildTransferRecord_Shape(t *testing.T) {
	in := registry.TransferArgs{
		Sender:           registry.NewOwnedAccount("alice::1220"),
		Receiver:         registry.NewOwnedAccount("bob::1220"),
		Amount:           "10.00",
		InstrumentID:     registry.InstrumentID{Admin: "DSO::1220", ID: "Amulet"},
		RequestedAt:      time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC),
		ExecuteBefore:    time.Date(2026, 5, 30, 13, 0, 0, 0, time.UTC),
		InputHoldingCids: []string{"00cid-h1", "00cid-h2"},
		Meta:             registry.Metadata{Values: map[string]string{"k": "v"}},
	}
	v := buildTransferRecord(in, genV2)
	rec := v.GetRecord()
	if rec == nil {
		t.Fatal("top-level value is not a Record")
	}
	wantLabels := []string{"sender", "receiver", "amount", "instrumentId", "requestedAt", "executeBefore", "inputHoldingCids", "meta"}
	if len(rec.Fields) != len(wantLabels) {
		t.Fatalf("field count: got %d, want %d", len(rec.Fields), len(wantLabels))
	}
	for i, want := range wantLabels {
		if rec.Fields[i].Label != want {
			t.Errorf("field[%d] label: got %q, want %q", i, rec.Fields[i].Label, want)
		}
	}
	// Inner type spot-checks: sender is an Account record (owner is an
	// Optional Party), amount is Numeric, instrumentId is Record,
	// inputHoldingCids is List of ContractId, requestedAt is Timestamp.
	senderAcct := rec.Fields[0].Value.GetRecord()
	if senderAcct == nil || len(senderAcct.Fields) != 3 || senderAcct.Fields[0].Label != "owner" {
		t.Fatalf("sender not a 3-field Account record: %v", rec.Fields[0].Value.Sum)
	}
	ownerOpt := senderAcct.Fields[0].Value.GetOptional()
	if ownerOpt == nil || ownerOpt.Value.GetParty() != "alice::1220" {
		t.Errorf("sender.owner not Some(Party alice): %v", senderAcct.Fields[0].Value.Sum)
	}
	if rec.Fields[2].Value.GetNumeric() != "10.00" {
		t.Errorf("amount not a Numeric: %v", rec.Fields[2].Value.Sum)
	}
	if instr := rec.Fields[3].Value.GetRecord(); instr == nil || len(instr.Fields) != 2 {
		t.Errorf("instrumentId not a 2-field Record: %v", rec.Fields[3].Value.Sum)
	}
	// inputHoldingCids: list of 2 ContractIds.
	list := rec.Fields[6].Value.GetList()
	if list == nil || len(list.Elements) != 2 {
		t.Fatalf("inputHoldingCids not a 2-element list: %v", rec.Fields[6].Value.Sum)
	}
	if list.Elements[0].GetContractId() != "00cid-h1" {
		t.Errorf("inputHoldingCids[0] not ContractId: %v", list.Elements[0].Sum)
	}
	// requestedAt: timestamp in microseconds since epoch.
	wantMicros := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC).UnixMicro()
	if got := rec.Fields[4].Value.GetTimestamp(); got != wantMicros {
		t.Errorf("requestedAt: got %d, want %d", got, wantMicros)
	}
}

// TestBuildTransferRecord_V1Shape pins the V1 (CIP-0056) difference:
// sender/receiver are bare Party values, NOT Account records. Sending
// the V2 Account shape to a V1 registry fails with "Expected text but
// was {" — this test guards against that regression.
func TestBuildTransferRecord_V1Shape(t *testing.T) {
	in := registry.TransferArgs{
		Sender:           registry.NewOwnedAccount("alice::1220"),
		Receiver:         registry.NewOwnedAccount("bob::1220"),
		Amount:           "10.00",
		InstrumentID:     registry.InstrumentID{Admin: "DSO::1220", ID: "Amulet"},
		RequestedAt:      time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC),
		ExecuteBefore:    time.Date(2026, 5, 30, 13, 0, 0, 0, time.UTC),
		InputHoldingCids: []string{"00cid-h1"},
		Meta:             registry.Metadata{Values: map[string]string{}},
	}
	rec := buildTransferRecord(in, genV1).GetRecord()
	if rec == nil {
		t.Fatal("top-level value is not a Record")
	}
	// sender/receiver must be bare Party values, not Account records.
	if got := rec.Fields[0].Value.GetParty(); got != "alice::1220" {
		t.Errorf("V1 sender: got %v, want bare Party alice::1220", rec.Fields[0].Value.Sum)
	}
	if got := rec.Fields[1].Value.GetParty(); got != "bob::1220" {
		t.Errorf("V1 receiver: got %v, want bare Party bob::1220", rec.Fields[1].Value.Sum)
	}
	if rec.Fields[0].Value.GetRecord() != nil {
		t.Error("V1 sender must NOT be an Account record")
	}
	// Shared fields unchanged from V2.
	if rec.Fields[2].Value.GetNumeric() != "10.00" {
		t.Errorf("amount: got %v", rec.Fields[2].Value.Sum)
	}
}

// TestJsonToAnyValue_Variants exercises the JSON-to-AnyValue
// converter across every variant the registry may hand us. The
// participant fails the exercise if the AnyValue variant constructor
// is wrong, so we lock the discriminator names in here as a contract.
func TestJsonToAnyValue_Variants(t *testing.T) {
	tests := []struct {
		name      string
		in        any
		wantCtor  string
		wantInner string // inner Value's getter string (debug-only assertion key)
		assert    func(*testing.T, *lapiv2.Value)
	}{
		{
			name:     "explicit_AV_Text",
			in:       map[string]any{"AV_Text": "hello"},
			wantCtor: "AV_Text",
			assert: func(t *testing.T, v *lapiv2.Value) {
				if v.GetVariant().Value.GetText() != "hello" {
					t.Errorf("inner text: %v", v.GetVariant().Value.Sum)
				}
			},
		},
		{
			name:     "explicit_AV_Party",
			in:       map[string]any{"AV_Party": "alice::1220"},
			wantCtor: "AV_Party",
			assert: func(t *testing.T, v *lapiv2.Value) {
				if v.GetVariant().Value.GetParty() != "alice::1220" {
					t.Errorf("inner party: %v", v.GetVariant().Value.Sum)
				}
			},
		},
		{
			name:     "explicit_AV_Int_from_number",
			in:       map[string]any{"AV_Int": float64(42)},
			wantCtor: "AV_Int",
			assert: func(t *testing.T, v *lapiv2.Value) {
				if got := v.GetVariant().Value.GetInt64(); got != 42 {
					t.Errorf("inner int: got %d, want 42", got)
				}
			},
		},
		{
			name:     "explicit_AV_Int_from_string",
			in:       map[string]any{"AV_Int": "9999999999"},
			wantCtor: "AV_Int",
			assert: func(t *testing.T, v *lapiv2.Value) {
				if got := v.GetVariant().Value.GetInt64(); got != 9999999999 {
					t.Errorf("inner int: got %d, want 9999999999", got)
				}
			},
		},
		{
			name:     "explicit_AV_Decimal",
			in:       map[string]any{"AV_Decimal": "1234.5678"},
			wantCtor: "AV_Decimal",
			assert: func(t *testing.T, v *lapiv2.Value) {
				if v.GetVariant().Value.GetNumeric() != "1234.5678" {
					t.Errorf("inner decimal: %v", v.GetVariant().Value.Sum)
				}
			},
		},
		{
			name:     "explicit_AV_Bool",
			in:       map[string]any{"AV_Bool": true},
			wantCtor: "AV_Bool",
		},
		{
			name:     "explicit_AV_Time",
			in:       map[string]any{"AV_Time": "2026-05-30T12:00:00Z"},
			wantCtor: "AV_Time",
			assert: func(t *testing.T, v *lapiv2.Value) {
				wantMicros := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC).UnixMicro()
				if got := v.GetVariant().Value.GetTimestamp(); got != wantMicros {
					t.Errorf("inner timestamp: got %d, want %d", got, wantMicros)
				}
			},
		},
		{
			name:     "bare_string_falls_back_to_AV_Text",
			in:       "fallback",
			wantCtor: "AV_Text",
		},
		{
			name:     "bare_integer_float64_falls_back_to_AV_Int",
			in:       float64(7),
			wantCtor: "AV_Int",
		},
		{
			name:     "bare_decimal_float64_falls_back_to_AV_Decimal",
			in:       float64(7.5),
			wantCtor: "AV_Decimal",
		},
		{
			name:     "bare_bool_falls_back_to_AV_Bool",
			in:       false,
			wantCtor: "AV_Bool",
		},
		{
			name:     "bare_list_falls_back_to_AV_List",
			in:       []any{"a", "b"},
			wantCtor: "AV_List",
			assert: func(t *testing.T, v *lapiv2.Value) {
				list := v.GetVariant().Value.GetList()
				if list == nil || len(list.Elements) != 2 {
					t.Errorf("inner list: %v", v.GetVariant().Value.Sum)
				}
			},
		},
		{
			name:     "bare_map_falls_back_to_AV_Map",
			in:       map[string]any{"k": "v"},
			wantCtor: "AV_Map",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, err := jsonToAnyValue(tc.in)
			if err != nil {
				t.Fatalf("jsonToAnyValue: %v", err)
			}
			variant := v.GetVariant()
			if variant == nil {
				t.Fatalf("not a Variant: %v", v.Sum)
			}
			if variant.Constructor != tc.wantCtor {
				t.Errorf("constructor: got %q, want %q", variant.Constructor, tc.wantCtor)
			}
			if tc.assert != nil {
				tc.assert(t, v)
			}
		})
	}
}

// TestDisclosedContractsToProto_DecodesBlobAndPreservesIDs covers the
// happy path + the most-common failure (a non-base64 blob) for the
// registry → proto conversion. Bad base64 = wrapped error with the
// contract id called out so the user knows which entry to look at.
func TestDisclosedContractsToProto_DecodesBlobAndPreservesIDs(t *testing.T) {
	blob := []byte{0x01, 0x02, 0x03}
	encoded := base64.StdEncoding.EncodeToString(blob)
	in := []registry.DisclosedContract{
		{ContractID: "00cid-a", CreatedEventBlob: encoded, SynchronizerID: "global-domain"},
	}
	out, err := disclosedContractsToProto(in)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("count: got %d, want 1", len(out))
	}
	if out[0].ContractId != "00cid-a" {
		t.Errorf("contractId: %q", out[0].ContractId)
	}
	if string(out[0].CreatedEventBlob) != string(blob) {
		t.Errorf("createdEventBlob: %v vs %v", out[0].CreatedEventBlob, blob)
	}
	if out[0].SynchronizerId != "global-domain" {
		t.Errorf("synchronizerId: %q", out[0].SynchronizerId)
	}
}

func TestDisclosedContractsToProto_BadBase64Errors(t *testing.T) {
	in := []registry.DisclosedContract{
		{ContractID: "00cid-bad", CreatedEventBlob: "not!!base64!!", SynchronizerID: "g"},
	}
	_, err := disclosedContractsToProto(in)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	// Error must call out which contract id failed so the user has
	// somewhere to look (the registry hands back many disclosed
	// contracts per response; a generic decode error isn't actionable).
	if !contains(err.Error(), "00cid-bad") {
		t.Errorf("error %q missing contract id", err.Error())
	}
}

func TestBuildChoiceContextRecord_PreservesKeyOrder(t *testing.T) {
	in := map[string]any{
		"z": "last",
		"a": "first",
		"m": "middle",
	}
	v, err := buildChoiceContextRecord(in)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// Outer Record { values: TextMap AnyValue } — the Daml type is
	// TextMap, NOT GenMap; the participant rejects a GenMap here.
	rec := v.GetRecord()
	if rec == nil || len(rec.Fields) != 1 || rec.Fields[0].Label != "values" {
		t.Fatalf("shape: %v", v.Sum)
	}
	textMap := rec.Fields[0].Value.GetTextMap()
	if textMap == nil {
		t.Fatalf("values not a TextMap: %v", rec.Fields[0].Value.Sum)
	}
	if len(textMap.Entries) != 3 {
		t.Fatalf("entry count: %d", len(textMap.Entries))
	}
	wantKeys := []string{"a", "m", "z"} // lexicographic
	for i, want := range wantKeys {
		gotKey := textMap.Entries[i].Key
		if gotKey != want {
			t.Errorf("entries[%d].key: got %q, want %q", i, gotKey, want)
		}
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
