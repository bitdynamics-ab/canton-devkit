package ledger

import (
	"context"
	"fmt"

	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// EventsByContractId fetches the lifecycle events (create + archive + any
// exercises) for a single contract, projected through the requesting
// party's visibility. Used by:
//   - `tx replay` (per-party projection of a single transaction's
//     effects on a contract).
//   - Web UI Explorer's contract-detail drawer "lifecycle" tab.
//
// Returns a response containing two oneof-discriminated event records
// (Created, Archived); the dazl-client GetEventsByContractIdResponse type
// carries optional fields rather than a oneof — check non-nil per field.
func (c *Client) EventsByContractId(ctx context.Context, req *lapiv2.GetEventsByContractIdRequest) (*lapiv2.GetEventsByContractIdResponse, error) {
	resp, err := c.eventQuery.GetEventsByContractId(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ledger.EventsByContractId: %w", err)
	}
	return resp, nil
}
