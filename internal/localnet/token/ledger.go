package token

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
	adminv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2/admin"
)

// LedgerClient is the narrow slice of *ledger.Client the live-ACS
// orchestration paths use. It's the seam that lets tests inject a fake
// without a real participant. Add methods as paths come under test.
type LedgerClient interface {
	LedgerEnd(ctx context.Context) (ledger.LedgerEnd, error)
	ActiveContracts(ctx context.Context, req ledger.ActiveContractsRequest) (<-chan ledger.StreamItem[*lapiv2.GetActiveContractsResponse], error)
	Updates(ctx context.Context, req ledger.UpdatesRequest) (<-chan ledger.StreamItem[*lapiv2.GetUpdatesResponse], error)
	ResolveActAndReadParties(ctx context.Context) ([]string, error)
	ListKnownParties(ctx context.Context) (*adminv2.ListKnownPartiesResponse, error)
	GrantUserActAndReadAs(ctx context.Context, userID string, parties []string) error
	GrantUserActAndReadAsAnyParty(ctx context.Context, userID string) error
	ListKnownPackages(ctx context.Context) (*adminv2.ListKnownPackagesResponse, error)
}

// dialLedgerFn is the test seam runBalanceLive and scanWorkspace dial
// through: it upcasts dialLedger's concrete *ledger.Client to
// LedgerClient, and tests reassign it to return a fakeLedger. A package
// var (rather than a BalanceOptions field) keeps the seam invisible to
// callers; the package's tests run sequentially, so the mutation is safe.
var dialLedgerFn = func(ctx context.Context, conn LedgerConn) (LedgerClient, func(), error) {
	return dialLedger(ctx, conn)
}

// dialLedgerConcreteFn is the seam for mint/transfer accept paths that open
// a *ledger.Client. Tests replace it to assert which participant the accept
// step dials without needing a live gRPC server.
var dialLedgerConcreteFn = dialLedger

// dialSenderFn is the seam for the initial (sender-side) dial in runMintLive
// and runTransferLiveOnLedger. Tests replace it with a stub that returns a
// pre-built no-op client so execution reaches the accept-side dial.
var dialSenderFn = dialLedger

// LedgerConn captures everything a live ledger call needs: the gRPC
// endpoint and the Bearer JWT the participant accepts.
type LedgerConn struct {
	Endpoint string // host:port, e.g. "localhost:61169"
	Token    string // Bearer JWT; empty allowed when the participant runs unauthenticated
	Insecure bool   // plaintext gRPC (LocalNet default)

	// Instance + Role let dialLedger resolve a per-role JWT
	// automatically when Token is empty: it tries the registry's
	// captured credentials first, then falls back to issuing a
	// fresh JWT via splice.SignToken against the cached project's
	// env files. Either path produces a Daml user-token with the
	// per-role audience the participant expects.
	Instance string
	Role     string // "sv" / "app-provider" / "app-user"
}

// resolveAcceptConn returns the LedgerConn that the accept step should use.
// When the receiver party lives on a different role than the sender, the
// conn is re-pointed at the receiver's participant; otherwise the sender's
// conn is returned unchanged so the same connection is reused.
func resolveAcceptConn(conn LedgerConn, instance, receiverPartyID string) LedgerConn {
	role := receiverRole(instance, receiverPartyID)
	if role == "" || role == conn.Role {
		return conn
	}
	ep := ResolveLedgerEndpoint(instance, role)
	if ep == "" {
		return conn
	}
	acceptConn := conn
	acceptConn.Endpoint = ep
	acceptConn.Role = role
	return acceptConn
}

// ResolveLedgerEndpoint returns the role's participant ledger gRPC endpoint
// (host:port) only while the registry says the instance is running. Stopped,
// paused, failed, and mid-transition instances retain their allocated ports,
// but those ports are not live endpoints; returning them would bypass the
// registry fallback and produce a connection-refused error. Empty role
// defaults to app-provider. Both the CLI and Web UI call this shared resolver.
func ResolveLedgerEndpoint(instance, role string) string {
	if role == "" {
		role = DefaultRole
	}
	state, err := registry.Read(instance)
	if err != nil {
		return ""
	}
	if state.Status != registry.StatusRunning {
		return ""
	}
	if port, ok := state.Ports["participant_ledger_"+role]; ok && port > 0 {
		return "localhost:" + strconv.Itoa(port)
	}
	return ""
}

