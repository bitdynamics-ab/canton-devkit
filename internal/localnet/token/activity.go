package token

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// The activity feed (BIT-219 lens 1, Activity tab) reconstructs an
// instrument's transfer/mint/burn history from the ledger transaction
// stream — no off-ledger transfer-events-v2 registry required. On a UTXO
// ledger every movement archives the sender's Holding contracts and
// creates the receiver's within one transaction, so a single scan over
// HoldingV2 create/archive events is enough to net out who sent what to
// whom:
//
//   - created holding  → a credit to its owner
//   - archived holding → a debit from its owner
//
// Per transaction we net the per-party deltas and classify:
//
//   - only credits, no debits → mint  (new supply appeared)
//   - only debits, no credits → burn  (supply destroyed)
//   - both                    → transfer
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

// ActivityEvent is one netted ledger transaction touching an instrument.
type ActivityEvent struct {
	Offset     int64        `json:"offset"`
	UpdateID   string       `json:"update_id"`
	RecordTime string       `json:"record_time"`
	Instrument string       `json:"instrument_id"`
	Kind       string       `json:"kind"` // mint | burn | transfer
	Amount     string       `json:"amount"`
	Senders    []PartyDelta `json:"senders,omitempty"`
	Receivers  []PartyDelta `json:"receivers,omitempty"`
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
// (limit <= 0 means no cap). Pure — unit-testable without a ledger.
func buildActivity(txs []rawTx, instrument string, limit int) []ActivityEvent {
	out := make([]ActivityEvent, 0, len(txs))
	for _, tx := range txs {
		ev, ok := netTransaction(tx, instrument)
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
func netTransaction(tx rawTx, instrument string) (ActivityEvent, bool) {
	net := map[string]*big.Float{}
	order := []string{}
	touched := false
	for _, d := range tx.deltas {
		if d.instrument != instrument {
			continue
		}
		touched = true
		amt, ok := new(big.Float).SetString(zeroIfEmpty(d.amount))
		if !ok {
			continue
		}
		if _, seen := net[d.party]; !seen {
			net[d.party] = new(big.Float)
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
	credited := new(big.Float)
	debited := new(big.Float)
	for _, p := range order {
		v := net[p]
		switch v.Sign() {
		case 1:
			receivers = append(receivers, PartyDelta{Party: p, Amount: v.Text('f', -1)})
			credited.Add(credited, v)
		case -1:
			mag := new(big.Float).Neg(v)
			senders = append(senders, PartyDelta{Party: p, Amount: mag.Text('f', -1)})
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
		Amount:     amount.Text('f', -1),
		Senders:    senders,
		Receivers:  receivers,
	}, true
}

// RunActivity scans the ledger transaction stream (offset 0 → ledger end)
// for HoldingV2 create/archive events and reconstructs the instrument's
// activity history. opts.Instrument selects the instrument; opts.Limit
// caps the returned events (default 50). Read-only; no submission.
func RunActivity(ctx context.Context, opts BalanceOptions) ([]ActivityEvent, error) {
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
	client, cleanup, err := dialLedger(ctx, conn)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	// Widen read-as to every registered party alias (BIT-215 #1) so the
	// activity feed reconstructs movements across ALL aliased parties,
	// then re-resolve the authoritative granted set.
	parties, err := resolveReadableParties(ctx, client, opts.Instance, opts.Role)
	if err != nil {
		return nil, err
	}
	if len(parties) == 0 {
		return []ActivityEvent{}, nil
	}

	end, err := client.LedgerEnd(ctx)
	if err != nil {
		return nil, fmt.Errorf("ledger end: %w", err)
	}
	endInc := end.Offset
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := client.Updates(streamCtx, ledger.UpdatesRequest{
		BeginExclusive: 0,
		EndInclusive:   &endInc,
		UpdateFormat: &lapiv2.UpdateFormat{
			IncludeTransactions: &lapiv2.TransactionFormat{
				EventFormat:      holdingInterfaceFilterV2(parties),
				TransactionShape: lapiv2.TransactionShape_TRANSACTION_SHAPE_ACS_DELTA,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("open updates stream: %w", err)
	}

	// contractId → its holding facts, so archived events (which carry
	// only the contract id) can be resolved back to owner/instrument/amount.
	byContract := map[string]rawHoldingDelta{}
	var txs []rawTx
	for item := range stream {
		if item.Err != nil {
			return nil, fmt.Errorf("updates stream: %w", item.Err)
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
				for _, iv := range c.GetInterfaceViews() {
					view, ok := extractHoldingViewV2(iv)
					if !ok {
						continue
					}
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

	return buildActivity(txs, opts.Instrument, limit), nil
}
