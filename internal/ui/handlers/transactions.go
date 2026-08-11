// Transactions + Timeline view backend.
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
	"strings"
	"time"

	apitypes "github.com/bitdynamics-ab/canton-devkit/internal/api/types"
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

// MountTransactions installs the Explorer transactions endpoints.
//
//   - GET /api/instances/{name}/transactions
//     — bounded list (party/template/from/to filters, like CLI `tx ls`)
//   - GET /api/instances/{name}/transactions/{update_id}/replay
//     — per-party visibility projection of one transaction (CLI `tx replay`)
func MountTransactions(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instances/{name}/transactions", handleTransactionsList)
	mux.HandleFunc("GET /api/instances/{name}/transactions/{update_id}/replay", handleTxReplay)
}

// The transaction list row + event shapes are shared with the CLI
// `tx ls --format json` via internal/api/types
// (apitypes.TransactionRow / TransactionEvent / TransactionsListResponse),
// so the two surfaces can never drift.

func handleTransactionsList(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := registry.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance name", err)
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
	limit := transactionsDefaultLimit
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			if n > transactionsMaxLimit {
				n = transactionsMaxLimit
			}
			limit = n
		}
	}

	// Filters — mirror the CLI `tx ls`'s --party / --template /
	// --from / --to. `party` / `template` are repeatable
	// (?party=a&party=b) AND comma-splittable; `from` / `to` are
	// inclusive/exclusive offset bounds. Validate them BEFORE any
	// gRPC work so a bad value fails 400 rather than 502.
	reqParties := splitCSV(r.URL.Query()["party"])
	reqTemplates := splitCSV(r.URL.Query()["template"])
	if _, err := ledger.BuildTemplateFilters(reqTemplates); err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest,
			"invalid template filter: "+err.Error(),
			"template accepts #pkg-name:Module:Entity (package-name reference) or <pkg-id>:Module:Entity (exact pin)")
		return
	}
	fromOff, fromSet, ferr := parseOffsetParam(r.URL.Query().Get("from"))
	if ferr != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest,
			"invalid from: "+ferr.Error())
		return
	}
	toOff, toSet, terr := parseOffsetParam(r.URL.Query().Get("to"))
	if terr != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest,
			"invalid to: "+terr.Error())
		return
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

	// Party scope: an explicit ?party is honoured verbatim (mirrors the
	// CLI's `tx ls --party`). Otherwise — including a ?template with no
	// ?party — project through the JWT's own act/read parties. Splice
	// LocalNet signs user-id JWTs by default, so a bare wildcard would be
	// PermissionDenied even with a template filter; resolving the user's
	// concrete parties and applying the template per party is the shape
	// that returns data.
	//
	// Disambiguate the three failure modes (mirrors
	// handlers/contracts.go):
	//   - transport / dial error  → 502 BAD_GATEWAY
	//   - PermissionDenied        → 503 EXPLORER_NEEDS_PARTY_JWT
	//   - no party rights granted → 503 EXPLORER_NEEDS_PARTY_JWT
	effParties := reqParties
	if len(reqParties) == 0 {
		resolved, err := client.ResolveActAndReadParties(ctx)
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
		if len(resolved) == 0 {
			writeErrorWithCode(w, http.StatusServiceUnavailable,
				"EXPLORER_NEEDS_PARTY_JWT",
				"this JWT has no party-rights",
				"grant actAs/readAs rights to the user via UserManagementService")
			return
		}
		effParties = resolved
	}
	end, err := client.LedgerEnd(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, "ledger end probe", err)
		return
	}

	// Offset window. Mirrors the CLI's resolveOffsetWindow precedence:
	//   - ?to set  → that exact end; otherwise the current ledger end.
	//   - ?from set → that exact begin (so from=0 reads from genesis).
	//   - ?from unset → a generous recent window
	//     (end - transactionsWindowSpan), independent of limit, so a
	//     sparse filtered match is still found without O(history) work.
	endInc := end.Offset
	if toSet {
		endInc = toOff
	}
	var beginExclusive int64
	if fromSet {
		beginExclusive = fromOff
	} else {
		beginExclusive = endInc - transactionsWindowSpan
		if beginExclusive < 0 {
			beginExclusive = 0
		}
	}
	if beginExclusive > endInc {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest,
			fmt.Sprintf("from %d must be <= to %d", beginExclusive, endInc))
		return
	}
	// Stream context is derived from the request context so we can
	// cancel mid-iteration at the streamCap wall without disturbing
	// the rest of the handler (the parent ctx still drives writeJSON).
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	stream, err := client.Updates(streamCtx, ledger.UpdatesRequest{
		BeginExclusive: beginExclusive,
		EndInclusive:   &endInc,
		// Shared filter builder so ?party/?template behave exactly like
		// the CLI's flags. ACS_DELTA = the flat create/archive
		// projection the table renders.
		UpdateFormat: ledger.BuildUpdateFormat(effParties, reqTemplates, true,
			lapiv2.TransactionShape_TRANSACTION_SHAPE_ACS_DELTA),
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
	// Why we DON'T break at `count >= limit`: Updates returns rows in
	// ASCENDING offset order, and we want the NEWEST `limit`
	// transactions (that's what the table renders). Breaking at
	// `count == limit` would return the OLDEST rows in the window.
	// (Contracts/ACS doesn't have this trade-off: ACS is a snapshot,
	// not a time-ordered stream, so its break-at-limit is correct.)
	//
	// What we DO bound:
	//   1. Window — BeginExclusive floors the scan at a recent offset
	//      (see above), so this is O(window) not O(history).
	//   2. Memory — fixed ring of `limit` rows; new arrivals
	//      overwrite the oldest.
	//   3. Loop iterations — `streamCap` is a hard wall (sized at
	//      max(100*limit, 10_000)); when it fires we set
	//      windowTruncated so the client knows the scan was clipped.
	//   4. Wall clock — `streamCtx` inherits the request's 15s
	//      timeout; ctx.Err() is polled on every item.
	streamCap := 100 * limit
	if streamCap < 10_000 {
		streamCap = 10_000
	}
	buf := make([]apitypes.TransactionRow, limit)
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
	rows := make([]apitypes.TransactionRow, 0, count)
	start := (head - count + limit) % limit
	for i := 0; i < count; i++ {
		rows = append(rows, buf[(start+i)%limit])
	}

	// Reverse so newest is first — the table reads top-to-bottom.
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}

	// scanned_from is the lower bound (exclusive) of the offset window
	// inspected; window_truncated is true when we stopped before
	// reaching EOF (streamCap / deadline), so the rows are the newest
	// of a clipped scan rather than the complete recent history.
	writeJSON(w, http.StatusOK, apitypes.TransactionsListResponse{
		SchemaVersion:   apitypes.SchemaVersion,
		Instance:        name,
		Role:            role,
		LedgerEnd:       endInc,
		Transactions:    rows,
		Count:           len(rows),
		ScannedFrom:     beginExclusive,
		WindowTruncated: windowTruncated,
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
// apitypes.TransactionRow. Returns nil for entries we don't surface
// (currently only OffsetCheckpoint, since those are heartbeats not
// state changes).
func projectUpdate(resp *lapiv2.GetUpdatesResponse) *apitypes.TransactionRow {
	if resp == nil {
		return nil
	}
	if t := resp.GetTransaction(); t != nil {
		// Shared per-event decoder (ledger.ProjectTransactionEvents) so
		// the CLI Explorer (`contracts watch` / `tx ls`) and this
		// handler agree on the create/archive/exercise projection.
		summaries := ledger.ProjectTransactionEvents(t)
		events := make([]apitypes.TransactionEvent, 0, len(summaries))
		for _, s := range summaries {
			events = append(events, apitypes.TransactionEvent{
				Kind:       eventKindAbbrev(s.Kind),
				ContractID: s.ContractID,
				Template:   s.TemplateID,
				Witnesses:  s.Witnesses,
			})
		}
		row := apitypes.TransactionRow{
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
		row := apitypes.TransactionRow{
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
		row := apitypes.TransactionRow{
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

// splitCSV flattens repeated query values AND comma-separated values
// into a trimmed, non-empty slice. So ?party=a,b&party=c yields
// [a b c]. Mirrors the CLI's StringSliceVar comma semantics so the
// two surfaces parse multi-value filters identically.
func splitCSV(vals []string) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// parseOffsetParam parses an optional non-negative int64 offset query
// param. Returns (0, false, nil) when the param is absent, (n, true,
// nil) when present and valid, and an error for a malformed or
// negative value so the handler can reject it 400.
func parseOffsetParam(s string) (int64, bool, error) {
	if s == "" {
		return 0, false, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("%q is not an integer offset", s)
	}
	if n < 0 {
		return 0, false, fmt.Errorf("offset must be >= 0, got %d", n)
	}
	return n, true, nil
}
