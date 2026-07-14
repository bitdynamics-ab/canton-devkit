package token

import (
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/registry"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// V2 allocation choice-argument Value builders. Hand-built (like the transfer
// builders in value_builders.go) from the typed `registry.Allocation*`
// structs into the deeply-nested `lapiv2.Value` records
// AllocationFactory_Allocate accepts. Shapes track upstream Splice's
// AllocationV2.daml / AllocationInstructionV2.daml; the field lists below are
// pinned to those records.

// buildSettlementInfoRecord:
//
//	SettlementInfo with executors : [Party]; id : Text;
//	  cid : Optional AnyContractId; meta : Metadata
//
// cid is left None — the settlement id text correlates a batch.
func buildSettlementInfoRecord(s registry.SettlementInfo) *lapiv2.Value {
	return recordValue([]field{
		{"executors", listValue(s.Executors, partyValue)},
		{"id", textValue(s.ID)},
		{"cid", optionalValue(nil)},
		{"meta", buildMetadataRecord(s.Meta)},
	})
}

// buildTransferLegSideRecord (v2 flat directed side):
//
//	TransferLegSide with transferLegId : Text;
//	  side : TransferSide (enum SenderSide | ReceiverSide); otherside : Account;
//	  amount : Decimal; instrumentId : Text; meta : Metadata
func buildTransferLegSideRecord(s registry.TransferLegSide) *lapiv2.Value {
	return recordValue([]field{
		{"transferLegId", textValue(s.TransferLegID)},
		{"side", enumValue(s.Side)},
		{"otherside", buildAccountRecord(s.Otherside)},
		{"amount", numericValue(s.Amount)},
		{"instrumentId", textValue(s.InstrumentID)},
		{"meta", buildMetadataRecord(s.Meta)},
	})
}

// enumValue builds a Daml enum Value (a nullary sum-type constructor).
func enumValue(constructor string) *lapiv2.Value {
	return &lapiv2.Value{Sum: &lapiv2.Value_Enum{Enum: &lapiv2.Enum{Constructor: constructor}}}
}

// buildAllocationSpecRecord:
//
//	AllocationSpecification with admin : Party; authorizer : Account;
//	  transferLegSides : [TransferLegSide]; settlementDeadline : Optional Time;
//	  nextIterationFunding : Optional (TextMap Decimal); committed : Bool;
//	  meta : Metadata
func buildAllocationSpecRecord(a registry.AllocationSpecification) *lapiv2.Value {
	var deadline *lapiv2.Value
	if a.SettlementDeadline != nil {
		deadline = timestampValue(*a.SettlementDeadline)
	}
	var nextFunding *lapiv2.Value
	if a.NextIterationFunding != nil {
		nextFunding = numericTextMapValue(a.NextIterationFunding)
	}
	return recordValue([]field{
		{"admin", partyValue(a.Admin)},
		{"authorizer", buildAccountRecord(a.Authorizer)},
		{"transferLegSides", listValue(a.TransferLegSides, buildTransferLegSideRecord)},
		{"settlementDeadline", optionalValue(deadline)},
		{"nextIterationFunding", optionalValue(nextFunding)},
		{"committed", boolValue(a.Committed)},
		{"meta", buildMetadataRecord(a.Meta)},
	})
}

// optionalValue builds a Daml `Optional a`: Some(inner) when inner is
// non-nil, None otherwise. The arbitrary-inner counterpart to
// optionalPartyValue.
func optionalValue(inner *lapiv2.Value) *lapiv2.Value {
	if inner == nil {
		return &lapiv2.Value{Sum: &lapiv2.Value_Optional{Optional: &lapiv2.Optional{}}}
	}
	return &lapiv2.Value{Sum: &lapiv2.Value_Optional{Optional: &lapiv2.Optional{Value: inner}}}
}

// numericTextMapValue builds a Daml `TextMap Decimal` from a
// string→decimal-string map. Entries are key-sorted for a deterministic wire
// form.
func numericTextMapValue(m map[string]string) *lapiv2.Value {
	entries := make([]*lapiv2.TextMap_Entry, 0, len(m))
	for _, k := range sortedKeys(m) {
		entries = append(entries, &lapiv2.TextMap_Entry{Key: k, Value: numericValue(m[k])})
	}
	return &lapiv2.Value{Sum: &lapiv2.Value_TextMap{TextMap: &lapiv2.TextMap{Entries: entries}}}
}
