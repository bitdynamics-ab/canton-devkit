package ledger

import (
	"context"
	"fmt"

	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// LedgerApiVersion returns the participant's reported Ledger API version
// (semver + a set of experimental-features descriptors). Used by:
//   - `localnet doctor` to confirm the participant speaks a v2 API at all
//     (catches the "user pointed --version at a 2.x Splice" misconfig).
//   - The future "supported features" matrix in the Web UI's
//     About / Diagnostics drawer.
//
// Cheap call (~1ms on LocalNet); safe to use as a connectivity probe in
// long-running clients that want to detect participant restarts.
func (c *Client) LedgerApiVersion(ctx context.Context) (*lapiv2.GetLedgerApiVersionResponse, error) {
	resp, err := c.version.GetLedgerApiVersion(ctx, &lapiv2.GetLedgerApiVersionRequest{})
	if err != nil {
		return nil, fmt.Errorf("ledger.LedgerApiVersion: %w", err)
	}
	return resp, nil
}
