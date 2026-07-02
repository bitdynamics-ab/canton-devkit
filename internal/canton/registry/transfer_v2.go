package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

// Token-standard generation discriminators for the transfer wire shape.
// V1 (CIP-0056) and V2 (CIP-0112) differ in three places on the
// transfer-factory / accept choices — sender/receiver type, the
// factory choice's controller field, and the accept choice's arg set —
// so the JSON encoders below switch on this.
const (
	TransferVersionV1 = "v1"
	TransferVersionV2 = "v2"
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
// Reject + withdraw follow the same choice-contexts pattern.

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
	return Account{Owner: &party}
}

// TransferArgs is the structured `transfer` field of the
// transfer-factory choice. Mirrors the standard's `Transfer` record.
// The two generations differ in exactly one field: in V2 (CIP-0112)
// `sender`/`receiver` are `Account` records, while in V1 (CIP-0056)
// they are bare `Party` strings (this mirrors the read-path
// HoldingView.owner difference). Every other field is identical, so a
// single struct with a generation-aware MarshalJSON covers both. The
// json tags drive unmarshal (and the V2 marshal default).
type TransferArgs struct {
	// Version selects the sender/receiver wire shape: "v1" => bare
	// Party, "v2"/"" => Account. Not serialized; set by the enclosing
	// choice args at marshal time.
	Version          string       `json:"-"`
	Sender           Account      `json:"sender"`
	Receiver         Account      `json:"receiver"`
	Amount           string       `json:"amount"`
	InstrumentID     InstrumentID `json:"instrumentId"`
	RequestedAt      time.Time    `json:"requestedAt"`
	ExecuteBefore    time.Time    `json:"executeBefore"`
	InputHoldingCids []string     `json:"inputHoldingCids"`
	Meta             Metadata     `json:"meta"`
}

// partyOf returns an Account's owner party (or "" when None) — used to
// flatten an Account down to the bare Party the V1 transfer expects.
func partyOf(a Account) string {
	if a.Owner != nil {
		return *a.Owner
	}
	return ""
}

