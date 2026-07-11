// Web UI Explorer handlers.
//
// Wraps the Canton Ledger API v2 (`internal/canton/ledger`) so the
// browser can read an Active Contract Set snapshot without speaking
// gRPC directly. Auth + endpoint discovery happens here: the
// participant ledger port + JWT come from `registry.Read(name)`,
// so the browser doesn't need either.
//
// Endpoints
//
//	GET /api/instances/{name}/contracts?role=<app-user|app-provider|sv>&limit=N
//	  → 200 {schema_version, instance, role, ledger_end, contracts: [{
//	          contract_id, template_id, payload, signatories, observers,
//	          created_at, package_name, package_version
//	        }]}
//	  → 503 PARTICIPANT_PORT_NOT_RECORDED if state.json predates
//	    participant-port capture.
//	An ACS snapshot at ledger end. limit defaults to 100; cap is 1000
//	(server-side defence). No filters by template/party — the JWT's
//	claim already filters server-side.
//
//	GET /api/instances/{name}/contracts/stream?role=<…>
//	  → 200 text/event-stream of `event: contracts` frames carrying
//	    {"event":"created|archived", …} as the participant's
//	    UpdateService publishes them. Hard-capped at maxStreamEvents per
//	    subscription; a final {"event":"truncated"} frame precedes close.
//
//	GET /api/instances/{name}/contracts/{contract_id}?role=<…>
//	  → 200 {schema_version, instance, role, contract: {…}} — a deep view
//	    of one contract (CreatedEvent payload + ArchivedEvent, if any)
//	    backing the contract-detail drawer.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	apitypes "github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// isPermissionDenied is the gRPC error-classifier the Explorer
// uses to recognise the "user-id JWT can't see this party's
// contracts" case. Returns false for non-gRPC errors (already
// handled as a generic 502).
func isPermissionDenied(err error) bool {
	s, ok := status.FromError(err)
	return ok && s.Code() == codes.PermissionDenied
}

const (
	contractsRequestTimeout = 12 * time.Second
	contractsDefaultLimit   = 100
	contractsMaxLimit       = 1000
)

// MountContracts installs the Explorer endpoints on mux. Pure
// gRPC call, hub-independent.
//
// Endpoints:
//   - GET /api/instances/{name}/contracts                — ACS snapshot
//   - GET /api/instances/{name}/contracts/stream         — SSE live updates
//   - GET /api/instances/{name}/contracts/{contract_id}  — contract detail
//
// The /stream path is matched first by Go 1.22 stdlib mux (more
// specific routes win), so {contract_id} never captures "stream".
func MountContracts(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instances/{name}/contracts", handleContractsList)
	mux.HandleFunc("GET /api/instances/{name}/contracts/stream", handleContractsStream)
	mux.HandleFunc("GET /api/instances/{name}/contracts/{contract_id}", handleContractDetail)
}

const (
	// maxStreamEvents caps the number of SSE events a single
	// /contracts/stream subscription will emit before closing with a
	// `truncated` final event — one browser tab can't OOM the UI
	// server with an unbounded stream. 10k events at the
	// typical ~120-byte JSON payload is ~1.2 MB per client.
	maxStreamEvents = 10_000
	// contractsStreamHeartbeat is the keep-alive cadence on the SSE
	// stream. Idle proxies / browser tabs drop a TCP connection
	// after ~60s; a 25s SSE comment keeps the socket warm and is
	// invisible to the client.
	contractsStreamHeartbeat = 25 * time.Second
)

// The ACS row + list response shapes are shared with the CLI
// `contracts ls --format json` via internal/api/types
// (apitypes.ContractRow / ContractsListResponse), so the two surfaces
// can never drift and TestSchemaShape_GoldenPinsFieldLevelShape pins
// the field layout against frontend/src/api.ts.

