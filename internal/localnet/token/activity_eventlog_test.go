package token

import (
	"context"
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

func enumValue(ctor string) *lapiv2.Value {
	return &lapiv2.Value{Sum: &lapiv2.Value_Enum{Enum: &lapiv2.Enum{Constructor: ctor}}}
}

// optPartyVal builds Some party (empty → None). Wraps the production
// optionalPartyValue (*string) with a string-arg convenience.
func optPartyVal(p string) *lapiv2.Value {
	if p == "" {
		return optionalPartyValue(nil)
	}
	return optionalPartyValue(&p)
}

// accountValue builds an Account { owner: Optional Party, provider:
// Optional Party, id: Text } record with the given owner.
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

// consumeAndRender is the pure classification pipeline the live path
// runs after opening the stream.
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

// TestConsumeEventLogStream_TruncatesAtCap pins the OOM guard: the
// consumer stops at maxActivityScan with truncated=true.
func TestConsumeEventLogStream_TruncatesAtCap(t *testing.T) {
	ch := make(chan ledger.StreamItem[*lapiv2.GetUpdatesResponse], maxActivityScan+1)
	for i := 0; i < maxActivityScan+1; i++ {
		ch <- ledger.StreamItem[*lapiv2.GetUpdatesResponse]{Value: &lapiv2.GetUpdatesResponse{}}
	}
	close(ch)
	_, truncated, err := consumeEventLogStream(ch, "MYT")
	if err != nil {
		t.Fatalf("consumeEventLogStream: %v", err)
	}
	if !truncated {
		t.Errorf("truncated = false; want true after %d items", maxActivityScan+1)
	}
}

// --- dispatch: RunActivityResult picks EventLog vs netting ---

// eventLogFixtures is a shared movement set expressed in BOTH the
// EventLog exercised-event form and the equivalent HoldingV2
// create/archive netting form, so a dispatch test can assert the two
// paths classify identical movements the same way (never double-counted:
// exactly one path runs per invocation).
func mintUpdate() *lapiv2.GetUpdatesResponse {
	return eventLogUpdate(eventLogFix{
		offset: 5, updateID: "u5", admin: "dso", account: "bob", createdCid: 1,
		legs: []legFix{{side: "ReceiverSide", amount: "500.0", instrument: "MYT", legID: "L1"}},
	})
}

// fakeForDispatch wires a fake ledger returning `parties` as readable
// and vetting the given packages; updatesFn feeds whichever stream the
// path under test opens.
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

// TestRunActivityResult_FallsBackToNettingWhenNoEventLogPackage: without
// the transfer-events package vetted, the netting path runs (the
// EventLog exercised event is invisible to the ACS_DELTA holding filter,
// so the netting scan simply sees no holdings and returns empty — the
// point is that Source is "transaction", never "event_log").
func TestRunActivityResult_FallsBackToNettingWhenNoEventLogPackage(t *testing.T) {
	fake := fakeForDispatch(
		[]string{"bob"},
		[]string{"splice-api-token-holding-v2"}, // no transfer-events
		mintUpdate,
	)
	// Netting path: feed a HoldingV2 create so it classifies a mint via
	// the create/archive delta path, proving Source="transaction".
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
