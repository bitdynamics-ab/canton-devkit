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

	// transactionsWindowSpan is how many offsets back from the ledger
	// end the list scans by default. Canton offsets are
	// participant-global (topology events, checkpoints, and other
	// parties' transactions consume offsets between the caller's
	// matches), so the window is generous enough that a filtered
	// query still finds recent matches, while keeping the scan
	// O(window) instead of O(full-history-from-0). Mirrors the CLI's
	// defaultTxWindowSpan in internal/cli/localnet/contracts.go.
	transactionsWindowSpan = 10_000
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
	// Disambiguate the three failure modes (review blocker #5,
	// mirrors handlers/contracts.go):
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
	// Lower-bound the scan at a recent offset instead of 0.
	//
	// The old floor of BeginExclusive:0 re-scanned the WHOLE ledger
	// on every request and, once history exceeded streamCap, the
	// ascending stream meant we processed only the OLDEST streamCap
	// updates and returned "newest N of the oldest streamCap" — i.e.
	// ancient activity labelled "newest first" — with HTTP 200.
	// Splice LocalNet continuously emits amulet round/system
	// transactions, so an instance left up for days easily crosses
	// that threshold. Since we already know the ledger end, we floor
	// the window at end-transactionsWindowSpan: a generous recent
	// window that captures the newest matches without O(history) work
	// on every tab switch.
	beginExclusive := endInc - transactionsWindowSpan
	if beginExclusive < 0 {
		beginExclusive = 0
	}
	// Stream context is derived from the request context so we can
	// cancel mid-iteration when we've reached `limit` without
	// disturbing the rest of the handler (the parent ctx still
	// drives writeJSON). Without this we'd churn rows forever on a
	// busy participant — the original `rows = rows[len(rows)-limit:]`
	// reslice never broke the loop. (Review blocker #4.)
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	stream, err := client.Updates(streamCtx, ledger.UpdatesRequest{
		BeginExclusive: beginExclusive,
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

	// Fixed-size ring buffer (review blocker #4; tightened in
	// follow-up).
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
	//   1. Window — BeginExclusive floors the scan at a recent offset
	//      (see above), so this is O(window) not O(history).
	//   2. Memory — fixed ring of `limit` rows; new arrivals
	//      overwrite the oldest. O(limit) regardless of window size.
	//   3. Loop iterations — `streamCap` is a hard wall (sized at
	//      max(100*limit, 10_000)). With the bounded window it should
	//      almost never fire; when it does we set windowTruncated so
	//      the client knows the result is the newest N of a clipped
	//      scan, not silently-stale data.
	//   4. Wall clock — `streamCtx` is derived from the request
	//      ctx (which has a 15s timeout); we poll ctx.Err() on
	//      every item so a slow upstream surfaces promptly as
	//      DeadlineExceeded (also flagged as windowTruncated).
	streamCap := 100 * limit
	if streamCap < 10_000 {
		streamCap = 10_000
	}
	buf := make([]txRow, limit)
	head, count := 0, 0
	processed := 0
	// windowTruncated is true when we stopped before draining the
	// window to EOF — either the streamCap wall or a mid-scan
	// deadline. Surfaced to the client so the Explorer can show a
	// "showing a partial window" hint instead of presenting clipped
	// data as the complete recent history.
	windowTruncated := false
	for item := range stream {
		// Honour parent-context cancellation promptly — a slow
		// upstream shouldn't keep the goroutine alive past the
		// request timeout.
		if err := streamCtx.Err(); err != nil {
			windowTruncated = true
			break
		}
		if item.Err != nil {
			if errors.Is(item.Err, io.EOF) {
				break
			}
			// Treat our own cancellation (DeadlineExceeded /
			// Canceled) as a non-error termination — we have
			// whatever rows fit in the ring, but flag that the scan
			// didn't reach EOF.
			if errors.Is(item.Err, context.Canceled) || errors.Is(item.Err, context.DeadlineExceeded) {
				windowTruncated = true
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
			windowTruncated = true
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
		// scanned_from is the lower bound (exclusive) of the offset
		// window we inspected; window_truncated is true when we
		// stopped before reaching EOF (streamCap / deadline), so the
		// rows are the newest of a clipped scan rather than the
		// complete recent history.
		"scanned_from":     beginExclusive,
		"window_truncated": windowTruncated,
	})
}

// eventKindAbbrev maps the shared projection's past-tense EventKind
// ("created"/"archived"/"exercised") to the abbreviated form this
// handler has always emitted on the wire ("create"/"archive"/
// "exercise"). Kept stable so the frontend's existing render path
// doesn't break; the CLI uses the past-tense form directly.
func eventKindAbbrev(k ledger.EventKind) string {
	switch k {
	case ledger.EventCreated:
		return "create"
	case ledger.EventArchived:
		return "archive"
	case ledger.EventExercised:
		return "exercise"
	default:
		return string(k)
	}
}

// projectUpdate folds the GetUpdatesResponse oneof into a unified
// txRow. Returns nil for entries we don't surface (currently only
// OffsetCheckpoint, since those are heartbeats not state changes).
func projectUpdate(resp *lapiv2.GetUpdatesResponse) *txRow {
	if resp == nil {
		return nil
	}
	if t := resp.GetTransaction(); t != nil {
		// Shared per-event decoder (ledger.ProjectTransactionEvents) so
		// the CLI Explorer (`contracts watch` / `tx ls`) and this
		// handler agree on the create/archive/exercise projection.
		summaries := ledger.ProjectTransactionEvents(t)
		events := make([]txEventRow, 0, len(summaries))
		for _, s := range summaries {
			events = append(events, txEventRow{
				Kind:       eventKindAbbrev(s.Kind),
				ContractID: s.ContractID,
				Template:   s.TemplateID,
				Witnesses:  s.Witnesses,
			})
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
