package token

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// EventLog-native activity path — an alternative to activity.go's netting scan.
//
// At 0.6.12 AmuletRules implements the EventLog interface
// (splice-api-token-transfer-events-v2), so the admin (dso for Amulet)
// exercises a non-consuming EventLog_HoldingsChange choice on every holdings
// movement. That event carries the authoritative account, input/output holding
// counts and per-leg transfer sides — no netting guesswork. When the package is
// vetted we read these events directly.
//
// EventLog_HoldingsChange (verified against Splice 0.6.12, commit 17fd29aa)
// choice argument shape:
//
//	EventLog_HoldingsChange with
//	  admin            : Party
//	  account          : Account                 -- {owner?, provider?, id}
//	  inputHoldingCids : [ContractId Holding]    -- archived (consumed)
//	  transferLegSides : [TransferLegSide]
//	  outputHoldingCids: [ContractId Holding]    -- created
//	  observers        : [Party]
//	  extraArgs        : ...
//
//	TransferLegSide with
//	  transferLegId : Text
//	  side          : TransferSide  -- enum SenderSide | ReceiverSide
//	  otherside     : Account
//	  amount        : Decimal
//	  instrumentId  : Text
//	  meta          : Metadata
//
// The choice is exercised on an interface, so it surfaces only under the
// LEDGER_EFFECTS shape (ACS_DELTA carries created/archived, not exercised).

// eventLogHoldingsChangeChoice is the choice name the admin exercises to report
// a holdings change. Pinning it (alongside the interface id) keeps a future
// second EventLog choice from being mis-parsed as a holdings change.
const eventLogHoldingsChangeChoice = "EventLog_HoldingsChange"

// Daml TransferSide enum constructors and their normalized leg-side labels
// (the types.TokenTransferLeg.Side wire values).
const (
	transferSideSenderCtor   = "SenderSide"
	transferSideReceiverCtor = "ReceiverSide"
	transferSideSender       = "sender"
	transferSideReceiver     = "receiver"
)

// RunActivityViaEventLog reads an instrument's activity from the admin's
// EventLog_HoldingsChange events instead of netting HoldingV2 deltas, tagging
// each as Source="event_log". Read-only; no submission.
//
// When the stream yields no EventLog events for the instrument, Events is empty
// and the caller falls back to the netting path, so an instrument whose admin
// does not implement EventLog is never silently blank.
func RunActivityViaEventLog(ctx context.Context, client LedgerClient, parties []string, opts BalanceOptions) (ActivityResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}

	end, err := client.LedgerEnd(ctx)
	if err != nil {
		return ActivityResult{}, fmt.Errorf("ledger end: %w", err)
	}
	endInc := end.Offset

	// Wrap so an early break (cap hit, decode error) cancels the upstream pump
	// instead of draining the ledger for the parent ctx's lifetime.
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := client.Updates(streamCtx, ledger.UpdatesRequest{
		BeginExclusive: 0,
		EndInclusive:   &endInc,
		UpdateFormat: &lapiv2.UpdateFormat{
			IncludeTransactions: &lapiv2.TransactionFormat{
				EventFormat: eventLogInterfaceFilter(parties),
				// Exercised choices appear only in the ledger-effects tree.
				TransactionShape: lapiv2.TransactionShape_TRANSACTION_SHAPE_LEDGER_EFFECTS,
			},
		},
	})
	if err != nil {
		return ActivityResult{}, fmt.Errorf("open updates stream: %w", err)
	}

	events, truncated, err := consumeEventLogStream(stream, opts.Instrument)
	if err != nil {
		return ActivityResult{}, err
	}

	decimals := instrumentDecimals(opts.Instance, opts.Instrument)
	return ActivityResult{
		Events:    renderEventLogActivity(events, decimals, limit),
		Truncated: truncated,
	}, nil
}

// eventLogInterfaceFilter builds the EventFormat matching exercised EventLog
// choices. IncludeInterfaceView is false — classification needs only the
// exercised choice argument, not the interface view. Empty parties → wildcard.
func eventLogInterfaceFilter(parties []string) *lapiv2.EventFormat {
	pkg, mod, entity := splitInterfaceID(EventLogInterfaceV2)
	entry := &lapiv2.CumulativeFilter{
		IdentifierFilter: &lapiv2.CumulativeFilter_InterfaceFilter{
			InterfaceFilter: &lapiv2.InterfaceFilter{
				InterfaceId:          &lapiv2.Identifier{PackageId: pkg, ModuleName: mod, EntityName: entity},
				IncludeInterfaceView: false,
			},
		},
	}
	filter := &lapiv2.Filters{Cumulative: []*lapiv2.CumulativeFilter{entry}}
	if len(parties) == 0 {
		return &lapiv2.EventFormat{FiltersForAnyParty: filter, Verbose: true}
	}
	byParty := make(map[string]*lapiv2.Filters, len(parties))
	for _, p := range parties {
		byParty[p] = filter
	}
	return &lapiv2.EventFormat{FiltersByParty: byParty, Verbose: true}
}

