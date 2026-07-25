package token

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
	adminv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2/admin"
)

// --- synthetic EventLog_HoldingsChange fixture builders ---

// legFix is one TransferLegSide in a synthetic EventLog event.
type legFix struct {
	side       string // "SenderSide" | "ReceiverSide" (Daml enum ctor)
	otherside  string // counterparty owner party
	amount     string
	instrument string
	legID      string
}

// eventLogFix parameterizes one synthetic EventLog_HoldingsChange.
type eventLogFix struct {
	offset      int64
	updateID    string
	admin       string
	account     string // the party whose holdings changed
	consumedCid int    // len(inputHoldingCids)
	createdCid  int    // len(outputHoldingCids)
	legs        []legFix
	// omitInterfaceID drops ExercisedEvent.InterfaceId to prove the
	// choice-name-only discriminator path.
	omitInterfaceID bool
	// wrongChoice sets a non-EventLog choice name so the consumer skips it.
	wrongChoice bool
}

// optPartyVal builds Some party (empty → None), a string-arg convenience over
// optionalPartyValue.
func optPartyVal(p string) *lapiv2.Value {
	if p == "" {
		return optionalPartyValue(nil)
	}
	return optionalPartyValue(&p)
}

// accountValue builds an Account record with the given owner.
func accountValue(owner string) *lapiv2.Value {
	return recordValue([]field{
		{"owner", optPartyVal(owner)},
		{"provider", optPartyVal("")},
		{"id", textValue("acct-" + owner)},
	})
}

func legValue(l legFix) *lapiv2.Value {
	return recordValue([]field{
		{"transferLegId", textValue(l.legID)},
		{"side", enumValue(l.side)},
		{"otherside", accountValue(l.otherside)},
		{"amount", numericValue(l.amount)},
		{"instrumentId", textValue(l.instrument)},
		{"meta", recordValue(nil)},
	})
}

// holdingsChangeArg builds the EventLog_HoldingsChange choice argument
// record from a fixture.
func holdingsChangeArg(f eventLogFix) *lapiv2.Value {
	cids := func(n int) *lapiv2.Value {
		items := make([]string, n)
		for i := range items {
			items[i] = "cid"
		}
		return listValue(items, contractIDValue)
	}
	return recordValue([]field{
		{"admin", partyValue(f.admin)},
		{"account", accountValue(f.account)},
		{"inputHoldingCids", cids(f.consumedCid)},
		{"transferLegSides", listValue(f.legs, legValue)},
		{"outputHoldingCids", cids(f.createdCid)},
		{"observers", listValue([]string(nil), partyValue)},
	})
}

// eventLogUpdate wraps a synthetic EventLog_HoldingsChange exercised
// event in a GetUpdatesResponse transaction.
func eventLogUpdate(f eventLogFix) *lapiv2.GetUpdatesResponse {
	_, mod, entity := splitInterfaceID(EventLogInterfaceV2)
	choice := eventLogHoldingsChangeChoice
	if f.wrongChoice {
		choice = "SomeOtherChoice"
	}
	x := &lapiv2.ExercisedEvent{
		Choice:         choice,
		ChoiceArgument: holdingsChangeArg(f),
		Consuming:      false,
	}
	if !f.omitInterfaceID {
		x.InterfaceId = &lapiv2.Identifier{PackageId: "#pkg", ModuleName: mod, EntityName: entity}
	}
	return &lapiv2.GetUpdatesResponse{
		Update: &lapiv2.GetUpdatesResponse_Transaction{
			Transaction: &lapiv2.Transaction{
				UpdateId: f.updateID,
				Offset:   f.offset,
				Events:   []*lapiv2.Event{{Event: &lapiv2.Event_Exercised{Exercised: x}}},
			},
		},
	}
}

