package token

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/registry"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// V2 exercise orchestration: assemble + submit the on-ledger half of a
// V2 Token Standard transfer or accept. The off-ledger registry calls
// have already happened by the time exerciseV2 is called; this file
// is purely about turning a (factoryId / instructionId, choice context,
// disclosed contracts) tuple into a SubmitAndWaitForTransaction request
// the participant accepts.
//
// Commands.UserId MUST match the JWT's subject claim — Canton's
// authorizer rejects a mismatch with "Claims are only valid for userId
// '<sub>', actual userId is '<UserId>'". The Splice LocalNet per-role
// tokens are all issued with subject "ledger-api-user", so that's the
// user id every submission carries. (Field is wire-named
// `application_id` upstream but the dazl proto bindings expose it as
// UserId after the v2 ledger API rename.)
const exerciseUserID = "ledger-api-user"

// exerciseV2TransferFactory submits TransferFactory_Transfer against
// the factory contract returned by the registry. The participant
// resolves the interface choice via the disclosed factory contract +
// any disclosed dependency contracts (e.g. AmuletRules for fees).
//
// Returns the SubmitAndWaitForTransaction response — caller mines it
// for the resulting TransferInstruction contract id (Offer kind) or
// the receiver's new Holding (Direct kind).
func exerciseV2TransferFactory(
	ctx context.Context,
	client *ledger.Client,
	actAs string,
	factoryID string,
	transferArgs registry.TransferArgs,
	factoryResp *registry.TransferFactoryResponse,
	gen Generation,
) (*lapiv2.SubmitAndWaitForTransactionResponse, error) {
	// Build the choice argument record. Per TransferInstructionV2.daml,
	// TransferFactory_Transfer takes { transfer, actors, extraArgs } —
	// actors is the controller list (the sender, who authorizes the
	// transfer). There is NO expectedAdmin field on the choice (the
	// admin is carried inside transfer.instrumentId).
	extraArgs, err := buildExtraArgsRecord(factoryResp.ChoiceContextData(), registry.Metadata{Values: map[string]string{}})
	if err != nil {
		return nil, fmt.Errorf("build extraArgs: %w", err)
	}
	choiceArg := recordValue([]field{
		{"transfer", buildTransferRecord(transferArgs)},
		{"actors", listValue([]string{actAs}, partyValue)},
		{"extraArgs", extraArgs},
	})

	disclosed, err := disclosedContractsToProto(factoryResp.DisclosedContractsList())
	if err != nil {
		return nil, fmt.Errorf("convert disclosed contracts: %w", err)
	}

	pkg, mod, entity := splitInterfaceID(transferFactoryInterface(gen))
	exercise := &lapiv2.Command{
		Command: &lapiv2.Command_Exercise{
			Exercise: &lapiv2.ExerciseCommand{
				TemplateId: &lapiv2.Identifier{
					PackageId:  pkg,
					ModuleName: mod,
					EntityName: entity,
				},
				ContractId:     factoryID,
				Choice:         "TransferFactory_Transfer",
				ChoiceArgument: choiceArg,
			},
		},
	}
	return submitExercise(ctx, client, actAs, []*lapiv2.Command{exercise}, disclosed, gen)
}

// exerciseV2AcceptInstruction submits TransferInstruction_Accept against
// the pending TransferInstruction contract. The instructionID is the
// receiver-discovered contract id; the choice context + disclosed
// contracts came from POST /choice-contexts/accept.
func exerciseV2AcceptInstruction(
	ctx context.Context,
	client *ledger.Client,
	actAs string,
	instructionID string,
	ctxResp *registry.ChoiceContextResponse,
	gen Generation,
) (*lapiv2.SubmitAndWaitForTransactionResponse, error) {
	extraArgs, err := buildExtraArgsRecord(ctxResp.ChoiceContextData, registry.Metadata{Values: map[string]string{}})
	if err != nil {
		return nil, fmt.Errorf("build extraArgs: %w", err)
	}
	// Per TransferInstructionV2.daml, TransferInstruction_Accept takes
	// { actors, extraArgs } — actors is the controller (the receiver).
	choiceArg := recordValue([]field{
		{"actors", listValue([]string{actAs}, partyValue)},
		{"extraArgs", extraArgs},
	})

	disclosed, err := disclosedContractsToProto(ctxResp.DisclosedContracts)
	if err != nil {
		return nil, fmt.Errorf("convert disclosed contracts: %w", err)
	}

	pkg, mod, entity := splitInterfaceID(transferInstructionInterface(gen))
	exercise := &lapiv2.Command{
		Command: &lapiv2.Command_Exercise{
			Exercise: &lapiv2.ExerciseCommand{
				TemplateId: &lapiv2.Identifier{
					PackageId:  pkg,
					ModuleName: mod,
					EntityName: entity,
				},
				ContractId:     instructionID,
				Choice:         "TransferInstruction_Accept",
				ChoiceArgument: choiceArg,
			},
		},
	}
	return submitExercise(ctx, client, actAs, []*lapiv2.Command{exercise}, disclosed, gen)
}