func handleContractsList(w http.ResponseWriter, r *http.Request) {
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
	limit := contractsDefaultLimit
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			if n > contractsMaxLimit {
				n = contractsMaxLimit
			}
			limit = n
		}
	}

	endpoint, tok, ok := resolveLedgerForRole(w, name, role)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), contractsRequestTimeout)
	defer cancel()

	client, err := ledger.Dial(ctx, ledger.DialOptions{
		Endpoint:  endpoint,
		Token:     tok,
		PlainText: true, // Splice LocalNet
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "dial canton ledger", err)
		return
	}
	defer func() { _ = client.Close() }()

	// Snapshot the ACS at ledger end. ActiveAtOffset = LedgerEnd so
	// the snapshot reflects the latest state; passing 0 would also
	// work but uses an undefined "begin" sentinel.
	end, err := client.LedgerEnd(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, "ledger end probe", err)
		return
	}

	// resolve the JWT's user-id → set of parties it can
	// actAs / readAs. Splice LocalNet signs user-id JWTs by
	// default, so PartyManagementService.ListKnownParties would
	// PermissionDenied (admin claim required) and a wildcard
	// FiltersForAnyParty filter would too. UserManagementService
	// is the right API: it returns the rights attached to the
	// token's own user, which we map to a party set.
	parties, err := client.ResolveActAndReadParties(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, "resolve user rights", err)
		return
	}
	if len(parties) == 0 {
		// Token has no party rights — surface the friendly empty
		// state rather than fail. Common for a freshly-created user
		// that hasn't been granted actAs/readAs on any party yet.
		writeErrorWithCode(w, http.StatusServiceUnavailable,
			"EXPLORER_NEEDS_PARTY_JWT",
			"this JWT has no party-rights",
			"grant actAs/readAs rights to the user via UserManagementService, "+
				"or use a party-id JWT")
		return
	}
	byParty := make(map[string]*lapiv2.Filters, len(parties))
	for _, p := range parties {
		byParty[p] = &lapiv2.Filters{}
	}
	// Verbose=true asks the participant to include payload field
	// labels — required for our typed payload preview; otherwise
	// we'd get positional-only Record values.
	stream, err := client.ActiveContracts(ctx, ledger.ActiveContractsRequest{
		ActiveAtOffset: end.Offset,
		EventFormat: &lapiv2.EventFormat{
			FiltersByParty: byParty,
			Verbose:        true,
		},
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "open ACS stream", err)
		return
	}

	rows := make([]apitypes.ContractRow, 0, limit)
	truncated := false
	for item := range stream {
		if item.Err != nil {
			if errors.Is(item.Err, io.EOF) {
				break
			}
			// PermissionDenied on the ACS stream means the JWT can
			// see the party (ListKnownParties returned it) but
			// doesn't hold actAs/readAs rights for it — typical for
			// the LocalNet user-id JWT. Surface the same structured
			// "needs party rights" code so the frontend renders the
			// friendly empty state instead of a 502.
			if isPermissionDenied(item.Err) {
				writeErrorWithCode(w, http.StatusServiceUnavailable,
					"EXPLORER_NEEDS_PARTY_JWT",
					"this JWT doesn't have party-rights to read the ACS",
					"Splice LocalNet signs user-id tokens by default; the "+
						"explorer's per-party filter needs party rights resolved "+
						"via UserManagementService.")
				return
			}
			writeError(w, http.StatusBadGateway, "ACS stream", item.Err)
			return
		}
		if len(rows) >= limit {
			truncated = true
			break
		}
		// Each ACS response carries a oneof — we only project
		// ActiveContract entries in this MVP. Reassignments
		// (incomplete assigned/unassigned) are skipped.
		ac := item.Value.GetActiveContract()
		if ac == nil || ac.CreatedEvent == nil {
			continue
		}
		ev := ac.CreatedEvent
		// nilToEmpty: Go marshals nil slices as JSON `null`, which
		// then crashes the frontend with `null.length is undefined`
		// on render. Convert to empty slice up-front so the wire
		// contract is "always an array".
		sigs := ev.GetSignatories()
		if sigs == nil {
			sigs = []string{}
		}
		obs := ev.GetObservers()
		if obs == nil {
			obs = []string{}
		}
		row := apitypes.ContractRow{
			ContractID:     ev.GetContractId(),
			TemplateID:     formatTemplateID(ev.TemplateId),
			Signatories:    sigs,
			Observers:      obs,
			PackageName:    ev.GetPackageName(),
			PackageVersion: "", // CreatedEvent has no version; left empty in MVP
		}
		if ev.CreatedAt != nil {
			row.CreatedAt = ev.CreatedAt.AsTime().Format(time.RFC3339)
		}
		if ev.CreateArguments != nil {
			row.Payload = recordToMap(ev.CreateArguments)
		}
		rows = append(rows, row)
	}

	// truncated:true means the participant had more matching contracts
	// than the limit; the frontend shows a "showing first N" hint.
	writeJSON(w, http.StatusOK, apitypes.ContractsListResponse{
		SchemaVersion: apitypes.SchemaVersion,
		Instance:      name,
		Role:          role,
		LedgerEnd:     end.Offset,
		Contracts:     rows,
		Truncated:     truncated,
		Limit:         limit,
	})
}

