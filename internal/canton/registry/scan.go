package registry

import "context"

// DSOResponse is the typed payload of [Client.GetDsoInfo]. The Splice
// scan API's `/v0/dso` endpoint returns the DSO (Decentralized
// Synchronizer Organization) party identifier plus metadata — the
// canonical "who am I federated with?" probe.
//
// Fields are a subset of the upstream JSON; we surface only what
// downstream consumers (Web UI status panel, doctor probe) actually
// read. Adding fields is a backward-compatible change — encoding/json
// silently ignores unknown fields by default, and the typed Go struct
// can grow new fields without breaking existing callers.
type DSOResponse struct {
	// DsoParty is the well-known party ID of the DSO governance entity,
	// e.g. "DSO::12203abc...". Stable across an entire Splice network.
	DsoParty string `json:"dso_party_id"`
}

// GetDsoInfo fetches the DSO party metadata from the scan app's public
// API. This is THE canonical Splice connectivity probe — the endpoint
// is unauthenticated (TokenSource is honored if set, but the response
// is identical without auth), cheap (sub-100ms on LocalNet), and
// returns a stable string for the entire network's lifetime.
//
// Use cases:
//   - `localnet doctor` "is the SV scan app reachable?" check (future).
//   - Web UI status panel's "Synchronizer: <dso>" badge.
//   - Smoke test in the integration suite.
//
// Available on the SV tenant only — app-provider / app-user nginx
// configs don't expose `/api/scan/...`. The caller is responsible for
// dialing the right tenant's BaseURL.
func (c *Client) GetDsoInfo(ctx context.Context) (*DSOResponse, error) {
	var resp DSOResponse
	if err := c.doJSON(ctx, "GET", "/api/scan/v0/dso", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