// eventLogUpdatePair wraps two EventLog_HoldingsChange exercised events —
// the sender-side and receiver-side reports of one transfer — into a single
// transaction (same updateID/offset), the shape a real transfer produces.
func eventLogUpdatePair(sender, receiver eventLogFix) *lapiv2.GetUpdatesResponse {
	_, mod, entity := splitInterfaceID(EventLogInterfaceV2)
	mk := func(f eventLogFix) *lapiv2.Event {
		x := &lapiv2.ExercisedEvent{
			Choice:         eventLogHoldingsChangeChoice,
			ChoiceArgument: holdingsChangeArg(f),
			InterfaceId:    &lapiv2.Identifier{PackageId: "#pkg", ModuleName: mod, EntityName: entity},
		}
		return &lapiv2.Event{Event: &lapiv2.Event_Exercised{Exercised: x}}
	}
	return &lapiv2.GetUpdatesResponse{
		Update: &lapiv2.GetUpdatesResponse_Transaction{
			Transaction: &lapiv2.Transaction{
				UpdateId: sender.updateID,
				Offset:   sender.offset,
				Events:   []*lapiv2.Event{mk(sender), mk(receiver)},
			},
		},
	}
}

// TestEventLog_DedupsPairedTransferSides: a transfer surfaces as two
// EventLog_HoldingsChange exercises in one update — one per account side —
// sharing the same updateID and transferLegId. The consumer must collapse
// the pair to a single event rather than double-count the movement.
func TestEventLog_DedupsPairedTransferSides(t *testing.T) {
	upd := []*lapiv2.GetUpdatesResponse{eventLogUpdatePair(
		eventLogFix{offset: 30, updateID: "u30", admin: "dso", account: "bob", consumedCid: 1,
			legs: []legFix{{side: "SenderSide", otherside: "app-user", amount: "100.0", instrument: "MYT", legID: "L1"}}},
		eventLogFix{offset: 30, updateID: "u30", admin: "dso", account: "app-user", createdCid: 1,
			legs: []legFix{{side: "ReceiverSide", otherside: "bob", amount: "100.0", instrument: "MYT", legID: "L1"}}},
	)}
	got := consumeAndRender(t, upd, "MYT")
	if len(got) != 1 {
		t.Fatalf("paired sides not deduped: want 1 event, got %d", len(got))
	}
	if got[0].Kind != "transfer" || got[0].Amount != "100" {
		t.Errorf("kind/amount = %s/%s, want transfer/100", got[0].Kind, got[0].Amount)
	}
}

// consumeAndRender runs the pure classification pipeline the live path uses
// after opening the stream.
func consumeAndRender(t *testing.T, updates []*lapiv2.GetUpdatesResponse, instrument string) []ActivityEvent {
	t.Helper()
	events, truncated, err := consumeEventLogStream(newUpdatesStream(updates, nil), instrument)
	if err != nil {
		t.Fatalf("consumeEventLogStream: %v", err)
	}
	if truncated {
		t.Fatalf("unexpected truncation")
	}
	return renderEventLogActivity(events, -1, 50)
}

// TestEventLog_Mint: a mint reports only created holdings (no consumed),
// a single receiver leg on the minted account, no sender.
func TestEventLog_Mint(t *testing.T) {
	upd := []*lapiv2.GetUpdatesResponse{eventLogUpdate(eventLogFix{
		offset: 10, updateID: "u10", admin: "dso", account: "bob",
		consumedCid: 0, createdCid: 1,
		legs: []legFix{{side: "ReceiverSide", otherside: "", amount: "1000.0", instrument: "MYT", legID: "L1"}},
	})}
	got := consumeAndRender(t, upd, "MYT")
	if len(got) != 1 {
		t.Fatalf("want 1 event, got %d", len(got))
	}
	ev := got[0]
	if ev.Kind != "mint" || ev.Amount != "1000" {
		t.Errorf("kind/amount = %s/%s, want mint/1000", ev.Kind, ev.Amount)
	}
	if ev.Source != string(types.ActivitySourceEventLog) {
		t.Errorf("source = %q, want event_log", ev.Source)
	}
	if len(ev.Senders) != 0 || len(ev.Receivers) != 1 || ev.Receivers[0].Party != "bob" {
		t.Errorf("receivers wrong: %+v", ev)
	}
}

