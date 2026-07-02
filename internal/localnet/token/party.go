package token

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	adminv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2/admin"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// Party registry orchestration. On LocalNet there is no trust
// boundary between parties — the `unsafe` dev secret signs for every
// role — so the token tool treats the instance as one god-mode
// workspace: a developer allocates a party by alias, the participant
// grants the role's user act/read-as for it, and the alias is recorded
// so every later command (and the matrix / activity scan) can refer to
// it by name. Both the CLI (`localnet party …`) and the Web UI call
// into these functions, so the two surfaces stay in lock-step (see
// the CLI ↔ Web UI parity rule in CONTRIBUTING.md).

// validAlias keeps aliases readable and collision-free with party-id
// syntax: a letter followed by letters/digits/hyphens. Rejects empty,
// leading digit/hyphen, and anything containing "::" (which would be
// mistaken for a raw party id by ResolveAlias).
var validAlias = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// ErrAliasInvalid / ErrAliasInUse are user-error conditions (exit 1),
// not system failures.
var (
	ErrAliasInvalid = errors.New("invalid party alias")
	ErrAliasInUse   = errors.New("party alias already registered")
	ErrAliasUnknown = errors.New("party alias not registered")
)

// PartyOptions carries the live-ledger connection + the alias for the
// party verbs. Endpoint/Token/Role/Insecure mirror BalanceOptions.
type PartyOptions struct {
	Instance string
	Alias    string
	Endpoint string
	Token    string
	Role     string
	Insecure bool
}

