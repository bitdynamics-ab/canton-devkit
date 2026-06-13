package ledger

import (
	"context"
	"fmt"
	"sort"

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

// ListMyUserRights returns the party-rights and admin claims attached
// to the JWT's claimed user (the empty UserId asks the participant
// to use the token's own user). Lets the Web UI Explorer resolve
// a Splice LocalNet user-id token to the concrete party set it
// can read.
//
// Each Right is a oneof — typical entries:
//
//	CanActAs.Party       — can submit commands as that party
//	CanReadAs.Party      — can subscribe to that party's ACS / stream
//	ParticipantAdmin     — admin claim (rare for vanilla tokens)
//	CanReadAsAnyParty    — wildcard read (also rare; admin-equivalent)
//
// Callers walk Rights and union the parties via [ResolveActAndReadParties].
func (c *Client) ListMyUserRights(ctx context.Context) (*adminv2.ListUserRightsResponse, error) {
	resp, err := c.userMgmt.ListUserRights(ctx, &adminv2.ListUserRightsRequest{})
	if err != nil {
		return nil, fmt.Errorf("ledger.ListMyUserRights: %w", err)
	}
	return resp, nil
}

// ResolveActAndReadParties walks the JWT's user-rights and returns the
// union of parties the token can ActAs or ReadAs. Empty result means
// the JWT carries no party claims and the caller should treat the
// query as "nothing visible".
//
// Returns a deterministic-order slice (sorted) so consumers using the
// result as a map key get stable behaviour across calls.
//
// If the user has CanReadAsAnyParty or ParticipantAdmin, this still
// returns the explicit per-party set — those claims expand "across all
// parties" semantics inside Canton itself; we can't enumerate "any
// party" client-side without a separate ListKnownParties call.
func (c *Client) ResolveActAndReadParties(ctx context.Context) ([]string, error) {
	resp, err := c.ListMyUserRights(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(resp.GetRights()))
	for _, r := range resp.GetRights() {
		if a := r.GetCanActAs(); a != nil {
			seen[a.GetParty()] = struct{}{}
		}
		if rd := r.GetCanReadAs(); rd != nil {
			seen[rd.GetParty()] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// GrantUserActAndReadAs grants the calling JWT's user CanActAs +
// CanReadAs for every supplied party. Idempotent: Canton's grant API
// returns the resulting full rights list and tolerates re-grants of
// already-held rights, so calling this multiple times is safe.
//
// Why this exists: the Splice V2 alpha boot doesn't auto-grant the
// `ledger-api-user` user any party rights on the per-role participants
// (stable Splice 0.6.4 does). Without these grants the JWT is accepted
// but every ACS / submit RPC returns PermissionDenied. The token CLI
// auto-grants the local party before its first ledger call so V2 boots
// behave like stable Splice boots from the caller's perspective.
//
// userId="" asks the participant to apply the grant to the JWT's own
// user — same shape as ListMyUserRights.
func (c *Client) GrantUserActAndReadAs(ctx context.Context, userID string, parties []string) error {
	if len(parties) == 0 {
		return nil
	}
	rights := make([]*adminv2.Right, 0, len(parties)*2)
	for _, p := range parties {
		rights = append(rights,
			&adminv2.Right{Kind: &adminv2.Right_CanActAs_{CanActAs: &adminv2.Right_CanActAs{Party: p}}},
			&adminv2.Right{Kind: &adminv2.Right_CanReadAs_{CanReadAs: &adminv2.Right_CanReadAs{Party: p}}},
		)
	}
	_, err := c.userMgmt.GrantUserRights(ctx, &adminv2.GrantUserRightsRequest{
		UserId: userID,
		Rights: rights,
	})
	if err != nil {
		return fmt.Errorf("ledger.GrantUserActAndReadAs: %w", err)
	}
	return nil
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
