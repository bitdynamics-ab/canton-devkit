package token

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/registry"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// V2 allocation / DvP orchestration. An allocation is an authorizer's
// ready-to-settle approval of one or more transfer legs of a settlement
// batch. This file is the flow glue (resolve registry + ledger, coin-select,
// emit progress); value shapes + exercise seams live in
// allocation_builders.go and exercise_allocation.go.

// AllocationOptions is the input shape for RunAllocate — shared by the CLI
// `token allocate` flags and the POST /api/tokens/{symbol}/allocate handler
// so the two surfaces cannot drift.
type AllocationOptions struct {
	Instance   string
	Instrument string // symbol OR raw instrument id
	From       string // authorizer / sender account owner funding the leg
	To         string // receiver account owner of the transfer leg
	Amount     string // decimal string
	Executor   string // settlement executor party; empty defaults to From (LocalNet: you own everyone)
	// SettlementDeadline is the optional RFC3339 (or duration) deadline.
	// Committed allocations lock funds until it passes; empty leaves it unset.
	SettlementDeadline string
	// Committed locks the funds until SettlementDeadline (the choice enforces
	// the no-early-withdraw invariant).
	Committed bool

	// Live-submit envelope, same as TransferOptions.
	Endpoint    string
	Token       string
	Role        string
	Insecure    bool
	RegistryURL string
}

// AllocationActionOptions is the input shape for the per-allocation
// withdraw / cancel / settle verbs — the target allocation id + the
// live-submit envelope.
type AllocationActionOptions struct {
	Instance     string
	AllocationID string
	// Party is the actor exercising the choice (authorizer for withdraw,
	// executor for cancel/settle). Empty falls back to the JWT's first
	// granted party.
	Party string

	Endpoint    string
	Token       string
	Role        string
	Insecure    bool
	RegistryURL string
}

// ListAllocationsOptions is the input for RunListAllocations — an ACS scan
// of the Allocation interface, optionally filtered to one party.
type ListAllocationsOptions struct {
	Instance string
	Party    string // empty → all visible allocations

	Endpoint string
	Token    string
	Role     string
	Insecure bool
}

