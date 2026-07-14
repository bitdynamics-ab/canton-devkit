package token

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// maxActivityScan caps how many ledger update items RunActivity will
// consume before declaring the result truncated. Mirrors
// maxWorkspaceScan in workspace.go — a runaway transaction stream on a
// busy ledger would otherwise pump us into OOM via an unbounded slice.
const maxActivityScan = 10_000

// The activity feed reconstructs an
// instrument's transfer/mint/burn history from the ledger transaction
// stream — no off-ledger transfer-events-v2 registry required. On a UTXO
// ledger every movement archives the sender's Holding contracts and
// creates the receiver's within one transaction, so a single scan over
// HoldingV2 create/archive events is enough to net out who sent what to
// whom:
//
// - created holding → a credit to its owner
// - archived holding → a debit from its owner
//
// Per transaction we net the per-party deltas and classify:
//
// - only credits, no debits → mint (new supply appeared)
// - only debits, no credits → burn (supply destroyed)
// - both → transfer
//
// Archived events don't carry the HoldingView (only the contract id), so
// we build a contractId → (owner, instrument, amount) map from the create
// events seen earlier in the same scan and resolve debits against it.

// PartyDelta is one party's signed magnitude in an activity event. The
// amount is always the positive magnitude; the sender/receiver list it
// appears in carries the direction.
type PartyDelta struct {
	Party  string `json:"party"`
	Amount string `json:"amount"`
}

// ActivityEvent is one ledger movement touching an instrument. Two
// paths produce it: the HoldingV2 create/archive netting path
// (Source="transaction") and the EventLog-native path
// (Source="event_log", built from the admin's EventLog_HoldingsChange
// events). Both render identically on the CLI --format json and the Web
// UI feed; Source lets a surface label the row's provenance.
type ActivityEvent struct {
	Offset     int64        `json:"offset"`
	UpdateID   string       `json:"update_id"`
	RecordTime string       `json:"record_time"`
	Instrument string       `json:"instrument_id"`
	Kind       string       `json:"kind"`   // mint | burn | transfer
	Source     string       `json:"source"` // event_log | transaction (types.TokenActivitySource)
	Amount     string       `json:"amount"`
	Senders    []PartyDelta `json:"senders,omitempty"`
	Receivers  []PartyDelta `json:"receivers,omitempty"`
}

// ActivityResult is the activity feed plus a Truncated flag indicating
// the underlying transaction stream hit maxActivityScan and stopped
// short. The UI can then render "showing N of many" rather than silently
// misreporting the feed.
type ActivityResult struct {
	Events    []ActivityEvent `json:"events"`
	Truncated bool            `json:"truncated,omitempty"`
}

// rawHoldingDelta is one create/archive of a Holding contract.
type rawHoldingDelta struct {
	party      string
	instrument string
	amount     string // positive Decimal string
	created    bool   // true = credit (created), false = debit (archived)
}

// rawTx is one transaction's holding deltas, before netting.
type rawTx struct {
	offset     int64
	updateID   string
	recordTime string
	deltas     []rawHoldingDelta
}