// dialLedger opens a ledger client against the given endpoint. Caller
// MUST defer the returned cleanup.
//
// Token resolution order:
// 1. conn.Token explicit (`--token` flag wins).
// 2. registry.State.Credentials[Role] — captured by `localnet up`.
// 3. splice.SignToken fallback — issues a fresh user-token from the
// project's env files; used when creds-capture races / fails.
// 4. error pointing at `localnet creds --raw` for the user to override.
func dialLedger(ctx context.Context, conn LedgerConn) (*ledger.Client, func(), error) {
	if conn.Endpoint == "" {
		return nil, func() {}, fmt.Errorf("ledger endpoint is required (host:port)")
	}
	token, err := resolveLedgerToken(conn)
	if err != nil {
		return nil, func() {}, err
	}

	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	opts := ledger.DialOptions{
		Endpoint: conn.Endpoint,
		// PlainText defaults to true (LocalNet-first); Insecure is the
		// same gate exposed on our struct for symmetry.
		PlainText: conn.Insecure || !strings.HasPrefix(conn.Endpoint, "https://"),
	}
	if token != "" {
		opts.Token = ledger.StaticToken(token)
	}
	client, err := ledger.Dial(dialCtx, opts)
	if err != nil {
		return nil, func() {}, fmt.Errorf("dial ledger %s: %w", conn.Endpoint, err)
	}
	// V2-alpha gap: the `-dev` boot does NOT auto-grant `ledger-api-user`
	// CanActAs/CanReadAs on the local party (stable 0.6.4 does), so every
	// ACS / submit returns PermissionDenied even with an accepted JWT.
	// Grant the local party set on first dial; idempotent afterwards.
	if err := ensureLocalPartyRights(ctx, client, conn.Role); err != nil {
		_ = client.Close()
		return nil, func() {}, err
	}
	return client, func() { _ = client.Close() }, nil
}

// jwtPattern matches a compact JWS (three base64url segments, `eyJ`
// header). Used by resolveLedgerToken to scrub any bearer token that
// might otherwise ride out in an error string.
var jwtPattern = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)

// redactJWTs replaces any JWT-shaped substring with a fixed marker.
// Defence-in-depth: today's token-resolution errors don't carry the token
// itself, but a future change could, and this guard ensures a bearer
// token can never reach the user via an error.
func redactJWTs(err error) error {
	if err == nil {
		return nil
	}
	masked := jwtPattern.ReplaceAllString(err.Error(), "[redacted-jwt]")
	if masked == err.Error() {
		return err // nothing matched — preserve the original error and its wrapping
	}
	return errors.New(masked)
}

// ensureLocalPartyRights grants per-party Act/Read rights for the
// role-matched local parties when the JWT carries none. A no-op when the
// user already has Act/Read rights.
func ensureLocalPartyRights(ctx context.Context, c LedgerClient, role string) error {
	// Wildcard read: interface-filtered ACS queries must read contracts
	// owned by parties we don't host (the DSO, instrument admins, other
	// holders), which per-party CanReadAs can't cover. Best-effort +
	// idempotent.
	_ = c.GrantUserActAndReadAsAnyParty(ctx, exerciseUserID)

	rights, err := c.ResolveActAndReadParties(ctx)
	if err != nil {
		return fmt.Errorf("probe user rights: %w", err)
	}
	if len(rights) > 0 {
		return nil
	}
	parties, err := localPartiesForRole(ctx, c, role)
	if err != nil {
		return fmt.Errorf("discover local parties for role %q: %w", role, err)
	}
	if len(parties) == 0 {
		return fmt.Errorf("no local parties found for role %q on this participant — "+
			"V2 onboarding may not have completed; check `localnet status`", role)
	}
	if err := c.GrantUserActAndReadAs(ctx, exerciseUserID, parties); err != nil {
		return fmt.Errorf("grant Act/Read rights for parties %v: %w", parties, err)
	}
	return nil
}