// MarshalJSON emits the generation-correct `transfer` shape. The
// registry decodes this against the real Daml choice signature, so the
// sender/receiver type must match the vetted package's generation.
func (t TransferArgs) MarshalJSON() ([]byte, error) {
	out := map[string]any{
		"amount":           t.Amount,
		"instrumentId":     t.InstrumentID,
		"requestedAt":      t.RequestedAt,
		"executeBefore":    t.ExecuteBefore,
		"inputHoldingCids": t.InputHoldingCids,
		"meta":             t.Meta,
	}
	if t.Version == TransferVersionV1 {
		// V1 (CIP-0056): sender/receiver are bare Party.
		out["sender"] = partyOf(t.Sender)
		out["receiver"] = partyOf(t.Receiver)
	} else {
		// V2 (CIP-0112): sender/receiver are Account records.
		out["sender"] = t.Sender
		out["receiver"] = t.Receiver
	}
	return json.Marshal(out)
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
// TransferFactory_Transfer choice argument. The registry validates it
// against the real choice signature, so the field set must match the
// instrument's generation exactly:
//
//   - V2 (CIP-0112, TransferInstructionV2.daml): { transfer, actors,
//     extraArgs } — `actors` is the controller list (the sender).
//   - V1 (CIP-0056, TransferInstructionV1.daml): { expectedAdmin,
//     transfer, extraArgs } — `expectedAdmin` is the factory admin the
//     choice validates against; there is no `actors` field (the
//     controller is fixed to transfer.sender).
//
// Version selects which shape MarshalJSON emits (empty => v2). The json
// tags drive unmarshal and the V2 marshal default.
type TransferFactoryChoiceArgs struct {
	// Version is the wire generation ("v1"/"v2"; empty => v2). Not serialized.
	Version string `json:"-"`
	// ExpectedAdmin is the V1 factory admin to validate; ignored for V2.
	ExpectedAdmin string       `json:"-"`
	Transfer      TransferArgs `json:"transfer"`
	Actors        []string     `json:"actors"`
	ExtraArgs     ExtraArgs    `json:"extraArgs"`
}

// MarshalJSON emits the generation-correct choice-argument shape,
// propagating the wire generation into the nested transfer so its
// sender/receiver match.
func (a TransferFactoryChoiceArgs) MarshalJSON() ([]byte, error) {
	tr := a.Transfer
	tr.Version = a.Version
	if a.Version == TransferVersionV1 {
		// V1 (CIP-0056): { expectedAdmin, transfer, extraArgs }.
		return json.Marshal(map[string]any{
			"expectedAdmin": a.ExpectedAdmin,
			"transfer":      tr,
			"extraArgs":     a.ExtraArgs,
		})
	}
	// V2 (CIP-0112): { transfer, actors, extraArgs }.
	return json.Marshal(map[string]any{
		"transfer":  tr,
		"actors":    a.Actors,
		"extraArgs": a.ExtraArgs,
	})
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

// transferFactoryPath / choiceContextPath build the version-segmented
// registry endpoints from the Client's configured generation. V1 and V2
// share identical path shapes — only the {version} segment differs (both
// confirmed present in the upstream OpenAPI).
func (c *Client) transferFactoryPath() string {
	return fmt.Sprintf("/registry/transfer-instruction/%s/transfer-factory", c.versionSeg())
}

func (c *Client) choiceContextPath(kind, instructionID string) string {
	return fmt.Sprintf("/registry/transfer-instruction/%s/%s/choice-contexts/%s",
		c.versionSeg(), url.PathEscape(instructionID), kind)
}

func (c *Client) versionSeg() string {
	if c.version == "" {
		return "v2"
	}
	return c.version
}

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
	if err := c.doJSON(ctx, "POST", c.transferFactoryPath(), req, &out); err != nil {
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
	// choiceContextPath url-escapes the instruction id so reserved URL
	// chars ('#', '?', ' ') aren't mis-parsed as a separate segment.
	path := c.choiceContextPath("accept", instructionID)
	if err := c.doJSON(ctx, "POST", path, req, &out); err != nil {
		return nil, fmt.Errorf("GetAcceptChoiceContext: %w", err)
	}
	return &out, nil
}

// GetRejectChoiceContext fetches the choice context the receiver needs
// to exercise RejectTransferInstruction. Not yet exercised by CLI / UI
// flows; included so the registry-client contract matches the OpenAPI
// spec.
func (c *Client) GetRejectChoiceContext(ctx context.Context, instructionID string, req ChoiceContextRequest) (*ChoiceContextResponse, error) {
	if instructionID == "" {
		return nil, fmt.Errorf("GetRejectChoiceContext: instructionID is required")
	}
	var out ChoiceContextResponse
	path := c.choiceContextPath("reject", instructionID)
	if err := c.doJSON(ctx, "POST", path, req, &out); err != nil {
		return nil, fmt.Errorf("GetRejectChoiceContext: %w", err)
	}
	return &out, nil
}

// GetWithdrawChoiceContext fetches the choice context the sender needs
// to exercise WithdrawTransferInstruction. Not yet exercised by CLI / UI
// flows; included so the registry-client contract matches the OpenAPI
// spec.
func (c *Client) GetWithdrawChoiceContext(ctx context.Context, instructionID string, req ChoiceContextRequest) (*ChoiceContextResponse, error) {
	if instructionID == "" {
		return nil, fmt.Errorf("GetWithdrawChoiceContext: instructionID is required")
	}
	var out ChoiceContextResponse
	path := c.choiceContextPath("withdraw", instructionID)
	if err := c.doJSON(ctx, "POST", path, req, &out); err != nil {
		return nil, fmt.Errorf("GetWithdrawChoiceContext: %w", err)
	}
	return &out, nil
}
