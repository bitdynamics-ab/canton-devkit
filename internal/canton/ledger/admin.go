package ledger

import (
	"context"
	"fmt"
	"sort"

	adminv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2/admin"
)

// ListKnownParties enumerates the parties allocated on the participant.
// Used by `localnet env` to surface the per-role party IDs and by the
// Web UI Explorer's party-picker dropdown.
func (c *Client) ListKnownParties(ctx context.Context) (*adminv2.ListKnownPartiesResponse, error) {
	resp, err := c.partyMgmt.ListKnownParties(ctx, &adminv2.ListKnownPartiesRequest{})
	if err != nil {
		return nil, fmt.Errorf("ledger.ListKnownParties: %w", err)
	}
	return resp, nil
}

// AllocateParty creates a new party on the participant. Used by the
// token CLI (`token create`) when a new test identity is needed.
//
// The participant assigns the canonical party ID; the request's
// PartyIdHint is advisory (the participant appends a fingerprint).
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

// UploadDarFile uploads a DAR archive to the participant, which validates,
// extracts, and registers the contained packages — available for command
// submission immediately on success. For multi-participant uploads the
// caller dials each participant's admin port and calls this once each.
func (c *Client) UploadDarFile(ctx context.Context, req *adminv2.UploadDarFileRequest) (*adminv2.UploadDarFileResponse, error) {
	resp, err := c.packageMgmt.UploadDarFile(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ledger.UploadDarFile: %w", err)
	}
	return resp, nil
}

// ListMyUserRights returns the party-rights and admin claims attached to
// the JWT's claimed user (the empty UserId asks the participant to use the
// token's own user).
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
// sorted union of parties the token can ActAs or ReadAs. Empty means no
// party claims — the caller treats the query as "nothing visible".
//
// CanReadAsAnyParty / ParticipantAdmin still return only the explicit
// per-party set: those claims expand "across all parties" inside Canton,
// and we can't enumerate "any party" client-side without a separate
// ListKnownParties call.
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
// CanReadAs for every supplied party. Idempotent: Canton tolerates
// re-grants of already-held rights.
//
// Why this exists: the Splice V2 alpha boot doesn't auto-grant the
// `ledger-api-user` user any party rights (stable 0.6.4 does), so without
// these grants the JWT is accepted but every ACS / submit RPC returns
// PermissionDenied. The token CLI grants the local party before its first
// ledger call. userId="" applies the grant to the JWT's own user.
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

// GrantUserActAndReadAsAnyParty grants the user the wildcard
// CanActAsAnyParty + CanReadAsAnyParty rights. Per-party grants only cover
// parties we know about, but token-standard flows must read AND submit
// against contracts owned by parties we don't host — the DSO, instrument
// admins, other holders — or those RPCs PermissionDenied. Idempotent.
// LocalNet-only, where the operator owns everyone.
func (c *Client) GrantUserActAndReadAsAnyParty(ctx context.Context, userID string) error {
	_, err := c.userMgmt.GrantUserRights(ctx, &adminv2.GrantUserRightsRequest{
		UserId: userID,
		Rights: []*adminv2.Right{
			{Kind: &adminv2.Right_CanExecuteAsAnyParty_{CanExecuteAsAnyParty: &adminv2.Right_CanExecuteAsAnyParty{}}},
			{Kind: &adminv2.Right_CanReadAsAnyParty_{CanReadAsAnyParty: &adminv2.Right_CanReadAsAnyParty{}}},
		},
	})
	if err != nil {
		return fmt.Errorf("ledger.GrantUserActAndReadAsAnyParty: %w", err)
	}
	return nil
}

// ListKnownPackages enumerates packages from the admin perspective — same
// set as [Client.ListPackages] but with extra metadata (source DAR hash,
// upload time, per-synchronizer vetting state) only the admin API
// surfaces. The plain ListPackages is fine for "is this package present?"
// checks.
func (c *Client) ListKnownPackages(ctx context.Context) (*adminv2.ListKnownPackagesResponse, error) {
	resp, err := c.packageMgmt.ListKnownPackages(ctx, &adminv2.ListKnownPackagesRequest{})
	if err != nil {
		return nil, fmt.Errorf("ledger.ListKnownPackages: %w", err)
	}
	return resp, nil
}
