package token

import "testing"

func burnHoldings() []holdingRef {
	return []holdingRef{
		{ContractID: "c1", Owner: "bob", Admin: "alice", Instrument: "MYT", Amount: "10.0"},
		{ContractID: "c2", Owner: "bob", Admin: "alice", Instrument: "MYT", Amount: "25.0"},
		{ContractID: "c3", Owner: "bob", Provider: "prov", Admin: "alice", Instrument: "MYT", Amount: "100.0"},
	}
}

func TestComputeBurn_ExactNoChange(t *testing.T) {
	// 10 + 25 = 35 exactly covers 35 → no change.
	picked, change, err := computeBurn(burnHoldings(), "35")
	if err != nil {
		t.Fatal(err)
	}
	if len(picked) != 2 || change != "0" {
		t.Errorf("exact burn: picked=%d change=%q, want 2/0", len(picked), change)
	}
}

func TestComputeBurn_WithChange(t *testing.T) {
	// burn 30: smallest-first picks 10 then 25 (=35), change 5.
	picked, change, err := computeBurn(burnHoldings(), "30")
	if err != nil {
		t.Fatal(err)
	}
	if len(picked) != 2 || change != "5" {
		t.Errorf("burn 30: picked=%d change=%q, want 2/5", len(picked), change)
	}
}

func TestComputeBurn_Insufficient(t *testing.T) {
	if _, _, err := computeBurn(burnHoldings(), "1000"); err == nil {
		t.Error("want insufficient error when burn amount exceeds holdings")
	}
}

func TestBurnActAs_UnionOwnersProviderAdmin(t *testing.T) {
	// picks c1 (bob) + c3 (bob, prov) → actAs {bob, prov, alice}.
	picked := []holdingRef{burnHoldings()[0], burnHoldings()[2]}
	got := burnActAs(picked, "alice")
	want := map[string]bool{"bob": true, "prov": true, "alice": true}
	if len(got) != 3 {
		t.Fatalf("actAs = %v, want 3 unique", got)
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("unexpected actAs party %q", p)
		}
	}
}

func TestBurnActAs_Dedup(t *testing.T) {
	// admin == an owner → not duplicated.
	picked := []holdingRef{{Owner: "alice", Admin: "alice"}}
	got := burnActAs(picked, "alice")
	if len(got) != 1 || got[0] != "alice" {
		t.Errorf("dedup failed: %v", got)
	}
}