// RunAllocate performs the full authorizer-side allocation flow: coin-select
// the authorizer's holdings, POST the allocation-factory, and exercise
// AllocationFactory_Allocate. Returns the resulting Allocation (finalized) or
// AllocationInstruction (pending) contract id.
func RunAllocate(ctx context.Context, out io.Writer, opts AllocationOptions) (string, error) {
	if err := requireFields("allocate", opts.Instance, opts.Instrument, opts.From, opts.To, opts.Amount); err != nil {
		return "", err
	}
	aliases := aliasMapForInstance(opts.Instance)
	opts.From = ResolveAlias(aliases, opts.From)
	opts.To = ResolveAlias(aliases, opts.To)
	opts.Executor = ResolveAlias(aliases, opts.Executor)
	if err := validatePartyID("--from", opts.From); err != nil {
		return "", err
	}
	if err := validatePartyID("--to", opts.To); err != nil {
		return "", err
	}
	if err := validateAmount("allocate", opts.Amount); err != nil {
		return "", err
	}
	if opts.Endpoint == "" {
		emit(out, "allocate", map[string]any{
			"from": opts.From, "to": opts.To, "amount": opts.Amount,
		})
		return "", ErrNeedsV2LocalNet
	}
	if opts.Role == "" {
		opts.Role = "app-user"
	}
	// LocalNet single-operator default: self-execute settlement unless the
	// caller named an executor.
	if opts.Executor == "" {
		opts.Executor = opts.From
	}

	deadline, err := parseSettlementDeadline(opts.SettlementDeadline)
	if err != nil {
		return "", err
	}

	ref := instrumentRefOrRaw(opts.Instance, opts.Instrument)

	conn := LedgerConn{
		Endpoint: opts.Endpoint,
		Token:    opts.Token,
		Insecure: opts.Insecure,
		Instance: opts.Instance,
		Role:     opts.Role,
	}
	client, cleanup, err := dialLedger(ctx, conn)
	if err != nil {
		return "", err
	}
	defer cleanup()

	holdings, err := listSenderHoldings(ctx, client, opts.From, ref.InstrumentID)
	if err != nil {
		return "", fmt.Errorf("list authorizer holdings: %w", err)
	}
	picked, total, err := selectInputHoldings(holdings, opts.Amount)
	if err != nil {
		return "", err
	}
	emit(out, "allocate: selected", map[string]any{
		"input_count": len(picked), "total_input": total, "amount": opts.Amount,
	})

	admin := ref.IssuerParty
	if admin == "" {
		admin = inferAdminFromHoldings(holdings)
	}
	if admin == "" {
		return "", fmt.Errorf("could not determine instrument admin party — pass an instrument with a recorded issuer or rely on at least one holding being present for the authorizer")
	}

	// Build the authorizer Account from the picked holdings — the DAML
	// AllocationFactory_Allocate impl asserts inputHolding.account ==
	// allocation.authorizer, so use the holdings' own account (owner /
	// provider / id) rather than assuming a bare owned account.
	authorizer := authorizerAccountFromHoldings(picked, opts.From)

	now := time.Now().UTC()
	settlement := registry.SettlementInfo{
		Executors: []string{opts.Executor},
		ID:        newSettlementID(),
		Meta:      registry.Metadata{Values: map[string]string{}},
	}
	spec := registry.AllocationSpecification{
		Admin:      admin,
		Authorizer: authorizer,
		TransferLegSides: []registry.TransferLegSide{{
			TransferLegID: "leg0",
			Side:          "SenderSide",
			Otherside:     registry.NewOwnedAccount(opts.To),
			Amount:        opts.Amount,
			InstrumentID:  ref.InstrumentID,
			Meta:          registry.Metadata{Values: map[string]string{}},
		}},
		SettlementDeadline: deadline,
		Committed:          opts.Committed,
		Meta:               registry.Metadata{Values: map[string]string{}},
	}
	choiceArgs := registry.AllocationFactoryChoiceArgs{
		Settlement:       settlement,
		Allocation:       spec,
		RequestedAt:      now,
		InputHoldingCids: contractIDsOf(picked),
		Actors:           accountPartiesOf(admin, opts.From, authorizerProvider(picked)),
		ExtraArgs: registry.ExtraArgs{
			Context: registry.Metadata{Values: map[string]string{}},
			Meta:    registry.Metadata{Values: map[string]string{}},
		},
	}

	// The issuer's on-ledger TokenRules contract IS the V2 AllocationFactory
	// (interface instance V2.AllocationFactory for TokenRules); its admin ==
	// the instrument admin, so it satisfies the impl's allocation.admin ==
	// tokenRules.admin assertion. The scan registry's default factory (admin
	// = DSO) does not, so it rejects issuer-created instruments with
	// "Instrument-id must match the factory". Exercise the TokenRules
	// directly, mirroring the on-ledger transfer path.
	tokenRulesCID, err := findTokenRules(ctx, client, admin)
	if err != nil {
		return "", fmt.Errorf("look up TokenRules: %w", err)
	}
	if tokenRulesCID == "" {
		return "", fmt.Errorf("no on-ledger TokenRules for issuer %s — "+
			"run `localnet token create --endpoint ...` first", admin)
	}

	// Provider-scoped authorizer accounts need an AccountConfig in the
	// choice context; self-custodial (provider=None) accounts use the basic
	// config the choice synthesizes, so none is supplied.
	var accountConfigCIDs []string
	if p := authorizerProvider(picked); p != "" {
		cid, cerr := findOrCreateAccountConfig(ctx, client, admin, opts.From, p)
		if cerr != nil {
			return "", fmt.Errorf("ensure authorizer account config: %w", cerr)
		}
		accountConfigCIDs = []string{cid}
	}

	// Act as the authorizer's account parties plus the admin: the locked
	// holdings + allocation the choice creates are admin-co-signed, and on
	// LocalNet we host the admin.
	authorizerParties := accountPartiesOf(admin, opts.From, authorizerProvider(picked))
	factoryActAs := dedupParties(append(append([]string{}, authorizerParties...), admin))

	emit(out, "allocate: factory", map[string]any{
		"factory_id": tokenRulesCID,
		"admin":      admin,
		"committed":  opts.Committed,
	})

	resp, err := exerciseAllocationFactoryOnLedger(ctx, client, factoryActAs, tokenRulesCID, accountConfigCIDs, choiceArgs)
	if err != nil {
		return "", fmt.Errorf("exercise AllocationFactory_Allocate: %w", err)
	}
	id, finalized, ok := findCreatedAllocationID(resp)
	if !ok {
		return "", fmt.Errorf("AllocationFactory_Allocate produced no Allocation or AllocationInstruction contract")
	}
	emit(out, "allocate: submitted", map[string]any{
		"allocation_id": id,
		"finalized":     finalized,
		"update_id":     resp.GetTransaction().GetUpdateId(),
	})
	return id, nil
}

