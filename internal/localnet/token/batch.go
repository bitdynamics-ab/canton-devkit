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
// all-or-nothing instead of as two sequential submits.
//
// SAFETY / TRADEOFF (load-bearing): batching is atomic — if any action in
// the batch fails, the WHOLE transaction rolls back and nothing lands.
// That is stronger than the sequential path but REMOVES the partial-
// recovery model: with the sequential flow a transfer offer that commits
// but whose accept then fails leaves a retryable pending instruction; with
// a batch there is no intermediate state to retry, only the original
// holdings. Because of that we make batching OPT-IN (TransferOptions.Atomic)
// and keep the sequential path as the default, so operators who want the
// resumable offer→accept two-step still get it.
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

// batchResult / batchActionResult MIRROR internal/api/types.BatchResult /
// BatchActionResult byte-for-byte (same JSON tags); the local copy keeps
// this package free of an api/types import, exactly as BalanceRow mirrors
// api/types.TokenHolding. Adding a field requires updating both (the
// schema_shape_test golden pins the api/types side).
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

// batchAction is one step of a batch: the token-standard action variant
// plus a human label for the result row. The value is a pre-built
// TokenStandardAction Daml value (a TSA_* variant wrapping a ChoiceCall).
type batchAction struct {
	// Kind is the api/types.BatchActionResult.Kind label (e.g.
	// "transfer_v2", "transfer_accept_v2").
	Kind string
	// Value is the encoded TokenStandardAction variant for this action.
	Value *lapiv2.Value
}

// scopedHoldings pairs a ScopedAccount with the holding contract ids
// threaded into the batch for that account. buildHoldingMapValue turns a
// slice of these into the HoldingMap record's byAdminAndAccount GenMap
// (ScopedAccount → TextMap [ContractId Holding]).
type scopedHoldings struct {
	// Admin is the instrument admin (ScopedAccount.admin).
	Admin string
	// Account is the V2 account the holdings belong to.
	Account registry.Account
	// Instrument is the instrument id (InstrumentId.id). It keys the
	// per-account holdings TextMap: ExecuteBatch looks holdings up by
	// `TextMap.lookup instrumentId.id` (getHoldingsForInstrument), so this
	// MUST equal the transfer's instrumentId.id or the batch finds no
	// holdings and interpretation fails with inputAmount = 0.
	Instrument string
	// HoldingCIDs are the input Holding contract ids for this account.
	HoldingCIDs []string
}

// ensureBatchingUtility returns the user's BatchingUtility contract id,
// creating one if none exists yet. Mirrors ensureTokenRules: the user is
// the sole signatory, so a plain single-party create suffices and one
// utility per user is enough to run every batch that user submits.
//
// Idempotent — a re-run reuses the existing utility rather than spawning a
// duplicate. Returns "" only on error.
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
// id, or "" when none exists. ACS-queries by the wallet package's template
// filter (same #package-name form ensureTokenRules uses for TokenRules).
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

// createBatchingUtility creates the user-owned BatchingUtility contract.
// Single-signer (signatory user), so a plain submit-and-wait as the user
// suffices. Returns the created contract id. Needs a concrete package id
// (CreateCommand does not resolve the #package-name reference), resolved at
// runtime so we survive the alpha's snapshot rotation.
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

