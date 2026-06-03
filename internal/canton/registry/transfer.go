package registry

import (
	"context"
	"fmt"
	"net/url"
)

// V2 Token Standard transfer-instruction endpoints. The token registry
// (on LocalNet, the Splice validator app for the receiver tenant) hosts
// these so a wallet can assemble the factory contract + choice context
// needed to actually exercise the V2 transfer choices over the Ledger
// API.
//
// Upstream source-of-truth for the request / response shapes:
// https://github.com/canton-network/splice/blob/token-standard-v2-upcoming/token-standard/splice-api-token-transfer-instruction-v2/openapi/transfer-instruction-v2.yaml

const (
	// pathTransferFactory is the route to fetch the TransferFactory
	// contract + its choice context, used by the sender at the start
	// of a V2 transfer.
	pathTransferFactory = "/registry/transfer-instruction/v2/transfer-factory"
)

// pathAcceptChoiceContext returns the route to fetch the choice
// context required by the receiver to exercise
// `AcceptTransferInstruction` on a pending V2 TransferInstruction.
// `id` is the contract ID of the TransferInstruction; we URL-escape
// it because contract IDs are upstream-defined opaque strings that
// MAY contain characters that need encoding in a path segment.
func pathAcceptChoiceContext(id string) string {
	return "/registry/transfer-instruction/v2/" + url.PathEscape(id) + "/choice-contexts/accept"
}

// GetFactoryRequest is the body of POST .../transfer-factory.
//
// ChoiceArguments mirrors the OpenAPI's free-form `object`; we hand
// any caller-supplied value through to the server so this client
// doesn't have to track every choice-argument schema in lockstep with
// upstream. Typical content: sender/receiver party IDs, instrument id,
// amount — encoded the way the Daml JSON API expects.
type GetFactoryRequest struct {
	ChoiceArguments    any  `json:"choiceArguments"`
	ExcludeDebugFields bool `json:"excludeDebugFields,omitempty"`
}

// TransferFactoryWithChoiceContext is the 200 response from
// POST .../transfer-factory. The sender then exercises the factory
// choice (referenced by FactoryID) on the ledger, attaching the
// disclosed contracts and context-data from ChoiceContext.
type TransferFactoryWithChoiceContext struct {
	FactoryID     string        `json:"factoryId"`
	TransferKind  TransferKind  `json:"transferKind"`
	ChoiceContext ChoiceContext `json:"choiceContext"`
}

// TransferKind enumerates the upstream `transferKind` values. We keep
// the wire encoding (lowercase strings) so unknown future kinds round-
// trip cleanly through the typed Go struct.
type TransferKind string

const (
	// TransferKindSelf is sender == receiver — usually instant, no
	// receiver approval step.
	TransferKindSelf TransferKind = "self"
	// TransferKindDirect is "pre-approved" — receiver has signalled
	// they accept incoming transfers; no AcceptTransferInstruction
	// step is needed.
	TransferKindDirect TransferKind = "direct"
	// TransferKindOffer is the standard two-step flow — sender creates
	// a TransferInstruction, receiver later exercises
	// AcceptTransferInstruction (which needs its own choice context;
	// see [Client.GetAcceptChoiceContext]).
	TransferKindOffer TransferKind = "offer"
)

// GetChoiceContextRequest is the body of POST .../choice-contexts/accept.
// `Meta` is server-passthrough metadata; usually empty for the MVP
// receiver-accept flow.
type GetChoiceContextRequest struct {
	Meta               map[string]string `json:"meta,omitempty"`
	ExcludeDebugFields bool              `json:"excludeDebugFields,omitempty"`
}

// ChoiceContext groups the disclosed contracts and free-form context
// data that an interface choice needs at exercise time. Mirrors the
// OpenAPI shape verbatim — `ChoiceContextData` stays `any` because the
// upstream schema is open-ended (registry-defined per-asset).
type ChoiceContext struct {
	ChoiceContextData  any                 `json:"choiceContextData,omitempty"`
	DisclosedContracts []DisclosedContract `json:"disclosedContracts,omitempty"`
}

// DisclosedContract is one entry of `ChoiceContext.disclosedContracts`.
// CreatedEventBlob is forwarded unchanged to the Ledger gRPC
// `SubmitAndWait` request's `disclosed_contracts` field.
type DisclosedContract struct {
	TemplateID       string `json:"templateId"`
	ContractID       string `json:"contractId"`
	CreatedEventBlob string `json:"createdEventBlob"`
	SynchronizerID   string `json:"synchronizerId"`
	// debugPackageName / debugPayload / debugCreatedAt are omitted from
	// the typed struct — they're explicitly marked "use only if you
	// trust the provider" in the upstream OpenAPI, and our consumers
	// don't need them. Set `ExcludeDebugFields: true` on requests to
	// also save server bandwidth.
}

// GetTransferFactory fetches the V2 TransferFactory contract + its
// choice context that the sender needs to start a transfer.
//
// `choiceArguments` is the structured argument object the Daml JSON
// API would accept for the corresponding choice — see the upstream
// CLI's [transfer.ts] for an end-to-end example:
//
//	{
//	  "expectedAdmin": "<instrument admin party id>",
//	  "transfer": {"sender": "...", "receiver": "...", "amount": "10.0", ...},
//	  ...
//	}
//
// Pass any encodable Go value (map/struct/etc). On HTTP non-2xx
// responses the returned error wraps an [*APIError] (use errors.As
// to extract status + body for typed error rendering).
//
// [transfer.ts]: https://github.com/canton-network/splice/blob/token-standard-v2-upcoming/token-standard/cli/src/commands/transfer.ts
func (c *Client) GetTransferFactory(ctx context.Context, choiceArguments any, excludeDebug bool) (*TransferFactoryWithChoiceContext, error) {
	req := GetFactoryRequest{ChoiceArguments: choiceArguments, ExcludeDebugFields: excludeDebug}
	var out TransferFactoryWithChoiceContext
	if err := c.doJSON(ctx, "POST", pathTransferFactory, req, &out); err != nil {
		return nil, fmt.Errorf("GetTransferFactory: %w", err)
	}
	return &out, nil
}

// GetAcceptChoiceContext fetches the choice context the RECEIVER needs
// to exercise `AcceptTransferInstruction` on a pending V2
// TransferInstruction. `transferInstructionID` is the contract ID of
// the pending instruction (returned by the sender's
// GetTransferFactory + ledger submission).
//
// Pass an empty `meta` for the common case.
func (c *Client) GetAcceptChoiceContext(ctx context.Context, transferInstructionID string, meta map[string]string, excludeDebug bool) (*ChoiceContext, error) {
	if transferInstructionID == "" {
		return nil, fmt.Errorf("GetAcceptChoiceContext: transferInstructionID is required")
	}
	req := GetChoiceContextRequest{Meta: meta, ExcludeDebugFields: excludeDebug}
	var out ChoiceContext
	if err := c.doJSON(ctx, "POST", pathAcceptChoiceContext(transferInstructionID), req, &out); err != nil {
		return nil, fmt.Errorf("GetAcceptChoiceContext(%s): %w", transferInstructionID, err)
	}
	return &out, nil
}
