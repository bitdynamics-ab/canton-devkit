package token

import "testing"

// sample holdings: bob holds MYT across 3 UTXOs + Amulet in 1; alice holds Amulet.
func sampleHoldings() []HoldingContract {
	return []HoldingContract{
		{ContractID: "c1", Party: "bob", Admin: "alice", InstrumentID: "MYT", Amount: "1000.0"},
		{ContractID: "c2", Party: "bob", Admin: "alice", InstrumentID: "MYT", Amount: "250.0"},
		{ContractID: "c3", Party: "bob", Admin: "alice", InstrumentID: "MYT", Amount: "25.0", Locked: true},
		{ContractID: "c4", Party: "bob", Admin: "DSO", InstrumentID: "Amulet", Amount: "75.16"},
		{ContractID: "c5", Party: "alice", Admin: "DSO", InstrumentID: "Amulet", Amount: "17255.00"},
	}
}

func TestBuildMatrix_SumsPerPartyInstrument(t *testing.T) {
	ws := &Workspace{Holdings: sampleHoldings()}
	m := buildMatrix(ws)

	// bob/MYT = 1000 + 250 + 25 = 1275
	got := cellAmount(m.Cells, "bob", "MYT")
	if got != "1275.0" {
		t.Errorf("bob/MYT: got %q, want 1275.0", got)
	}
	// totals: MYT = 1275, Amulet = 75.16 + 17255.00 = 17330.16
	if tot := cellAmount(m.Totals, "", "Amulet"); tot != "17330.16" {
		t.Errorf("Amulet total: got %q, want 17330.16", tot)
	}
	if tot := cellAmount(m.Totals, "", "MYT"); tot != "1275.0" {
		t.Errorf("MYT total: got %q, want 1275.0", tot)
	}
	// cells are sparse — no zero entries; bob holds 2 instruments, alice 1
	if len(m.Cells) != 3 {
		t.Errorf("cell count: got %d, want 3 (bob×2 + alice×1)", len(m.Cells))
	}
}

func TestInstrumentsFromHoldings_DistinctAndLabeled(t *testing.T) {
	// instance "" → registry.Read fails → no metadata enrichment, which
	// is the path we assert (symbol falls back to instrumentId).
	instr := instrumentsFromHoldings(sampleHoldings(), "")
	if len(instr) != 2 {
		t.Fatalf("instrument count: got %d, want 2 (MYT, Amulet)", len(instr))
	}
	bySym := map[string]InstrumentRef{}
	for _, i := range instr {
		bySym[i.Symbol] = i
	}
	if bySym["Amulet"].Standard != "Splice Amulet" {
		t.Errorf("Amulet standard: got %q", bySym["Amulet"].Standard)
	}
	if bySym["MYT"].Standard != "CIP-0112 v2" {
		t.Errorf("MYT standard: got %q", bySym["MYT"].Standard)
	}
	if !bySym["MYT"].OnLedger {
		t.Errorf("MYT should be on_ledger")
	}
	// symbol falls back to instrumentId when no metadata
	if bySym["MYT"].Symbol != "MYT" {
		t.Errorf("MYT symbol fallback: got %q", bySym["MYT"].Symbol)
	}
}

func TestStandardFor(t *testing.T) {
	if standardFor("Amulet") != "Splice Amulet" {
		t.Error("Amulet should be Splice Amulet")
	}
	if standardFor("MYT") != "CIP-0112 v2" {
		t.Error("non-Amulet should be CIP-0112 v2")
	}
}

func cellAmount(cells []MatrixCell, party, inst string) string {
	for _, c := range cells {
		if c.Party == party && c.InstrumentID == inst {
			return c.Amount
		}
	}
	return ""
}
