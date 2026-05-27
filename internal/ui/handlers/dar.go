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
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/admin"
	adminproto "github.com/bitdynamics-ab/canton-devkit/internal/canton/admin/proto"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// darRequestTimeout caps each DAR-list request. Canton's ListDars
// returns inline data (no pagination on the wire), so 8 s is a
// generous ceiling — typical responses come back in <100 ms.
const darRequestTimeout = 8 * time.Second

// MountDAR installs the DAR endpoints on mux. Hub-independent
// (pure gRPC calls, no SSE).
func MountDAR(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instances/{name}/dar", handleDARList)
	mux.HandleFunc("POST /api/instances/{name}/dar", handleDARUpload)
}

// darUploadMax is the hard ceiling on the multipart body for DAR
// upload. Canton DARs are typically <10 MiB; 64 MiB covers even
// vendored multi-module bundles without inviting "fill the disk"
// attacks. Same shape as snapshot upload (BIT-184).
const darUploadMax = 64 << 20

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

// handleDARUpload accepts a multipart/form-data POST containing one
// or more `.dar` files plus an optional `role` field (defaults to
// app-user). Each file is uploaded to the named participant's
// Admin API via UploadDar. `vet_all_packages` is forced ON — the
// dev-workflow expectation is "I dropped this DAR, please make it
// usable"; advanced vetting flows belong on a dedicated screen.
//
// Wire shape:
//
//	POST /api/instances/{name}/dar
//	multipart fields:
//	  role  — optional, default "app-user"
//	  file  — repeatable; each is one DAR
//
//	→ 200 {schema_version, instance, role, dar_ids: [pkg-id, …]}
//	→ 4xx with the standard error taxonomy
func handleDARUpload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := registry.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance name", err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, darUploadMax)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeErrorWithCode(w, http.StatusRequestEntityTooLarge,
				ErrCodeRequestTooLarge,
				fmt.Sprintf("upload exceeds %d MiB cap", darUploadMax>>20))
			return
		}
		writeError(w, http.StatusBadRequest, "parse multipart", err)
		return
	}
	role := strings.TrimSpace(r.FormValue("role"))
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
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		writeError(w, http.StatusBadRequest, "no file uploaded", nil)
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
	portKey := "participant_admin_" + role
	adminPort, hasPort := state.Ports[portKey]
	if !hasPort || adminPort == 0 {
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

	// Read each uploaded file into memory. Bounded by the
	// MaxBytesReader on the request body, so the worst case is
	// one ~64 MiB allocation.
	uploads := make([]*adminproto.UploadDarRequest_UploadDarData, 0, len(files))
	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "open uploaded file", err)
			return
		}
		body, err := io.ReadAll(f)
		_ = f.Close()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "read uploaded file", err)
			return
		}
		desc := fh.Filename
		uploads = append(uploads, &adminproto.UploadDarRequest_UploadDarData{
			Bytes:       body,
			Description: &desc,
		})
	}

	ctx, cancel := context.WithTimeout(r.Context(), darUploadTimeout)
	defer cancel()

	client, err := admin.Connect(ctx, admin.Config{
		Host:     "localhost:" + strconv.Itoa(adminPort),
		Token:    cred.JWT,
		Insecure: true,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "dial canton admin", err)
		return
	}
	defer func() { _ = client.Close() }()

	resp, err := client.Package.UploadDar(ctx, &adminproto.UploadDarRequest{
		Dars:               uploads,
		VetAllPackages:     true,
		SynchronizeVetting: true,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "upload dar", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": 1,
		"instance":       name,
		"role":           role,
		"dar_ids":        resp.GetDarIds(),
		"count":          len(resp.GetDarIds()),
	})
}

// darUploadTimeout caps the per-upload deadline. DAR uploads are
// usually <2s; 30s is comfortable for multi-module bundles or a
// briefly-slow participant.
const darUploadTimeout = 30 * time.Second
