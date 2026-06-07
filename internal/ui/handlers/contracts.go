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
//
// MVP scope: ACS snapshot only — no filters by template/party (the
// JWT's claim already filters server-side), no live SSE stream, no
// contract-detail drawer. Those are tracked as follow-ups in the
// ticket. limit defaults to 100; cap is 1000 (server-side defence).
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

// MountContracts installs the Explorer ACS endpoint on mux. Pure
// gRPC call, hub-independent.
func MountContracts(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instances/{name}/contracts", handleContractsList)
}

// contractRow is the projection we expose over JSON. We deliberately
// flatten the proto's discriminated-union shape into a flat struct so
// the frontend doesn't have to walk the oneof — the table only cares
// about ActiveContract entries; in-flight reassignments are skipped
// in the MVP and tracked as a follow-up.
type contractRow struct {
	ContractID     string         `json:"contract_id"`
	TemplateID     string         `json:"template_id"`
	Payload        map[string]any `json:"payload,omitempty"`
	Signatories    []string       `json:"signatories"`
	Observers      []string       `json:"observers"`
	CreatedAt      string         `json:"created_at,omitempty"`
	PackageName    string         `json:"package_name,omitempty"`
	PackageVersion string         `json:"package_version,omitempty"`
}

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
			"restart the instance with `dpm localnet down --name "+name+
				"` followed by `dpm localnet up --name "+name+
				"` — the new up flow captures all Canton API ports")
		return
	}
	cred, hasCred := state.Credentials[role]
	if !hasCred {
		writeError(w, http.StatusInternalServerError,
			"no JWT recorded for role "+role,
			fmt.Errorf("missing credential for role %q", role))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), contractsRequestTimeout)
	defer cancel()

	client, err := ledger.Dial(ctx, ledger.DialOptions{
		Endpoint:  "localhost:" + strconv.Itoa(ledgerPort),
		Token:     ledger.StaticToken(cred.JWT),
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

	rows := make([]contractRow, 0, limit)
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
						"via UserManagementService. Tracked as a follow-up to .")
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
		row := contractRow{
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

	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": 1,
		"instance":       name,
		"role":           role,
		"ledger_end":     end.Offset,
		"contracts":      rows,
		// truncated:true means the participant had more matching
		"truncated": truncated,
		"limit":     limit,
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
// map[string]any. Recurses into nested records and lists. Anything
// it can't classify becomes a string of its textual proto form —
// safer than panicking on a value type we haven't enumerated.
//
// This is a deliberately-best-effort renderer for the MVP table.
// The full typed decoder (using Daml-LF metadata) is a follow-up.
func recordToMap(r *lapiv2.Record) map[string]any {
	if r == nil {
		return nil
	}
	out := make(map[string]any, len(r.Fields))
	for _, f := range r.Fields {
		out[f.GetLabel()] = valueToAny(f.GetValue())
	}
	return out
}

func valueToAny(v *lapiv2.Value) any {
	if v == nil {
		return nil
	}
	switch s := v.Sum.(type) {
	case *lapiv2.Value_Bool:
		return s.Bool
	case *lapiv2.Value_Int64:
		return s.Int64
	case *lapiv2.Value_Numeric:
		return s.Numeric
	case *lapiv2.Value_Text:
		return s.Text
	case *lapiv2.Value_Party:
		return s.Party
	case *lapiv2.Value_ContractId:
		return s.ContractId
	case *lapiv2.Value_Date:
		return s.Date
	case *lapiv2.Value_Timestamp:
		return s.Timestamp
	case *lapiv2.Value_Record:
		return recordToMap(s.Record)
	case *lapiv2.Value_List:
		items := s.List.GetElements()
		out := make([]any, len(items))
		for i, e := range items {
			out[i] = valueToAny(e)
		}
		return out
	case *lapiv2.Value_Optional:
		opt := s.Optional.GetValue()
		if opt == nil {
			return nil
		}
		return valueToAny(opt)
	default:
		// Variants, enums, maps, GenMap — left as proto-textual
		// fallback. Tracked as follow-up; rare in dev workloads.
		b, _ := json.Marshal(v.Sum)
		return string(b)
	}
}
