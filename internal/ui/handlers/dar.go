// BIT-187 — Web UI DAR Manager handlers.
//
// Wraps the Canton Admin API (`internal/canton/admin`) so the
// browser can list uploaded DAR packages without speaking gRPC
// directly. Auth is handled here: the participant admin port +
// JWT come from `registry.Read(name)`, so the browser doesn't
// need either. CSRF is the global same-Origin gate.
//
// Endpoints
//
//   GET /api/instances/{name}/dar?role=<app_user|app_provider|sv>
//     → 200 {schema_version, instance, role, dars: [{main, name, version, description}]}
//     → 503 PARTICIPANT_PORT_NOT_RECORDED if state.json lacks the
//       per-role admin port (instance brought up before BIT-190
//       landed; re-`up` to capture)
//
// Role defaults to "app_user" since that's the common dev target.
// Upload + diff endpoints are deferred to a follow-up — the MVP is
// "show me what's already on the participant".
package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/admin"
	adminproto "github.com/bitdynamics-ab/canton-devkit/internal/canton/admin/proto"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// darRequestTimeout caps each DAR-list request. Canton's ListDars
// returns inline data (no pagination on the wire), so 8 s is a
// generous ceiling — typical responses come back in <100 ms.
const darRequestTimeout = 8 * time.Second

// MountDAR installs the DAR-list endpoint on mux. Hub-independent
// (pure gRPC call, no SSE), so always mounted regardless of the
// hub-nil read-only mode.
func MountDAR(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instances/{name}/dar", handleDARList)
}

// validRole pins the per-role string to the literal set Splice
// LocalNet ships. Hyphens match the state.Credentials keys + the
// CLI's --role flag default. Rejects anything else before we
// touch the registry — clearer 400 than "no port for that role".
var validRole = map[string]bool{
	"app-user":     true,
	"app-provider": true,
	"sv":           true,
}

func handleDARList(w http.ResponseWriter, r *http.Request) {
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
			"role must be one of app_user, app_provider, sv")
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

	// Look up the per-role admin port that BIT-190 records into
	// state.json. Older instances brought up before that fix don't
	// have the key — surface a clear 503 with the remediation
	// rather than crashing or pretending the API is down.
	portKey := "participant_admin_" + role
	adminPort, hasPort := state.Ports[portKey]
	if !hasPort || adminPort == 0 {
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
		// Should never happen for a healthy instance — captureCredentials
		// fills every recorded role. Treat as a 500 since something's
		// genuinely off if the registry has a port but no credential.
		writeError(w, http.StatusInternalServerError,
			"no JWT recorded for role "+role,
			fmt.Errorf("missing credential for role %q", role))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), darRequestTimeout)
	defer cancel()

	client, err := admin.Connect(ctx, admin.Config{
		Host:     "localhost:" + strconv.Itoa(adminPort),
		Token:    cred.JWT,
		Insecure: true, // Splice LocalNet is plaintext by convention
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "dial canton admin", err)
		return
	}
	defer func() { _ = client.Close() }()

	resp, err := client.Package.ListDars(ctx, &adminproto.ListDarsRequest{})
	if err != nil {
		writeError(w, http.StatusBadGateway, "list dars", err)
		return
	}

	// Project the proto into a UI-stable shape. We deliberately
	// don't pass the protobuf JSON straight through — the proto
	// types include internal `state` fields and the field tags
	// would marshal as snake_case anyway. Explicit projection keeps
	// the wire format under our control.
	type darRow struct {
		Main        string `json:"main"`
		Name        string `json:"name"`
		Version     string `json:"version"`
		Description string `json:"description,omitempty"`
	}
	rows := make([]darRow, 0, len(resp.GetDars()))
	for _, d := range resp.GetDars() {
		rows = append(rows, darRow{
			Main:        d.GetMain(),
			Name:        d.GetName(),
			Version:     d.GetVersion(),
			Description: d.GetDescription(),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": 1,
		"instance":       name,
		"role":           role,
		"dars":           rows,
	})
}
