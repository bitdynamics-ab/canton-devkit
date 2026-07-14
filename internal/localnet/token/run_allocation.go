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
// batch. The wallet (authorizer) side:
//
//	RunAllocate           — pick holdings, POST allocation-factory, exercise
//	                        AllocationFactory_Allocate → Allocation (or a
//	                        pending AllocationInstruction) contract id.
//	RunListAllocations    — ACS-query the Allocation interface for a summary
//	                        list (the Allocations panel / `token allocations`).
//	RunAllocationWithdraw — Allocation_Withdraw (committed: only after the
//	                        settlement deadline; the choice enforces it).
//	RunAllocationCancel   — Allocation_Cancel.
//	RunSettle             — SettlementFactory_SettleBatch (executor-driven;
//	                        TODO-flagged, wants a live instance to prove).
//
// The value shapes + exercise seams live in allocation_builders.go and
// exercise_allocation.go; this file is the flow glue (resolve registry +
// ledger, coin-select, emit progress) that mirrors run_transfer.go.

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
	// SettlementDeadline is the optional RFC3339 (or duration) deadline. A
	// committed allocation locks funds until this passes. Empty leaves it
	// unset (a non-committed allocation withdrawable any time).
	SettlementDeadline string
	// Committed marks the allocation committed: funds are locked and cannot
	// be withdrawn before SettlementDeadline (the choice enforces it).
	Committed bool

	// Live-submit envelope, same as TransferOptions.
	Endpoint    string
	Token       string
	Role        string
	Insecure    bool
	RegistryURL string
}

