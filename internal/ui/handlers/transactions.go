// follow-up — Transactions + Timeline view backend.
//
// Wraps the Canton Ledger API v2 UpdateService. The Explorer
// screen's Transactions and Timeline tabs are projections over
// the same data; we expose a single endpoint that returns the
// transaction list and let the frontend render either shape.
//
// Returns up to `limit` transactions ending at the participant's
// current ledger end. Reassignments + topology events are
// included as their own row kinds so the timeline can render the
// full activity strip, not just submissions.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

const (
	transactionsRequestTimeout = 15 * time.Second
	transactionsDefaultLimit   = 100
	transactionsMaxLimit       = 1000
)

// MountTransactions installs the Explorer transactions endpoint.
func MountTransactions(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instances/{name}/transactions", handleTransactionsList)
}

// txRow is the unified projection across the GetUpdatesResponse
// oneof. Each row has a kind discriminator so the frontend can
// pick its render path without re-walking the proto.
type txRow struct {
	Kind         string       `json:"kind"` // "transaction" | "reassignment" | "topology" | "checkpoint"
	Offset       int64        `json:"offset"`
	UpdateID     string       `json:"update_id,omitempty"`
	WorkflowID   string       `json:"workflow_id,omitempty"`
	CommandID    string       `json:"command_id,omitempty"`
	RecordTime   string       `json:"record_time,omitempty"`
	Synchronizer string       `json:"synchronizer,omitempty"`
	EventCount   int          `json:"event_count,omitempty"`
	Events       []txEventRow `json:"events,omitempty"`
}

// txEventRow projects a CreatedEvent / ArchivedEvent into a flat
// shape the table + timeline can render directly. We keep the full
// CreatedEvent payload for transactions because the Explorer's
// drawer needs it; ArchivedEvent's contract_id is enough.
type txEventRow struct {
	Kind       string   `json:"kind"` // "create" | "archive" | "exercise"
	ContractID string   `json:"contract_id,omitempty"`
	Template   string   `json:"template,omitempty"`
	Witnesses  []string `json:"witnesses,omitempty"`
}