// localPartiesForRole returns the locally-hosted party IDs whose name
// matches the role's prefix (e.g. role=app-user matches `app_user_*`).
// Only locally-hosted parties can be the subject of grants.
func localPartiesForRole(ctx context.Context, c LedgerClient, role string) ([]string, error) {
	resp, err := c.ListKnownParties(ctx)
	if err != nil {
		return nil, err
	}
	prefix := strings.ReplaceAll(role, "-", "_") + "_"
	var out []string
	for _, p := range resp.GetPartyDetails() {
		if !p.GetIsLocal() {
			continue
		}
		if strings.HasPrefix(p.GetParty(), prefix) {
			out = append(out, p.GetParty())
		}
	}
	return out, nil
}

// resolveReadableParties returns the parties a scan should cover. It
// widens the role's user with CanReadAs for every registered party alias
// so the matrix / activity feed see all aliased parties, then re-resolves
// the authoritative granted set via ResolveActAndReadParties.
//
// Granting before resolving is deliberate: ResolveActAndReadParties stays
// the source of truth for the ACS filter, so a party that couldn't be
// granted (e.g. hosted on another participant) never enters the filter —
// querying it would PermissionDenied the whole stream. The grant is
// best-effort.
func resolveReadableParties(ctx context.Context, c LedgerClient, instance, role string) ([]string, error) {
	if extra := partiesFromState(instance); len(extra) > 0 {
		_ = c.GrantUserActAndReadAs(ctx, exerciseUserID, extra)
	}
	parties, err := c.ResolveActAndReadParties(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve readable parties: %w", err)
	}
	if len(parties) == 0 {
		parties, _ = localPartiesForRole(ctx, c, role)
	}
	return dropPhantomAliases(parties, aliasSet(instance)), nil
}

// aliasSet returns the set of registered party aliases for an instance
// (the keys of state.Parties), empty when the instance is unknown.
func aliasSet(instance string) map[string]struct{} {
	set := map[string]struct{}{}
	if instance == "" {
		return set
	}
	state, err := registry.Read(instance)
	if err != nil {
		return set
	}
	for alias := range state.Parties {
		set[alias] = struct{}{}
	}
	return set
}

