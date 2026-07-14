package token

import (
	"context"
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/registry"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// Atomic multi-step token choreography via the wallet's BatchingUtilityV2
// (splice-util-token-standard-wallet 1.1.0). BatchingUtility_ExecuteBatch
// threads a HoldingMap through a list of TokenStandardAction in ONE
// transaction, so a transfer + its receiver-side accept commit
// all-or-nothing.
//
// Atomicity removes the partial-recovery model: the sequential flow leaves a
// retryable pending instruction if an accept fails, a batch leaves only the
// original holdings. So batching is OPT-IN (TransferOptions.Atomic) and the
// sequential path stays the default.
//
// Signatures pinned to Splice 0.6.12 (see v2_surface.go BatchingUtility*
// consts):
//
//	template BatchingUtility with user : Party            -- signatory user
//	nonconsuming choice BatchingUtility_ExecuteBatch
//	  with
//	    inputHoldingMap       : HoldingMap
//	    actions               : [TokenStandardAction]
//	    archiveAfterExecution : Bool
//	  returns BatchingUtility_ExecuteBatchResult with
//	    actionResults   : [TokenStandardActionResult]
//	    outputHoldingMap : HoldingMap

// batchResult / batchActionResult mirror internal/api/types.BatchResult /
// BatchActionResult (same JSON tags) to keep this package free of an
// api/types import. Adding a field requires updating both; schema_shape_test
// pins the api/types side.
type batchResult struct {
	UpdateID string              `json:"update_id"`
	Actions  []batchActionResult `json:"actions"`
	OK       bool                `json:"ok"`
}

type batchActionResult struct {
	Kind   string `json:"kind"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// batchAction is one step of a batch: a pre-built TokenStandardAction Daml
// value (a TSA_* variant wrapping a ChoiceCall) plus a label for the result
// row.
type batchAction struct {
	Kind  string // api/types.BatchActionResult.Kind label, e.g. "transfer_v2"
	Value *lapiv2.Value
}

// scopedHoldings pairs a ScopedAccount with the holding contract ids threaded
// into the batch for that account. buildHoldingMapValue turns a slice of these
// into the HoldingMap's byAdminAndAccount GenMap.
type scopedHoldings struct {
	Admin   string
	Account registry.Account
	// Instrument keys the per-account holdings TextMap: ExecuteBatch looks
	// holdings up by `TextMap.lookup instrumentId.id`, so this MUST equal the
	// transfer's instrumentId.id or the batch finds no holdings and interpretation
	// fails with inputAmount = 0.
	Instrument  string
	HoldingCIDs []string
}

// ensureBatchingUtility returns the user's BatchingUtility contract id,
// creating one if none exists. Idempotent: a re-run reuses the existing
// utility. The user is the sole signatory, so one utility per user suffices.
func ensureBatchingUtility(ctx context.Context, client *ledger.Client, user string) (string, error) {
	cid, err := findBatchingUtility(ctx, client, user)
	if err != nil {
		return "", fmt.Errorf("look up BatchingUtility: %w", err)
	}
	if cid != "" {
		return cid, nil
	}
	return createBatchingUtility(ctx, client, user)
}

// findBatchingUtility returns the user's existing BatchingUtility contract
// id, or "" when none exists.
func findBatchingUtility(ctx context.Context, client *ledger.Client, user string) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	end, err := client.LedgerEnd(ctx)
	if err != nil {
		return "", fmt.Errorf("ledger end: %w", err)
	}
	pkg, mod, entity := splitInterfaceID(BatchingUtilityTemplateV2)
	req := ledger.ActiveContractsRequest{
		ActiveAtOffset: end.Offset,
		EventFormat: &lapiv2.EventFormat{
			FiltersByParty: map[string]*lapiv2.Filters{
				user: {Cumulative: []*lapiv2.CumulativeFilter{{
					IdentifierFilter: &lapiv2.CumulativeFilter_TemplateFilter{
						TemplateFilter: &lapiv2.TemplateFilter{
							TemplateId: &lapiv2.Identifier{PackageId: pkg, ModuleName: mod, EntityName: entity},
						},
					},
				}}},
			},
		},
	}
	stream, err := client.ActiveContracts(ctx, req)
	if err != nil {
		return "", fmt.Errorf("query BatchingUtility: %w", err)
	}
	for item := range stream {
		if item.Err != nil {
			return "", item.Err
		}
		entry, ok := item.Value.ContractEntry.(*lapiv2.GetActiveContractsResponse_ActiveContract)
		if !ok {
			continue
		}
		if ce := entry.ActiveContract.GetCreatedEvent(); ce != nil {
			return ce.GetContractId(), nil
		}
	}
	return "", nil
}

// createBatchingUtility creates the user-owned BatchingUtility contract and
// returns its contract id. CreateCommand does not resolve the #package-name
// reference, so the package id is resolved at runtime to survive the alpha's
// snapshot rotation.
func createBatchingUtility(ctx context.Context, client *ledger.Client, user string) (string, error) {
	pkgID, err := resolvePackageID(ctx, client, "splice-util-token-standard-wallet")
	if err != nil {
		return "", err
	}
	_, mod, entity := splitInterfaceID(BatchingUtilityTemplateV2)
	create := &lapiv2.Command{
		Command: &lapiv2.Command_Create{
			Create: &lapiv2.CreateCommand{
				TemplateId: &lapiv2.Identifier{PackageId: pkgID, ModuleName: mod, EntityName: entity},
				CreateArguments: &lapiv2.Record{Fields: []*lapiv2.RecordField{
					{Label: "user", Value: partyValue(user)},
				}},
			},
		},
	}
	resp, err := submitForTransaction(ctx, client, user, []*lapiv2.Command{create}, nil,
		createdContractFormat(user, ""))
	if err != nil {
		return "", fmt.Errorf("create BatchingUtility: %w", err)
	}
	cid := firstCreatedOfTemplate(resp, "BatchingUtility")
	if cid == "" {
		return "", fmt.Errorf("BatchingUtility created but contract id not found")
	}
	return cid, nil
}

// executeBatch runs a list of token-standard actions atomically by exercising
// BatchingUtility_ExecuteBatch, threading the input holdings through the
// batch's HoldingMap. All actions commit all-or-nothing.
//
// actAs must cover every party whose authority the batched choices need (the
// batch runs inner actions with the submitter's authority; on LocalNet we pass
// the union of sender/receiver/admin parties) plus the utility's signatory (user).
func executeBatch(
	ctx context.Context,
	client *ledger.Client,
	user string,
	actAs []string,
	inputHoldings []scopedHoldings,
	actions []batchAction,
	archiveAfterExecution bool,
) (batchResult, error) {
	utilCID, err := ensureBatchingUtility(ctx, client, user)
	if err != nil {
		return batchResult{}, err
	}

	actionValues := make([]*lapiv2.Value, len(actions))
	for i, a := range actions {
		actionValues[i] = a.Value
	}
	choiceArg := recordValue([]field{
		{"inputHoldingMap", buildHoldingMapValue(inputHoldings)},
		{"actions", &lapiv2.Value{Sum: &lapiv2.Value_List{List: &lapiv2.List{Elements: actionValues}}}},
		{"archiveAfterExecution", boolValue(archiveAfterExecution)},
	})
	pkg, mod, entity := splitInterfaceID(BatchingUtilityTemplateV2)
	cmd := &lapiv2.Command{
		Command: &lapiv2.Command_Exercise{
			Exercise: &lapiv2.ExerciseCommand{
				TemplateId:     &lapiv2.Identifier{PackageId: pkg, ModuleName: mod, EntityName: entity},
				ContractId:     utilCID,
				Choice:         BatchingUtilityExecuteBatchChoiceV2,
				ChoiceArgument: choiceArg,
			},
		},
	}
	// LEDGER_EFFECTS so the response carries the ExecuteBatch exercised
	// event (its actionResults) — the ACS-delta shape omits exercise nodes.
	resp, err := submitForTransactionMulti(ctx, client, dedupParties(append(append([]string{}, actAs...), user)),
		[]*lapiv2.Command{cmd}, nil, ledgerEffectsFormat(actAs))
	if err != nil {
		return batchResult{}, fmt.Errorf("exercise %s: %w", BatchingUtilityExecuteBatchChoiceV2, err)
	}
	return parseBatchResult(resp, actions), nil
}

// parseBatchResult maps an ExecuteBatch transaction response to a batchResult.
// When the exercise result can't be read (no effects, or an alpha snapshot
// changed the result record) it falls back to one OK row per submitted action.
func parseBatchResult(resp *lapiv2.SubmitAndWaitForTransactionResponse, actions []batchAction) batchResult {
	tx := resp.GetTransaction()
	res := batchResult{UpdateID: tx.GetUpdateId(), OK: true}

	var resultRec *lapiv2.Record
	for _, ev := range tx.GetEvents() {
		ex := ev.GetExercised()
		if ex == nil || ex.GetChoice() != BatchingUtilityExecuteBatchChoiceV2 {
			continue
		}
		resultRec = recordOf(ex.GetExerciseResult())
		break
	}

	// One row per submitted action, labelled off the action we sent (result
	// variant shapes aren't frozen across alpha snapshots). Reaching here means
	// the batch committed atomically, so every action is OK.
	results := actionResultsList(resultRec)
	for i, a := range actions {
		row := batchActionResult{Kind: a.Kind, OK: true}
		if results != nil && i >= len(results) {
			row.Detail = "no result element returned for this action"
		}
		res.Actions = append(res.Actions, row)
	}
	return res
}

// actionResultsList pulls the `actionResults` list out of an
// ExecuteBatch result record, or nil when absent/unreadable.
func actionResultsList(rec *lapiv2.Record) []*lapiv2.Value {
	if rec == nil {
		return nil
	}
	for _, f := range rec.Fields {
		if f.Label != "actionResults" {
			continue
		}
		if l := f.Value.GetList(); l != nil {
			return l.GetElements()
		}
	}
	return nil
}

// ledgerEffectsFormat requests the LEDGER_EFFECTS shape for the first actAs
// party so the response carries exercised (choice) events — needed to read the
// batch's actionResults. ACS_DELTA omits exercise nodes.
func ledgerEffectsFormat(actAs []string) *lapiv2.TransactionFormat {
	party := ""
	if len(actAs) > 0 {
		party = actAs[0]
	}
	return &lapiv2.TransactionFormat{
		TransactionShape: lapiv2.TransactionShape_TRANSACTION_SHAPE_LEDGER_EFFECTS,
		EventFormat: &lapiv2.EventFormat{
			FiltersByParty: map[string]*lapiv2.Filters{
				party: {Cumulative: []*lapiv2.CumulativeFilter{{
					IdentifierFilter: &lapiv2.CumulativeFilter_WildcardFilter{
						WildcardFilter: &lapiv2.WildcardFilter{},
					},
				}}},
			},
			Verbose: true,
		},
	}
}

// --- HoldingMap / TokenStandardAction value builders -----------------
//
// TODO: the deep TokenStandardAction / AnyContractId record shapes below are
// hand-built from the 0.6.12 confirmed signatures but not yet exercised against
// a live token-standard-v2 ledger. Verify field order/labels for each TSA_*
// variant's ChoiceCall when an e2e gate lands; the batch path is opt-in so the
// default flow is unaffected until then.

// buildHoldingMapValue encodes the HoldingMap the batch threads its input
// holdings through. Upstream (Splice.Util.Token.Wallet.BatchingUtilityV2):
//
//	data HoldingMap = HoldingMap with
//	  byAdminAndAccount : Map.Map ScopedAccount (TextMap.TextMap [ContractId V2.Holding])
//
// `Map.Map` (DA.Map) wire-encodes as a GenMap keyed by the ScopedAccount record;
// `TextMap.TextMap` as a TextMap. A list-of-tuples encoding trips the
// preprocessor with "mismatching type: ...:HoldingMap and value: List(...)".
// Empty input yields an empty GenMap.
//
// The inner TextMap is keyed by the INSTRUMENT ID, not an index: ExecuteBatch
// resolves holdings with `TextMap.lookup instrumentId.id`, so an index key
// ("0") would miss and the batched transfer would see inputAmount = 0.
func buildHoldingMapValue(in []scopedHoldings) *lapiv2.Value {
	entries := make([]genMapEntry, 0, len(in))
	for _, sh := range in {
		cids := listValue(sh.HoldingCIDs, contractIDValue)
		holdingTextMap := &lapiv2.Value{Sum: &lapiv2.Value_TextMap{TextMap: &lapiv2.TextMap{
			Entries: []*lapiv2.TextMap_Entry{{Key: sh.Instrument, Value: cids}},
		}}}
		entries = append(entries, genMapEntry{
			key:   buildScopedAccountRecord(sh.Admin, sh.Account),
			value: holdingTextMap,
		})
	}
	return recordValue([]field{
		{"byAdminAndAccount", genMapValue(entries)},
	})
}

// buildScopedAccountRecord:
//
//	data ScopedAccount = ScopedAccount with
//	  admin   : Party
//	  account : Account
func buildScopedAccountRecord(admin string, account registry.Account) *lapiv2.Value {
	return recordValue([]field{
		{"admin", partyValue(admin)},
		{"account", buildAccountRecord(account)},
	})
}

// buildChoiceCallRecord:
//
//	data ChoiceCall arg = ChoiceCall with
//	  cid : AnyContractId
//	  arg : arg
//
// `cid` is `AnyContractId`, upstream (MetadataV1.daml) a type synonym for a
// bare contract id, NOT a record:
//
//	type AnyContractId = ContractId AnyContract
//
// so the wire value is a plain Value_ContractId; a { contractId, meta } record
// trips the preprocessor with "mismatching type: ContractId ...:AnyContract and
// value: Record(...)". `arg` is the same choice-argument record the sequential
// path builds, reused verbatim so the two paths can't drift.
func buildChoiceCallRecord(cid string, arg *lapiv2.Value) *lapiv2.Value {
	return recordValue([]field{
		{"cid", contractIDValue(cid)},
		{"arg", arg},
	})
}

// tsaTransferFactoryTransferV2 wraps a TransferFactory_Transfer call as the
// TSA_TransferFactory_TransferV2 variant. factoryCID is the issuer's TokenRules;
// arg is the same argument record the sequential path builds.
func tsaTransferFactoryTransferV2(factoryCID string, arg *lapiv2.Value) batchAction {
	return batchAction{
		Kind:  "transfer_v2",
		Value: variantValue("TSA_TransferFactory_TransferV2", buildChoiceCallRecord(factoryCID, arg)),
	}
}

// tsaTransferInstructionAcceptV2 wraps a TransferInstruction_Accept call as the
// TSA_TransferInstruction_AcceptV2 variant. instructionCID is the pending
// instruction; arg is the same accept argument record the sequential path builds.
func tsaTransferInstructionAcceptV2(instructionCID string, arg *lapiv2.Value) batchAction {
	return batchAction{
		Kind:  "transfer_accept_v2",
		Value: variantValue("TSA_TransferInstruction_AcceptV2", buildChoiceCallRecord(instructionCID, arg)),
	}
}