func handleTransactionsList(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := registry.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance name", err)
		return
	}
	role := r.URL.Query().Get("role")
	if role == "" {
		role = "app-user"
	}
	if !validRole[role] {
		writeErrorWithCode(w, http.StatusBadRequest,
			ErrCodeInvalidRequest,
			"invalid role: "+role,
			"role must be one of app-user, app-provider, sv")
		return
	}
	limit := transactionsDefaultLimit
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			if n > transactionsMaxLimit {
				n = transactionsMaxLimit
			}
			limit = n
		}
	}

	state, err := registry.Read(name)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			writeErrorWithCode(w, http.StatusNotFound,
				ErrCodeNotFound,
				"instance "+name+" not registered")
			return
		}
		writeError(w, http.StatusInternalServerError, "read state", err)
		return
	}
	portKey := "participant_ledger_" + role
	ledgerPort, hasPort := state.Ports[portKey]
	if !hasPort || ledgerPort == 0 {
		writeErrorWithCode(w, http.StatusServiceUnavailable,
			"PARTICIPANT_PORT_NOT_RECORDED",
			"instance "+name+" was started before participant ports were recorded",
			"restart the instance to capture all Canton API ports")
		return
	}
	cred, hasCred := state.Credentials[role]
	if !hasCred {
		writeError(w, http.StatusInternalServerError,
			"no JWT recorded for role "+role,
			fmt.Errorf("missing credential for role %q", role))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), transactionsRequestTimeout)
	defer cancel()

	client, err := ledger.Dial(ctx, ledger.DialOptions{
		Endpoint:  "localhost:" + strconv.Itoa(ledgerPort),
		Token:     ledger.StaticToken(cred.JWT),
		PlainText: true,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "dial canton ledger", err)
		return
	}
	defer func() { _ = client.Close() }()

	// resolve user-id → party-rights set so the filter
	// matches what the JWT can actually see.
	//
	// Disambiguate the three failure modes (mirrors
	// handlers/contracts.go):
	//   - transport / dial error  → 502 BAD_GATEWAY
	//   - PermissionDenied        → 503 EXPLORER_NEEDS_PARTY_JWT
	//   - no party rights granted → 503 EXPLORER_NEEDS_PARTY_JWT
	parties, err := client.ResolveActAndReadParties(ctx)
	if err != nil {
		if isPermissionDenied(err) {
			writeErrorWithCode(w, http.StatusServiceUnavailable,
				"EXPLORER_NEEDS_PARTY_JWT",
				"participant denied user-rights lookup",
				"the JWT's user has no party-rights — grant actAs/readAs via UserManagementService")
			return
		}
		writeError(w, http.StatusBadGateway, "resolve party rights", err)
		return
	}
	if len(parties) == 0 {
		writeErrorWithCode(w, http.StatusServiceUnavailable,
			"EXPLORER_NEEDS_PARTY_JWT",
			"this JWT has no party-rights",
			"grant actAs/readAs rights to the user via UserManagementService")
		return
	}
	byParty := make(map[string]*lapiv2.Filters, len(parties))
	for _, p := range parties {
		byParty[p] = &lapiv2.Filters{}
	}
	end, err := client.LedgerEnd(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, "ledger end probe", err)
		return
	}

	endInc := end.Offset
	// Stream context is derived from the request context so we can
	// cancel mid-iteration when we've reached `limit` without
	// disturbing the rest of the handler (the parent ctx still
	// drives writeJSON). Without this we'd churn rows forever on a
	// busy participant — the original `rows = rows[len(rows)-limit:]`
	// reslice never broke the loop.
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	stream, err := client.Updates(streamCtx, ledger.UpdatesRequest{
		BeginExclusive: 0,
		EndInclusive:   &endInc,
		UpdateFormat: &lapiv2.UpdateFormat{
			IncludeTransactions: &lapiv2.TransactionFormat{
				EventFormat: &lapiv2.EventFormat{
					FiltersByParty: byParty,
					Verbose:        true,
				},
				TransactionShape: lapiv2.TransactionShape_TRANSACTION_SHAPE_ACS_DELTA,
			},
		},
	})
	if err != nil {
		if isPermissionDenied(err) {
			writeErrorWithCode(w, http.StatusServiceUnavailable,
				"EXPLORER_NEEDS_PARTY_JWT",
				"participant denied the updates stream",
				"the JWT's party rights don't grant read access — see ")
			return
		}
		writeError(w, http.StatusBadGateway, "open updates stream", err)
		return
	}

	// Fixed-size ring buffer.
	//
	// Why we DON'T break at `count >= limit`:
	// ------------------------------------------------------------
	// Updates returns rows in ASCENDING offset order. We want the
	// NEWEST `limit` transactions (that's what the table renders).
	// Breaking at the first time `count == limit` would give us
	// the OLDEST `limit` rows in the window, not the newest — a
	// silent semantic regression the user would notice as "the
	// Explorer always shows the same ancient transactions, not my
	// recent ones." (Contracts/ACS doesn't have this trade-off:
	// ACS is a SNAPSHOT, not a time-ordered stream, so its
	// `break at limit` is correct.)
	//
	// What we DO bound:
	//   1. Memory — fixed ring of `limit` rows; new arrivals
	//      overwrite the oldest. O(limit) regardless of history.
	//   2. Loop iterations — `streamCap` is a hard wall (sized at
	//      max(100*limit, 10_000) so a busy ledger doesn't spin
	//      forever, but generous enough that we drain typical
	//      LocalNet histories naturally to EOF).
	//   3. Wall clock — `streamCtx` is derived from the request
	//      ctx (which has a 15s timeout); we poll ctx.Err() on
	//      every item so a slow upstream surfaces promptly as
	//      DeadlineExceeded.
	//
	// The "stop when we have what we need" approach is
	// genuinely incompatible with newest-N semantics on a
	// strictly-ascending stream. If a future Canton release adds
	// reverse-iteration (or a server-side "latest N" RPC), we can
	// move to a real early-break path.
	streamCap := 100 * limit
	if streamCap < 10_000 {
		streamCap = 10_000
	}
	buf := make([]txRow, limit)
	head, count := 0, 0
	processed := 0
	for item := range stream {
		// Honour parent-context cancellation promptly — a slow
		// upstream shouldn't keep the goroutine alive past the
		// request timeout.
		if err := streamCtx.Err(); err != nil {
			break
		}
		if item.Err != nil {
			if errors.Is(item.Err, io.EOF) {
				break
			}
			// Treat our own cancellation (DeadlineExceeded /
			// Canceled) as a non-error termination — we have
			// whatever rows fit in the ring.
			if errors.Is(item.Err, context.Canceled) || errors.Is(item.Err, context.DeadlineExceeded) {
				break
			}
			if isPermissionDenied(item.Err) {
				writeErrorWithCode(w, http.StatusServiceUnavailable,
					"EXPLORER_NEEDS_PARTY_JWT",
					"participant denied mid-stream",
					"the JWT's party rights don't grant read access")
				return
			}
			writeError(w, http.StatusBadGateway, "updates stream", item.Err)
			return
		}
		if row := projectUpdate(item.Value); row != nil {
			buf[head] = *row
			head = (head + 1) % limit
			if count < limit {
				count++
			}
		}
		processed++
		if processed >= streamCap {
			cancelStream()
			break
		}
	}
	// Unwrap the ring into chronological order (oldest first), then
	// reverse below to newest-first for the table.
	rows := make([]txRow, 0, count)
	start := (head - count + limit) % limit
	for i := 0; i < count; i++ {
		rows = append(rows, buf[(start+i)%limit])
	}

	// Reverse so newest is first — the table reads top-to-bottom.
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": 1,
		"instance":       name,
		"role":           role,
		"ledger_end":     endInc,
		"transactions":   rows,
		"count":          len(rows),
	})
}