// buildActivity nets each transaction's holding deltas into an
// ActivityEvent for the given instrument, newest-first, capped at limit
// (limit <= 0 means no cap). decimals controls the fractional precision
// of formatted amounts; <0 falls back to 18 (the V2 test-token cap).
// Pure — unit-testable without a ledger.
func buildActivity(txs []rawTx, instrument string, decimals int, limit int) []ActivityEvent {
	out := make([]ActivityEvent, 0, len(txs))
	for _, tx := range txs {
		ev, ok := netTransaction(tx, instrument, decimals)
		if ok {
			out = append(out, ev)
		}
	}
	// Newest first by offset (descending).
	sort.SliceStable(out, func(i, j int) bool { return out[i].Offset > out[j].Offset })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// netTransaction folds one transaction's deltas for a single instrument
// into senders/receivers + a kind. Returns ok=false when the transaction
// doesn't touch the instrument or nets to nothing.
//
// Amount arithmetic uses math/big.Rat (not big.Float): Float carries a
// binary mantissa and silently drifts past ~15 decimal digits (0.1 +
// 0.2 → "0.30000000000000004"), which would net 18-decimal-place V2
// amounts wrong. Rat is exact for decimal input and renders back via
// FloatString(decimals).
func netTransaction(tx rawTx, instrument string, decimals int) (ActivityEvent, bool) {
	net := map[string]*big.Rat{}
	order := []string{}
	touched := false
	for _, d := range tx.deltas {
		if d.instrument != instrument {
			continue
		}
		touched = true
		amt, ok := new(big.Rat).SetString(zeroIfEmpty(d.amount))
		if !ok {
			continue
		}
		if _, seen := net[d.party]; !seen {
			net[d.party] = new(big.Rat)
			order = append(order, d.party)
		}
		if d.created {
			net[d.party].Add(net[d.party], amt)
		} else {
			net[d.party].Sub(net[d.party], amt)
		}
	}
	if !touched {
		return ActivityEvent{}, false
	}

	var senders, receivers []PartyDelta
	credited := new(big.Rat)
	debited := new(big.Rat)
	for _, p := range order {
		v := net[p]
		switch v.Sign() {
		case 1:
			receivers = append(receivers, PartyDelta{Party: p, Amount: formatDecimal(v, decimals)})
			credited.Add(credited, v)
		case -1:
			mag := new(big.Rat).Neg(v)
			senders = append(senders, PartyDelta{Party: p, Amount: formatDecimal(mag, decimals)})
			debited.Add(debited, mag)
		}
	}
	// All parties netted to zero (e.g. a self-split producing only
	// change) — no movement worth surfacing.
	if len(senders) == 0 && len(receivers) == 0 {
		return ActivityEvent{}, false
	}

	kind := "transfer"
	amount := credited
	switch {
	case len(senders) == 0:
		kind = "mint"
		amount = credited
	case len(receivers) == 0:
		kind = "burn"
		amount = debited
	}
	return ActivityEvent{
		Offset:     tx.offset,
		UpdateID:   tx.updateID,
		RecordTime: tx.recordTime,
		Instrument: instrument,
		Kind:       kind,
		Source:     string(types.ActivitySourceTransaction),
		Amount:     formatDecimal(amount, decimals),
		Senders:    senders,
		Receivers:  receivers,
	}, true
}

// formatDecimal renders r as an exact base-10 string with the given
// number of fractional digits, trimming trailing zeros (and a bare
// trailing '.') so "1000.0000000000" → "1000" and 0.1+0.2-0.3 → "0".
// decimals < 0 defaults to 18 (V2 test-token cap).
func formatDecimal(r *big.Rat, decimals int) string {
	if decimals < 0 {
		decimals = 18
	}
	s := r.FloatString(decimals)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ".")
	}
	if s == "" || s == "-" {
		return "0"
	}
	return s
}