// TestEventLog_Burn: a burn reports only consumed holdings, a single
// sender leg on the burned account, no receiver.
func TestEventLog_Burn(t *testing.T) {
	upd := []*lapiv2.GetUpdatesResponse{eventLogUpdate(eventLogFix{
		offset: 20, updateID: "u20", admin: "dso", account: "bob",
		consumedCid: 1, createdCid: 0,
		legs: []legFix{{side: "SenderSide", otherside: "", amount: "50.0", instrument: "MYT", legID: "L1"}},
	})}
	got := consumeAndRender(t, upd, "MYT")
	if len(got) != 1 || got[0].Kind != "burn" || got[0].Amount != "50" {
		t.Fatalf("want burn/50, got %+v", got)
	}
	if len(got[0].Senders) != 1 || got[0].Senders[0].Party != "bob" || len(got[0].Receivers) != 0 {
		t.Errorf("burn should have sender bob, no receiver: %+v", got[0])
	}
}

// TestEventLog_Transfer: bob sends 100 to app-user. The account whose
// holdings changed (bob) carries a SenderSide leg; otherside is the
// receiver. Both sides populated → transfer.
func TestEventLog_Transfer(t *testing.T) {
	upd := []*lapiv2.GetUpdatesResponse{eventLogUpdate(eventLogFix{
		offset: 30, updateID: "u30", admin: "dso", account: "bob",
		consumedCid: 3, createdCid: 2, // spent 3 UTXOs, got change + recipient output
		legs: []legFix{{side: "SenderSide", otherside: "app-user", amount: "100.0", instrument: "MYT", legID: "L1"}},
	})}
	got := consumeAndRender(t, upd, "MYT")
	if len(got) != 1 {
		t.Fatalf("want 1 event, got %d", len(got))
	}
	ev := got[0]
	if ev.Kind != "transfer" || ev.Amount != "100" {
		t.Errorf("kind/amount = %s/%s, want transfer/100", ev.Kind, ev.Amount)
	}
	if len(ev.Senders) != 1 || ev.Senders[0].Party != "bob" || ev.Senders[0].Amount != "100" {
		t.Errorf("sender wrong: %+v", ev.Senders)
	}
	if len(ev.Receivers) != 1 || ev.Receivers[0].Party != "app-user" || ev.Receivers[0].Amount != "100" {
		t.Errorf("receiver wrong: %+v", ev.Receivers)
	}
}

// TestEventLog_FiltersOtherInstrumentAndNewestFirst: a leg for another
// instrument is dropped, and events sort newest-first by offset.
func TestEventLog_FiltersOtherInstrumentAndNewestFirst(t *testing.T) {
	upd := []*lapiv2.GetUpdatesResponse{
		eventLogUpdate(eventLogFix{offset: 1, updateID: "u1", admin: "dso", account: "a", createdCid: 1,
			legs: []legFix{{side: "ReceiverSide", amount: "1.0", instrument: "MYT", legID: "L1"}}}),
		// Event only touches instrument "Other" → dropped entirely.
		eventLogUpdate(eventLogFix{offset: 2, updateID: "u2", admin: "dso", account: "a", createdCid: 1,
			legs: []legFix{{side: "ReceiverSide", amount: "5.0", instrument: "Other", legID: "L1"}}}),
		eventLogUpdate(eventLogFix{offset: 3, updateID: "u3", admin: "dso", account: "b", createdCid: 1,
			legs: []legFix{{side: "ReceiverSide", amount: "2.0", instrument: "MYT", legID: "L1"}}}),
	}
	got := consumeAndRender(t, upd, "MYT")
	if len(got) != 2 {
		t.Fatalf("want 2 MYT events (Other filtered out), got %d", len(got))
	}
	if got[0].Offset != 3 || got[1].Offset != 1 {
		t.Errorf("not newest-first: %d, %d", got[0].Offset, got[1].Offset)
	}
}

