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
// User id is hardcoded to "canton-devkit" — Splice participants accept
// any user id under the `unsafe-jwt-hmac-256` auth mode (it's a label,
// not an auth check; the JWT's `sub` is what auth checks). We set it
// so log forensics on Canton side can attribute commands to this CLI.
// (Field is still wire-named `application_id` upstream but the dazl
// proto bindings expose it as UserId after the v2 ledger API rename.)

const exerciseUserID = "canton-devkit"

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
) (*lapiv2.SubmitAndWaitForTransactionResponse, error) {
	// Build the choice argument record: { expectedAdmin, transfer, extraArgs }.
	extraArgs, err := buildExtraArgsRecord(factoryResp.ChoiceContextData, registry.Metadata{Values: map[string]string{}})
	if err != nil {
		return nil, fmt.Errorf("build extraArgs: %w", err)
	}
	choiceArg := recordValue([]field{
		{"expectedAdmin", partyValue(transferArgs.InstrumentID.Admin)},
		{"transfer", buildTransferRecord(transferArgs)},
		{"extraArgs", extraArgs},
	})

	disclosed, err := disclosedContractsToProto(factoryResp.DisclosedContracts)
	if err != nil {
		return nil, fmt.Errorf("convert disclosed contracts: %w", err)
	}

	pkg, mod, entity := splitInterfaceID(TransferFactoryInterfaceV2)
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
	return submitExercise(ctx, client, actAs, []*lapiv2.Command{exercise}, disclosed)
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
) (*lapiv2.SubmitAndWaitForTransactionResponse, error) {
	extraArgs, err := buildExtraArgsRecord(ctxResp.ChoiceContextData, registry.Metadata{Values: map[string]string{}})
	if err != nil {
		return nil, fmt.Errorf("build extraArgs: %w", err)
	}
	// Accept's choice argument is just { extraArgs }.
	choiceArg := recordValue([]field{{"extraArgs", extraArgs}})

	disclosed, err := disclosedContractsToProto(ctxResp.DisclosedContracts)
	if err != nil {
		return nil, fmt.Errorf("convert disclosed contracts: %w", err)
	}

	pkg, mod, entity := splitInterfaceID(TransferInstructionInterfaceV2)
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
	return submitExercise(ctx, client, actAs, []*lapiv2.Command{exercise}, disclosed)
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
	}
	// Default submission timeout: 60s. Commit latency on a healthy
	// LocalNet is sub-second; the headroom covers GC / image-pull
	// stalls without leaving the user waiting forever on a stuck
	// synchronizer.
	subCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return client.SubmitAndWaitForTransaction(subCtx, req)
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
