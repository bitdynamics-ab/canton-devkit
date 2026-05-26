package ledger

import (
	"context"
	"fmt"

	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// UpdatesRequest is the typed input to [Client.Updates].
//
// The Ledger API v2 consolidated the v1 GetTransactions / GetTransactionTrees
// split into a single GetUpdates RPC parameterised by TransactionShape:
//   - TRANSACTION_SHAPE_ACS_DELTA: flat created/archived events (the
//     "transactions" view from v1) — what the contracts-watch CLI and
//     ACS-table Web UI need.
//   - TRANSACTION_SHAPE_LEDGER_EFFECTS: hierarchical exercise + create
//     tree (the "transaction trees" view from v1) — what `tx replay`
//     and the contract-detail drawer need to project visibility per party.
//
// Pass the shape via UpdateFormat.IncludeTransactions.TransactionShape.
// We don't add a typed convenience here because the dazl-client request
// shape already nests it; abstracting would just trade one shape for another.
type UpdatesRequest struct {
	// BeginExclusive is the offset to resume from (exclusive). Use 0
	// (or LedgerEnd-typed 0) for "from the beginning of the ledger".
	// Use the offset returned by a prior [Client.LedgerEnd] for "tail
	// from now".
	BeginExclusive int64

	// EndInclusive, if non-nil, bounds the stream — useful for `tx ls
	// --to <offset>` and snapshot-then-resume workflows. Nil means
	// "tail forever (until ctx cancels or participant closes)".
	EndInclusive *int64

	// UpdateFormat carries party + template + interface filters AND the
	// TransactionShape selector (flat vs tree). Pass nil for the wildcard
	// shape (the participant rejects this for security reasons in most
	// configs; production callers always set it).
	UpdateFormat *lapiv2.UpdateFormat
}

// Updates opens a server-streaming subscription on the participant's
// transaction + reassignment + topology stream. The stream is open-ended
// unless EndInclusive is set; cancel via ctx.
//
// Each StreamItem.Value carries a oneof discriminator (transaction vs
// reassignment vs offset-checkpoint vs topology-transaction). Type-assert
// via the dazl-client `GetUpdatesResponse_Transaction` etc. wrapper types.
//
// OffsetCheckpoint events are participant heartbeats: they don't represent
// state change, just confirm that "no events occurred up through offset
// N." Consumers persisting their resume cursor should treat checkpoints as
// "safe to advance the cursor to this offset" without invoking domain
// logic.
func (c *Client) Updates(ctx context.Context, req UpdatesRequest) (<-chan StreamItem[*lapiv2.GetUpdatesResponse], error) {
	r := &lapiv2.GetUpdatesRequest{
		BeginExclusive: req.BeginExclusive,
		EndInclusive:   req.EndInclusive,
		UpdateFormat:   req.UpdateFormat,
	}
	return pumpStream(ctx, func(ctx context.Context) (streamReceiver[*lapiv2.GetUpdatesResponse], error) {
		return c.update.GetUpdates(ctx, r)
	})
}

// UpdateByOffset fetches a single update (transaction, reassignment, or
// topology event) at a specific ledger offset. Used by `tx replay` and the
// Web UI Explorer's "show me transaction at offset N" drawer.
//
// Returns nil + a wrapped NotFound-style error if no update sits at that
// offset (e.g. it's an OffsetCheckpoint gap). Callers can `errors.Is`
// against grpc/status codes if they need to distinguish "not found" from
// "participant error".
func (c *Client) UpdateByOffset(ctx context.Context, req *lapiv2.GetUpdateByOffsetRequest) (*lapiv2.GetUpdateResponse, error) {
	resp, err := c.update.GetUpdateByOffset(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ledger.UpdateByOffset: %w", err)
	}
	return resp, nil
}

// UpdateById fetches a single update by its transaction (or reassignment)
// ID. Used by `tx replay <id>` and the Web UI deep-link path
// (`/transactions/<id>`).
func (c *Client) UpdateById(ctx context.Context, req *lapiv2.GetUpdateByIdRequest) (*lapiv2.GetUpdateResponse, error) {
	resp, err := c.update.GetUpdateById(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ledger.UpdateById: %w", err)
	}
	return resp, nil
}
