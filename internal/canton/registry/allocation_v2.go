package registry

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// V2 Token Standard allocation / DvP registry endpoints — the off-ledger
// half of the V2 allocation flow defined by upstream Splice
// (splice-api-token-allocation-v2 and -allocation-instruction-v2). The
// authorizer POSTs the allocation-factory, then exercises
// AllocationFactory_Allocate on the ledger with the returned choice context,
// producing a finalized Allocation or a pending AllocationInstruction.
//
// Note the upstream path inconsistency: per-instruction choice-contexts use
// the singular `allocation-instruction`, but finalized-Allocation withdraw /
// cancel use the *plural* `allocations` segment.
//
// Settlement is executor-driven and has NO registry choice-context endpoint:
// the executor GETs the settlement-factory context and exercises
// SettlementFactory_SettleBatch directly.
//
// The concrete registry-path consts live in the token package's
// v2_surface.go (single source of truth for the exact segments); this client
// is handed them so the two never drift.

// SettlementInfo mirrors Splice.Api.Token.AllocationV2.SettlementInfo. The
// executor + settlement id correlate allocations of the same DvP batch.
type SettlementInfo struct {
	Executors []string `json:"executors"`
	ID        string   `json:"id"`
	Cid       *string  `json:"cid"`
	Meta      Metadata `json:"meta"`
}

// Reference mirrors the standard's `Reference` record (an id + optional
// contract-id pointer). Only the id text is load-bearing; the cid is left null.
type Reference struct {
	ID  string  `json:"id"`
	Cid *string `json:"cid"`
}

// TransferLegSide mirrors the standard's `TransferLegSide` — one directed
// leg the allocation authorizes. `transferLegId` correlates the
// sender/receiver halves.
type TransferLegSide struct {
	TransferLegID string   `json:"transferLegId"`
	Side          string   `json:"side"` // "SenderSide" | "ReceiverSide"
	Otherside     Account  `json:"otherside"`
	Amount        string   `json:"amount"`
	InstrumentID  string   `json:"instrumentId"` // Text id, not the {admin,id} record
	Meta          Metadata `json:"meta"`
}

// TransferLeg mirrors the standard's `TransferLeg` record.
type TransferLeg struct {
	Sender       Account      `json:"sender"`
	Receiver     Account      `json:"receiver"`
	Amount       string       `json:"amount"`
	InstrumentID InstrumentID `json:"instrumentId"`
	Meta         Metadata     `json:"meta"`
}

// AllocationSpecification mirrors the standard's `AllocationSpecification`
// — the DvP terms. `authorizer` is the account owner funding the legs;
// `committed` locks the funds until `settlementDeadline` (no early withdraw).
type AllocationSpecification struct {
	Admin              string            `json:"admin"`
	Authorizer         Account           `json:"authorizer"`
	TransferLegSides   []TransferLegSide `json:"transferLegSides"`
	SettlementDeadline *time.Time        `json:"settlementDeadline"`
	// NextIterationFunding is the optional per-leg funding for the next
	// iteration of an iterated allocation (TextMap Decimal). Nil for the
	// common one-shot case.
	NextIterationFunding map[string]string `json:"nextIterationFunding"`
	Committed            bool              `json:"committed"`
	Meta                 Metadata          `json:"meta"`
}

// AllocationFactoryChoiceArgs is the JSON encoding of the Daml
// AllocationFactory_Allocate choice argument. The registry validates it
// against the real choice signature, so the field set must match the
// upstream record exactly. The instrument admin travels inside `allocation`
// (AllocationSpecification.admin), not as a top-level arg — unlike the
// transfer V1 factory's expectedAdmin.
type AllocationFactoryChoiceArgs struct {
	Settlement       SettlementInfo          `json:"settlement"`
	Allocation       AllocationSpecification `json:"allocation"`
	RequestedAt      time.Time               `json:"requestedAt"`
	InputHoldingCids []string                `json:"inputHoldingCids"`
	Actors           []string                `json:"actors"`
	ExtraArgs        ExtraArgs               `json:"extraArgs"`
}

// AllocationFactoryRequest is the body POST'd to the allocation-factory
// endpoint. The registry validates `choiceArguments` against its own policy
// before the authorizer ever touches the ledger.
type AllocationFactoryRequest struct {
	ChoiceArguments AllocationFactoryChoiceArgs `json:"choiceArguments"`
}

// AllocationFactoryResponse is what the allocation-factory endpoint hands
// back: the factory contract to exercise + the choice context (data blob +
// disclosed contracts).
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
// SettlementFactory_SettleBatch on, plus the choice context.
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

// GetAllocationFactory posts the authorizer's intended allocation and
// returns the factory contract + choice context to exercise
// (AllocationFactory_Allocate). A 4xx here means the allocation would have
// been rejected on-ledger anyway (insufficient funds, admin mismatch); it
// round-trips via APIError so the CLI / UI can print the registry's reason.
func (c *Client) GetAllocationFactory(ctx context.Context, path string, req AllocationFactoryRequest) (*AllocationFactoryResponse, error) {
	var out AllocationFactoryResponse
	if err := c.doJSON(ctx, "POST", path, req, &out); err != nil {
		return nil, fmt.Errorf("GetAllocationFactory: %w", err)
	}
	return &out, nil
}

// GetSettlementFactory fetches the SettlementFactory contract + choice
// context the executor exercises SettlementFactory_SettleBatch on. GET with
// no per-instruction id: the executor settles a batch, not one allocation.
func (c *Client) GetSettlementFactory(ctx context.Context, path string) (*SettlementFactoryResponse, error) {
	var out SettlementFactoryResponse
	if err := c.doJSON(ctx, "GET", path, nil, &out); err != nil {
		return nil, fmt.Errorf("GetSettlementFactory: %w", err)
	}
	return &out, nil
}

// GetAllocationChoiceContext fetches the choice context for a per-allocation
// (or per-instruction) choice — withdraw / cancel / accept. The id is
// substituted into pathTemplate url-escaped, so a '#' / '?' in the id can't
// truncate the path.
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

// substituteID replaces the single `{...}` placeholder in a registry path
// template with the url-escaped id. Replaces whichever segment is present
// rather than matching the exact name, so an upstream rename (e.g.
// `{allocationId}` → `{allocationInstructionId}`) can't leave it in the path.
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