// RunListAllocations ACS-queries the Allocation interface and returns a
// summary list. Filters to opts.Party when set. Backs both the CLI
// `token allocations` verb and the GET /api/tokens/allocations handler.
func RunListAllocations(ctx context.Context, opts ListAllocationsOptions) ([]types.AllocationSummary, error) {
	if opts.Endpoint == "" {
		return nil, ErrNeedsV2LocalNet
	}
	if opts.Role == "" {
		opts.Role = "app-user"
	}
	party := ResolveAlias(aliasMapForInstance(opts.Instance), opts.Party)

	conn := LedgerConn{
		Endpoint: opts.Endpoint,
		Token:    opts.Token,
		Insecure: opts.Insecure,
		Instance: opts.Instance,
		Role:     opts.Role,
	}
	// dialLedgerFn (not dialLedger): the list path uses only the narrow
	// LedgerClient interface, so tests inject a fakeLedger through this seam.
	client, cleanup, err := dialLedgerFn(ctx, conn)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	end, err := client.LedgerEnd(ctx)
	if err != nil {
		return nil, fmt.Errorf("ledger end: %w", err)
	}
	var parties []string
	if party != "" {
		parties = []string{party}
	}
	stream, err := client.ActiveContracts(ctx, ledger.ActiveContractsRequest{
		ActiveAtOffset: end.Offset,
		EventFormat:    allocationInterfaceFilter(parties),
	})
	if err != nil {
		return nil, fmt.Errorf("ACS query: %w", err)
	}
	out := make([]types.AllocationSummary, 0)
	for item := range stream {
		if item.Err != nil {
			return nil, fmt.Errorf("ACS stream: %w", item.Err)
		}
		entry, ok := item.Value.ContractEntry.(*lapiv2.GetActiveContractsResponse_ActiveContract)
		if !ok {
			continue
		}
		created := entry.ActiveContract.GetCreatedEvent()
		if created == nil {
			continue
		}
		av, ok := extractAllocationView(created.GetInterfaceViews())
		if !ok {
			continue
		}
		if party != "" && av.Authorizer != party {
			continue
		}
		out = append(out, types.AllocationSummary{
			ContractID:   created.GetContractId(),
			Status:       types.AllocationStatusReady, // a live Allocation is ready-to-settle
			SettlementID: av.SettlementID,
			Authorizer:   av.Authorizer,
			LegCount:     av.LegCount,
			Committed:    av.Committed,
		})
	}
	return out, nil
}

// RunAllocationWithdraw exercises Allocation_Withdraw against a finalized
// Allocation. For a committed allocation the choice refuses until the
// settlement deadline passes; the participant's assertion failure is
// surfaced to the caller.
func RunAllocationWithdraw(ctx context.Context, out io.Writer, opts AllocationActionOptions) error {
	return runAllocationAction(ctx, out, opts, "withdraw", "Allocation_Withdraw")
}

// RunAllocationCancel exercises Allocation_Cancel against a finalized
// Allocation.
func RunAllocationCancel(ctx context.Context, out io.Writer, opts AllocationActionOptions) error {
	return runAllocationAction(ctx, out, opts, "cancel", "Allocation_Cancel")
}

