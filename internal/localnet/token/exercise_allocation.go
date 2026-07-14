package token

import (
	"context"
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/registry"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// V2 allocation / DvP exercise orchestration — assembles + submits the
// on-ledger half of an allocation flow. The off-ledger registry calls have
// already happened; this file turns a (factory/contract id, choice context,
// disclosed contracts) tuple into a SubmitAndWaitForTransaction.

// exerciseAllocationFactory submits AllocationFactory_Allocate against the
// factory contract the registry returned. Returns the submit response —
// caller mines it for the created Allocation (finalized) or
// AllocationInstruction (pending) contract id.
func exerciseAllocationFactory(
	ctx context.Context,
	client *ledger.Client,
	actAs string,
	factoryID string,
	args registry.AllocationFactoryChoiceArgs,
	factoryResp *registry.AllocationFactoryResponse,
) (*lapiv2.SubmitAndWaitForTransactionResponse, error) {
	extraArgs, err := buildExtraArgsRecord(factoryResp.ChoiceContextData(), registry.Metadata{Values: map[string]string{}})
	if err != nil {
		return nil, fmt.Errorf("build extraArgs: %w", err)
	}
	choiceArg := recordValue([]field{
		{"settlement", buildSettlementInfoRecord(args.Settlement)},
		{"allocation", buildAllocationSpecRecord(args.Allocation)},
		{"requestedAt", timestampValue(args.RequestedAt)},
		{"inputHoldingCids", listValue(args.InputHoldingCids, contractIDValue)},
		{"extraArgs", extraArgs},
		{"actors", listValue(args.Actors, partyValue)},
	})

	disclosed, err := disclosedContractsToProto(factoryResp.DisclosedContractsList())
	if err != nil {
		return nil, fmt.Errorf("convert disclosed contracts: %w", err)
	}

	pkg, mod, entity := splitInterfaceID(AllocationFactoryInterfaceV2)
	exercise := &lapiv2.Command{
		Command: &lapiv2.Command_Exercise{
			Exercise: &lapiv2.ExerciseCommand{
				TemplateId:     &lapiv2.Identifier{PackageId: pkg, ModuleName: mod, EntityName: entity},
				ContractId:     factoryID,
				Choice:         "AllocationFactory_Allocate",
				ChoiceArgument: choiceArg,
			},
		},
	}
	return submitAllocation(ctx, client, actAs, []*lapiv2.Command{exercise}, disclosed)
}

// exerciseAllocationChoice submits a nonconsuming Allocation choice —
// Allocation_Withdraw or Allocation_Cancel — against a finalized Allocation.
// Both take the same `{ extraArgs }` argument shape; the caller passes the
// choice name.
func exerciseAllocationChoice(
	ctx context.Context,
	client *ledger.Client,
	actAs string,
	allocationID string,
	choice string,
	ctxResp *registry.ChoiceContextResponse,
) (*lapiv2.SubmitAndWaitForTransactionResponse, error) {
	extraArgs, err := buildExtraArgsRecord(ctxResp.ChoiceContextData, registry.Metadata{Values: map[string]string{}})
	if err != nil {
		return nil, fmt.Errorf("build extraArgs: %w", err)
	}
	choiceArg := recordValue([]field{
		{"extraArgs", extraArgs},
	})
	disclosed, err := disclosedContractsToProto(ctxResp.DisclosedContracts)
	if err != nil {
		return nil, fmt.Errorf("convert disclosed contracts: %w", err)
	}
	pkg, mod, entity := splitInterfaceID(AllocationInterfaceV2)
	exercise := &lapiv2.Command{
		Command: &lapiv2.Command_Exercise{
			Exercise: &lapiv2.ExerciseCommand{
				TemplateId:     &lapiv2.Identifier{PackageId: pkg, ModuleName: mod, EntityName: entity},
				ContractId:     allocationID,
				Choice:         choice,
				ChoiceArgument: choiceArg,
			},
		},
	}
	return submitAllocation(ctx, client, actAs, []*lapiv2.Command{exercise}, disclosed)
}

// submitAllocation is the shared submission seam for the allocation
// exercises. Requests the actAs party's created events with the Allocation +
// AllocationInstruction interface views attached, so the caller can mine the
// resulting contract id (finalized vs pending).
func submitAllocation(
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
		TransactionFormat: allocationTxFormat(actAs),
	}
	return client.SubmitAndWaitForTransaction(ctx, req)
}

// allocationTxFormat requests the submit response with the actAs party's
// created events + the Allocation and AllocationInstruction interface views,
// which findCreatedAllocationID walks to surface the resulting contract id.
func allocationTxFormat(actAs string) *lapiv2.TransactionFormat {
	return &lapiv2.TransactionFormat{
		TransactionShape: lapiv2.TransactionShape_TRANSACTION_SHAPE_ACS_DELTA,
		EventFormat: &lapiv2.EventFormat{
			FiltersByParty: map[string]*lapiv2.Filters{
				actAs: {Cumulative: []*lapiv2.CumulativeFilter{
					interfaceFilterEntry(AllocationInterfaceV2),
					interfaceFilterEntry(AllocationInstructionInterfaceV2),
				}},
			},
			Verbose: false,
		},
	}
}

// findCreatedAllocationID scans a submit response for a created event
// implementing either the Allocation (finalized) or AllocationInstruction
// (pending) interface, preferring finalized when both appear. Returns
// (id, finalized, ok). Match on module + entity only — the package id
// rotates across V2 alpha snapshots.
func findCreatedAllocationID(resp *lapiv2.SubmitAndWaitForTransactionResponse) (string, bool, bool) {
	tx := resp.GetTransaction()
	if tx == nil {
		return "", false, false
	}
	var pendingID string
	for _, ev := range tx.GetEvents() {
		created := ev.GetCreated()
		if created == nil {
			continue
		}
		for _, iv := range created.GetInterfaceViews() {
			id := iv.GetInterfaceId()
			if id == nil {
				continue
			}
			switch id.GetEntityName() {
			case "Allocation":
				return created.GetContractId(), true, true
			case "AllocationInstruction":
				pendingID = created.GetContractId()
			}
		}
	}
	if pendingID != "" {
		return pendingID, false, true
	}
	return "", false, false
}
