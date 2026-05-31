package token

import "testing"

func TestBuildActivity_Mint(t *testing.T) {
	// A mint: receiver holding created, nothing archived.
	txs := []rawTx{{
		offset: 10, updateID: "u10", recordTime: "t10",
		deltas: []rawHoldingDelta{
			{party: "bob", instrument: "MYT", amount: "1000.0", created: true},
		},
	}}
	got := buildActivity(txs, "MYT", 50)
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
	got := buildActivity(txs, "MYT", 50)
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
	got := buildActivity(txs, "MYT", 50)
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
	got := buildActivity(txs, "MYT", 50)
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
	got := buildActivity(txs, "MYT", 2)
	if len(got) != 2 {
		t.Fatalf("limit not applied: got %d", len(got))
	}
	for _, ev := range got {
		if ev.Offset == 4 {
			t.Errorf("zero-net transaction should have been skipped")
		}
	}
}