// dropPhantomAliases removes resolved "parties" that are bare registry
// alias strings rather than real party ids. Pre-alias-resolution commands
// could grant act/read for an unresolved alias (e.g. "issuer"); those
// grants persist and would otherwise show as phantom, holdingless
// duplicate rows alongside the real "issuer::fingerprint" party. A real
// party id always carries a "::"-separated fingerprint, so a
// fingerprint-less string matching a known alias is a phantom; genuine
// fingerprint-less dev parties are kept.
func dropPhantomAliases(parties []string, aliases map[string]struct{}) []string {
	out := make([]string, 0, len(parties))
	for _, p := range parties {
		if !strings.Contains(p, "::") {
			if _, isAlias := aliases[p]; isAlias {
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

// resolveLedgerToken implements the four-step token resolution order
// documented on dialLedger. Returns "" when no token is needed
// (participant runs without auth). Errors pass through redactJWTs so a
// bearer token can never leak via the error path.
func resolveLedgerToken(conn LedgerConn) (string, error) {
	tok, err := resolveLedgerTokenRaw(conn)
	return tok, redactJWTs(err)
}

func resolveLedgerTokenRaw(conn LedgerConn) (string, error) {
	if conn.Token != "" {
		return conn.Token, nil
	}
	if conn.Instance == "" {
		// No instance + no token: legitimate for an unauthenticated
		// participant. The dial proceeds with no Authorization header;
		// the participant 401s if it wanted one.
		return "", nil
	}
	state, err := registry.Read(conn.Instance)
	if err != nil {
		return "", fmt.Errorf("read instance state: %w", err)
	}

	role := conn.Role
	if role == "" {
		role = DefaultRole
	}

	// Path #2: captured credentials from `localnet up`.
	if c, ok := state.Credentials[role]; ok && c.JWT != "" {
		return c.JWT, nil
	}

	// Path #3: sign a fresh token from the project's env files — the
	// "creds not captured / lost" path the V2 alpha hits when onboarding
	// interrupts up's tail steps.
	inputs, err := splice.LoadCredentialInputs(state.ProjectDir)
	if err != nil {
		return "", fmt.Errorf(
			"no captured credentials for role %q and could not load env files (%w); "+
				"run `canton-devkit localnet creds %s --role %s --format raw` "+
				"to confirm token issuance, or pass --token explicitly",
			role, err, conn.Instance, role)
	}
	for _, in := range inputs {
		if string(in.Role) == role {
			tok, signErr := splice.SignToken(in)
			if signErr != nil {
				return "", fmt.Errorf("sign token for role %q: %w", role, signErr)
			}
			return tok, nil
		}
	}
	return "", fmt.Errorf(
		"no captured credentials AND no env entry for role %q on instance %q; "+
			"run `canton-devkit localnet creds %s --role %s --format raw` "+
			"to confirm what's available, or pass --token explicitly",
		role, conn.Instance, conn.Instance, role)
}

// Generation identifies a token-standard interface generation.
type Generation int

const (
	genV1 Generation = 1 // Token Standard V1 (CIP-0056) — splice-api-token-*-v1
	genV2 Generation = 2 // Token Standard V2 (CIP-0112) — splice-api-token-*-v2
)

func (g Generation) String() string {
	switch g {
	case genV1:
		return "v1"
	case genV2:
		return "v2"
	default:
		return ""
	}
}

// Surfaces is the set of token-standard generations whose Holding
// interface package is known to a participant. A participant can carry
// both at once during the V1→V2 transition.
type Surfaces struct {
	HasV1 bool
	HasV2 bool

	// HasEventLog is set when splice-api-token-transfer-events-v2 (the
	// EventLog interface) is known. When present, the activity feed can
	// read the admin's authoritative change history instead of netting
	// HoldingV2 create/archive deltas itself.
	HasEventLog bool
}

// Any reports whether any token-standard holding package is known.
func (s Surfaces) Any() bool { return s.HasV1 || s.HasV2 }

// discoverTokenSurfaces checks which Holding interface packages the
// participant knows — the basis for per-instrument generation routing. A
// generation is available only when its package is present (no implicit
// fallback). ListKnownPackages proves upload, not synchronizer vetting;
// use darops.EnsureVetted when the vetting state itself matters.
func discoverTokenSurfaces(ctx context.Context, client LedgerClient) (Surfaces, error) {
	resp, err := client.ListKnownPackages(ctx)
	if err != nil {
		return Surfaces{}, fmt.Errorf("list known packages: %w", err)
	}
	var s Surfaces
	for _, p := range resp.GetPackageDetails() {
		switch p.GetName() {
		case "splice-api-token-holding-v1":
			s.HasV1 = true
		case "splice-api-token-holding-v2":
			s.HasV2 = true
		case "splice-api-token-transfer-events-v2":
			s.HasEventLog = true
		}
	}
	return s, nil
}

// interfaceFilterEntry builds one interface-view CumulativeFilter.
func interfaceFilterEntry(interfaceID string) *lapiv2.CumulativeFilter {
	pkg, mod, entity := splitInterfaceID(interfaceID)
	return &lapiv2.CumulativeFilter{
		IdentifierFilter: &lapiv2.CumulativeFilter_InterfaceFilter{
			InterfaceFilter: &lapiv2.InterfaceFilter{
				InterfaceId:             &lapiv2.Identifier{PackageId: pkg, ModuleName: mod, EntityName: entity},
				IncludeInterfaceView:    true,
				IncludeCreatedEventBlob: false,
			},
		},
	}
}

// generationInterfaceFilter builds the EventFormat matching the given
// per-generation interface ids. Only vetted generations' filters are
// included, so we never reference an unvetted package. Parties: empty →
// wildcard (FiltersForAnyParty); non-empty → per-party.
func generationInterfaceFilter(surfaces Surfaces, v1ID, v2ID string, parties []string) *lapiv2.EventFormat {
	var cumulative []*lapiv2.CumulativeFilter
	if surfaces.HasV1 {
		cumulative = append(cumulative, interfaceFilterEntry(v1ID))
	}
	if surfaces.HasV2 {
		cumulative = append(cumulative, interfaceFilterEntry(v2ID))
	}
	filter := &lapiv2.Filters{Cumulative: cumulative}
	if len(parties) == 0 {
		return &lapiv2.EventFormat{FiltersForAnyParty: filter, Verbose: true}
	}
	byParty := make(map[string]*lapiv2.Filters, len(parties))
	for _, p := range parties {
		byParty[p] = filter
	}
	return &lapiv2.EventFormat{FiltersByParty: byParty, Verbose: true}
}

// holdingInterfaceFilter builds the EventFormat matching every Holding
// of the vetted generations.
func holdingInterfaceFilter(surfaces Surfaces, parties []string) *lapiv2.EventFormat {
	return generationInterfaceFilter(surfaces, HoldingInterfaceV1, HoldingInterfaceV2, parties)
}

// transferInstructionInterfaceFilter builds the EventFormat matching a
// pending TransferInstruction of either vetted generation, so accept
// routes by the instruction's own generation rather than the
// participant's surfaces.
func transferInstructionInterfaceFilter(surfaces Surfaces, parties []string) *lapiv2.EventFormat {
	return generationInterfaceFilter(surfaces,
		TransferInstructionInterfaceV1, TransferInstructionInterfaceV2, parties)
}

// holdingView is the generation-agnostic form of a Holding InterfaceView
// (V1 or V2) — the fields balance / the Web UI need. Generation records
// which interface the view came from, for per-instrument write routing.
type holdingView struct {
	Generation   Generation
	Owner        string // the holding owner party
	Provider     string // V2 account.provider ("" for V1 / when None)
	AccountID    string // V2 account.id ("" for V1)
	InstrumentID string // instrumentId.id
	Admin        string // instrumentId.admin (the issuer party)
	Amount       string // amount as a Decimal string (we don't round on the wire)
	Locked       bool   // lock present (Some) — held by an active allocation/proposal
}

// extractHoldingViewV2 walks a participant InterfaceView Record for the
// fields balance needs. ok=false when the view is unparseable — the
// caller skips that contract rather than failing the whole ACS scan.
//
// The Daml record shape this targets (per HoldingV2.daml):
//
//	HoldingView { account, instrumentId, amount, lock?, meta }
//	  Account { owner: Optional Party, provider: Optional Party, id: Text }
//	  InstrumentId { admin: Party, id: Text }
func extractHoldingViewV2(view *lapiv2.InterfaceView) (holdingView, bool) {
	if view == nil || view.ViewValue == nil {
		return holdingView{}, false
	}
	fields := view.ViewValue.Fields
	out := holdingView{Generation: genV2}
	for _, f := range fields {
		switch f.Label {
		case "account":
			rec := recordOf(f.Value)
			if rec == nil {
				return out, false
			}
			for _, af := range rec.Fields {
				switch af.Label {
				case "owner":
					out.Owner = optionalPartyOf(af.Value)
				case "provider":
					out.Provider = optionalPartyOf(af.Value)
				case "id":
					out.AccountID = textOf(af.Value)
				}
			}
		case "instrumentId":
			rec := recordOf(f.Value)
			if rec == nil {
				return out, false
			}
			for _, ifld := range rec.Fields {
				switch ifld.Label {
				case "admin":
					out.Admin = partyOf(ifld.Value)
				case "id":
					out.InstrumentID = textOf(ifld.Value)
				}
			}
		case "amount":
			out.Amount = numericOf(f.Value)
		case "lock":
			// Some(lock) means the holding is locked into an active
			// allocation/proposal and can't be spent.
			if o, ok := f.Value.Sum.(*lapiv2.Value_Optional); ok && o.Optional != nil && o.Optional.Value != nil {
				out.Locked = true
			}
		}
	}
	if out.InstrumentID == "" || out.Amount == "" {
		return out, false
	}
	return out, true
}

// extractHoldingViewV1 walks a V1 HoldingView InterfaceView. It differs
// from V2 in one field: owner is a direct `Party`, not nested in an
// Account.
//
//	HoldingView { owner: Party, instrumentId: InstrumentId, amount, lock?, meta }
func extractHoldingViewV1(view *lapiv2.InterfaceView) (holdingView, bool) {
	if view == nil || view.ViewValue == nil {
		return holdingView{}, false
	}
	out := holdingView{Generation: genV1}
	for _, f := range view.ViewValue.Fields {
		switch f.Label {
		case "owner":
			out.Owner = partyOf(f.Value)
		case "instrumentId":
			rec := recordOf(f.Value)
			if rec == nil {
				return out, false
			}
			for _, ifld := range rec.Fields {
				switch ifld.Label {
				case "admin":
					out.Admin = partyOf(ifld.Value)
				case "id":
					out.InstrumentID = textOf(ifld.Value)
				}
			}
		case "amount":
			out.Amount = numericOf(f.Value)
		case "lock":
			if o, ok := f.Value.Sum.(*lapiv2.Value_Optional); ok && o.Optional != nil && o.Optional.Value != nil {
				out.Locked = true
			}
		}
	}
	if out.InstrumentID == "" || out.Amount == "" {
		return out, false
	}
	return out, true
}

// interfaceGeneration classifies an InterfaceView's interface id by its
// Daml module name. Covers both the Holding interfaces (read path) and
// the TransferInstruction interfaces (so a pending instruction is
// classified by the contract it is, not by the vetted surfaces).
func interfaceGeneration(id *lapiv2.Identifier) (Generation, bool) {
	if id == nil {
		return 0, false
	}
	switch {
	case strings.Contains(id.ModuleName, "HoldingV2"),
		strings.Contains(id.ModuleName, "TransferInstructionV2"):
		return genV2, true
	case strings.Contains(id.ModuleName, "HoldingV1"),
		strings.Contains(id.ModuleName, "TransferInstructionV1"):
		return genV1, true
	}
	return 0, false
}

// extractBestHoldingView picks exactly one holding view per contract —
// preferring the richer V2 view, falling back to V1 — so a contract that
// implements both interfaces is counted once (never a double-counted
// balance). Returns the view tagged with the generation it came from.
func extractBestHoldingView(views []*lapiv2.InterfaceView) (holdingView, bool) {
	var v1, unclassified *lapiv2.InterfaceView
	for _, iv := range views {
		gen, ok := interfaceGeneration(iv.GetInterfaceId())
		if !ok {
			if unclassified == nil {
				unclassified = iv
			}
			continue
		}
		if gen == genV2 {
			if hv, ok := extractHoldingViewV2(iv); ok {
				return hv, true
			}
		} else if v1 == nil {
			v1 = iv
		}
	}
	if v1 != nil {
		if hv, ok := extractHoldingViewV1(v1); ok {
			return hv, true
		}
	}
	// Safety net for a view whose interface id we couldn't classify:
	// try the V2 shape, then V1. Real ledgers always set the interface id.
	if unclassified != nil {
		if hv, ok := extractHoldingViewV2(unclassified); ok && hv.Owner != "" {
			return hv, true
		}
		return extractHoldingViewV1(unclassified)
	}
	return holdingView{}, false
}

// --- tiny Value walkers, focused on the fields we need ---

func recordOf(v *lapiv2.Value) *lapiv2.Record {
	if v == nil {
		return nil
	}
	if r, ok := v.Sum.(*lapiv2.Value_Record); ok {
		return r.Record
	}
	return nil
}

func partyOf(v *lapiv2.Value) string {
	if v == nil {
		return ""
	}
	if p, ok := v.Sum.(*lapiv2.Value_Party); ok {
		return p.Party
	}
	return ""
}

func optionalPartyOf(v *lapiv2.Value) string {
	if v == nil {
		return ""
	}
	if o, ok := v.Sum.(*lapiv2.Value_Optional); ok && o.Optional != nil && o.Optional.Value != nil {
		return partyOf(o.Optional.Value)
	}
	return ""
}

func textOf(v *lapiv2.Value) string {
	if v == nil {
		return ""
	}
	if t, ok := v.Sum.(*lapiv2.Value_Text); ok {
		return t.Text
	}
	return ""
}

func numericOf(v *lapiv2.Value) string {
	if v == nil {
		return ""
	}
	if n, ok := v.Sum.(*lapiv2.Value_Numeric); ok {
		return n.Numeric
	}
	return ""
}