// consumeEventLogStream drains a ledger updates channel into
// types.TokenActivityEvent records for the given instrument, stopping at
// maxActivityScan (truncated=true). Only exercised EventLog_HoldingsChange
// events with at least one leg matching the instrument are retained.
func consumeEventLogStream(stream <-chan ledger.StreamItem[*lapiv2.GetUpdatesResponse], instrument string) ([]types.TokenActivityEvent, bool, error) {
	var out []types.TokenActivityEvent
	var scanned int
	var truncated bool
	for item := range stream {
		if item.Err != nil {
			return nil, false, fmt.Errorf("updates stream: %w", item.Err)
		}
		scanned++
		if scanned > maxActivityScan {
			truncated = true
			break
		}
		t := item.Value.GetTransaction()
		if t == nil {
			continue
		}
		for _, e := range t.GetEvents() {
			x := e.GetExercised()
			if x == nil || !isEventLogHoldingsChange(x) {
				continue
			}
			if ev, ok := decodeHoldingsChange(x, t, instrument); ok {
				out = append(out, ev)
			}
		}
	}
	return out, truncated, nil
}

// isEventLogHoldingsChange reports whether an exercised event is the EventLog
// interface's holdings-change choice. Matches on module + entity (the package
// id rotates across V2 snapshots, module/entity don't) plus the choice name.
func isEventLogHoldingsChange(x *lapiv2.ExercisedEvent) bool {
	if x.GetChoice() != eventLogHoldingsChangeChoice {
		return false
	}
	_, mod, entity := splitInterfaceID(EventLogInterfaceV2)
	if id := x.GetInterfaceId(); id != nil {
		return id.GetModuleName() == mod && id.GetEntityName() == entity
	}
	// Some participants omit the interface id; the choice name is then the
	// sole discriminator.
	return true
}

// decodeHoldingsChange maps one exercised EventLog_HoldingsChange into a
// types.TokenActivityEvent scoped to the target instrument. Returns ok=false
// when the argument is unparseable or no leg touches the instrument (the
// instrument only appears inside the legs, so a leg-less event is dropped).
func decodeHoldingsChange(x *lapiv2.ExercisedEvent, t *lapiv2.Transaction, instrument string) (types.TokenActivityEvent, bool) {
	rec := recordOf(x.GetChoiceArgument())
	if rec == nil {
		return types.TokenActivityEvent{}, false
	}
	ev := types.TokenActivityEvent{
		Source:       types.ActivitySourceEventLog,
		UpdateID:     t.GetUpdateId(),
		Offset:       t.GetOffset(),
		InstrumentID: instrument,
	}
	if t.GetRecordTime() != nil {
		ev.RecordTime = t.GetRecordTime().AsTime().Format(time.RFC3339Nano)
	}
	for _, f := range rec.Fields {
		switch f.Label {
		case "admin":
			ev.Admin = partyOf(f.Value)
		case "account":
			ev.Account = accountOwner(f.Value)
		case "inputHoldingCids":
			ev.ConsumedHoldingCount = listLen(f.Value)
		case "outputHoldingCids":
			ev.CreatedHoldingCount = listLen(f.Value)
		case "transferLegSides":
			ev.TransferLegs = decodeTransferLegs(f.Value, instrument)
		}
	}
	if len(ev.TransferLegs) == 0 {
		return types.TokenActivityEvent{}, false
	}
	return ev, true
}

// decodeTransferLegs walks the transferLegSides list, keeping only the
// legs whose instrumentId matches the target. Each TransferLegSide
// becomes one TokenTransferLeg.
func decodeTransferLegs(v *lapiv2.Value, instrument string) []types.TokenTransferLeg {
	var out []types.TokenTransferLeg
	for _, el := range listElems(v) {
		rec := recordOf(el)
		if rec == nil {
			continue
		}
		var leg types.TokenTransferLeg
		for _, f := range rec.Fields {
			switch f.Label {
			case "transferLegId":
				leg.TransferLegID = textOf(f.Value)
			case "side":
				leg.Side = transferSideLabel(f.Value)
			case "otherside":
				leg.Otherside = accountOwner(f.Value)
			case "amount":
				leg.Amount = numericOf(f.Value)
			case "instrumentId":
				leg.InstrumentID = textOf(f.Value)
			}
		}
		if leg.InstrumentID != instrument {
			continue
		}
		out = append(out, leg)
	}
	return out
}

