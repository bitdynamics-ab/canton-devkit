package registry

import (
	"context"
	"fmt"
	"time"
)

// V2 Token Standard transfer-instruction registry endpoints. These
// implement the off-ledger half of the V2 transfer flow defined by
// upstream Splice:
//
//	token-standard/splice-api-token-transfer-instruction-v2/openapi/
//	  transfer-instruction-v2.yaml
//
// Flow recap, sender side:
//
//	1. ACS-query for sender's HoldingV2 contracts on the instrument
//	2. POST /registry/transfer-instruction/v2/transfer-factory
//	   → returns (factoryId, choiceContextData, disclosedContracts)
//	3. Exercise TransferFactory_Transfer on the ledger with the
//	   choice context + disclosed contracts the registry handed back
//
// Receiver side, on a pending TransferInstruction:
//
//	1. POST /registry/transfer-instruction/v2/{id}/choice-contexts/accept
//	   → returns (choiceContextData, disclosedContracts)
//	2. Exercise AcceptTransferInstruction with that choice context
//
// Reject + withdraw follow the same choice-contexts pattern; not in
// MVP scope.

// TransferKind is the enum the registry returns to distinguish how the
// factory choice resolves: Direct (atomic transfer with no holding
// instruction), Offer (creates a TransferInstruction the receiver must
// accept), or Self (sender = receiver, ledger-only reshuffle).
type TransferKind string

const (
	TransferKindDirect TransferKind = "Direct"
	TransferKindOffer  TransferKind = "Offer"
	TransferKindSelf   TransferKind = "Self"
)

// InstrumentID mirrors the V2 standard's `Splice.Api.Token.HoldingV2.
// InstrumentId` record: the admin party + asset id pair that uniquely
// names an instrument on a participant.
type InstrumentID struct {
	Admin string `json:"admin"`
	ID    string `json:"id"`
}

// Account mirrors the V2 standard's `Splice.Api.Token.HoldingV2.Account`
// record. In V2 the transfer sender/receiver are Accounts, not bare
// parties — an Account is { owner: Optional Party, provider: Optional
// Party, id: Text }.
//
// JSON encoding follows the Daml JSON API: Optional Party is the party
// string when Some, or null when None (omitempty + pointer gives us
// that for free). For a regular wallet transfer, owner is set,
// provider is left None, and id is the empty-string default account.
type Account struct {
	Owner    *string `json:"owner"`
	Provider *string `json:"provider"`
	ID       string  `json:"id"`
}

// NewOwnedAccount builds the common Account: owner set, no provider,
// default ("") account id. This is what a wallet uses for a plain
// party-to-party transfer.
func NewOwnedAccount(party string) Account {
	p := party
	return Account{Owner: &p, Provider: nil, ID: ""}
}

// TransferArgs is the structured `transfer` field of the
// transfer-factory choice. Mirrors `Splice.Api.Token.TransferInstructionV2.
// Transfer` from the standard. Field order matches the OpenAPI spec.
type TransferArgs struct {
	Sender           Account      `json:"sender"`
	Receiver         Account      `json:"receiver"`
	Amount           string       `json:"amount"`
	InstrumentID     InstrumentID `json:"instrumentId"`
	RequestedAt      time.Time    `json:"requestedAt"`
	ExecuteBefore    time.Time    `json:"executeBefore"`
	InputHoldingCids []string     `json:"inputHoldingCids"`
	Meta             Metadata     `json:"meta"`
}

// Metadata is the standard's open-ended `Metadata` record — a string
// map plus optional values map. Empty when the caller has nothing to
// attach.
type Metadata struct {
	Values map[string]string `json:"values"`
}

// ExtraArgs carries the optional choice-context + meta envelope every
// V2 factory choice takes.
type ExtraArgs struct {
	Context Metadata `json:"context"`
	Meta    Metadata `json:"meta"`
}

// TransferFactoryRequest is the body POST'd to
// /registry/transfer-instruction/v2/transfer-factory. The registry
// validates `transfer` against its own policy (e.g. for Amulet:
// fee schedules, lock state) and rejects bad requests before the
// sender ever touches the ledger.
type TransferFactoryRequest struct {
	ChoiceArguments TransferFactoryChoiceArgs `json:"choiceArguments"`
}

// TransferFactoryChoiceArgs is the JSON encoding of the Daml
// TransferFactory_Transfer choice argument (per
// TransferInstructionV2.daml): { transfer, actors, extraArgs }. The
// registry validates this against the real choice signature, so the
// field set must match exactly — `actors` is the controller list (the
// sender party authorizing the transfer); there is no `expectedAdmin`
// field (the admin lives inside transfer.instrumentId).
type TransferFactoryChoiceArgs struct {
	Transfer  TransferArgs `json:"transfer"`
	Actors    []string     `json:"actors"`
	ExtraArgs ExtraArgs    `json:"extraArgs"`
}

// DisclosedContract is the OpenAPI shape registries return when they
// need to hand the caller off-ledger contract knowledge the participant
// can't see directly. Each entry pairs an opaque base64-encoded
// `createdEventBlob` with the contract id + synchronizer id; the gRPC
// `Commands.disclosedContracts` field takes the decoded blob bytes.
type DisclosedContract struct {
	ContractID       string `json:"contractId"`
	CreatedEventBlob string `json:"createdEventBlob"`
	SynchronizerID   string `json:"synchronizerId"`
}

// TransferFactoryResponse is what the registry hands back: the factory
// contract to exercise, the kind of transfer to expect, and the choice
// context (opaque data blob + disclosed contracts). Per the OpenAPI
// `TransferFactoryWithChoiceContext` schema, choiceContextData +
// disclosedContracts are nested under `choiceContext` — NOT at the top
// level. The convenience accessors below flatten them so callers don't
// have to reach through the nesting.
type TransferFactoryResponse struct {
	FactoryID     string                `json:"factoryId"`
	TransferKind  TransferKind          `json:"transferKind"`
	ChoiceContext ChoiceContextResponse `json:"choiceContext"`
}

