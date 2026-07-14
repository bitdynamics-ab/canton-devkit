package token

import (
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/registry"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// V2 allocation choice-argument Value builders. Hand-built (like the
// transfer builders in value_builders.go) from the typed
// `registry.Allocation*` structs into the deeply-nested `lapiv2.Value`
// records AllocationFactory_Allocate accepts. References (upstream Splice,
// branch token-standard-v2-upcoming):
//
//	token-standard/splice-api-token-allocation-v2/
//	  daml/Splice/Api/Token/AllocationV2.daml
//	    — SettlementInfo, TransferLeg, TransferLegSide, AllocationSpecification
//	token-standard/splice-api-token-allocation-instruction-v2/
//	  daml/Splice/Api/Token/AllocationInstructionV2.daml
//	    — AllocationFactory_Allocate signature

// buildReferenceRecord:
//
//	data Reference = Reference with
//	  id  : Text
//	  cid : Optional AnyContractId
//
// cid is left None for our flows (the settlement id text is what
// correlates a batch).
func buildReferenceRecord(r registry.Reference) *lapiv2.Value {
	var cid *lapiv2.Value
	if r.Cid != nil {
		cid = contractIDValue(*r.Cid)
	}
	return recordValue([]field{
		{"id", textValue(r.ID)},
		{"cid", optionalValue(cid)},
	})
}

// buildSettlementInfoRecord:
//
//	data SettlementInfo = SettlementInfo with
//	  executor       : Party
//	  settlementRef  : Reference
//	  requestedAt    : Time
//	  allocateBefore : Time
//	  settleBefore   : Time
//	  meta           : Metadata
func buildSettlementInfoRecord(s registry.SettlementInfo) *lapiv2.Value {
	return recordValue([]field{
		{"executor", partyValue(s.Executor)},
		{"settlementRef", buildReferenceRecord(s.SettlementRef)},
		{"requestedAt", timestampValue(s.RequestedAt)},
		{"allocateBefore", timestampValue(s.AllocateBefore)},
		{"settleBefore", timestampValue(s.SettleBefore)},
		{"meta", buildMetadataRecord(s.Meta)},
	})
}

// buildTransferLegRecord:
//
//	data TransferLeg = TransferLeg with
//	  sender       : Account
//	  receiver     : Account
//	  amount       : Decimal
//	  instrumentId : InstrumentId
//	  meta         : Metadata
func buildTransferLegRecord(l registry.TransferLeg) *lapiv2.Value {
	return recordValue([]field{
		{"sender", buildAccountRecord(l.Sender)},
		{"receiver", buildAccountRecord(l.Receiver)},
		{"amount", numericValue(l.Amount)},
		{"instrumentId", buildInstrumentIDRecord(l.InstrumentID)},
		{"meta", buildMetadataRecord(l.Meta)},
	})
}

// buildTransferLegSideRecord:
//
//	data TransferLegSide = TransferLegSide with
//	  transferLegId : Text
//	  transferLeg   : TransferLeg
func buildTransferLegSideRecord(s registry.TransferLegSide) *lapiv2.Value {
	return recordValue([]field{
		{"transferLegId", textValue(s.TransferLegID)},
		{"transferLeg", buildTransferLegRecord(s.TransferLeg)},
	})
}

// buildAllocationSpecRecord:
//
//	data AllocationSpecification = AllocationSpecification with
//	  admin                : Party
//	  authorizer           : Account
//	  transferLegSides     : [TransferLegSide]
//	  settlementDeadline   : Optional Time
//	  nextIterationFunding : Optional (TextMap Decimal)
//	  committed            : Bool
//	  meta                 : Metadata
func buildAllocationSpecRecord(a registry.AllocationSpecification) *lapiv2.Value {
	var deadline *lapiv2.Value
	if a.SettlementDeadline != nil {
		deadline = timestampValue(*a.SettlementDeadline)
	}
	var nextFunding *lapiv2.Value
	if a.NextIterationFunding != nil {
		// TextMap Decimal — encode each value as a Numeric Value.
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
// non-nil, None (empty Optional) otherwise. Complements
// optionalPartyValue (which is Party-specialised) for arbitrary inner
// Values.
func optionalValue(inner *lapiv2.Value) *lapiv2.Value {
	if inner == nil {
		return &lapiv2.Value{Sum: &lapiv2.Value_Optional{Optional: &lapiv2.Optional{}}}
	}
	return &lapiv2.Value{Sum: &lapiv2.Value_Optional{Optional: &lapiv2.Optional{Value: inner}}}
}

// numericTextMapValue builds a Daml `TextMap Decimal` from a string→
// decimal-string map. Entries are sorted by key for a deterministic wire
// form, like textMapValue.
func numericTextMapValue(m map[string]string) *lapiv2.Value {
	entries := make([]*lapiv2.TextMap_Entry, 0, len(m))
	for _, k := range sortedKeys(m) {
		entries = append(entries, &lapiv2.TextMap_Entry{Key: k, Value: numericValue(m[k])})
	}
	return &lapiv2.Value{Sum: &lapiv2.Value_TextMap{TextMap: &lapiv2.TextMap{Entries: entries}}}
}