// renderEventLogActivity folds decoded TokenActivityEvents into the shared
// ActivityEvent shape, newest-first, capped at limit (<=0 → no cap).
// Classification mirrors the netting path so both feeds read identically.
func renderEventLogActivity(events []types.TokenActivityEvent, decimals, limit int) []ActivityEvent {
	out := make([]ActivityEvent, 0, len(events))
	for _, ev := range events {
		if rendered, ok := renderOneEventLog(ev, decimals); ok {
			out = append(out, rendered)
		}
	}
	sortByOffsetDesc(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// renderOneEventLog classifies a single EventLog event into an ActivityEvent.
// The account whose holdings changed owns the local side of each leg; the leg's
// otherside is the counterparty:
//
//   - side=receiver → account credited, otherside debited
//   - side=sender   → account debited, otherside credited
//   - only credits  → mint; only debits → burn; both → transfer
//
// Returns ok=false when the legs net to nothing.
func renderOneEventLog(ev types.TokenActivityEvent, decimals int) (ActivityEvent, bool) {
	credit := map[string]*big.Rat{}
	debit := map[string]*big.Rat{}
	var creditOrder, debitOrder []string
	add := func(m map[string]*big.Rat, order *[]string, party string, amt *big.Rat) {
		if party == "" {
			return
		}
		if _, seen := m[party]; !seen {
			m[party] = new(big.Rat)
			*order = append(*order, party)
		}
		m[party].Add(m[party], amt)
	}

	for _, leg := range ev.TransferLegs {
		amt, ok := new(big.Rat).SetString(zeroIfEmpty(leg.Amount))
		if !ok {
			continue
		}
		switch leg.Side {
		case transferSideReceiver:
			add(credit, &creditOrder, ev.Account, amt)
			add(debit, &debitOrder, leg.Otherside, amt)
		case transferSideSender:
			add(debit, &debitOrder, ev.Account, amt)
			add(credit, &creditOrder, leg.Otherside, amt)
		}
	}

	receivers := deltasFrom(credit, creditOrder, decimals)
	senders := deltasFrom(debit, debitOrder, decimals)
	if len(senders) == 0 && len(receivers) == 0 {
		return ActivityEvent{}, false
	}

	// Amount is the total moved: credited side for mint/transfer, debited for burn.
	kind := "transfer"
	amount := sumRats(credit)
	switch {
	case len(senders) == 0:
		kind = "mint"
		amount = sumRats(credit)
	case len(receivers) == 0:
		kind = "burn"
		amount = sumRats(debit)
	}

	return ActivityEvent{
		Offset:     ev.Offset,
		UpdateID:   ev.UpdateID,
		RecordTime: ev.RecordTime,
		Instrument: ev.InstrumentID,
		Kind:       kind,
		Source:     string(types.ActivitySourceEventLog),
		Amount:     formatDecimal(amount, decimals),
		Senders:    senders,
		Receivers:  receivers,
	}, true
}

// sumRats totals every magnitude in a party→magnitude map.
func sumRats(m map[string]*big.Rat) *big.Rat {
	total := new(big.Rat)
	for _, v := range m {
		total.Add(total, v)
	}
	return total
}

// deltasFrom renders a party→magnitude map into PartyDelta rows in the
// given first-seen order, dropping zero rows.
func deltasFrom(m map[string]*big.Rat, order []string, decimals int) []PartyDelta {
	var out []PartyDelta
	for _, p := range order {
		v := m[p]
		if v.Sign() == 0 {
			continue
		}
		out = append(out, PartyDelta{Party: p, Amount: formatDecimal(v, decimals)})
	}
	return out
}

// sortByOffsetDesc orders events newest-first by ledger offset, stable
// so equal-offset events keep their scan order.
func sortByOffsetDesc(evs []ActivityEvent) {
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].Offset > evs[j].Offset })
}

// --- tiny Value walkers specific to the EventLog choice argument ---

// accountOwner pulls the owner party from an Account record value. Returns ""
// when the account has no owner (a treasury / minting account may omit it).
func accountOwner(v *lapiv2.Value) string {
	rec := recordOf(v)
	if rec == nil {
		return ""
	}
	for _, f := range rec.Fields {
		if f.Label == "owner" {
			return optionalPartyOf(f.Value)
		}
	}
	return ""
}

// transferSideLabel normalizes a Daml TransferSide enum value into the
// "sender"/"receiver" wire label; "" for an unrecognized constructor.
func transferSideLabel(v *lapiv2.Value) string {
	if v == nil {
		return ""
	}
	e, ok := v.Sum.(*lapiv2.Value_Enum)
	if !ok || e.Enum == nil {
		return ""
	}
	switch e.Enum.GetConstructor() {
	case transferSideSenderCtor:
		return transferSideSender
	case transferSideReceiverCtor:
		return transferSideReceiver
	}
	return ""
}

// listElems returns the elements of a Daml list value, or nil.
func listElems(v *lapiv2.Value) []*lapiv2.Value {
	if v == nil {
		return nil
	}
	if l, ok := v.Sum.(*lapiv2.Value_List); ok && l.List != nil {
		return l.List.GetElements()
	}
	return nil
}

// listLen returns the length of a Daml list value, or 0.
func listLen(v *lapiv2.Value) int { return len(listElems(v)) }