// ChoiceContextData returns the choice-context data blob (flattened
// accessor over the nested choiceContext).
func (r *TransferFactoryResponse) ChoiceContextData() map[string]any {
	return r.ChoiceContext.ChoiceContextData
}

// DisclosedContractsList returns the disclosed contracts the participant
// needs to look up the factory + dependencies (flattened accessor).
func (r *TransferFactoryResponse) DisclosedContractsList() []DisclosedContract {
	return r.ChoiceContext.DisclosedContracts
}

// ChoiceContextRequest is the body POST'd to the per-instruction
// choice-context endpoints. Per the OpenAPI GetChoiceContextRequest
// schema, `meta` is a flat string→string map (additionalProperties:
// string) — NOT the {values:...} Metadata wrapper the on-ledger
// records use. Empty map serialises to `{}`, which the registry
// accepts.
type ChoiceContextRequest struct {
	Meta map[string]string `json:"meta"`
}

// ChoiceContextResponse is the leaner shape /choice-contexts/accept
// (and /reject + /withdraw) returns: no factory id (caller already has
// the TransferInstruction contract id), just the choice context blob
// + disclosed contracts.
type ChoiceContextResponse struct {
	ChoiceContextData  map[string]any      `json:"choiceContextData"`
	DisclosedContracts []DisclosedContract `json:"disclosedContracts"`
}

const (
	transferFactoryPath          = "/registry/transfer-instruction/v2/transfer-factory"
	choiceContextAcceptPathFmt   = "/registry/transfer-instruction/v2/%s/choice-contexts/accept"
	choiceContextRejectPathFmt   = "/registry/transfer-instruction/v2/%s/choice-contexts/reject"
	choiceContextWithdrawPathFmt = "/registry/transfer-instruction/v2/%s/choice-contexts/withdraw"
)

// GetTransferFactory posts the sender's intended transfer to the
// registry and returns the factory contract + choice context the
// sender will exercise. The registry is asset-specific — the caller
// dialed it via the instrument's `tokenRegistryUrl` view field (or a
// well-known fallback URL for Amulet).
//
// The registry runs its own preflight: validates fee schedules,
// lock states, ACL gates. A 4xx here means the transfer would have
// been rejected on-ledger anyway; surfacing the body to the user via
// APIError lets the CLI / UI print the registry's actual reason
// rather than a generic "transfer rejected".
func (c *Client) GetTransferFactory(ctx context.Context, req TransferFactoryRequest) (*TransferFactoryResponse, error) {
	var out TransferFactoryResponse
	if err := c.doJSON(ctx, "POST", transferFactoryPath, req, &out); err != nil {
		return nil, fmt.Errorf("GetTransferFactory: %w", err)
	}
	return &out, nil
}

// GetAcceptChoiceContext fetches the choice context the receiver needs
// to exercise AcceptTransferInstruction on a pending instruction. The
// instructionID is the V2 contract id of the TransferInstruction
// contract (the receiver discovers it via their ACS).
func (c *Client) GetAcceptChoiceContext(ctx context.Context, instructionID string, req ChoiceContextRequest) (*ChoiceContextResponse, error) {
	if instructionID == "" {
		return nil, fmt.Errorf("GetAcceptChoiceContext: instructionID is required")
	}
	var out ChoiceContextResponse
	path := fmt.Sprintf(choiceContextAcceptPathFmt, instructionID)
	if err := c.doJSON(ctx, "POST", path, req, &out); err != nil {
		return nil, fmt.Errorf("GetAcceptChoiceContext: %w", err)
	}
	return &out, nil
}

// GetRejectChoiceContext + GetWithdrawChoiceContext are stubbed here
// for the symmetry the OpenAPI spec defines but are not exercised by
// MVP CLI / UI flows — receiver reject and sender withdraw are
// follow-up surface area. Keeping the methods present means the
// registry-client contract is complete for callers that want to wire
// them later without touching this file again.

// GetRejectChoiceContext fetches the choice context the receiver needs
// to exercise RejectTransferInstruction. Out of MVP scope; included
// for API completeness.
func (c *Client) GetRejectChoiceContext(ctx context.Context, instructionID string, req ChoiceContextRequest) (*ChoiceContextResponse, error) {
	if instructionID == "" {
		return nil, fmt.Errorf("GetRejectChoiceContext: instructionID is required")
	}
	var out ChoiceContextResponse
	path := fmt.Sprintf(choiceContextRejectPathFmt, instructionID)
	if err := c.doJSON(ctx, "POST", path, req, &out); err != nil {
		return nil, fmt.Errorf("GetRejectChoiceContext: %w", err)
	}
	return &out, nil
}

// GetWithdrawChoiceContext fetches the choice context the sender needs
// to exercise WithdrawTransferInstruction. Out of MVP scope; included
// for API completeness.
func (c *Client) GetWithdrawChoiceContext(ctx context.Context, instructionID string, req ChoiceContextRequest) (*ChoiceContextResponse, error) {
	if instructionID == "" {
		return nil, fmt.Errorf("GetWithdrawChoiceContext: instructionID is required")
	}
	var out ChoiceContextResponse
	path := fmt.Sprintf(choiceContextWithdrawPathFmt, instructionID)
	if err := c.doJSON(ctx, "POST", path, req, &out); err != nil {
		return nil, fmt.Errorf("GetWithdrawChoiceContext: %w", err)
	}
	return &out, nil
}
