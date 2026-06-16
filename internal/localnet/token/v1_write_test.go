package token

import (
	"testing"

	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// TestTransferInterfaceByGeneration pins the per-generation interface +
// registry-segment selection the off-ledger transfer/accept routes on.
func TestTransferInterfaceByGeneration(t *testing.T) {
	if got := transferFactoryInterface(genV1); got != TransferFactoryInterfaceV1 {
		t.Errorf("v1 factory iface = %q", got)
	}
	if got := transferFactoryInterface(genV2); got != TransferFactoryInterfaceV2 {
		t.Errorf("v2 factory iface = %q", got)
	}
	if got := transferInstructionInterface(genV1); got != TransferInstructionInterfaceV1 {
		t.Errorf("v1 instruction iface = %q", got)
	}
	if got := transferInstructionInterface(genV2); got != TransferInstructionInterfaceV2 {
		t.Errorf("v2 instruction iface = %q", got)
	}
	if got := registryVersionSeg(genV1); got != "v1" {
		t.Errorf("v1 seg = %q", got)
	}
	if got := registryVersionSeg(genV2); got != "v2" {
		t.Errorf("v2 seg = %q", got)
	}
	if got := transferInstructionModule(genV1); got != "Splice.Api.Token.TransferInstructionV1" {
		t.Errorf("v1 module = %q", got)
	}
	if got := transferInstructionModule(genV2); got != "Splice.Api.Token.TransferInstructionV2" {
		t.Errorf("v2 module = %q", got)
	}
}

// TestPickGeneration: the transfer routes on the generation of the
// holdings being spent.
func TestPickGeneration(t *testing.T) {
	if got := pickGeneration([]holdingRef{{Gen: genV1}}); got != genV1 {
		t.Errorf("v1 holdings → v1, got %v", got)
	}
	if got := pickGeneration([]holdingRef{{Gen: genV2}}); got != genV2 {
		t.Errorf("v2 holdings → v2, got %v", got)
	}
	if got := pickGeneration(nil); got != genV2 {
		t.Errorf("empty → default v2, got %v", got)
	}
}

// TestInterfaceGeneration_ClassifiesInstructionAndHolding pins that the
// shared classifier recognizes BOTH the Holding interfaces (read path)
// and the TransferInstruction interfaces. The accept now routes by the
// instruction's OWN interface (instructionGeneration), not the
// participant's vetted surfaces — so misclassifying a TransferInstruction
// would misroute a V1 instruction on a dual-surface participant and
// strand the transfer.
func TestInterfaceGeneration_ClassifiesInstructionAndHolding(t *testing.T) {
	cases := []struct {
		module string
		want   Generation
		ok     bool
	}{
		{"Splice.Api.Token.TransferInstructionV1", genV1, true},
		{"Splice.Api.Token.TransferInstructionV2", genV2, true},
		{"Splice.Api.Token.HoldingV1", genV1, true},
		{"Splice.Api.Token.HoldingV2", genV2, true},
		{"Splice.Api.Token.MetadataV1", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, ok := interfaceGeneration(&lapiv2.Identifier{ModuleName: c.module})
		if ok != c.ok || got != c.want {
			t.Errorf("interfaceGeneration(%q) = (%v,%v), want (%v,%v)", c.module, got, ok, c.want, c.ok)
		}
	}
	if _, ok := interfaceGeneration(nil); ok {
		t.Error("nil interface id must not classify")
	}
}