// resolveLedgerForRole replicates the registry → port → JWT lookup
// the snapshot and stream paths share. Returns the participant
// endpoint, a TokenSource ready for ledger.Dial, and a true ok on
// success. On failure it has already written a structured error
// envelope to w; the caller just returns.
func resolveLedgerForRole(w http.ResponseWriter, name, role string) (endpoint string, tok ledger.TokenSource, ok bool) {
	state, err := registry.Read(name)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			writeErrorWithCode(w, http.StatusNotFound,
				ErrCodeNotFound,
				"instance "+name+" not registered")
			return "", nil, false
		}
		writeError(w, http.StatusInternalServerError, "read state", err)
		return "", nil, false
	}
	portKey := "participant_ledger_" + role
	ledgerPort, hasPort := state.Ports[portKey]
	if !hasPort || ledgerPort == 0 {
		writeErrorWithCode(w, http.StatusServiceUnavailable,
			"PARTICIPANT_PORT_NOT_RECORDED",
			"instance "+name+" was started before participant ports were recorded",
			"restart the instance with `dpm localnet down "+name+
				"` followed by `dpm localnet up "+name+
				"` — the new up flow captures all Canton API ports")
		return "", nil, false
	}
	cred, hasCred := state.Credentials[role]
	if !hasCred {
		writeError(w, http.StatusInternalServerError,
			"no JWT recorded for role "+role,
			fmt.Errorf("missing credential for role %q", role))
		return "", nil, false
	}
	return "localhost:" + strconv.Itoa(ledgerPort), ledger.StaticToken(cred.JWT), true
}

// streamEventFrame is the JSON shape we publish for each
// create/archive over /contracts/stream. Mirrors the snapshot's
// contractRow projection so the frontend can apply the same de-dup
// + render path on both surfaces.
type streamEventFrame struct {
	Event       string   `json:"event"` // "created" | "archived" | "truncated"
	ContractID  string   `json:"contract_id,omitempty"`
	Template    string   `json:"template,omitempty"`
	Signatories []string `json:"signatories,omitempty"`
	Observers   []string `json:"observers,omitempty"`
	Offset      int64    `json:"offset,omitempty"`
	At          int64    `json:"at,omitempty"` // unix-ts seconds
	UpdateID    string   `json:"update_id,omitempty"`
	Reason      string   `json:"reason,omitempty"` // for "truncated"
}