// projectUpdate folds the GetUpdatesResponse oneof into a unified
// txRow. Returns nil for entries we don't surface (currently only
// OffsetCheckpoint, since those are heartbeats not state changes).
func projectUpdate(resp *lapiv2.GetUpdatesResponse) *txRow {
	if resp == nil {
		return nil
	}
	if t := resp.GetTransaction(); t != nil {
		events := make([]txEventRow, 0, len(t.GetEvents()))
		for _, e := range t.GetEvents() {
			if c := e.GetCreated(); c != nil {
				events = append(events, txEventRow{
					Kind:       "create",
					ContractID: c.GetContractId(),
					Template:   formatTemplateID(c.GetTemplateId()),
					Witnesses:  c.GetWitnessParties(),
				})
				continue
			}
			if a := e.GetArchived(); a != nil {
				events = append(events, txEventRow{
					Kind:       "archive",
					ContractID: a.GetContractId(),
					Template:   formatTemplateID(a.GetTemplateId()),
					Witnesses:  a.GetWitnessParties(),
				})
				continue
			}
			if ex := e.GetExercised(); ex != nil {
				events = append(events, txEventRow{
					Kind:       "exercise",
					ContractID: ex.GetContractId(),
					Template:   formatTemplateID(ex.GetTemplateId()),
					Witnesses:  ex.GetWitnessParties(),
				})
			}
		}
		row := txRow{
			Kind:         "transaction",
			Offset:       t.GetOffset(),
			UpdateID:     t.GetUpdateId(),
			WorkflowID:   t.GetWorkflowId(),
			CommandID:    t.GetCommandId(),
			Synchronizer: t.GetSynchronizerId(),
			EventCount:   len(events),
			Events:       events,
		}
		if t.GetRecordTime() != nil {
			row.RecordTime = t.GetRecordTime().AsTime().Format(time.RFC3339Nano)
		}
		return &row
	}
	if rs := resp.GetReassignment(); rs != nil {
		row := txRow{
			Kind:       "reassignment",
			Offset:     rs.GetOffset(),
			UpdateID:   rs.GetUpdateId(),
			WorkflowID: rs.GetWorkflowId(),
			CommandID:  rs.GetCommandId(),
		}
		if rs.GetRecordTime() != nil {
			row.RecordTime = rs.GetRecordTime().AsTime().Format(time.RFC3339Nano)
		}
		return &row
	}
	if tp := resp.GetTopologyTransaction(); tp != nil {
		row := txRow{
			Kind:     "topology",
			Offset:   tp.GetOffset(),
			UpdateID: tp.GetUpdateId(),
		}
		if tp.GetRecordTime() != nil {
			row.RecordTime = tp.GetRecordTime().AsTime().Format(time.RFC3339Nano)
		}
		return &row
	}
	// OffsetCheckpoint and unspecified kinds are skipped.
	return nil
}