// runAllocationAction is the shared withdraw/cancel flow. Like the allocate
// factory path, both choices are exercised against the issuer's own
// on-ledger allocation state (the TestToken impl reads the local test-token
// choice context — TokenRules event-log + authorizer AccountConfig cids —
// from extraArgs, not the scan registry's opaque blob). It fetches the
// target Allocation's view to resolve the admin, the authorizer account, and
// the settlement executors, then exercises the named choice with the
// controller `actors` the state machine requires:
//   - withdraw → the authorizer's account parties (AccountConfig.daml
//     STAS_Account authorizer)
//   - cancel   → the settlement executors (STAS_Parties executors)
func runAllocationAction(ctx context.Context, out io.Writer, opts AllocationActionOptions, verb, choice string) error {
	if err := requireFields(verb, opts.Instance, opts.AllocationID); err != nil {
		return err
	}
	if opts.Endpoint == "" {
		emit(out, verb, map[string]any{"allocation_id": opts.AllocationID})
		return ErrNeedsV2LocalNet
	}
	if opts.Role == "" {
		opts.Role = "app-user"
	}
	opts.Party = ResolveAlias(aliasMapForInstance(opts.Instance), opts.Party)

	conn := LedgerConn{
		Endpoint: opts.Endpoint,
		Token:    opts.Token,
		Insecure: opts.Insecure,
		Instance: opts.Instance,
		Role:     opts.Role,
	}
	client, cleanup, err := dialLedger(ctx, conn)
	if err != nil {
		return err
	}
	defer cleanup()

	av, err := findAllocationView(ctx, client, opts.AllocationID)
	if err != nil {
		return fmt.Errorf("look up allocation %s: %w", opts.AllocationID, err)
	}
	admin := av.Admin
	if admin == "" {
		return fmt.Errorf("allocation %s view carries no admin — cannot resolve TokenRules", opts.AllocationID)
	}

	// The issuer's on-ledger TokenRules IS the allocation's event log +
	// AccountConfig context source (interface instance for TokenRules); the
	// choice's getEventLogFromContext / extractAccountConfigMap read it.
	tokenRulesCID, err := findTokenRules(ctx, client, admin)
	if err != nil {
		return fmt.Errorf("look up TokenRules: %w", err)
	}
	if tokenRulesCID == "" {
		return fmt.Errorf("no on-ledger TokenRules for issuer %s — "+
			"run `localnet token create --endpoint ...` first", admin)
	}

	// Provider-scoped authorizer accounts need an AccountConfig in the
	// context; self-custodial (provider="") accounts use the synthesized
	// basic config, so none is supplied.
	var accountConfigCIDs []string
	if av.AuthorizerProvider != "" {
		cid, cerr := findOrCreateAccountConfig(ctx, client, admin, av.Authorizer, av.AuthorizerProvider)
		if cerr != nil {
			return fmt.Errorf("ensure authorizer account config: %w", cerr)
		}
		accountConfigCIDs = []string{cid}
	}

	// Controller `actors` per the allocation state machine
	// (AccountConfig.daml:254-255).
	var actors []string
	switch choice {
	case "Allocation_Cancel":
		actors = av.Executors
	default: // Allocation_Withdraw
		actors = accountPartiesOf(admin, av.Authorizer, av.AuthorizerProvider)
	}
	if len(actors) == 0 {
		return fmt.Errorf("%s: allocation %s view carries no %s actors", verb, opts.AllocationID, verb)
	}

	// Act as the controller actors plus the admin: the unlocked holdings the
	// choice recreates are admin-co-signed, and on LocalNet we host the admin.
	actAs := dedupParties(append(append([]string{}, actors...), admin))

	emit(out, verb+": exercising", map[string]any{
		"allocation_id": opts.AllocationID,
		"admin":         admin,
		"actors":        actors,
	})

	resp, err := exerciseAllocationChoice(ctx, client, actAs, opts.AllocationID, choice, actors, tokenRulesCID, accountConfigCIDs)
	if err != nil {
		return fmt.Errorf("exercise %s: %w", choice, err)
	}
	emit(out, verb+": submitted", map[string]any{
		"allocation_id": opts.AllocationID,
		"update_id":     resp.GetTransaction().GetUpdateId(),
	})
	return nil
}