// executeBatch runs a list of token-standard actions atomically. It ensures
// the user's (nonconsuming, reusable) BatchingUtility exists, then exercises
// BatchingUtility_ExecuteBatch on it, threading the given input holdings
// through the batch's HoldingMap. All actions commit all-or-nothing in that
// one exercise; a failure rolls back every action (see the SAFETY note at
// the top of this file). actAs must cover every party whose authority the
// batched choices need (the batch runs the inner actions with the
// submitter's authority, so on LocalNet we pass the union of the
// sender/receiver/admin parties, as the sequential path does) plus the
// utility's signatory (user).
//
// Returns the batch outcome mapped to a types-shaped batchResult (the
// package-local mirror of api/types.BatchResult that keeps this package free
// of an api/types import, same pattern BalanceRow uses).
func executeBatch(
	ctx context.Context,
	client *ledger.Client,
	user string,
	actAs []string,
	inputHoldings []scopedHoldings,
	actions []batchAction,
	archiveAfterExecution bool,
) (batchResult, error) {
	// Reuse-or-create the user's utility once (like ensureTokenRules). The
	// utility is nonconsuming, so it survives every batch — the atomicity we
	// care about is over the actions, not the utility create.
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

// parseBatchResult maps an ExecuteBatch transaction response to a
// batchResult. It walks the transaction's exercised events for the
// BatchingUtility_ExecuteBatch node and reads actionResults off its
// exercise result; when the result can't be read (participant returned no
// effects, or an alpha snapshot changed the result record) it falls back to
// one OK row per submitted action so the caller still gets a usable summary
// keyed on the actions it sent.
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

	// actionResults is a [TokenStandardActionResult]; we surface one
	// BatchActionResult per submitted action, in order. The result variant
	// shapes differ per action and aren't frozen across alpha snapshots, so
	// we key the row label off the action we sent and treat a present result
	// element as OK (the whole batch already committed atomically — a failed
	// action would have rolled the transaction back, so reaching here means
	// every action succeeded).
	results := actionResultsList(resultRec)
	for i, a := range actions {
		row := batchActionResult{Kind: a.Kind, OK: true}
		if results != nil && i >= len(results) {
			// Fewer results than actions — flag the mismatch without failing
			// the (already-committed) batch.
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

// ledgerEffectsFormat requests the LEDGER_EFFECTS transaction shape for the
// first actAs party so the response carries exercised (choice) events —
// needed to read the batch's actionResults. ACS_DELTA omits exercise nodes.
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
// TODO(BIT-token-v2): the deep TokenStandardAction / AnyContractId record
// shapes below are hand-built from the 0.6.12 confirmed signatures but NOT
// yet exercised against a live token-standard-v2 ledger (no live participant
// in this worktree). Verify the exact record field order/labels for each
// TSA_* variant's ChoiceCall when the alpha e2e gate lands; the batch path is
// opt-in (TransferOptions.Atomic) so the default flow is unaffected until
// then.

// buildHoldingMapValue encodes the HoldingMap the batch threads its input
// holdings through. BatchingUtility_ExecuteBatch's `inputHoldingMap` field is
// the HoldingMap RECORD, not the list holdingMapFromList consumes. Upstream
// (Splice.Util.Token.Wallet.BatchingUtilityV2):
//
//	data HoldingMap = HoldingMap with
//	  byAdminAndAccount : Map.Map ScopedAccount (TextMap.TextMap [ContractId V2.Holding])
//
// `Map.Map` is DA.Map → wire-encoded as a GenMap keyed by the ScopedAccount
// record (Ord); `TextMap.TextMap` is DA.TextMap → wire-encoded as a TextMap.
// The previous list-of-tuples encoding tripped the preprocessor with
// "mismatching type: ...:HoldingMap and value: List(...)". An empty input
// yields the record with an empty GenMap (self-funded actions that carry
// their own holdings in the action arg).
//
// The inner TextMap is keyed by the INSTRUMENT ID, not an arbitrary index:
// ExecuteBatch resolves a transfer's holdings with
// `TextMap.lookup instrumentId.id` (getHoldingsForInstrument), so an index
// key ("0") would miss and the batched transfer would see inputAmount = 0.
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
// `cid` is `AnyContractId`, which upstream (MetadataV1.daml) is a type
// SYNONYM for a bare contract id, NOT a record:
//
//	type AnyContractId = ContractId AnyContract
//
// so the wire value is a plain Value_ContractId. Encoding it as a
// { contractId, meta } record tripped the preprocessor with "mismatching
// type: ContractId ...:AnyContract and value: Record(...)". `arg` is the
// same choice-argument record the sequential path builds for the
// corresponding on-ledger exercise (e.g. the TransferFactory_Transfer arg),
// reused verbatim so the two paths can't drift.
func buildChoiceCallRecord(cid string, arg *lapiv2.Value) *lapiv2.Value {
	return recordValue([]field{
		{"cid", contractIDValue(cid)},
		{"arg", arg},
	})
}

// tsaTransferFactoryTransferV2 wraps a TransferFactory_Transfer call as the
// TSA_TransferFactory_TransferV2 TokenStandardAction variant. cid is the
// factory (the issuer's TokenRules) contract id; arg is the same
// TransferFactory_Transfer argument record the sequential on-ledger path
// builds.
func tsaTransferFactoryTransferV2(factoryCID string, arg *lapiv2.Value) batchAction {
	return batchAction{
		Kind:  "transfer_v2",
		Value: variantValue("TSA_TransferFactory_TransferV2", buildChoiceCallRecord(factoryCID, arg)),
	}
}

// tsaTransferInstructionAcceptV2 wraps a TransferInstruction_Accept call as
// the TSA_TransferInstruction_AcceptV2 variant. cid is the pending
// instruction; arg is the same accept argument record the sequential path
// builds.
func tsaTransferInstructionAcceptV2(instructionCID string, arg *lapiv2.Value) batchAction {
	return batchAction{
		Kind:  "transfer_accept_v2",
		Value: variantValue("TSA_TransferInstruction_AcceptV2", buildChoiceCallRecord(instructionCID, arg)),
	}
}