// AllocationActionOptions is the input shape for the per-allocation
// withdraw / cancel / settle verbs — just the target allocation id + the
// live-submit envelope.
type AllocationActionOptions struct {
	Instance     string
	AllocationID string
	// Party is the actor exercising the choice (the authorizer for
	// withdraw, an executor for cancel/settle). Empty falls back to the
	// JWT's first granted party (single-party participant).
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

// RunAllocate performs the full authorizer-side allocation flow: resolve
// registry + ledger, coin-select the authorizer's holdings, POST the
// allocation-factory, and exercise AllocationFactory_Allocate. Returns the
// resulting Allocation (finalized) or AllocationInstruction (pending)
// contract id.
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
	// LocalNet single-operator default: the settlement executor is the
	// authorizer unless the caller named one explicitly (you own every
	// party on LocalNet, so self-executed settlement is the common case).
	if opts.Executor == "" {
		opts.Executor = opts.From
	}

	deadline, err := parseSettlementDeadline(opts.SettlementDeadline)
	if err != nil {
		return "", err
	}

	regBaseURL, regHost, err := resolveRegistryURL(opts.Instance, opts.RegistryURL)
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

	regCli, err := registry.Dial(registry.DialOptions{
		BaseURL:    regBaseURL,
		HostHeader: regHost,
		Token:      registry.StaticToken(resolveRegistryToken(opts.Token, opts.Instance, opts.Role)),
		Version:    "v2",
	})
	if err != nil {
		return "", fmt.Errorf("dial registry: %w", err)
	}

	now := time.Now().UTC()
	settlement := registry.SettlementInfo{
		Executor:       opts.Executor,
		SettlementRef:  registry.Reference{ID: newSettlementID()},
		RequestedAt:    now,
		AllocateBefore: now.Add(24 * time.Hour),
		SettleBefore:   now.Add(48 * time.Hour),
		Meta:           registry.Metadata{Values: map[string]string{}},
	}
	instrumentID := registry.InstrumentID{Admin: admin, ID: ref.InstrumentID}
	spec := registry.AllocationSpecification{
		Admin:      admin,
		Authorizer: registry.NewOwnedAccount(opts.From),
		TransferLegSides: []registry.TransferLegSide{{
			TransferLegID: "leg0",
			TransferLeg: registry.TransferLeg{
				Sender:       registry.NewOwnedAccount(opts.From),
				Receiver:     registry.NewOwnedAccount(opts.To),
				Amount:       opts.Amount,
				InstrumentID: instrumentID,
				Meta:         registry.Metadata{Values: map[string]string{}},
			},
		}},
		SettlementDeadline: deadline,
		Committed:          opts.Committed,
		Meta:               registry.Metadata{Values: map[string]string{}},
	}
	choiceArgs := registry.AllocationFactoryChoiceArgs{
		ExpectedAdmin:    admin,
		Settlement:       settlement,
		Allocation:       spec,
		RequestedAt:      now,
		InputHoldingCids: contractIDsOf(picked),
		Actors:           []string{opts.From},
		ExtraArgs: registry.ExtraArgs{
			Context: registry.Metadata{Values: map[string]string{}},
			Meta:    registry.Metadata{Values: map[string]string{}},
		},
	}
	factoryResp, err := regCli.GetAllocationFactory(ctx, AllocationFactoryPathV2, registry.AllocationFactoryRequest{ChoiceArguments: choiceArgs})
	if err != nil {
		return "", fmt.Errorf("registry allocation-factory: %w", err)
	}
	emit(out, "allocate: factory", map[string]any{
		"factory_id": factoryResp.FactoryID,
		"disclosed":  len(factoryResp.DisclosedContractsList()),
		"committed":  opts.Committed,
	})

	resp, err := exerciseAllocationFactory(ctx, client, opts.From, factoryResp.FactoryID, choiceArgs, factoryResp)
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
	// LedgerClient interface (LedgerEnd + ActiveContracts), so tests inject
	// a fakeLedger through this seam. The write paths keep concrete
	// dialLedger since they need SubmitAndWaitForTransaction.
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
			Status:       types.AllocationStatusReady, // a live Allocation contract is ready-to-settle
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
// settlement deadline has passed — the participant returns the assertion
// failure, which the caller surfaces. Backs the CLI + the withdraw handler.
func RunAllocationWithdraw(ctx context.Context, out io.Writer, opts AllocationActionOptions) error {
	return runAllocationAction(ctx, out, opts, "withdraw", AllocationWithdrawPathV2, "Allocation_Withdraw")
}

// RunAllocationCancel exercises Allocation_Cancel against a finalized
// Allocation. Backs the CLI + the cancel handler.
func RunAllocationCancel(ctx context.Context, out io.Writer, opts AllocationActionOptions) error {
	return runAllocationAction(ctx, out, opts, "cancel", AllocationCancelPathV2, "Allocation_Cancel")
}

// runAllocationAction is the shared withdraw/cancel flow: fetch the
// per-allocation choice context from the registry, then exercise the named
// nonconsuming Allocation choice.
func runAllocationAction(ctx context.Context, out io.Writer, opts AllocationActionOptions, verb, pathTemplate, choice string) error {
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

	regBaseURL, regHost, err := resolveRegistryURL(opts.Instance, opts.RegistryURL)
	if err != nil {
		return err
	}
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

	actor := opts.Party
	if actor == "" {
		actor, err = pickActAsParty(ctx, client)
		if err != nil {
			return fmt.Errorf("resolve %s party: %w", verb, err)
		}
	}

	regCli, err := registry.Dial(registry.DialOptions{
		BaseURL:    regBaseURL,
		HostHeader: regHost,
		Token:      registry.StaticToken(resolveRegistryToken(opts.Token, opts.Instance, opts.Role)),
		Version:    "v2",
	})
	if err != nil {
		return fmt.Errorf("dial registry: %w", err)
	}
	ctxResp, err := regCli.GetAllocationChoiceContext(ctx, pathTemplate, opts.AllocationID,
		registry.ChoiceContextRequest{Meta: map[string]string{}})
	if err != nil {
		return fmt.Errorf("registry %s choice-context: %w", verb, err)
	}
	resp, err := exerciseAllocationChoice(ctx, client, actor, opts.AllocationID, choice, ctxResp)
	if err != nil {
		return fmt.Errorf("exercise %s: %w", choice, err)
	}
	emit(out, verb+": submitted", map[string]any{
		"allocation_id": opts.AllocationID,
		"update_id":     resp.GetTransaction().GetUpdateId(),
	})
	return nil
}

// RunSettle drives the executor-side SettlementFactory_SettleBatch for the
// batch a finalized Allocation belongs to.
//
// TODO(BIT-ALLOC-SETTLE): end-to-end settlement is not yet proven — it
// wants a live V2 instance. There is NO registry choice-contexts/settle
// endpoint (settlement is executor-driven via the settlement-factory
// context), so this fetches that factory context and assembles the
// SettlementFactory_SettleBatch exercise, but the FinalizedAllocation list
// + the batch's transferLegs must be reconstructed from live allocation
// state, which needs a running ledger to validate against. Until then this
// returns a clear not-proven error after wiring the factory lookup, so the
// CLI/UI surface exists and the seam is exercised. The factory fetch is
// real (proves the endpoint wiring); the batch assembly is the TODO.
func RunSettle(ctx context.Context, out io.Writer, opts AllocationActionOptions) error {
	if err := requireFields("settle", opts.Instance, opts.AllocationID); err != nil {
		return err
	}
	if opts.Endpoint == "" {
		emit(out, "settle", map[string]any{"allocation_id": opts.AllocationID})
		return ErrNeedsV2LocalNet
	}
	if opts.Role == "" {
		opts.Role = "app-user"
	}
	opts.Party = ResolveAlias(aliasMapForInstance(opts.Instance), opts.Party)

	regBaseURL, regHost, err := resolveRegistryURL(opts.Instance, opts.RegistryURL)
	if err != nil {
		return err
	}
	regCli, err := registry.Dial(registry.DialOptions{
		BaseURL:    regBaseURL,
		HostHeader: regHost,
		Token:      registry.StaticToken(resolveRegistryToken(opts.Token, opts.Instance, opts.Role)),
		Version:    "v2",
	})
	if err != nil {
		return fmt.Errorf("dial registry: %w", err)
	}
	// Real: prove the settlement-factory endpoint wiring resolves.
	factoryResp, err := regCli.GetSettlementFactory(ctx, SettlementFactoryPathV2)
	if err != nil {
		return fmt.Errorf("registry settlement-factory: %w", err)
	}
	emit(out, "settle: factory", map[string]any{
		"factory_id": factoryResp.FactoryID,
		"disclosed":  len(factoryResp.DisclosedContractsList()),
	})
	// TODO(BIT-ALLOC-SETTLE): assemble + submit exerciseSettleBatch. The
	// finalizedAllocations list + transferLegs the batch settles must be
	// mined from the live Allocation contract(s) for this settlement id;
	// that reconstruction wants a running V2 instance to validate against.
	return fmt.Errorf("settle: SettlementFactory_SettleBatch end-to-end not yet proven " +
		"(no registry settle choice-context endpoint — executor-driven; " +
		"batch assembly needs a live V2 instance). Settlement-factory context " +
		"resolved OK (factory_id above); wiring the FinalizedAllocation batch is the remaining step")
}

// --- helpers -------------------------------------------------------

// newSettlementID returns a fresh settlement-ref id, reusing the
// command-id random source (hex, collision-safe).
func newSettlementID() string {
	id, err := newCommandID()
	if err != nil {
		// newCommandID only fails if crypto/rand fails, which is
		// effectively never; fall back to a static prefix so the flow
		// still submits (the registry only requires uniqueness per batch).
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

// allocationView is the generation-agnostic structured form of an
// Allocation InterfaceView (V2) — just the fields the summary/list surface
// needs.
type allocationView struct {
	SettlementID string
	Authorizer   string
	Admin        string
	LegCount     int
	Committed    bool
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
// The AllocationView record (AllocationV2.daml) nests the settlement +
// spec under an `allocation` field:
//
//	AllocationView { allocation : AllocationSpecification, holdingCids, meta }
//
// where AllocationSpecification carries admin / authorizer(Account) /
// transferLegSides / settlementDeadline / committed. The settlement id
// lives under the nested settlement's ref; some registries surface it
// flat. We read whichever is present.
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
			// Nested AllocationSpecification — recurse.
			if rec := recordOf(f.Value); rec != nil {
				walkAllocationFields(rec.Fields, out)
			}
		case "admin":
			out.Admin = partyOf(f.Value)
		case "authorizer":
			// Account record: owner is the funding party.
			if rec := recordOf(f.Value); rec != nil {
				for _, af := range rec.Fields {
					if af.Label == "owner" {
						out.Authorizer = optionalPartyOf(af.Value)
					}
				}
			} else {
				// Some views surface authorizer as a bare Party.
				if p := partyOf(f.Value); p != "" {
					out.Authorizer = p
				}
			}
		case "transferLegSides":
			out.LegCount = listLen(f.Value)
		case "committed":
			out.Committed = boolOf(f.Value)
		case "settlement":
			// Nested SettlementInfo — pull the settlement ref id.
			if rec := recordOf(f.Value); rec != nil {
				for _, sf := range rec.Fields {
					if sf.Label == "settlementRef" {
						if rref := recordOf(sf.Value); rref != nil {
							for _, rf := range rref.Fields {
								if rf.Label == "id" {
									out.SettlementID = textOf(rf.Value)
								}
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