// Settlement (executor-side SettlementFactory_SettleBatch) is intentionally
// not implemented / not exposed: end-to-end settlement is not yet proven on
// LocalNet. There is no registry settle choice-context endpoint (settlement
// is executor-driven via the settlement-factory context), and the
// FinalizedAllocation batch + transferLegs must be reconstructed from live
// allocation state, which needs a running V2 instance to validate against.
// The CLI `token settle` verb and the settle HTTP route/UI action were
// removed until this is functional — see docs/changes-from-proposal.md.

// --- helpers -------------------------------------------------------

// findAllocationView ACS-scans the Allocation interface and returns the view
// of the contract with the given id — the admin / authorizer account /
// executors the withdraw + cancel exercises need. Errors if the allocation
// is not visible (already settled/withdrawn, or wrong participant).
func findAllocationView(ctx context.Context, client *ledger.Client, allocationID string) (allocationView, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	end, err := client.LedgerEnd(ctx)
	if err != nil {
		return allocationView{}, fmt.Errorf("ledger end: %w", err)
	}
	stream, err := client.ActiveContracts(ctx, ledger.ActiveContractsRequest{
		ActiveAtOffset: end.Offset,
		EventFormat:    allocationInterfaceFilter(nil),
	})
	if err != nil {
		return allocationView{}, fmt.Errorf("ACS query: %w", err)
	}
	for item := range stream {
		if item.Err != nil {
			return allocationView{}, fmt.Errorf("ACS stream: %w", item.Err)
		}
		entry, ok := item.Value.ContractEntry.(*lapiv2.GetActiveContractsResponse_ActiveContract)
		if !ok {
			continue
		}
		created := entry.ActiveContract.GetCreatedEvent()
		if created == nil || created.GetContractId() != allocationID {
			continue
		}
		av, ok := extractAllocationView(created.GetInterfaceViews())
		if !ok {
			return allocationView{}, fmt.Errorf("allocation %s has no parseable Allocation view", allocationID)
		}
		return av, nil
	}
	return allocationView{}, fmt.Errorf("allocation %s not found in active contracts (already settled/withdrawn, or not visible to this party)", allocationID)
}

// authorizerAccountFromHoldings builds the V2 Account the allocation
// authorizes from the picked input holdings. The DAML impl asserts every
// inputHolding.account == allocation.authorizer, so the authorizer must be
// the holdings' own account (owner / provider / id), not a synthesized bare
// owned account. Falls back to a bare owned account for `from` when the
// holdings carry no account coordinates.
func authorizerAccountFromHoldings(picked []holdingRef, from string) registry.Account {
	if len(picked) == 0 {
		return registry.NewOwnedAccount(from)
	}
	h := picked[0]
	owner := h.Owner
	if owner == "" {
		owner = from
	}
	acct := registry.Account{Owner: &owner, ID: h.AccountID}
	if h.Provider != "" {
		p := h.Provider
		acct.Provider = &p
	}
	return acct
}

// authorizerProvider returns the account provider carried on the picked
// holdings ("" when self-custodial). All inputs share one account, so the
// first holding is authoritative.
func authorizerProvider(picked []holdingRef) string {
	if len(picked) == 0 {
		return ""
	}
	return picked[0].Provider
}

// newSettlementID returns a fresh settlement-ref id, reusing the command-id
// random source (hex, collision-safe).
func newSettlementID() string {
	id, err := newCommandID()
	if err != nil {
		// crypto/rand failure (effectively never); the registry only
		// requires uniqueness per batch, so a static fallback still submits.
		return "settlement-fallback"
	}
	return "settlement-" + id
}

// parseSettlementDeadline turns the user's --settlement-deadline string
// into an Optional Time. Accepts an RFC3339 timestamp or a Go duration
// (e.g. "1h", "30m") interpreted as "now + d". Empty → None.
func parseSettlementDeadline(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		u := t.UTC()
		return &u, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		t := time.Now().UTC().Add(d)
		return &t, nil
	}
	return nil, fmt.Errorf("--settlement-deadline %q is neither an RFC3339 timestamp nor a duration (e.g. 1h, 30m)", s)
}

