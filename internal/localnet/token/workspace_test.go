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

func TestSummarizeInstrument_KPIsAndDistribution(t *testing.T) {
	// alice holds 17255 Amulet, bob holds 75.16 across 1 UTXO.
	ws := &Workspace{Holdings: sampleHoldings()}
	s := summarizeInstrument(ws, "Amulet")

	if s.TotalSupply != "17330.16" {
		t.Errorf("total supply: got %q, want 17330.16", s.TotalSupply)
	}
	if s.HolderCount != 2 {
		t.Errorf("holder count: got %d, want 2", s.HolderCount)
	}
	if s.ContractCount != 2 {
		t.Errorf("contract count: got %d, want 2", s.ContractCount)
	}
	// Holders sorted by balance desc — alice (17255) first.
	if len(s.Holders) != 2 || s.Holders[0].Party != "alice" {
		t.Fatalf("holders not sorted desc by balance: %+v", s.Holders)
	}
	if s.Holders[0].Balance != "17255.00" {
		t.Errorf("alice balance: got %q, want 17255.00", s.Holders[0].Balance)
	}
	// alice ≈ 99.6% of supply.
	if s.Holders[0].PctOfSupply != "99.6" {
		t.Errorf("alice pct: got %q, want 99.6", s.Holders[0].PctOfSupply)
	}
}

func TestSummarizeInstrument_MultiUTXOContractCount(t *testing.T) {
	// bob holds MYT across 3 contracts; one holder, three contracts.
	ws := &Workspace{Holdings: sampleHoldings()}
	s := summarizeInstrument(ws, "MYT")
	if s.HolderCount != 1 || s.ContractCount != 3 {
		t.Errorf("MYT: holders=%d contracts=%d, want 1/3", s.HolderCount, s.ContractCount)
	}
	if s.Holders[0].PctOfSupply != "100.0" {
		t.Errorf("sole holder pct: got %q, want 100.0", s.Holders[0].PctOfSupply)
	}
}

func TestSummarizeInstrument_EmptyZeroSafe(t *testing.T) {
	s := summarizeInstrument(&Workspace{}, "MYT")
	if s.TotalSupply != "0" || s.HolderCount != 0 || s.ContractCount != 0 {
		t.Errorf("empty workspace not zero-safe: %+v", s)
	}
	if len(s.Holders) != 0 {
		t.Errorf("empty holders expected, got %+v", s.Holders)
	}
}

// TestBuildMatrix_PropagatesTruncated pins D1: when scanWorkspace
// hits maxWorkspaceScan and returns truncated=true, buildMatrix
// must carry the flag through so the UI can render
// "matrix shows N of many" instead of misleading per-instrument
// totals. The unbounded ACS read this guards against could otherwise
// silently pump us into OOM.
func TestBuildMatrix_PropagatesTruncated(t *testing.T) {
	ws := &Workspace{Truncated: true}
	m := buildMatrix(ws)
	if !m.Truncated {
		t.Errorf("BalanceMatrix.Truncated = false; want true (propagated from workspace)")
	}

	ws2 := &Workspace{}
	m2 := buildMatrix(ws2)
	if m2.Truncated {
		t.Errorf("BalanceMatrix.Truncated = true for untruncated workspace; want false")
	}
}