// TestEventLog_SkipsWrongChoiceAndOmittedInterfaceID: a non-EventLog
// choice is skipped; an EventLog_HoldingsChange with the interface id
// omitted is still decoded (choice name is the discriminator).
func TestEventLog_SkipsWrongChoiceAndOmittedInterfaceID(t *testing.T) {
	upd := []*lapiv2.GetUpdatesResponse{
		eventLogUpdate(eventLogFix{offset: 1, updateID: "u1", admin: "dso", account: "a", createdCid: 1,
			wrongChoice: true,
			legs:        []legFix{{side: "ReceiverSide", amount: "9.0", instrument: "MYT", legID: "L1"}}}),
		eventLogUpdate(eventLogFix{offset: 2, updateID: "u2", admin: "dso", account: "a", createdCid: 1,
			omitInterfaceID: true,
			legs:            []legFix{{side: "ReceiverSide", amount: "7.0", instrument: "MYT", legID: "L1"}}}),
	}
	got := consumeAndRender(t, upd, "MYT")
	if len(got) != 1 || got[0].Offset != 2 || got[0].Amount != "7" {
		t.Fatalf("want only the interface-id-omitted event (7), got %+v", got)
	}
}

// TestConsumeEventLogStream_TruncatesAtCap pins the OOM guard and the
// newest-first retention: the stream arrives oldest→newest, so once more
// than maxActivityScan events are retained, the ring buffer evicts the
// oldest and keeps the newest window. truncated must flip true, and the
// events returned must be the last maxActivityScan by offset.
func TestConsumeEventLogStream_TruncatesAtCap(t *testing.T) {
	total := maxActivityScan + 5
	upd := make([]*lapiv2.GetUpdatesResponse, total)
	for i := 0; i < total; i++ {
		upd[i] = eventLogUpdate(eventLogFix{
			offset: int64(i + 1), updateID: "u" + strconv.Itoa(i+1), admin: "dso", account: "a", createdCid: 1,
			legs: []legFix{{side: "ReceiverSide", amount: "1.0", instrument: "MYT", legID: "L1"}},
		})
	}
	events, truncated, err := consumeEventLogStream(newUpdatesStream(upd, nil), "MYT")
	if err != nil {
		t.Fatalf("consumeEventLogStream: %v", err)
	}
	if !truncated {
		t.Fatalf("truncated = false; want true after %d retained events", total)
	}
	if len(events) != maxActivityScan {
		t.Fatalf("retained %d events; want cap %d", len(events), maxActivityScan)
	}
	// Oldest 5 evicted: window holds offsets [6 .. total]. Order within the
	// window is stream order (oldest→newest); rendering sorts newest-first.
	rendered := renderEventLogActivity(events, -1, maxActivityScan)
	if rendered[0].Offset != int64(total) {
		t.Errorf("newest kept = %d; want %d", rendered[0].Offset, total)
	}
	if rendered[len(rendered)-1].Offset != 6 {
		t.Errorf("oldest kept = %d; want 6 (offsets 1-5 evicted)", rendered[len(rendered)-1].Offset)
	}
}

// --- dispatch: RunActivityResult picks EventLog vs netting ---

// mintUpdate is a synthetic EventLog mint used by the dispatch tests.
func mintUpdate() *lapiv2.GetUpdatesResponse {
	return eventLogUpdate(eventLogFix{
		offset: 5, updateID: "u5", admin: "dso", account: "bob", createdCid: 1,
		legs: []legFix{{side: "ReceiverSide", amount: "500.0", instrument: "MYT", legID: "L1"}},
	})
}

