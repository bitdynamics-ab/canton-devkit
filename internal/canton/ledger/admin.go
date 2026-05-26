package ledger

import (
	"context"
	"fmt"

	adminv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2/admin"
)

// ListKnownParties enumerates the parties allocated on the participant.
// Used by:
//   - `localnet env --name foo` to surface the per-role party IDs (Alice,
//     Bob, etc. — proposal line 110).
//   - Web UI Explorer's party-picker dropdown.
//   - `localnet status --name foo` if/when we surface "this participant
//     hosts: ..." details.
func (c *Client) ListKnownParties(ctx context.Context) (*adminv2.ListKnownPartiesResponse, error) {
	resp, err := c.partyMgmt.ListKnownParties(ctx, &adminv2.ListKnownPartiesRequest{})
	if err != nil {
		return nil, fmt.Errorf("ledger.ListKnownParties: %w", err)
	}
	return resp, nil
}

// AllocateParty creates a new party on the participant. Used by the M3
// token wizard (`token create` — proposal line 209) when a new test
// identity is needed, and by future `localnet party allocate <hint>`
// CLI if we ship one.
//
// The participant assigns the canonical party ID; the request's
// PartyIdHint is advisory (the participant appends a fingerprint).
// LocalServer's allocation latency is sub-second.
func (c *Client) AllocateParty(ctx context.Context, req *adminv2.AllocatePartyRequest) (*adminv2.AllocatePartyResponse, error) {
	resp, err := c.partyMgmt.AllocateParty(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ledger.AllocateParty: %w", err)
	}
	return resp, nil
}

// GetParticipantId returns the participant's own ID. Surfaced for the
// Web UI status panel and any "who am I talking to" smoke test.
func (c *Client) GetParticipantId(ctx context.Context) (*adminv2.GetParticipantIdResponse, error) {
	resp, err := c.partyMgmt.GetParticipantId(ctx, &adminv2.GetParticipantIdRequest{})
	if err != nil {
		return nil, fmt.Errorf("ledger.GetParticipantId: %w", err)
	}
	return resp, nil
}

// UploadDarFile uploads a DAR archive to the participant. The participant
// validates, extracts, and registers the contained packages — they
// become available for command submission immediately on success.
//
// Used by:
//   - `dpm localnet dar upload <path>` (proposal line 131) — the CLI
//     reads the .dar file into req.DarFile bytes.
//   - DAR Web UI drag-and-drop (proposal line 261) — the handler
//     streams the upload body into req.DarFile then calls this.
//
// For multi-participant uploads, the caller dials each participant's
// admin port and calls this once per participant. The CLI's --all-participants
// flag does that loop.
func (c *Client) UploadDarFile(ctx context.Context, req *adminv2.UploadDarFileRequest) (*adminv2.UploadDarFileResponse, error) {
	resp, err := c.packageMgmt.UploadDarFile(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ledger.UploadDarFile: %w", err)
	}
	return resp, nil
}

// ListKnownPackages enumerates packages from the admin perspective — same
// set as [Client.ListPackages] but with extra metadata (source DAR hash,
// upload time, vetting state per synchronizer) only the admin API
// surfaces. The package explorer Web UI prefers this for its richer
// view; the plain ListPackages is fine for "is this package present?"
// checks.
func (c *Client) ListKnownPackages(ctx context.Context) (*adminv2.ListKnownPackagesResponse, error) {
	resp, err := c.packageMgmt.ListKnownPackages(ctx, &adminv2.ListKnownPackagesRequest{})
	if err != nil {
		return nil, fmt.Errorf("ledger.ListKnownPackages: %w", err)
	}
	return resp, nil
}
