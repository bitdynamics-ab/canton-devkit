package token

import (
	"bytes"
	"context"
	"io"
	"testing"

	registry "github.com/bitdynamics-ab/canton-devkit/internal/canton/registry"
	rstate "github.com/bitdynamics-ab/canton-devkit/internal/registry"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// fieldOf returns the value of the first record field labelled `label`, or nil.
func fieldOf(rec *lapiv2.Record, label string) *lapiv2.Value {
	if rec == nil {
		return nil
	}
	for _, f := range rec.Fields {
		if f.Label == label {
			return f.Value
		}
	}
	return nil
}

// TestBuildScopedAccountRecord pins the ScopedAccount wire shape:
// { admin : Party, account : Account }.
func TestBuildScopedAccountRecord(t *testing.T) {
	owner := "bob::abc"
	acct := registry.Account{Owner: &owner, ID: "acc-1"}
	rec := recordOf(buildScopedAccountRecord("issuer::abc", acct))
	if rec == nil {
		t.Fatal("ScopedAccount is not a record")
	}
	if got := partyOf(fieldOf(rec, "admin")); got != "issuer::abc" {
		t.Errorf("admin = %q, want issuer::abc", got)
	}
	inner := recordOf(fieldOf(rec, "account"))
	if inner == nil {
		t.Fatal("account field is not a record")
	}
	if got := optionalPartyOf(fieldOf(inner, "owner")); got != "bob::abc" {
		t.Errorf("account.owner = %q, want bob::abc", got)
	}
	if got := textOf(fieldOf(inner, "id")); got != "acc-1" {
		t.Errorf("account.id = %q, want acc-1", got)
	}
}

// TestBuildChoiceCallRecord pins ChoiceCall = { cid : AnyContractId, arg }
// with `arg` threaded verbatim. AnyContractId is a bare contract id, so
// ChoiceCall.cid must be a plain Value_ContractId, NOT a { contractId, meta } record.
func TestBuildChoiceCallRecord(t *testing.T) {
	arg := textValue("the-arg")
	rec := recordOf(buildChoiceCallRecord("cid-1", arg))
	if rec == nil {
		t.Fatal("ChoiceCall is not a record")
	}
	// cid is a bare ContractId value, not a record.
	if recordOf(fieldOf(rec, "cid")) != nil {
		t.Errorf("ChoiceCall.cid is a record, want a bare ContractId: %#v", fieldOf(rec, "cid").GetSum())
	}
	if got := contractIDOf(fieldOf(rec, "cid")); got != "cid-1" {
		t.Errorf("ChoiceCall.cid = %q, want cid-1", got)
	}
	if got := textOf(fieldOf(rec, "arg")); got != "the-arg" {
		t.Errorf("ChoiceCall.arg not threaded through: %q", got)
	}
}

// TestTSAVariants pins the two TokenStandardAction variants we emit for a
// batched transfer: the constructor names and the wrapped ChoiceCall cid.
func TestTSAVariants(t *testing.T) {
	tr := tsaTransferFactoryTransferV2("factory-cid", textValue("targ"))
	if tr.Kind != "transfer_v2" {
		t.Errorf("transfer kind = %q, want transfer_v2", tr.Kind)
	}
	ctor, inner := variantOf(t, tr.Value)
	if ctor != "TSA_TransferFactory_TransferV2" {
		t.Errorf("transfer ctor = %q, want TSA_TransferFactory_TransferV2", ctor)
	}
	if got := contractIDOf(fieldOf(recordOf(inner), "cid")); got != "factory-cid" {
		t.Errorf("transfer ChoiceCall cid = %q, want factory-cid", got)
	}

	ac := tsaTransferInstructionAcceptV2("instr-cid", textValue("aarg"))
	if ac.Kind != "transfer_accept_v2" {
		t.Errorf("accept kind = %q, want transfer_accept_v2", ac.Kind)
	}
	actor, _ := variantOf(t, ac.Value)
	if actor != "TSA_TransferInstruction_AcceptV2" {
		t.Errorf("accept ctor = %q, want TSA_TransferInstruction_AcceptV2", actor)
	}
}

// genMapOf returns the GenMap under a Value, or nil.
func genMapOf(v *lapiv2.Value) *lapiv2.GenMap {
	if v == nil {
		return nil
	}
	if gm, ok := v.GetSum().(*lapiv2.Value_GenMap); ok {
		return gm.GenMap
	}
	return nil
}

// TestBuildHoldingMapValue pins the HoldingMap encoding: the record
// { byAdminAndAccount : Map ScopedAccount (TextMap [ContractId Holding]) },
// where Map is a GenMap keyed by the ScopedAccount record. Each entry carries
// the account's input holding cids. Empty input → record with empty GenMap.
func TestBuildHoldingMapValue(t *testing.T) {
	owner := "bob::abc"
	in := []scopedHoldings{{
		Admin:       "issuer::abc",
		Account:     registry.Account{Owner: &owner, ID: "acc-1"},
		Instrument:  "CTKC",
		HoldingCIDs: []string{"h1", "h2"},
	}}
	// HoldingMap is a record, not a bare list.
	rec := recordOf(buildHoldingMapValue(in))
	if rec == nil {
		t.Fatalf("HoldingMap is not a record: %#v", buildHoldingMapValue(in).GetSum())
	}
	gm := genMapOf(fieldOf(rec, "byAdminAndAccount"))
	if gm == nil {
		t.Fatalf("byAdminAndAccount is not a GenMap: %#v", fieldOf(rec, "byAdminAndAccount").GetSum())
	}
	if len(gm.Entries) != 1 {
		t.Fatalf("HoldingMap has %d entries, want 1", len(gm.Entries))
	}
	// The GenMap key is the ScopedAccount record; the value the TextMap of cids.
	sa := recordOf(gm.Entries[0].Key)
	if got := partyOf(fieldOf(sa, "admin")); got != "issuer::abc" {
		t.Errorf("entry.key.admin = %q, want issuer::abc", got)
	}
	tm, ok := gm.Entries[0].Value.GetSum().(*lapiv2.Value_TextMap)
	if !ok {
		t.Fatalf("entry.value is not a TextMap: %#v", gm.Entries[0].Value.GetSum())
	}
	if len(tm.TextMap.Entries) != 1 {
		t.Fatalf("holding textmap has %d entries, want 1", len(tm.TextMap.Entries))
	}
	// Keyed by instrument id, not an index — ExecuteBatch does
	// `TextMap.lookup instrumentId.id`.
	if got := tm.TextMap.Entries[0].Key; got != "CTKC" {
		t.Errorf("holding textmap key = %q, want the instrument id CTKC", got)
	}
	cids, ok := tm.TextMap.Entries[0].Value.GetSum().(*lapiv2.Value_List)
	if !ok {
		t.Fatalf("holding list is not a List: %#v", tm.TextMap.Entries[0].Value.GetSum())
	}
	var got []string
	for _, e := range cids.List.Elements {
		got = append(got, contractIDOf(e))
	}
	if !equalStrings(got, []string{"h1", "h2"}) {
		t.Errorf("holding cids = %v, want [h1 h2]", got)
	}

	// Empty input → record with an empty (non-nil) GenMap.
	emptyRec := recordOf(buildHoldingMapValue(nil))
	if emptyRec == nil {
		t.Fatalf("empty HoldingMap is not a record: %#v", buildHoldingMapValue(nil).GetSum())
	}
	emptyGM := genMapOf(fieldOf(emptyRec, "byAdminAndAccount"))
	if emptyGM == nil {
		t.Fatalf("empty byAdminAndAccount is not a GenMap: %#v", fieldOf(emptyRec, "byAdminAndAccount").GetSum())
	}
	if len(emptyGM.Entries) != 0 {
		t.Errorf("empty HoldingMap has %d entries, want 0", len(emptyGM.Entries))
	}
}

// batchTxResponse builds a response carrying one ExecuteBatch exercised event
// whose result record has an actionResults list of `n` elements.
func batchTxResponse(updateID string, n int) *lapiv2.SubmitAndWaitForTransactionResponse {
	results := make([]*lapiv2.Value, n)
	for i := range results {
		results[i] = recordValue(nil) // opaque TokenStandardActionResult
	}
	exResult := recordValue([]field{
		{"actionResults", &lapiv2.Value{Sum: &lapiv2.Value_List{List: &lapiv2.List{Elements: results}}}},
	})
	return &lapiv2.SubmitAndWaitForTransactionResponse{
		Transaction: &lapiv2.Transaction{
			UpdateId: updateID,
			Events: []*lapiv2.Event{{
				Event: &lapiv2.Event_Exercised{Exercised: &lapiv2.ExercisedEvent{
					Choice:         BatchingUtilityExecuteBatchChoiceV2,
					ExerciseResult: exResult,
				}},
			}},
		},
	}
}

// TestParseBatchResult_OneRowPerAction: a committed batch yields OK=true,
// the tx update id, and one BatchActionResult per submitted action (in
// order, labelled by the action's Kind).
func TestParseBatchResult_OneRowPerAction(t *testing.T) {
	actions := []batchAction{
		tsaTransferFactoryTransferV2("f", textValue("t")),
		tsaTransferInstructionAcceptV2("i", textValue("a")),
	}
	res := parseBatchResult(batchTxResponse("upd-1", 2), actions)
	if !res.OK {
		t.Error("res.OK = false; a committed batch is all-or-nothing so OK")
	}
	if res.UpdateID != "upd-1" {
		t.Errorf("UpdateID = %q, want upd-1", res.UpdateID)
	}
	if len(res.Actions) != 2 {
		t.Fatalf("got %d action rows, want 2", len(res.Actions))
	}
	if res.Actions[0].Kind != "transfer_v2" || res.Actions[1].Kind != "transfer_accept_v2" {
		t.Errorf("action kinds = [%q %q], want [transfer_v2 transfer_accept_v2]",
			res.Actions[0].Kind, res.Actions[1].Kind)
	}
	for i, a := range res.Actions {
		if !a.OK {
			t.Errorf("action %d OK = false; want true", i)
		}
	}
}

// TestParseBatchResult_ResultShortfall: when the participant returns fewer
// result elements than actions, the extra rows are flagged (Detail set) but
// the batch is NOT marked failed — it already committed atomically.
func TestParseBatchResult_ResultShortfall(t *testing.T) {
	actions := []batchAction{
		tsaTransferFactoryTransferV2("f", textValue("t")),
		tsaTransferInstructionAcceptV2("i", textValue("a")),
	}
	// Only one result element for two actions.
	res := parseBatchResult(batchTxResponse("upd-2", 1), actions)
	if !res.OK {
		t.Error("res.OK = false; a shortfall must not fail an already-committed batch")
	}
	if len(res.Actions) != 2 {
		t.Fatalf("got %d rows, want 2 (one per action regardless of result count)", len(res.Actions))
	}
	if res.Actions[1].Detail == "" {
		t.Error("second action should carry a shortfall Detail note")
	}
}

// TestParseBatchResult_NoEffects: a response with no exercised event (no
// LEDGER_EFFECTS, or an alpha shape change) still yields one OK row per
// action keyed off the submitted actions — a usable summary, never a panic.
func TestParseBatchResult_NoEffects(t *testing.T) {
	actions := []batchAction{tsaTransferFactoryTransferV2("f", textValue("t"))}
	resp := &lapiv2.SubmitAndWaitForTransactionResponse{
		Transaction: &lapiv2.Transaction{UpdateId: "upd-3"},
	}
	res := parseBatchResult(resp, actions)
	if res.UpdateID != "upd-3" || len(res.Actions) != 1 || !res.Actions[0].OK {
		t.Errorf("no-effects parse = %+v; want one OK transfer_v2 row with update upd-3", res)
	}
}

// TestRunTransfer_AtomicPropagatesToOnLedgerSeam: --atomic must reach the
// on-ledger orchestration via TransferOptions so it can branch to the batch
// path; the off-ledger path must never be taken for an on-ledger instrument.
func TestRunTransfer_AtomicPropagatesToOnLedgerSeam(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	seedToken(t, "demo", "XYZ", "issuer::abc", "on-ledger")

	var gotOpts TransferOptions
	var offCalled bool
	defer swapTransferOnLedger(func(_ context.Context, _ io.Writer, opts TransferOptions, _ rstate.TokenRef) (string, error) {
		gotOpts = opts
		return "", nil
	})()
	defer swapTransferOffLedger(func(context.Context, io.Writer, TransferOptions) error {
		offCalled = true
		return nil
	})()

	err := RunTransfer(context.Background(), &bytes.Buffer{}, TransferOptions{
		Instance: "demo", Instrument: "XYZ",
		From: "bob::abc", To: "issuer::abc", Amount: "10",
		Endpoint: "localhost:1", Role: "app-provider",
		AutoAccept: true, Atomic: true,
	})
	if err != nil {
		t.Fatalf("RunTransfer: %v", err)
	}
	if offCalled {
		t.Fatal("off-ledger path taken for an on-ledger instrument")
	}
	if !gotOpts.Atomic {
		t.Error("Atomic did not propagate to the on-ledger orchestration opts")
	}
	if !gotOpts.AutoAccept {
		t.Error("AutoAccept did not propagate alongside Atomic")
	}
}

// TestBatchResultMirrorsAPIType guards the local batchResult ↔
// api/types.BatchResult mirror: a cheap tripwire against renaming a field here
// (the api/types side is pinned by schema_shape_test).
func TestBatchResultMirrorsAPIType(t *testing.T) {
	r := batchResult{
		UpdateID: "u",
		OK:       true,
		Actions:  []batchActionResult{{Kind: "transfer_v2", OK: true, Detail: "d"}},
	}
	if r.UpdateID != "u" || !r.OK || r.Actions[0].Kind != "transfer_v2" {
		t.Fatal("batchResult field access drifted")
	}
}
