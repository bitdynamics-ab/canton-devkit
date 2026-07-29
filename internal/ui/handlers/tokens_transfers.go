package handlers

import (
	"net/http"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
)

// Pending transfer-offers HTTP surface. Delegates to
// token.RunListPendingTransfers — the same function the CLI `token
// transfers` verb calls — so CLI ↔ Web UI parity is automatic and the
// shared shape flows through internal/api/types.

// handlePendingTransfersList — GET /api/tokens/transfers?instance=[&party=].
// ACS-scans the TransferInstruction interface for the pending-offers list.
// Every active TransferInstruction is a still-pending offer (acceptance
// archives it), so the scan itself is the list.
func handlePendingTransfersList(w http.ResponseWriter, r *http.Request) {
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
	rows, truncated, err := token.RunListPendingTransfers(r.Context(), token.ListPendingTransfersOptions{
		Instance: instance,
		Party:    r.URL.Query().Get("party"),
		Role:     role,
		Insecure: true,
		Endpoint: ep,
	})
	if err != nil {
		mapTokenError(w, err, "transfers")
		return
	}
	if rows == nil {
		rows = []types.TransferSummary{}
	}
	writeJSON(w, http.StatusOK, types.PendingTransfersResponse{
		SchemaVersion:    types.SchemaVersion,
		PendingTransfers: rows,
		Truncated:        truncated,
		Aliases:          aliasMap(instance),
	})
}
