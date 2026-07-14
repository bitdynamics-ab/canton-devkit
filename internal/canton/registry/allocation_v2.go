package registry

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// V2 Token Standard allocation / DvP registry endpoints. These implement
// the off-ledger half of the V2 allocation flow defined by upstream
// Splice:
//
//	token-standard/splice-api-token-allocation-instruction-v2/openapi/
//	  allocation-instruction-v2.yaml
//	token-standard/splice-api-token-allocation-v2/openapi/
//	  allocation-v2.yaml
//
// Flow recap, authorizer (wallet) side:
//
//	1. ACS-query for the authorizer's HoldingV2 contracts on the instrument
//	2. POST /registry/allocation-instruction/v2/allocation-factory
//	   → returns (factoryId, choiceContextData, disclosedContracts)
//	3. Exercise AllocationFactory_Allocate on the ledger with the choice
//	   context + disclosed contracts the registry handed back — producing
//	   either a finalized Allocation or a pending AllocationInstruction.
//
// On a pending AllocationInstruction:
//
//	1. POST /registry/allocation-instruction/v2/{id}/choice-contexts/{accept,withdraw}
//	2. Exercise AllocationInstruction_Accept / _Withdraw with that context
//
// On a finalized Allocation (withdraw / cancel — note the *plural*
// `allocations` path segment upstream uses here):
//
//	1. POST /registry/allocations/v2/{id}/choice-contexts/{withdraw,cancel}
//	2. Exercise Allocation_Withdraw / _Cancel with that context
//
// Settlement is executor-driven and has NO registry choice-context
// endpoint: the executor fetches the settlement-factory context via
//	GET /registry/allocation/v2/settlement-factory
// and exercises SettlementFactory_SettleBatch directly.

// The registry-path consts (AllocationFactoryPathV2, SettlementFactoryPathV2,
// AllocationInstructionAcceptPathV2, AllocationInstructionWithdrawPathV2,
// AllocationWithdrawPathV2, AllocationCancelPathV2) live in the token
// package's v2_surface.go — the single source of truth for the exact
// (singular/plural-inconsistent) upstream segments. This client is handed
// the concrete paths so the two never drift.

// SettlementInfo mirrors the standard's `Splice.Api.Token.AllocationV2.
// SettlementInfo` record — the settlement this allocation belongs to. The
// executor + settlement id correlate allocations of the same DvP batch.
type SettlementInfo struct {
	Executor       string    `json:"executor"`
	SettlementRef  Reference `json:"settlementRef"`
	RequestedAt    time.Time `json:"requestedAt"`
	AllocateBefore time.Time `json:"allocateBefore"`
	SettleBefore   time.Time `json:"settleBefore"`
	Meta           Metadata  `json:"meta"`
}

// Reference mirrors the standard's `Reference` record (an id + optional
// contract-id pointer). Only the id text is load-bearing for our flows;
// the optional cid is left null.
type Reference struct {
	ID  string  `json:"id"`
	Cid *string `json:"cid"`
}

// TransferLegSide mirrors the standard's `TransferLegSide` — one directed
// leg of a settlement the allocation authorizes. `transferLegId` correlates
// the sender/receiver halves; `transferLeg` carries the movement.
type TransferLegSide struct {
	TransferLegID string      `json:"transferLegId"`
	TransferLeg   TransferLeg `json:"transferLeg"`
}

// TransferLeg mirrors the standard's `TransferLeg` record: sender/receiver
// Accounts, the amount, and the instrument being moved.
type TransferLeg struct {
	Sender       Account      `json:"sender"`
	Receiver     Account      `json:"receiver"`
	Amount       string       `json:"amount"`
	InstrumentID InstrumentID `json:"instrumentId"`
	Meta         Metadata     `json:"meta"`
}

// AllocationSpecification mirrors the standard's `AllocationSpecification`
// record — the DvP terms this allocation commits to. `authorizer` is the
// account owner funding the transfer legs; `committed` locks the funds
// until `settlementDeadline` (no early withdraw).
type AllocationSpecification struct {
	Admin              string            `json:"admin"`
	Authorizer         Account           `json:"authorizer"`
	TransferLegSides   []TransferLegSide `json:"transferLegSides"`
	SettlementDeadline *time.Time        `json:"settlementDeadline"`
	// NextIterationFunding is the optional per-leg funding for the next
	// iteration of an iterated allocation (TextMap Decimal). Nil for the
	// common one-shot allocation.
	NextIterationFunding map[string]string `json:"nextIterationFunding"`
	Committed            bool              `json:"committed"`
	Meta                 Metadata          `json:"meta"`
}

// AllocationFactoryChoiceArgs is the JSON encoding of the Daml
// AllocationFactory_Allocate choice argument. The registry validates it
// against the real choice signature, so the field set must match the
// upstream `AllocationFactory_Allocate` record exactly:
//
//	{ expectedAdmin, settlement, allocation, requestedAt, inputHoldingCids,
//	  extraArgs, actors }
//
// (`expectedAdmin` is the factory admin the choice validates against —
// analogous to the transfer V1 shape.)
type AllocationFactoryChoiceArgs struct {
	ExpectedAdmin    string                  `json:"expectedAdmin"`
	Settlement       SettlementInfo          `json:"settlement"`
	Allocation       AllocationSpecification `json:"allocation"`
	RequestedAt      time.Time               `json:"requestedAt"`
	InputHoldingCids []string                `json:"inputHoldingCids"`
	Actors           []string                `json:"actors"`
	ExtraArgs        ExtraArgs               `json:"extraArgs"`
}

