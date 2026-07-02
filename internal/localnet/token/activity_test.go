package token

import (
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

func TestBuildActivity_Mint(t *testing.T) {
	// A mint: receiver holding created, nothing archived.
	txs := []rawTx{{
		offset: 10, updateID: "u10", recordTime: "t10",
		deltas: []rawHoldingDelta{
			{party: "bob", instrument: "MYT", amount: "1000.0", created: true},
		},
	}}
	got := buildActivity(txs, "MYT", -1, 50)
	if len(got) != 1 {
		t.Fatalf("want 1 event, got %d", len(got))
	}
	ev := got[0]
	if ev.Kind != "mint" || ev.Amount != "1000" {
		t.Errorf("kind/amount: got %s/%s, want mint/1000", ev.Kind, ev.Amount)
	}
	if len(ev.Senders) != 0 || len(ev.Receivers) != 1 || ev.Receivers[0].Party != "bob" {
		t.Errorf("receivers wrong: %+v", ev)
	}
}

func TestBuildActivity_Burn(t *testing.T) {
	// A burn: holder's contract archived, nothing created for it.
	txs := []rawTx{{
		offset: 20, updateID: "u20",
		deltas: []rawHoldingDelta{
			{party: "bob", instrument: "MYT", amount: "50.0", created: false},
		},
	}}
	got := buildActivity(txs, "MYT", -1, 50)
	if len(got) != 1 || got[0].Kind != "burn" || got[0].Amount != "50" {
		t.Fatalf("want burn/50, got %+v", got)
	}
	if len(got[0].Senders) != 1 || len(got[0].Receivers) != 0 {
		t.Errorf("burn should have a sender, no receiver: %+v", got[0])
	}
}

func TestBuildActivity_TransferWithChange(t *testing.T) {
	// bob transfers 100 to app-user: archives 275 across 3 UTXOs,
	// receives 175 change, app-user receives 100. Net: bob -100, app +100.
	txs := []rawTx{{
		offset: 30, updateID: "u30",
		deltas: []rawHoldingDelta{
			{party: "bob", instrument: "MYT", amount: "12.0", created: false},
			{party: "bob", instrument: "MYT", amount: "13.0", created: false},
			{party: "bob", instrument: "MYT", amount: "250.0", created: false},
			{party: "bob", instrument: "MYT", amount: "175.0", created: true},
			{party: "app-user", instrument: "MYT", amount: "100.0", created: true},
		},
	}}
	got := buildActivity(txs, "MYT", -1, 50)
	if len(got) != 1 {
		t.Fatalf("want 1 event, got %d", len(got))
	}
	ev := got[0]
	if ev.Kind != "transfer" || ev.Amount != "100" {
		t.Errorf("kind/amount: got %s/%s, want transfer/100", ev.Kind, ev.Amount)
	}
	if len(ev.Senders) != 1 || ev.Senders[0].Party != "bob" || ev.Senders[0].Amount != "100" {
		t.Errorf("sender wrong: %+v", ev.Senders)
	}
	if len(ev.Receivers) != 1 || ev.Receivers[0].Party != "app-user" || ev.Receivers[0].Amount != "100" {
		t.Errorf("receiver wrong: %+v", ev.Receivers)
	}
}

func TestBuildActivity_FiltersInstrumentAndNewestFirst(t *testing.T) {
	txs := []rawTx{
		{offset: 1, deltas: []rawHoldingDelta{{party: "a", instrument: "MYT", amount: "1.0", created: true}}},
		{offset: 2, deltas: []rawHoldingDelta{{party: "a", instrument: "Amulet", amount: "5.0", created: true}}},
		{offset: 3, deltas: []rawHoldingDelta{{party: "b", instrument: "MYT", amount: "2.0", created: true}}},
	}
	got := buildActivity(txs, "MYT", -1, 50)
	if len(got) != 2 {
		t.Fatalf("want 2 MYT events (Amulet filtered out), got %d", len(got))
	}
	// Newest first: offset 3 then 1.
	if got[0].Offset != 3 || got[1].Offset != 1 {
		t.Errorf("not newest-first: %d, %d", got[0].Offset, got[1].Offset)
	}
}

func TestBuildActivity_LimitAndZeroNetSkipped(t *testing.T) {
	txs := []rawTx{
		{offset: 1, deltas: []rawHoldingDelta{{party: "a", instrument: "MYT", amount: "1.0", created: true}}},
		{offset: 2, deltas: []rawHoldingDelta{{party: "a", instrument: "MYT", amount: "2.0", created: true}}},
		{offset: 3, deltas: []rawHoldingDelta{{party: "a", instrument: "MYT", amount: "3.0", created: true}}},
		// self-split: created and archived same magnitude → nets to zero, skipped.
		{offset: 4, deltas: []rawHoldingDelta{
			{party: "a", instrument: "MYT", amount: "9.0", created: false},
			{party: "a", instrument: "MYT", amount: "9.0", created: true},
		}},
	}
	got := buildActivity(txs, "MYT", -1, 2)
	if len(got) != 2 {
		t.Fatalf("limit not applied: got %d", len(got))
	}
	for _, ev := range got {
		if ev.Offset == 4 {
			t.Errorf("zero-net transaction should have been skipped")
		}
	}
}

// TestConsumeActivityStream_TruncatesAtCap pins the OOM guard: when the
// updates stream emits more than maxActivityScan items, the consumer
// stops at the cap and returns truncated=true. Mirrors maxWorkspaceScan
// in workspace.go — a runaway transaction stream on a busy ledger
// would otherwise pump us into OOM via an unbounded slice.
func TestConsumeActivityStream_TruncatesAtCap(t *testing.T) {
	ch := make(chan ledger.StreamItem[*lapiv2.GetUpdatesResponse], maxActivityScan+1)
	for i := 0; i < maxActivityScan+1; i++ {
		ch <- ledger.StreamItem[*lapiv2.GetUpdatesResponse]{Value: &lapiv2.GetUpdatesResponse{}}
	}
	close(ch)
	txs, truncated, err := consumeActivityStream(ch)
	if err != nil {
		t.Fatalf("consumeActivityStream: %v", err)
	}
	if !truncated {
		t.Errorf("truncated = false; want true after %d items", maxActivityScan+1)
	}
	if len(txs) != 0 {
		// Empty GetUpdatesResponse contains no transaction; no rawTx
		// should accumulate. The point of the test is the cap path.
		t.Errorf("unexpected rawTx accumulation: got %d", len(txs))
	}
}

func TestConsumeActivityStream_BelowCapNotTruncated(t *testing.T) {
	ch := make(chan ledger.StreamItem[*lapiv2.GetUpdatesResponse], 4)
	for i := 0; i < 3; i++ {
		ch <- ledger.StreamItem[*lapiv2.GetUpdatesResponse]{Value: &lapiv2.GetUpdatesResponse{}}
	}
	close(ch)
	_, truncated, err := consumeActivityStream(ch)
	if err != nil {
		t.Fatalf("consumeActivityStream: %v", err)
	}
	if truncated {
		t.Errorf("truncated = true at 3 items; want false (cap is %d)", maxActivityScan)
	}
}

// TestNetTransaction_ExactDecimalNetting pins that netTransaction nets
// base-10 decimal amounts exactly (big.Rat, not big.Float — Float
// would surface 0.1 + 0.2 - 0.3 as a tiny non-zero drift).
func TestNetTransaction_ExactDecimalNetting(t *testing.T) {
	// Net: alice +0.1 +0.2 -0.3 = 0 (skipped). To prove the math is
	// exact, give alice +0.3 and bob -(0.1+0.2) so the event surfaces
	// with Amount that is exactly "0.3" — big.Float would give
	// "0.30000000000000004".
	tx := rawTx{
		offset: 1, updateID: "u1",
		deltas: []rawHoldingDelta{
			{party: "alice", instrument: "MYT", amount: "0.3", created: true},
			{party: "bob", instrument: "MYT", amount: "0.1", created: false},
			{party: "bob", instrument: "MYT", amount: "0.2", created: false},
		},
	}
	ev, ok := netTransaction(tx, "MYT", 18)
	if !ok {
		t.Fatalf("netTransaction returned ok=false")
	}
	if ev.Amount != "0.3" {
		t.Errorf("Amount = %q; want exactly \"0.3\" (big.Float would give 0.30000000000000004)", ev.Amount)
	}
	if len(ev.Senders) != 1 || ev.Senders[0].Amount != "0.3" {
		t.Errorf("sender amount = %+v; want exactly 0.3", ev.Senders)
	}
	if len(ev.Receivers) != 1 || ev.Receivers[0].Amount != "0.3" {
		t.Errorf("receiver amount = %+v; want exactly 0.3", ev.Receivers)
	}
}

// TestNetTransaction_ZeroNet_DropsEvent confirms 0.1+0.2-0.3 nets to
// exactly 0 (not 5.55e-17 drift) — the zero-net branch then drops the
// event. With big.Float this test would FAIL because the residual drift
// would land in either the senders or receivers slice.
func TestNetTransaction_ZeroNet_DropsEvent(t *testing.T) {
	tx := rawTx{
		offset: 1,
		deltas: []rawHoldingDelta{
			{party: "alice", instrument: "MYT", amount: "0.1", created: true},
			{party: "alice", instrument: "MYT", amount: "0.2", created: true},
			{party: "alice", instrument: "MYT", amount: "0.3", created: false},
		},
	}
	_, ok := netTransaction(tx, "MYT", 18)
	if ok {
		t.Errorf("0.1+0.2-0.3 must net to exact zero (event dropped); big.Float would leave drift")
	}
}

// TestNetTransaction_18DPRoundTrip pins exact 18-decimal-place fidelity:
// an 18-dp Decimal sent and received must round-trip bit-exact. big.Float
// silently loses precision past ~15 significant digits.
func TestNetTransaction_18DPRoundTrip(t *testing.T) {
	const amt = "1.123456789012345678"
	tx := rawTx{
		offset: 1,
		deltas: []rawHoldingDelta{
			{party: "alice", instrument: "MYT", amount: amt, created: false},
			{party: "bob", instrument: "MYT", amount: amt, created: true},
		},
	}
	ev, ok := netTransaction(tx, "MYT", 18)
	if !ok {
		t.Fatalf("netTransaction returned ok=false")
	}
	if ev.Amount != amt {
		t.Errorf("Amount = %q; want %q (18-dp round-trip)", ev.Amount, amt)
	}
}