// RunActivityResult reconstructs an instrument's transfer/mint/burn
// history from the ledger. opts.Instrument selects the instrument;
// opts.Limit caps the returned events (default 50). Truncated is set
// when the underlying updates stream was cut short at maxActivityScan to
// bound memory. Read-only; no submission.
//
// Two paths produce the feed, and RunActivityResult picks one so the
// same movement is never counted twice:
//
//   - EventLog-native (Source="event_log"): when the participant vets
//     splice-api-token-transfer-events-v2 AND the instrument admin
//     reports EventLog_HoldingsChange events (Amulet's admin is dso).
//     Reads the admin's authoritative holdings-change events.
//   - HoldingV2 netting (Source="transaction"): the fallback. Scans
//     HoldingV2 create/archive events and nets per-party deltas. Used
//     for instruments whose admin does not implement EventLog, and when
//     the EventLog stream yields nothing for the instrument.
func RunActivityResult(ctx context.Context, opts BalanceOptions) (ActivityResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	conn := LedgerConn{
		Endpoint: opts.Endpoint,
		Token:    opts.Token,
		Insecure: opts.Insecure,
		Instance: opts.Instance,
		Role:     opts.Role,
	}
	client, cleanup, err := dialLedgerFn(ctx, conn)
	if err != nil {
		return ActivityResult{}, err
	}
	defer cleanup()

	// Widen read-as to every registered party alias so the
	// activity feed reconstructs movements across ALL aliased parties,
	// then re-resolve the authoritative granted set.
	parties, err := resolveReadableParties(ctx, client, opts.Instance, opts.Role)
	if err != nil {
		return ActivityResult{}, err
	}
	if len(parties) == 0 {
		return ActivityResult{Events: []ActivityEvent{}}, nil
	}

	// Query every vetted holding surface (V1 + V2), not just V2, so a V1
	// instrument like Amulet appears in the activity feed on a stable
	// release — matching the balance / instruments / matrix read paths.
	surfaces, err := discoverTokenSurfaces(ctx, client)
	if err != nil {
		return ActivityResult{}, err
	}
	if !surfaces.Any() {
		return ActivityResult{Events: []ActivityEvent{}}, nil
	}

	// EventLog-native path: the admin reports holdings changes directly.
	// Take it only when the transfer-events package is vetted; if it
	// yields events use them, otherwise fall through to netting so an
	// instrument whose admin does not implement EventLog is never blank.
	if surfaces.HasEventLog {
		res, elErr := RunActivityViaEventLog(ctx, client, parties, opts)
		if elErr != nil {
			return ActivityResult{}, elErr
		}
		if len(res.Events) > 0 {
			return res, nil
		}
	}

	end, err := client.LedgerEnd(ctx)
	if err != nil {
		return ActivityResult{}, fmt.Errorf("ledger end: %w", err)
	}
	endInc := end.Offset
	// Wrap before opening the stream so an early break (cap hit, decode
	// error) cancels the upstream pump goroutine instead of letting it
	// drain the rest of the ledger for the lifetime of the parent ctx.
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := client.Updates(streamCtx, ledger.UpdatesRequest{
		BeginExclusive: 0,
		EndInclusive:   &endInc,
		UpdateFormat: &lapiv2.UpdateFormat{
			IncludeTransactions: &lapiv2.TransactionFormat{
				EventFormat:      holdingInterfaceFilter(surfaces, parties),
				TransactionShape: lapiv2.TransactionShape_TRANSACTION_SHAPE_ACS_DELTA,
			},
		},
	})
	if err != nil {
		return ActivityResult{}, fmt.Errorf("open updates stream: %w", err)
	}

	txs, truncated, err := consumeActivityStream(stream)
	if err != nil {
		return ActivityResult{}, err
	}

	decimals := instrumentDecimals(opts.Instance, opts.Instrument)
	return ActivityResult{
		Events:    buildActivity(txs, opts.Instrument, decimals, limit),
		Truncated: truncated,
	}, nil
}

// consumeActivityStream drains a ledger updates channel into rawTx
// records, stopping once maxActivityScan items have been seen (with
// truncated=true). Extracted from RunActivityResult so a fake channel
// can pin the cap behavior without dialing a real participant.
func consumeActivityStream(stream <-chan ledger.StreamItem[*lapiv2.GetUpdatesResponse]) ([]rawTx, bool, error) {
	// contractId → its holding facts, so archived events (which carry
	// only the contract id) can be resolved back to owner/instrument/amount.
	byContract := map[string]rawHoldingDelta{}
	var txs []rawTx
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
		tx := rawTx{offset: t.GetOffset(), updateID: t.GetUpdateId()}
		if t.GetRecordTime() != nil {
			tx.recordTime = t.GetRecordTime().AsTime().Format(time.RFC3339Nano)
		}
		for _, e := range t.GetEvents() {
			if c := e.GetCreated(); c != nil {
				// One delta per contract — prefer the richer V2 view, fall
				// back to V1 — so a V1 Holding (Amulet) registers in the
				// feed too, never double-counted when a contract implements
				// both interfaces.
				if view, ok := extractBestHoldingView(c.GetInterfaceViews()); ok {
					d := rawHoldingDelta{
						party:      view.Owner,
						instrument: view.InstrumentID,
						amount:     view.Amount,
						created:    true,
					}
					byContract[c.GetContractId()] = d
					tx.deltas = append(tx.deltas, d)
				}
				continue
			}
			if a := e.GetArchived(); a != nil {
				if d, ok := byContract[a.GetContractId()]; ok {
					tx.deltas = append(tx.deltas, rawHoldingDelta{
						party:      d.party,
						instrument: d.instrument,
						amount:     d.amount,
						created:    false,
					})
				}
			}
		}
		if len(tx.deltas) > 0 {
			txs = append(txs, tx)
		}
	}
	return txs, truncated, nil
}

// instrumentDecimals looks up the recorded decimals for an instrument
// from the registry state, falling back to -1 (formatDecimal then uses
// its 18-digit V2 default). Read-only; failures fall back silently.
func instrumentDecimals(instance, instrument string) int {
	state, err := registry.Read(instance)
	if err != nil {
		return -1
	}
	for _, ref := range state.Tokens {
		if ref.InstrumentID == instrument || ref.Symbol == instrument {
			if ref.Decimals > 0 {
				return ref.Decimals
			}
		}
	}
	return -1
}
