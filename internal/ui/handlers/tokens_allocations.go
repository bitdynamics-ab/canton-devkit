package handlers

import (
	"context"
	"io"
	"net/http"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
)

// V2 DvP allocation HTTP surface. Every handler delegates to
// internal/localnet/token.RunX — the same functions the CLI verbs call — so
// CLI ↔ Web UI parity is automatic and shared JSON shapes flow through
// internal/api/types.

// handleTokenAllocate — POST /api/tokens/{symbol}/allocate. Creates a V2
// allocation from the authorizer's holdings and returns the resulting
// Allocation (or pending AllocationInstruction) contract id.
func handleTokenAllocate(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	var body struct {
		From               string `json:"from"`
		To                 string `json:"to"`
		Amount             string `json:"amount"`
		Executor           string `json:"executor"`
		SettlementDeadline string `json:"settlement_deadline"`
		Committed          bool   `json:"committed"`
	}
	if err := decodeJSON(r.Body, &body); err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	role := roleFromQuery(r)
	id, err := runTokenAllocate(r.Context(), nil, token.AllocationOptions{
		Instance:           instance,
		Instrument:         r.PathValue("symbol"),
		From:               body.From,
		To:                 body.To,
		Amount:             body.Amount,
		Executor:           body.Executor,
		SettlementDeadline: body.SettlementDeadline,
		Committed:          body.Committed,
		Endpoint:           liveLedgerEndpoint(instance, role),
		Role:               role,
		Insecure:           true,
	})
	if err != nil {
		mapTokenError(w, err, "allocate")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": types.SchemaVersion,
		"allocation_id":  id,
	})
}

// handleAllocationsList — GET /api/tokens/allocations?instance=[&party=].
// ACS-scans the Allocation interface for the allocations table.
func handleAllocationsList(w http.ResponseWriter, r *http.Request) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	role := roleFromQuery(r)
	ep := liveLedgerEndpoint(instance, role)
	if ep == "" {
		writeErrorWithCode(w, http.StatusServiceUnavailable, "PARTICIPANT_PORT_NOT_RECORDED",
			"no live ledger endpoint for instance "+instance+" — restart it so ports are captured")
		return
	}
	rows, err := token.RunListAllocations(r.Context(), token.ListAllocationsOptions{
		Instance: instance,
		Party:    r.URL.Query().Get("party"),
		Role:     role,
		Insecure: true,
		Endpoint: ep,
	})
	if err != nil {
		mapTokenError(w, err, "allocations")
		return
	}
	if rows == nil {
		rows = []types.AllocationSummary{}
	}
	writeJSON(w, http.StatusOK, types.AllocationsResponse{
		SchemaVersion:    types.SchemaVersion,
		ResolvedEndpoint: types.ResolvedEndpoint{Endpoint: ep, Role: role},
		Allocations:      rows,
		Aliases:          aliasMap(instance),
	})
}

// handleAllocationWithdraw / Cancel —
// POST /api/tokens/allocations/{id}/{withdraw,cancel}. Each maps to
// the matching RunX with the path id + optional ?party=. Settlement
// (SettlementFactory_SettleBatch) is not yet functional on LocalNet, so no
// settle route is exposed.
func handleAllocationWithdraw(w http.ResponseWriter, r *http.Request) {
	runAllocationActionHandler(w, r, token.RunAllocationWithdraw, "withdraw")
}

func handleAllocationCancel(w http.ResponseWriter, r *http.Request) {
	runAllocationActionHandler(w, r, token.RunAllocationCancel, "cancel")
}

// runAllocationActionHandler is the shared body of the per-allocation action
// handlers: build AllocationActionOptions from the path id + ?party=,
// dispatch to the RunX, map the error.
func runAllocationActionHandler(
	w http.ResponseWriter, r *http.Request,
	run func(context.Context, io.Writer, token.AllocationActionOptions) error,
	op string,
) {
	instance, err := instanceFromQuery(r)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest, err.Error())
		return
	}
	role := roleFromQuery(r)
	err = run(r.Context(), nil, token.AllocationActionOptions{
		Instance:     instance,
		AllocationID: r.PathValue("id"),
		Party:        r.URL.Query().Get("party"),
		Endpoint:     liveLedgerEndpoint(instance, role),
		Role:         role,
		Insecure:     true,
	})
	mapTokenError(w, err, op)
}

// runTokenAllocate is the indirection point for handleTokenAllocate so tests
// can assert the threaded fields without a live ledger (same package-var
// pattern as runTokenCreate / runTokenTransfer).
var runTokenAllocate = token.RunAllocate
