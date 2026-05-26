package ledger

import (
	"context"
	"fmt"

	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

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
