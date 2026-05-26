package ledger

import (
	"context"
	"fmt"

	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// ActiveContractsRequest is the typed input to [Client.ActiveContracts].
// Mirrors GetActiveContractsRequest with the same field semantics:
//   - ActiveAtOffset: snapshot the ACS as of this ledger offset. Pair
//     with [Client.LedgerEnd] to get an exact "now" snapshot, then resume
//     [Client.Updates] from the same offset for exactly-once delivery.
//   - EventFormat: party + template + interface filters. Pass nil to
//     request the wildcard (FiltersForAnyParty with empty Filters), which
//     the participant accepts as "everything visible to the JWT's claim".
//     Production callers typically construct a per-party filter.
type ActiveContractsRequest struct {
	ActiveAtOffset int64
	EventFormat    *lapiv2.EventFormat
}

// ActiveContracts opens a server-streaming ACS snapshot from the
// participant. The stream yields one entry per active contract (or
// in-flight reassignment) visible to the JWT's party claim at
// ActiveAtOffset, then EOF.
//
// Channel semantics: see [StreamItem]. Cancel via ctx; the participant
// closes its end when the snapshot completes (a few seconds typically;
// minutes for very large ACS).
//
// Each StreamItem.Value carries a oneof discriminator (active vs
// incomplete-assigned vs incomplete-unassigned reassignment) — callers
// unwrap via the dazl-client `GetActiveContractsResponse_ActiveContract`
// etc. type assertions. We deliberately don't translate the oneof into a
// Go union here: this layer is a thin wrapper, and translation duplicates
// the proto shape without adding type safety.
func (c *Client) ActiveContracts(ctx context.Context, req ActiveContractsRequest) (<-chan StreamItem[*lapiv2.GetActiveContractsResponse], error) {
	r := &lapiv2.GetActiveContractsRequest{
		ActiveAtOffset: req.ActiveAtOffset,
		EventFormat:    req.EventFormat,
	}
	return pumpStream(ctx, func(ctx context.Context) (streamReceiver[*lapiv2.GetActiveContractsResponse], error) {
		return c.state.GetActiveContracts(ctx, r)
	})
}

// ConnectedSynchronizers lists the synchronizers (formerly "domains") the
// participant is currently connected to for a given party. Useful for the
// Web UI Explorer's "which synchronizer is this contract on?" affordance.
func (c *Client) ConnectedSynchronizers(ctx context.Context, party string) (*lapiv2.GetConnectedSynchronizersResponse, error) {
	resp, err := c.state.GetConnectedSynchronizers(ctx, &lapiv2.GetConnectedSynchronizersRequest{Party: party})
	if err != nil {
		return nil, fmt.Errorf("ledger.ConnectedSynchronizers: %w", err)
	}
	return resp, nil
}

// LedgerEnd is the current end-of-stream offset on the participant. Stream
// callers pass this (or an earlier offset) as the resume point for
// [Client.Updates] / [Client.UpdateTrees]; one-shot callers use it as a
// "snapshot at" cursor for [Client.ActiveContracts].
//
// Offset is an int64 ascending counter assigned by the participant. Zero
// means "before any event" (use as begin-exclusive lower bound). The wire
// proto uses int64 (not uint64) because Daml's offset semantics include
// "begin" as a sentinel value distinct from "first offset"; mirror that
// here rather than translating between signed/unsigned representations.
type LedgerEnd struct {
	Offset int64
}

// LedgerEnd returns the current end-of-stream offset on the participant.
// This is the simplest unary in the Ledger API v2 surface — it takes no
// arguments, returns a single offset, and is the canonical readiness probe
// (if it succeeds, auth + connectivity work).
//
// Useful before opening a stream: snapshot end, run ACS query, then resume
// Updates from end → present to get exactly-once delivery across the
// snapshot + tail boundary.
func (c *Client) LedgerEnd(ctx context.Context) (LedgerEnd, error) {
	resp, err := c.state.GetLedgerEnd(ctx, &lapiv2.GetLedgerEndRequest{})
	if err != nil {
		return LedgerEnd{}, fmt.Errorf("ledger.LedgerEnd: %w", err)
	}
	return LedgerEnd{Offset: resp.GetOffset()}, nil
}