// submitExercise is the shared submission seam: same Commands envelope
// for both transfer-factory and accept-instruction. The participant
// validates the JWT's actAs claim covers `actAs`; the ledger.dialLedger
// + auto-grant already established that on the dial path.
func submitExercise(
	ctx context.Context,
	client *ledger.Client,
	actAs string,
	commands []*lapiv2.Command,
	disclosed []*lapiv2.DisclosedContract,
	gen Generation,
) (*lapiv2.SubmitAndWaitForTransactionResponse, error) {
	cmdID, err := newCommandID()
	if err != nil {
		return nil, fmt.Errorf("generate command id: %w", err)
	}
	req := &lapiv2.SubmitAndWaitForTransactionRequest{
		Commands: &lapiv2.Commands{
			UserId:             exerciseUserID,
			CommandId:          cmdID,
			ActAs:              []string{actAs},
			Commands:           commands,
			DisclosedContracts: disclosed,
		},
		// Request the resulting transaction with the actAs party's
		// created events + the TransferInstructionV2 interface view, so
		// findCreatedInstructionID can extract the offer instruction's
		// contract id from the response. Without an explicit
		// TransactionFormat the participant returns a minimal
		// (event-less) transaction and the CID would be lost.
		TransactionFormat: transferInstructionTxFormat(actAs, gen),
	}
	// Default submission timeout: 60s. Commit latency on a healthy
	// LocalNet is sub-second; the headroom covers GC / image-pull
	// stalls without leaving the user waiting forever on a stuck
	// synchronizer.
	subCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return client.SubmitAndWaitForTransaction(subCtx, req)
}

// transferInstructionTxFormat builds the TransactionFormat that makes
// the submit response carry the actAs party's created events with the
// TransferInstructionV2 interface view attached. findCreatedInstructionID
// walks those views to surface the offer instruction's contract id.
//
// ACS_DELTA shape is sufficient: an offer transfer's create of the
// TransferInstruction is an ACS-positive event, so it shows up without
// needing the heavier LEDGER_EFFECTS tree shape.
func transferInstructionTxFormat(actAs string, gen Generation) *lapiv2.TransactionFormat {
	pkg, mod, entity := splitInterfaceID(transferInstructionInterface(gen))
	return &lapiv2.TransactionFormat{
		TransactionShape: lapiv2.TransactionShape_TRANSACTION_SHAPE_ACS_DELTA,
		EventFormat: &lapiv2.EventFormat{
			FiltersByParty: map[string]*lapiv2.Filters{
				actAs: {
					Cumulative: []*lapiv2.CumulativeFilter{{
						IdentifierFilter: &lapiv2.CumulativeFilter_InterfaceFilter{
							InterfaceFilter: &lapiv2.InterfaceFilter{
								InterfaceId: &lapiv2.Identifier{
									PackageId:  pkg,
									ModuleName: mod,
									EntityName: entity,
								},
								IncludeInterfaceView: true,
							},
						},
					}},
				},
			},
			Verbose: false,
		},
	}
}

// splitInterfaceID parses our `#package-name:Module:Entity` form into
// the proto Identifier shape. Mirrors parseInterfaceID in ledger.go
// but returns the `#package-name`-prefixed form for the Identifier
// PackageId field (Canton uses that to resolve package-name references
// at submit time, surviving the V2 alpha's weekly snapshot rotation).
func splitInterfaceID(qual string) (pkg, module, entity string) {
	q := strings.TrimPrefix(qual, "#")
	parts := strings.Split(q, ":")
	switch len(parts) {
	case 3:
		return "#" + parts[0], parts[1], parts[2]
	case 2:
		return "", parts[0], parts[1]
	}
	return "", "", ""
}

// newCommandID returns a fresh hex-encoded random 16-byte string for
// use as Commands.command_id. The participant requires unique command
// ids per (application_id, party, command_id) tuple so retries don't
// double-submit; using crypto-random gives us collision-safe ids
// without needing a UUID dependency.
func newCommandID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "canton-devkit-" + hex.EncodeToString(b[:]), nil
}