// RunPartyNew allocates a fresh party on the role's participant (party-id
// hint = alias), grants the role's user CanActAs + CanReadAs for it, and
// records the alias → party-id mapping in the instance registry. Returns
// the recorded PartyRef.
func RunPartyNew(ctx context.Context, opts PartyOptions) (*registry.PartyRef, error) {
	if !validAlias.MatchString(opts.Alias) {
		return nil, fmt.Errorf("%w: %q must be a letter followed by letters, digits or hyphens",
			ErrAliasInvalid, opts.Alias)
	}

	release, err := registry.Lock(opts.Instance)
	if err != nil {
		return nil, fmt.Errorf("acquire instance lock: %w", err)
	}
	defer release()

	state, err := registry.Read(opts.Instance)
	if err != nil {
		return nil, fmt.Errorf("read instance state: %w", err)
	}
	if _, dup := state.Parties[opts.Alias]; dup {
		return nil, fmt.Errorf("%w: %q", ErrAliasInUse, opts.Alias)
	}

	conn := LedgerConn{
		Endpoint: opts.Endpoint, Token: opts.Token,
		Insecure: opts.Insecure, Instance: opts.Instance, Role: roleOrDefault(opts.Role),
	}
	client, cleanup, err := dialLedger(ctx, conn)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	var partyID string
	var isLocal bool
	resp, err := client.AllocateParty(ctx, &adminv2.AllocatePartyRequest{PartyIdHint: opts.Alias})
	if err != nil {
		// Recoverable case: a prior `party new` allocated the party on
		// the node but didn't finish (e.g. the grant failed). The party
		// is permanent, so re-allocation is rejected — find the existing
		// one by hint and continue to grant + record. Idempotent retry.
		existing, lookupErr := findPartyByHint(ctx, client, opts.Alias)
		if lookupErr != nil || existing == "" {
			return nil, fmt.Errorf("allocate party %q: %w", opts.Alias, err)
		}
		partyID = existing
		isLocal = true
	} else {
		details := resp.GetPartyDetails()
		partyID = details.GetParty()
		isLocal = details.GetIsLocal()
	}
	if partyID == "" {
		return nil, fmt.Errorf("participant returned an empty party id for alias %q", opts.Alias)
	}

	// Grant the role's user act/read-as so the role's JWT can spend from
	// and see the new party — without this the party exists but is
	// invisible to every ACS / submit RPC. The participant requires an
	// explicit user id on GrantUserRights (unlike the ListMyUserRights
	// "" shorthand); the Splice per-role tokens all authenticate as
	// exerciseUserID ("ledger-api-user").
	if err := client.GrantUserActAndReadAs(ctx, exerciseUserID, []string{partyID}); err != nil {
		return nil, fmt.Errorf("grant rights for %q: %w", opts.Alias, err)
	}

	ref := registry.PartyRef{
		Alias:     opts.Alias,
		PartyID:   partyID,
		Role:      roleOrDefault(opts.Role),
		IsLocal:   isLocal,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if state.Parties == nil {
		state.Parties = map[string]registry.PartyRef{}
	}
	state.Parties[opts.Alias] = ref
	if err := registry.Write(state); err != nil {
		return nil, fmt.Errorf("persist instance state: %w", err)
	}
	return &ref, nil
}

// RunPartyList returns the instance's registered parties, sorted by
// alias. When Endpoint is set it first lazy-seeds the role parties
// (app-user / app-provider / sv) discovered on the ledger so a fresh
// instance lists them without an explicit `party new`. Best-effort:
// a ledger error just returns whatever's already recorded.
func RunPartyList(ctx context.Context, opts PartyOptions) ([]registry.PartyRef, error) {
	if opts.Endpoint != "" {
		_ = seedRoleParties(ctx, opts)
	}
	state, err := registry.Read(opts.Instance)
	if err != nil {
		return nil, fmt.Errorf("read instance state: %w", err)
	}
	out := make([]registry.PartyRef, 0, len(state.Parties))
	for _, ref := range state.Parties {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	return out, nil
}

// RunPartyRemove forgets an alias. It does NOT de-allocate the on-ledger
// party (parties are permanent on a Canton participant) — it only drops
// the alias mapping, so the name frees up and the party stops appearing
// in the workspace's read-as widening.
func RunPartyRemove(instance, alias string) error {
	release, err := registry.Lock(instance)
	if err != nil {
		return fmt.Errorf("acquire instance lock: %w", err)
	}
	defer release()

	state, err := registry.Read(instance)
	if err != nil {
		return fmt.Errorf("read instance state: %w", err)
	}
	if _, ok := state.Parties[alias]; !ok {
		return fmt.Errorf("%w: %q", ErrAliasUnknown, alias)
	}
	delete(state.Parties, alias)
	if err := registry.Write(state); err != nil {
		return fmt.Errorf("persist instance state: %w", err)
	}
	return nil
}

// seedRoleParties records the role parties (app-user, app-provider, sv)
// under their role-name aliases if they aren't already registered. Idempotent.
func seedRoleParties(ctx context.Context, opts PartyOptions) error {
	conn := LedgerConn{
		Endpoint: opts.Endpoint, Token: opts.Token,
		Insecure: opts.Insecure, Instance: opts.Instance, Role: roleOrDefault(opts.Role),
	}
	client, cleanup, err := dialLedger(ctx, conn)
	if err != nil {
		return err
	}
	defer cleanup()

	role := roleOrDefault(opts.Role)
	locals, err := localPartiesForRole(ctx, client, role)
	if err != nil || len(locals) == 0 {
		return err
	}
	sort.Strings(locals)

	release, err := registry.Lock(opts.Instance)
	if err != nil {
		return err
	}
	defer release()
	state, err := registry.Read(opts.Instance)
	if err != nil {
		return err
	}
	if state.Parties == nil {
		state.Parties = map[string]registry.PartyRef{}
	}
	// Already aliased under any name? Skip — don't double-seed.
	if AliasFor(state.Parties, locals[0]) != "" {
		return nil
	}
	if _, taken := state.Parties[role]; taken {
		return nil
	}
	state.Parties[role] = registry.PartyRef{
		Alias:     role,
		PartyID:   locals[0],
		Role:      role,
		IsLocal:   true,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	return registry.Write(state)
}

// findPartyByHint returns the locally-hosted party id whose id begins
// with "<hint>::" (Canton derives party ids as "<hint>::<fingerprint>").
// Used to recover an already-allocated party on an idempotent retry.
// Returns "" when none matches.
func findPartyByHint(ctx context.Context, c *ledger.Client, hint string) (string, error) {
	resp, err := c.ListKnownParties(ctx)
	if err != nil {
		return "", err
	}
	prefix := hint + "::"
	for _, p := range resp.GetPartyDetails() {
		if p.GetIsLocal() && strings.HasPrefix(p.GetParty(), prefix) {
			return p.GetParty(), nil
		}
	}
	return "", nil
}

func roleOrDefault(role string) string {
	if role == "" {
		return "app-user"
	}
	return role
}