// handleContractsStream serves Server-Sent Events over the
// participant's UpdateService stream. One subscription per HTTP
// request; the upstream gRPC stream is cancelled when the client
// disconnects (ctx.WithCancel + defer cancel). Hard-capped at
// maxStreamEvents events per subscription.
//
// Wire format (one frame per published payload):
//
//	event: contracts
//	data: {"event":"created","contract_id":"…", …}
//
// A final synthetic `{"event":"truncated", …}` is emitted before
// the connection closes when the cap is hit, so the frontend can
// switch to its snapshot reconciliation path.
func handleContractsStream(w http.ResponseWriter, r *http.Request) {
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
	// Parse `since` BEFORE we do any work that could fail with 5xx —
	// it's pure input validation and should reject 400 before we dial
	// the participant or look up credentials.
	var sinceParsed int64
	var sincePresent bool
	if rawSince := r.URL.Query().Get("since"); rawSince != "" {
		v, err := strconv.ParseInt(rawSince, 10, 64)
		if err != nil || v < 0 {
			writeError(w, http.StatusBadRequest, "invalid since",
				fmt.Errorf("since must be a non-negative integer offset"))
			return
		}
		sinceParsed = v
		sincePresent = true
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError,
			"streaming unsupported", fmt.Errorf("ResponseWriter is not http.Flusher"))
		return
	}

	endpoint, tok, ok := resolveLedgerForRole(w, name, role)
	if !ok {
		return
	}

	// Bound the dial+resolve phase by a short timeout so a slow
	// participant never holds the request open without surfacing
	// progress. The streaming phase below runs on the parent r.Context()
	// so it can outlive this timeout.
	setupCtx, cancelSetup := context.WithTimeout(r.Context(), contractsRequestTimeout)
	defer cancelSetup()

	client, err := ledger.Dial(setupCtx, ledger.DialOptions{
		Endpoint:  endpoint,
		Token:     tok,
		PlainText: true,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "dial canton ledger", err)
		return
	}
	defer func() { _ = client.Close() }()

	parties, err := client.ResolveActAndReadParties(setupCtx)
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

	// Resume offset: prefer the caller-supplied `since` query param
	// (parsed at the top of the handler), which the Explorer screen
	// threads from the snapshot endpoint's `ledger_end` response
	// field. This closes the snapshot→stream handoff race: any
	// create/archive between the snapshot's ledger_end and the
	// stream's own LedgerEnd() probe would otherwise be skipped
	// until the 30s reconciliation timer fires. When `since` is
	// absent (older clients / direct curl), fall back to the live
	// ledger end — strictly-live-tail semantics.
	var beginExclusive int64
	if sincePresent {
		beginExclusive = sinceParsed
	} else {
		end, err := client.LedgerEnd(setupCtx)
		if err != nil {
			writeError(w, http.StatusBadGateway, "ledger end probe", err)
			return
		}
		beginExclusive = end.Offset
	}

	// Per-call cancel covers TWO scenarios:
	//   1. Client disconnects (r.Context().Done()) → cancel propagates
	//      to the upstream gRPC stream so we don't leak it.
	//   2. We hit maxStreamEvents → we cancel to terminate the upstream
	//      before returning the truncated frame.
	streamCtx, cancelStream := context.WithCancel(r.Context())
	defer cancelStream()

	updates, err := client.Updates(streamCtx, ledger.UpdatesRequest{
		BeginExclusive: beginExclusive,
		// EndInclusive nil = tail forever (until ctx cancels).
		UpdateFormat: &lapiv2.UpdateFormat{
			IncludeTransactions: &lapiv2.TransactionFormat{
				EventFormat: &lapiv2.EventFormat{
					FiltersByParty: byParty,
					Verbose:        false,
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
				"the JWT's party rights don't grant read access")
			return
		}
		writeError(w, http.StatusBadGateway, "open updates stream", err)
		return
	}

	// SSE headers + immediate flush so the browser's onopen fires
	// before the first event arrives.
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	heartbeat := time.NewTicker(contractsStreamHeartbeat)
	defer heartbeat.Stop()

	emitted := 0
	// emit returns false when the cap is hit (the caller should
	// flush a `truncated` frame and exit).
	emit := func(p streamEventFrame) bool {
		buf, err := json.Marshal(p)
		if err != nil {
			return true // skip; should never happen
		}
		_, _ = fmt.Fprintf(w, "event: contracts\ndata: %s\n\n", buf)
		flusher.Flush()
		emitted++
		return emitted < maxStreamEvents
	}

	for {
		select {
		case <-streamCtx.Done():
			return
		case <-heartbeat.C:
			_, _ = w.Write([]byte(": keepalive\n\n"))
			flusher.Flush()
		case item, ok := <-updates:
			if !ok {
				return
			}
			if item.Err != nil {
				if errors.Is(item.Err, io.EOF) ||
					errors.Is(item.Err, context.Canceled) ||
					errors.Is(item.Err, context.DeadlineExceeded) {
					return
				}
				// Mid-stream failure: surface as a final SSE error
				// frame rather than a 502 (we've already 200'd).
				_, _ = fmt.Fprintf(w, "event: error\ndata: %q\n\n", item.Err.Error())
				flusher.Flush()
				return
			}
			cont := true
			projectStreamEvents(item.Value, func(p streamEventFrame) {
				if !cont {
					return
				}
				if !emit(p) {
					cont = false
				}
			})
			if !cont {
				// Hit the cap — emit the truncated marker and bail.
				_ = emitTruncated(w, flusher)
				return
			}
		}
	}
}

func emitTruncated(w http.ResponseWriter, flusher http.Flusher) error {
	buf, _ := json.Marshal(streamEventFrame{
		Event:  "truncated",
		Reason: fmt.Sprintf("stream cap of %d events reached", maxStreamEvents),
	})
	_, err := fmt.Fprintf(w, "event: contracts\ndata: %s\n\n", buf)
	flusher.Flush()
	return err
}

// projectStreamEvents walks one GetUpdatesResponse and invokes
// `out` for each create/archive event we care about. Reassignments
// and topology updates are ignored — the contracts table only
// cares about ACS deltas.
func projectStreamEvents(resp *lapiv2.GetUpdatesResponse, out func(streamEventFrame)) {
	if resp == nil {
		return
	}
	tx := resp.GetTransaction()
	if tx == nil {
		return
	}
	var atUnix int64
	if rt := tx.GetRecordTime(); rt != nil {
		atUnix = rt.AsTime().Unix()
	}
	for _, ev := range tx.GetEvents() {
		if c := ev.GetCreated(); c != nil {
			sigs := c.GetSignatories()
			if sigs == nil {
				sigs = []string{}
			}
			obs := c.GetObservers()
			if obs == nil {
				obs = []string{}
			}
			out(streamEventFrame{
				Event:       "created",
				ContractID:  c.GetContractId(),
				Template:    formatTemplateID(c.GetTemplateId()),
				Signatories: sigs,
				Observers:   obs,
				Offset:      tx.GetOffset(),
				At:          atUnix,
				UpdateID:    tx.GetUpdateId(),
			})
			continue
		}
		if a := ev.GetArchived(); a != nil {
			out(streamEventFrame{
				Event:      "archived",
				ContractID: a.GetContractId(),
				Template:   formatTemplateID(a.GetTemplateId()),
				Offset:     tx.GetOffset(),
				At:         atUnix,
				UpdateID:   tx.GetUpdateId(),
			})
		}
	}
}

// The contract-detail drawer payload (apitypes.ContractDetail /
// ContractDetailResponse) is shared with the CLI and pinned by the
// schema-shape golden.

// handleContractDetail returns a deep view of one contract — the
// CreatedEvent payload + the ArchivedEvent (if any) — using
// EventQueryService.GetEventsByContractId. The same primitive `tx
// replay` uses; we just project through the JWT's party set.
func handleContractDetail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := registry.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance name", err)
		return
	}
	cid := r.PathValue("contract_id")
	if cid == "" {
		writeErrorWithCode(w, http.StatusBadRequest,
			ErrCodeInvalidRequest,
			"contract_id is required")
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

	endpoint, tok, ok := resolveLedgerForRole(w, name, role)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), contractsRequestTimeout)
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

	parties, err := client.ResolveActAndReadParties(ctx)
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
	if len(parties) == 0 {
		writeErrorWithCode(w, http.StatusServiceUnavailable,
			"EXPLORER_NEEDS_PARTY_JWT",
			"this JWT has no party-rights")
		return
	}
	byParty := make(map[string]*lapiv2.Filters, len(parties))
	for _, p := range parties {
		byParty[p] = &lapiv2.Filters{}
	}

	resp, err := client.EventsByContractId(ctx, &lapiv2.GetEventsByContractIdRequest{
		ContractId: cid,
		EventFormat: &lapiv2.EventFormat{
			FiltersByParty: byParty,
			Verbose:        true,
		},
	})
	if err != nil {
		// NotFound from the participant means the JWT can't see this
		// contract — surface as 404 so the frontend can render "not
		// visible" instead of a generic 502.
		if s, ok := status.FromError(err); ok && s.Code() == codes.NotFound {
			writeErrorWithCode(w, http.StatusNotFound,
				ErrCodeNotFound,
				"contract not visible to this party set")
			return
		}
		if isPermissionDenied(err) {
			writeErrorWithCode(w, http.StatusServiceUnavailable,
				"EXPLORER_NEEDS_PARTY_JWT",
				"participant denied events lookup")
			return
		}
		writeError(w, http.StatusBadGateway, "events by contract id", err)
		return
	}

	detail := apitypes.ContractDetail{
		ContractID:  cid,
		Signatories: []string{},
		Observers:   []string{},
	}
	if cev := resp.GetCreated(); cev != nil && cev.GetCreatedEvent() != nil {
		e := cev.GetCreatedEvent()
		detail.TemplateID = formatTemplateID(e.GetTemplateId())
		detail.PackageName = e.GetPackageName()
		if sigs := e.GetSignatories(); sigs != nil {
			detail.Signatories = sigs
		}
		if obs := e.GetObservers(); obs != nil {
			detail.Observers = obs
		}
		if e.CreatedAt != nil {
			detail.CreatedAt = e.CreatedAt.AsTime().Format(time.RFC3339)
		}
		detail.CreatedOffset = e.GetOffset()
		if e.CreateArguments != nil {
			detail.Payload = recordToMap(e.CreateArguments)
		}
	}
	if av := resp.GetArchived(); av != nil && av.GetArchivedEvent() != nil {
		ae := av.GetArchivedEvent()
		detail.Archived = true
		detail.ArchivedOffset = ae.GetOffset()
		// ArchivedEvent has no record_time / update_id directly;
		// the wrapping Archived envelope carries the synchronizer
		// but not the tx id, so only the offset is surfaced here.
		if detail.TemplateID == "" {
			detail.TemplateID = formatTemplateID(ae.GetTemplateId())
		}
	}

	writeJSON(w, http.StatusOK, apitypes.ContractDetailResponse{
		SchemaVersion: apitypes.SchemaVersion,
		Instance:      name,
		Role:          role,
		Contract:      detail,
	})
}

// formatTemplateID renders a proto Identifier as the canonical
// "package_id:Module.Submodule:Entity" string the user sees in
// Daml tooling. Empty string if the proto is nil.
func formatTemplateID(id *lapiv2.Identifier) string {
	if id == nil {
		return ""
	}
	return id.GetPackageId() + ":" + id.GetModuleName() + ":" + id.GetEntityName()
}

// recordToMap converts a Daml-LF Record proto into a JSON-friendly
// map[string]any. Delegates to the shared ledger.RecordToMap so the
// Web UI ACS payload preview + contract drawer and the CLI
// `contracts ls --format json` decode payloads identically.
func recordToMap(r *lapiv2.Record) map[string]any {
	return ledger.RecordToMap(r)
}
