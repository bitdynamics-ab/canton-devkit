package token

import "testing"

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

// TestAcceptGeneration: the accept (no holdings) routes on the vetted
// surfaces, preferring V2 when both — which matches the transfer side,
// so the two never disagree on a contract.
func TestAcceptGeneration(t *testing.T) {
	if got := acceptGeneration(Surfaces{HasV1: true}); got != genV1 {
		t.Errorf("v1-only → v1, got %v", got)
	}
	if got := acceptGeneration(Surfaces{HasV2: true}); got != genV2 {
		t.Errorf("v2-only → v2, got %v", got)
	}
	if got := acceptGeneration(Surfaces{HasV1: true, HasV2: true}); got != genV2 {
		t.Errorf("both → v2 (matches transfer prefer-v2), got %v", got)
	}
}