// allocationView is the structured form of an Allocation InterfaceView (V2)
// — just the fields the summary/list surface needs.
type allocationView struct {
	SettlementID string
	Authorizer   string
	Admin        string
	LegCount     int
	Committed    bool

	// AuthorizerProvider / AuthorizerID are the remaining coordinates of the
	// authorizer Account (owner is Authorizer). Empty provider → self-custodial.
	// Executors are the settlement executors. These back the withdraw/cancel
	// exercises (actors + account-config resolution) but are not surfaced in
	// the summary list.
	AuthorizerProvider string
	AuthorizerID       string
	Executors          []string
}

// allocationInterfaceFilter builds the EventFormat matching every V2
// Allocation. Parties empty → wildcard (FiltersForAnyParty).
func allocationInterfaceFilter(parties []string) *lapiv2.EventFormat {
	filter := &lapiv2.Filters{Cumulative: []*lapiv2.CumulativeFilter{
		interfaceFilterEntry(AllocationInterfaceV2),
	}}
	if len(parties) == 0 {
		return &lapiv2.EventFormat{FiltersForAnyParty: filter, Verbose: true}
	}
	byParty := make(map[string]*lapiv2.Filters, len(parties))
	for _, p := range parties {
		byParty[p] = filter
	}
	return &lapiv2.EventFormat{FiltersByParty: byParty, Verbose: true}
}

// extractAllocationView walks an Allocation InterfaceView and pulls the
// summary fields. Best-effort: an unparseable view is skipped (ok=false)
// rather than failing the whole scan.
//
// The AllocationView record (AllocationV2.daml) nests the spec under an
// `allocation` field. The settlement id lives under the nested settlement's
// ref; some registries surface it flat — we read whichever is present.
func extractAllocationView(views []*lapiv2.InterfaceView) (allocationView, bool) {
	for _, iv := range views {
		id := iv.GetInterfaceId()
		if id == nil || id.GetEntityName() != "Allocation" {
			continue
		}
		if iv.ViewValue == nil {
			continue
		}
		out := allocationView{}
		walkAllocationFields(iv.ViewValue.Fields, &out)
		if out.Authorizer == "" && out.SettlementID == "" && out.LegCount == 0 {
			continue
		}
		return out, true
	}
	return allocationView{}, false
}

// walkAllocationFields fills an allocationView from a (possibly nested)
// record's fields. Tolerant of both a flat view and an `allocation`-wrapped
// spec (recurses one level).
func walkAllocationFields(fields []*lapiv2.RecordField, out *allocationView) {
	for _, f := range fields {
		switch f.Label {
		case "allocation":
			if rec := recordOf(f.Value); rec != nil {
				walkAllocationFields(rec.Fields, out)
			}
		case "admin":
			out.Admin = partyOf(f.Value)
		case "authorizer":
			// Account record (owner = funding party), or a bare Party in
			// some views.
			if rec := recordOf(f.Value); rec != nil {
				for _, af := range rec.Fields {
					switch af.Label {
					case "owner":
						out.Authorizer = optionalPartyOf(af.Value)
					case "provider":
						out.AuthorizerProvider = optionalPartyOf(af.Value)
					case "id":
						out.AuthorizerID = textOf(af.Value)
					}
				}
			} else {
				if p := partyOf(f.Value); p != "" {
					out.Authorizer = p
				}
			}
		case "transferLegSides":
			out.LegCount = listLen(f.Value)
		case "committed":
			out.Committed = boolOf(f.Value)
		case "settlement":
			// Nested SettlementInfo — pull the settlement ref id + executors.
			if rec := recordOf(f.Value); rec != nil {
				for _, sf := range rec.Fields {
					switch sf.Label {
					case "settlementRef":
						if rref := recordOf(sf.Value); rref != nil {
							for _, rf := range rref.Fields {
								if rf.Label == "id" {
									out.SettlementID = textOf(rf.Value)
								}
							}
						}
					case "executors":
						for _, e := range listElems(sf.Value) {
							if p := partyOf(e); p != "" {
								out.Executors = append(out.Executors, p)
							}
						}
					}
				}
			}
		case "settlementId":
			out.SettlementID = textOf(f.Value)
		}
	}
}

// boolOf reads a Daml Bool Value (false for non-bools).
func boolOf(v *lapiv2.Value) bool {
	if v == nil {
		return false
	}
	if b, ok := v.Sum.(*lapiv2.Value_Bool); ok {
		return b.Bool
	}
	return false
}