// AllocationFactoryRequest is the body POST'd to the allocation-factory
// endpoint. Mirrors TransferFactoryRequest: the registry validates
// `choiceArguments` against its own policy before the authorizer ever
// touches the ledger.
type AllocationFactoryRequest struct {
	ChoiceArguments AllocationFactoryChoiceArgs `json:"choiceArguments"`
}

// AllocationFactoryResponse is what the allocation-factory endpoint hands
// back: the factory contract to exercise + the choice context (data blob
// + disclosed contracts). Same nested `choiceContext` shape as the
// transfer factory; the flat accessors below match TransferFactoryResponse.
type AllocationFactoryResponse struct {
	FactoryID     string                `json:"factoryId"`
	ChoiceContext ChoiceContextResponse `json:"choiceContext"`
}

// ChoiceContextData returns the choice-context data blob (flattened accessor).
func (r *AllocationFactoryResponse) ChoiceContextData() map[string]any {
	return r.ChoiceContext.ChoiceContextData
}

// DisclosedContractsList returns the disclosed contracts (flattened accessor).
func (r *AllocationFactoryResponse) DisclosedContractsList() []DisclosedContract {
	return r.ChoiceContext.DisclosedContracts
}

// SettlementFactoryResponse is what the settlement-factory endpoint hands
// back: the SettlementFactory contract the executor exercises
// SettlementFactory_SettleBatch on, plus the choice context. Same shape as
// AllocationFactoryResponse (factory id + nested choiceContext).
type SettlementFactoryResponse struct {
	FactoryID     string                `json:"factoryId"`
	ChoiceContext ChoiceContextResponse `json:"choiceContext"`
}

// ChoiceContextData returns the choice-context data blob (flattened accessor).
func (r *SettlementFactoryResponse) ChoiceContextData() map[string]any {
	return r.ChoiceContext.ChoiceContextData
}

// DisclosedContractsList returns the disclosed contracts (flattened accessor).
func (r *SettlementFactoryResponse) DisclosedContractsList() []DisclosedContract {
	return r.ChoiceContext.DisclosedContracts
}

// GetAllocationFactory posts the authorizer's intended allocation to the
// registry and returns the factory contract + choice context the
// authorizer will exercise (AllocationFactory_Allocate). The path is the
// caller-supplied allocation-factory path const (v2_surface.go) so the
// singular/plural upstream inconsistency stays pinned in one place.
//
// A 4xx here means the allocation would have been rejected on-ledger
// anyway (insufficient funds, admin mismatch); it round-trips via APIError
// so the CLI / UI can print the registry's actual reason.
func (c *Client) GetAllocationFactory(ctx context.Context, path string, req AllocationFactoryRequest) (*AllocationFactoryResponse, error) {
	var out AllocationFactoryResponse
	if err := c.doJSON(ctx, "POST", path, req, &out); err != nil {
		return nil, fmt.Errorf("GetAllocationFactory: %w", err)
	}
	return &out, nil
}

// GetSettlementFactory fetches the SettlementFactory contract + choice
// context the executor exercises SettlementFactory_SettleBatch on. This is
// a GET with no per-instruction id (the executor settles a batch, not one
// allocation). Per the OpenAPI it takes an optional `meta` map on the
// query; we send none.
func (c *Client) GetSettlementFactory(ctx context.Context, path string) (*SettlementFactoryResponse, error) {
	var out SettlementFactoryResponse
	if err := c.doJSON(ctx, "GET", path, nil, &out); err != nil {
		return nil, fmt.Errorf("GetSettlementFactory: %w", err)
	}
	return &out, nil
}

// GetAllocationChoiceContext fetches the choice context for a per-allocation
// (or per-instruction) choice — withdraw / cancel / accept. `pathTemplate`
// is the caller-supplied registry path const carrying a single
// `{allocationId}` / `{allocationInstructionId}` placeholder, which this
// substitutes with the url-escaped id (so a '#' / '?' in the id can't
// truncate the path). Mirrors GetAcceptChoiceContext on the transfer side.
func (c *Client) GetAllocationChoiceContext(ctx context.Context, pathTemplate, id string, req ChoiceContextRequest) (*ChoiceContextResponse, error) {
	if id == "" {
		return nil, fmt.Errorf("GetAllocationChoiceContext: id is required")
	}
	var out ChoiceContextResponse
	if err := c.doJSON(ctx, "POST", substituteID(pathTemplate, id), req, &out); err != nil {
		return nil, fmt.Errorf("GetAllocationChoiceContext: %w", err)
	}
	return &out, nil
}

// substituteID replaces the single `{...Id}` placeholder in a registry
// path template with the url-escaped id. The upstream templates use
// `{allocationId}` and `{allocationInstructionId}`; we replace whichever
// `{...}` segment is present rather than matching the exact name, so a spec
// rename doesn't silently leave the placeholder in the path.
func substituteID(pathTemplate, id string) string {
	open := strings.IndexByte(pathTemplate, '{')
	if open < 0 {
		return pathTemplate
	}
	close := strings.IndexByte(pathTemplate[open:], '}')
	if close < 0 {
		return pathTemplate
	}
	close += open
	return pathTemplate[:open] + url.PathEscape(id) + pathTemplate[close+1:]
}