// fakeForDispatch wires a fake ledger returning `parties` as readable and
// vetting `packages`; updatesFn feeds whichever stream the path under test opens.
func fakeForDispatch(parties []string, packages []string, updatesFn func() *lapiv2.GetUpdatesResponse) *fakeLedger {
	return &fakeLedger{
		LedgerEndFn: func(context.Context) (ledger.LedgerEnd, error) {
			return ledger.LedgerEnd{Offset: 100}, nil
		},
		ResolveActAndReadPartiesFn: func(context.Context) ([]string, error) { return parties, nil },
		ListKnownPackagesFn: func(context.Context) (*adminv2.ListKnownPackagesResponse, error) {
			details := make([]*adminv2.PackageDetails, len(packages))
			for i, p := range packages {
				details[i] = &adminv2.PackageDetails{Name: p}
			}
			return &adminv2.ListKnownPackagesResponse{PackageDetails: details}, nil
		},
		UpdatesFn: func(context.Context, ledger.UpdatesRequest) (<-chan ledger.StreamItem[*lapiv2.GetUpdatesResponse], error) {
			return newUpdatesStream([]*lapiv2.GetUpdatesResponse{updatesFn()}, nil), nil
		},
	}
}

// TestRunActivityResult_UsesEventLogWhenVetted: when the participant
// vets splice-api-token-transfer-events-v2 and the admin reports an
// EventLog event, RunActivityResult returns the event_log-sourced feed.
func TestRunActivityResult_UsesEventLogWhenVetted(t *testing.T) {
	fake := fakeForDispatch(
		[]string{"bob"},
		[]string{"splice-api-token-holding-v2", "splice-api-token-transfer-events-v2"},
		mintUpdate,
	)
	withFakeDial(t, fake)

	res, err := RunActivityResult(context.Background(), BalanceOptions{
		Instance: "inst", Role: "app-user", Endpoint: "x:1", Instrument: "MYT",
	})
	if err != nil {
		t.Fatalf("RunActivityResult: %v", err)
	}
	if len(res.Events) != 1 {
		t.Fatalf("want 1 event, got %d", len(res.Events))
	}
	ev := res.Events[0]
	if ev.Source != string(types.ActivitySourceEventLog) {
		t.Errorf("source = %q, want event_log", ev.Source)
	}
	if ev.Kind != "mint" || ev.Amount != "500" {
		t.Errorf("kind/amount = %s/%s, want mint/500", ev.Kind, ev.Amount)
	}
}

// TestRunActivityResult_FallsBackToNettingWhenNoEventLogPackage: without the
// transfer-events package vetted, the netting path runs and Source is
// "transaction", never "event_log".
func TestRunActivityResult_FallsBackToNettingWhenNoEventLogPackage(t *testing.T) {
	fake := fakeForDispatch(
		[]string{"bob"},
		[]string{"splice-api-token-holding-v2"}, // no transfer-events
		mintUpdate,
	)
	// Feed a HoldingV2 create so the netting path classifies a mint.
	fake.UpdatesFn = func(context.Context, ledger.UpdatesRequest) (<-chan ledger.StreamItem[*lapiv2.GetUpdatesResponse], error) {
		return newUpdatesStream([]*lapiv2.GetUpdatesResponse{holdingCreateUpdate(7, "u7", "bob", "MYT", "500.0")}, nil), nil
	}
	withFakeDial(t, fake)

	res, err := RunActivityResult(context.Background(), BalanceOptions{
		Instance: "inst", Role: "app-user", Endpoint: "x:1", Instrument: "MYT",
	})
	if err != nil {
		t.Fatalf("RunActivityResult: %v", err)
	}
	if len(res.Events) != 1 {
		t.Fatalf("want 1 netted event, got %d", len(res.Events))
	}
	ev := res.Events[0]
	if ev.Source != string(types.ActivitySourceTransaction) {
		t.Errorf("source = %q, want transaction", ev.Source)
	}
	if ev.Kind != "mint" || ev.Amount != "500" {
		t.Errorf("kind/amount = %s/%s, want mint/500", ev.Kind, ev.Amount)
	}
}

