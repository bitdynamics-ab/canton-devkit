package token

import (
	"context"
	"errors"
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

// maxActivityScan caps how many activity records the feed retains before
// declaring the result truncated, bounding memory against a runaway
// transaction stream on a busy ledger.
const maxActivityScan = 10_000

// activityWindow is a fixed-capacity ring buffer that retains the most
// recently added items. The Updates gRPC stream only flows forward
// (oldest→newest), so keeping a window of the newest `cap` items lets the
// activity feeds return the newest history under a bound instead of the
// oldest slice (which a leading break would produce). truncated flips true
// once any item has been evicted.
type activityWindow[T any] struct {
	buf       []T
	cap       int
	start     int // index of the oldest retained item
	len       int
	truncated bool
}

func newActivityWindow[T any](capacity int) *activityWindow[T] {
	if capacity < 1 {
		capacity = 1
	}
	return &activityWindow[T]{buf: make([]T, capacity), cap: capacity}
}

// add appends v, evicting the oldest item (and setting truncated) when full.
func (w *activityWindow[T]) add(v T) {
	if w.len < w.cap {
		w.buf[(w.start+w.len)%w.cap] = v
		w.len++
		return
	}
	// Full: overwrite the oldest slot and advance start.
	w.buf[w.start] = v
	w.start = (w.start + 1) % w.cap
	w.truncated = true
}

// slice returns the retained items in insertion (oldest→newest) order.
func (w *activityWindow[T]) slice() []T {
	out := make([]T, 0, w.len)
	for i := 0; i < w.len; i++ {
		out = append(out, w.buf[(w.start+i)%w.cap])
	}
	return out
}

// The netting activity feed reconstructs an instrument's transfer/mint/burn
// history from the ledger transaction stream — no off-ledger registry required.
// On a UTXO ledger every movement archives the sender's Holdings and creates
// the receiver's in one transaction, so netting per-party create/archive deltas
// recovers who sent what: created → credit, archived → debit; only credits →
// mint, only debits → burn, both → transfer.
//
// Archived events carry only the contract id, so we build a contractId →
// (owner, instrument, amount) map from create events earlier in the same scan
// and resolve debits against it.

// PartyDelta is one party's magnitude in an activity event. The amount is
// always positive; the sender/receiver list it appears in carries direction.
type PartyDelta struct {
	Party  string `json:"party"`
	Amount string `json:"amount"`
}

// ActivityEvent is one ledger movement touching an instrument. Two paths
// produce it — the netting path (Source="transaction") and the EventLog-native
// path (Source="event_log") — rendering identically; Source labels provenance.
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

// ActivityResult is the activity feed plus a Truncated flag set when the
// underlying stream hit maxActivityScan, so the UI can render "showing N of
// many" rather than silently misreporting the feed. Endpoint and Role are
// the shared resolution metadata (endpoint contract): which participant
// host:port and act-as role produced the feed.
type ActivityResult struct {
	Events    []ActivityEvent `json:"events"`
	Truncated bool            `json:"truncated,omitempty"`
	Endpoint  string          `json:"endpoint,omitempty"`
	Role      string          `json:"role,omitempty"`
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

// buildActivity nets each transaction's holding deltas into an ActivityEvent
// for the given instrument, newest-first, capped at limit (<=0 → no cap).
// decimals sets fractional precision (<0 → 18, the V2 test-token cap). Pure.
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

// netTransaction folds one transaction's deltas for a single instrument into
// senders/receivers + a kind. Returns ok=false when it doesn't touch the
// instrument or nets to nothing.
//
// Uses math/big.Rat, not big.Float: Float's binary mantissa drifts past ~15
// decimal digits and would net 18-decimal V2 amounts wrong; Rat is exact for
// decimal input.
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

// RunActivityResult reconstructs an instrument's transfer/mint/burn history
// from the ledger. opts.Limit caps the events (default 50); Truncated is set
// when the stream was cut at maxActivityScan. Read-only; no submission.
//
// It picks one of two paths so a movement is never double-counted:
//
//   - EventLog-native (Source="event_log"): when the participant vets
//     splice-api-token-transfer-events-v2 AND the admin reports
//     EventLog_HoldingsChange events (Amulet's admin is dso).
//   - HoldingV2 netting (Source="transaction"): the fallback, used when the
//     admin does not implement EventLog or its stream yields nothing.
func RunActivityResult(ctx context.Context, opts BalanceOptions) (ActivityResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	if opts.Role == "" {
		opts.Role = DefaultRole
	}
	withResolution := func(res ActivityResult) ActivityResult {
		res.Endpoint = opts.Endpoint
		res.Role = opts.Role
		return res
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

	// Read as every registered party alias so the feed reconstructs movements
	// across all of them.
	parties, err := resolveReadableParties(ctx, client, opts.Instance, opts.Role)
	if err != nil {
		return ActivityResult{}, err
	}
	if len(parties) == 0 {
		return withResolution(ActivityResult{Events: []ActivityEvent{}}), nil
	}

	// Query every vetted holding surface (V1 + V2) so a V1 instrument like
	// Amulet also appears, matching the balance / instruments / matrix reads.
	surfaces, err := discoverTokenSurfaces(ctx, client)
	if err != nil {
		return ActivityResult{}, err
	}
	if !surfaces.Any() {
		return withResolution(ActivityResult{Events: []ActivityEvent{}}), nil
	}

	// Prefer the EventLog path when vetted; fall through to netting if it
	// yields nothing OR fails in a fallback-safe way (stream-open / malformed
	// event), so an instrument without a usable EventLog is never blank. Only
	// a cancelled/expired context aborts — retrying via netting would fail the
	// same way.
	if surfaces.HasEventLog {
		res, elErr := RunActivityViaEventLog(ctx, client, parties, opts)
		if elErr != nil && !isEventLogFallbackSafe(ctx, elErr) {
			return ActivityResult{}, elErr
		}
		if elErr == nil && len(res.Events) > 0 {
			return withResolution(res), nil
		}
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
	return withResolution(ActivityResult{
		Events:    buildActivity(txs, opts.Instrument, decimals, limit),
		Truncated: truncated,
	}), nil
}

// isEventLogFallbackSafe reports whether an EventLog-path error should fall
// through to the netting reconstruction rather than abort. A cancelled or
// expired context is NOT fallback-safe: the netting path would hit the same
// deadline. Every other failure (stream open, malformed event/shape) is
// safe to retry via netting, which reconstructs history independently.
func isEventLogFallbackSafe(ctx context.Context, err error) bool {
	if err == nil {
		return true
	}
	if ctx.Err() != nil {
		return false
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

// consumeActivityStream drains a ledger updates channel into rawTx records.
// The Updates gRPC stream only flows forward, so it keeps a sliding window of
// the newest maxActivityScan transactions (truncated once older ones are
// evicted) rather than the oldest slice a leading break would yield. The
// contractId→facts map spans the whole scan, so a late archive still resolves
// against a create whose tx row was already evicted from the window.
func consumeActivityStream(stream <-chan ledger.StreamItem[*lapiv2.GetUpdatesResponse]) ([]rawTx, bool, error) {
	// contractId → holding facts, so archived events (which carry only the
	// contract id) resolve back to owner/instrument/amount.
	byContract := map[string]rawHoldingDelta{}
	win := newActivityWindow[rawTx](maxActivityScan)
	for item := range stream {
		if item.Err != nil {
			return nil, false, fmt.Errorf("updates stream: %w", item.Err)
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
				// Prefer the richer V2 view, fall back to V1, so a V1 Holding
				// (Amulet) also registers, never double-counted.
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
			win.add(tx)
		}
	}
	return win.slice(), win.truncated, nil
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
