package token

import (
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

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
	if bySym["MYT"].Standard != "Token Standard V2 (CIP-0112)" {
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

// Regression: a token created but never minted (its TokenRules exists,
// no Holding does) must still appear in the instrument list. Before the
// fix, discovery was holdings-only, so a created-but-unminted instrument
// was invisible whenever the ledger was live.
func TestMergeInstruments_IncludesRecordedWithoutHoldings(t *testing.T) {
	holdings := []HoldingContract{
		{ContractID: "c1", Party: "bob", Admin: "alice", InstrumentID: "MYT", Amount: "100.0", Gen: genV2},
	}
	recorded := map[string]registry.TokenRef{
		// Minted (has a holding): enriches the holdings row, no duplicate.
		"MYT": {Symbol: "MYT", Name: "My Token", Decimals: 4, InstrumentID: "MYT", IssuerParty: "alice", Status: "on-ledger"},
		// Created but never minted: must still be listed, zero supply.
		"ZHE": {Symbol: "ZHE", Name: "ZHE LI", Decimals: 6, InstrumentID: "ZHE", IssuerParty: "issuer1", Status: "on-ledger"},
		// Offline-recorded (no ledger anchor): listed but not on_ledger.
		"OFF": {Symbol: "OFF", Name: "Offline", Decimals: 2, InstrumentID: "off-id", IssuerParty: "issuer2", Status: "recorded"},
	}

	instr := mergeInstruments(holdings, recorded)
	bySym := map[string]InstrumentRef{}
	for _, i := range instr {
		bySym[i.Symbol] = i
	}
	if len(instr) != 3 {
		t.Fatalf("instrument count: got %d, want 3 (MYT, OFF, ZHE)", len(instr))
	}

	// MYT: single enriched row (registry metadata applied, not duplicated).
	if myt := bySym["MYT"]; myt.Name != "My Token" || myt.Decimals != 4 || !myt.OnLedger {
		t.Errorf("MYT not enriched as a single on-ledger row: %+v", myt)
	}

	// ZHE: created-but-unminted, surfaced with the issuer as admin.
	zhe, ok := bySym["ZHE"]
	if !ok {
		t.Fatal("ZHE (created but unminted) should still be listed")
	}
	if zhe.Admin != "issuer1" || !zhe.OnLedger {
		t.Errorf("ZHE admin/on_ledger: got admin=%q on_ledger=%v, want issuer1/true", zhe.Admin, zhe.OnLedger)
	}
	if zhe.Name != "ZHE LI" || zhe.Decimals != 6 {
		t.Errorf("ZHE metadata not carried: %+v", zhe)
	}
	if zhe.Standard != "Token Standard V2 (CIP-0112)" {
		t.Errorf("ZHE standard: got %q, want V2", zhe.Standard)
	}

	// OFF: recorded offline → listed but flagged not on_ledger.
	if off := bySym["OFF"]; off.OnLedger || off.InstrumentID != "off-id" {
		t.Errorf("OFF should be listed as not-on_ledger with its instrument id: %+v", off)
	}
}

func TestStandardFor(t *testing.T) {
	if standardFor(genV2, "Amulet") != "Splice Amulet" {
		t.Error("Amulet should be Splice Amulet")
	}
	if standardFor(genV1, "MYT") != "Token Standard V1 (CIP-0056)" {
		t.Error("a V1 instrument should be Token Standard V1 (CIP-0056)")
	}
	if standardFor(genV2, "MYT") != "Token Standard V2 (CIP-0112)" {
		t.Error("a V2 instrument should be Token Standard V2 (CIP-0112)")
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