// TestRunActivityResult_FallsBackToNettingWhenEventLogStreamErrors: the
// transfer-events package is vetted, but the EventLog stream terminates with
// a fallback-safe error (not context cancel/deadline). RunActivityResult must
// fall through to netting rather than surface a blank/error feed.
func TestRunActivityResult_FallsBackToNettingWhenEventLogStreamErrors(t *testing.T) {
	calls := 0
	fake := fakeForDispatch(
		[]string{"bob"},
		[]string{"splice-api-token-holding-v2", "splice-api-token-transfer-events-v2"},
		mintUpdate,
	)
	fake.UpdatesFn = func(context.Context, ledger.UpdatesRequest) (<-chan ledger.StreamItem[*lapiv2.GetUpdatesResponse], error) {
		calls++
		if calls == 1 { // EventLog path: terminate with a transient stream error
			return newUpdatesStream(nil, errors.New("eventlog stream boom")), nil
		}
		// netting path: a HoldingV2 create the netter classifies as a mint
		return newUpdatesStream([]*lapiv2.GetUpdatesResponse{holdingCreateUpdate(7, "u7", "bob", "MYT", "500.0")}, nil), nil
	}
	withFakeDial(t, fake)

	res, err := RunActivityResult(context.Background(), BalanceOptions{
		Instance: "inst", Role: "app-user", Endpoint: "x:1", Instrument: "MYT",
	})
	if err != nil {
		t.Fatalf("RunActivityResult: %v", err)
	}
	if len(res.Events) != 1 || res.Events[0].Source != string(types.ActivitySourceTransaction) {
		t.Fatalf("want 1 netted (transaction) event after fallback, got %+v", res.Events)
	}
	if res.Events[0].Kind != "mint" || res.Events[0].Amount != "500" {
		t.Errorf("kind/amount = %s/%s, want mint/500", res.Events[0].Kind, res.Events[0].Amount)
	}
}

// TestRunActivityResult_AbortsWhenEventLogStreamContextCancelled: a cancelled
// context is NOT fallback-safe (netting would hit the same deadline), so the
// error propagates instead of silently retrying.
func TestRunActivityResult_AbortsWhenEventLogStreamContextCancelled(t *testing.T) {
	fake := fakeForDispatch(
		[]string{"bob"},
		[]string{"splice-api-token-holding-v2", "splice-api-token-transfer-events-v2"},
		mintUpdate,
	)
	fake.UpdatesFn = func(context.Context, ledger.UpdatesRequest) (<-chan ledger.StreamItem[*lapiv2.GetUpdatesResponse], error) {
		return newUpdatesStream(nil, context.Canceled), nil
	}
	withFakeDial(t, fake)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := RunActivityResult(ctx, BalanceOptions{
		Instance: "inst", Role: "app-user", Endpoint: "x:1", Instrument: "MYT",
	})
	if err == nil {
		t.Fatalf("want error on cancelled context, got nil")
	}
}

// holdingCreateUpdate builds a GetUpdatesResponse carrying one HoldingV2
// created event — the shape the netting path (consumeActivityStream)
// reads via the ACS_DELTA filter.
func holdingCreateUpdate(offset int64, updateID, owner, instrument, amount string) *lapiv2.GetUpdatesResponse {
	pkg, mod, entity := splitInterfaceID(HoldingInterfaceV2)
	view := &lapiv2.InterfaceView{
		InterfaceId: &lapiv2.Identifier{PackageId: pkg, ModuleName: mod, EntityName: entity},
		ViewValue: &lapiv2.Record{Fields: []*lapiv2.RecordField{
			{Label: "account", Value: accountValue(owner)},
			{Label: "instrumentId", Value: recordValue([]field{
				{"admin", partyValue("dso")}, {"id", textValue(instrument)},
			})},
			{Label: "amount", Value: numericValue(amount)},
		}},
	}
	created := &lapiv2.CreatedEvent{
		ContractId:     "c-" + updateID,
		InterfaceViews: []*lapiv2.InterfaceView{view},
	}
	return &lapiv2.GetUpdatesResponse{
		Update: &lapiv2.GetUpdatesResponse_Transaction{
			Transaction: &lapiv2.Transaction{
				UpdateId: updateID,
				Offset:   offset,
				Events:   []*lapiv2.Event{{Event: &lapiv2.Event_Created{Created: created}}},
			},
		},
	}
}
