// Web UI tx-replay handler — the per-party visibility projection.
//
// GET /api/instances/{name}/transactions/{update_id}/replay
//
//	?role=<app-user|app-provider|sv>   (default app-provider)
//	?party=<id>[&party=…]              (default: the JWT's own parties)
//
// Fetches one transaction by its update id with the LEDGER_EFFECTS
// shape (exercised choices, not just the ACS delta) and projects it
// through the requested party set. Querying the SAME transaction as
// different parties yields different event sets — that is the
// per-party visibility projection. The CLI's `dpm localnet tx replay
// --id <id>` is the mirror verb; both build their wire rows from
// ledger.ProjectReplayEvents so they can never drift.
package handlers

import (
	"context"
	"net/http"
	"time"

	apitypes "github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func handleTxReplay(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := registry.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance name", err)
		return
	}
	updateID := r.PathValue("update_id")
	if updateID == "" {
		writeErrorWithCode(w, http.StatusBadRequest,
			ErrCodeInvalidRequest, "update_id is required")
		return
	}
	role := r.URL.Query().Get("role")
	if role == "" {
		role = "app-provider"
	}
	if !validRole[role] {
		writeErrorWithCode(w, http.StatusBadRequest,
			ErrCodeInvalidRequest,
			"invalid role: "+role,
			"role must be one of app-user, app-provider, sv")
		return
	}
	// Explicit ?party overrides the JWT-derived projection so a user
	// can ask "what did party P see in this transaction?" — the whole
	// point of replay. Comma + repeat semantics match the list handler.
	reqParties := splitCSV(r.URL.Query()["party"])

	endpoint, tok, ok := resolveLedgerForRole(w, name, role)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), transactionsRequestTimeout)
	defer cancel()

	client, err := ledger.Dial(ctx, ledger.DialOptions{
		Endpoint:  endpoint,
		Token:     tok,
		PlainText: true,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "dial canton ledger", err)
		return
	}
	defer func() { _ = client.Close() }()

	// Resolve the party set the projection runs through. An explicit
	// ?party is honoured verbatim; otherwise project through the JWT's
	// own act/read parties (Splice signs user-id tokens, so a bare
	// wildcard PermissionDenies).
	effParties := reqParties
	if len(effParties) == 0 {
		resolved, err := client.ResolveActAndReadParties(ctx)
		if err != nil {
			if isPermissionDenied(err) {
				writeErrorWithCode(w, http.StatusServiceUnavailable,
					"EXPLORER_NEEDS_PARTY_JWT",
					"participant denied user-rights lookup",
					"grant actAs/readAs via UserManagementService")
				return
			}
			writeError(w, http.StatusBadGateway, "resolve user rights", err)
			return
		}
		if len(resolved) == 0 {
			writeErrorWithCode(w, http.StatusServiceUnavailable,
				"EXPLORER_NEEDS_PARTY_JWT",
				"this JWT has no party-rights",
				"grant actAs/readAs rights to the user via UserManagementService")
			return
		}
		effParties = resolved
	}

	// LEDGER_EFFECTS so exercised choices are visible, not just the
	// ACS delta — shared filter builder so ?party behaves like the
	// CLI's --party.
	uf := ledger.BuildUpdateFormat(effParties, nil, true,
		lapiv2.TransactionShape_TRANSACTION_SHAPE_LEDGER_EFFECTS)

	resp, err := client.UpdateById(ctx, &lapiv2.GetUpdateByIdRequest{
		UpdateId:     updateID,
		UpdateFormat: uf,
	})
	if err != nil {
		// NotFound means no transaction at that id is visible to this
		// party set — render "not visible" rather than a generic 502.
		if s, ok := status.FromError(err); ok && s.Code() == codes.NotFound {
			writeErrorWithCode(w, http.StatusNotFound,
				ErrCodeNotFound,
				"transaction not visible to this party set")
			return
		}
		if isPermissionDenied(err) {
			writeErrorWithCode(w, http.StatusServiceUnavailable,
				"EXPLORER_NEEDS_PARTY_JWT",
				"participant denied the update lookup",
				"the JWT's party rights don't grant read access")
			return
		}
		writeError(w, http.StatusBadGateway, "update by id", err)
		return
	}
	txn := resp.GetTransaction()
	if txn == nil {
		// A reassignment/topology update at this id has no event tree
		// to replay — same "not a transaction" shape the CLI returns.
		writeErrorWithCode(w, http.StatusNotFound,
			ErrCodeNotFound,
			"no transaction at the requested update id")
		return
	}

	events := projectReplayRows(txn)
	out := apitypes.TxReplayResponse{
		SchemaVersion: apitypes.SchemaVersion,
		Instance:      name,
		Parties:       effParties,
		UpdateID:      txn.GetUpdateId(),
		Offset:        txn.GetOffset(),
		WorkflowID:    txn.GetWorkflowId(),
		EventCount:    len(events),
		Events:        events,
	}
	if txn.GetEffectiveAt() != nil {
		out.EffectiveAt = txn.GetEffectiveAt().AsTime().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, out)
}

// projectReplayRows maps the shared ledger.ReplayEvent projection to
// the wire shape. Shared with the CLI `tx replay` (renderTxReplay)
// via ledger.ProjectReplayEvents.
func projectReplayRows(txn *lapiv2.Transaction) []apitypes.TxReplayEvent {
	summaries := ledger.ProjectReplayEvents(txn)
	rows := make([]apitypes.TxReplayEvent, 0, len(summaries))
	for _, s := range summaries {
		rows = append(rows, apitypes.TxReplayEvent{
			Kind:          string(s.Kind),
			NodeID:        s.NodeID,
			ContractID:    s.ContractID,
			TemplateID:    s.TemplateID,
			Choice:        s.Choice,
			ActingParties: s.ActingParties,
			Consuming:     s.Consuming,
			Signatories:   s.Signatories,
			Observers:     s.Observers,
		})
	}
	return rows
}
