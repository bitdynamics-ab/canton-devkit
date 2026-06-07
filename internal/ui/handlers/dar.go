// Web UI DAR Manager handlers.
//
// Wraps the Canton Admin API (`internal/canton/admin`) so the
// browser can list uploaded DAR packages without speaking gRPC
// directly. Auth is handled here: the participant admin port +
// JWT come from `registry.Read(name)`, so the browser doesn't
// need either. CSRF is the global same-Origin gate.
//
// Endpoints
//
//	GET /api/instances/{name}/dar?role=<app_user|app_provider|sv>
//	  → 200 {schema_version, instance, role, dars: [{main, name, version, description}]}
//	  → 503 PARTICIPANT_PORT_NOT_RECORDED if state.json lacks the
//	    per-role admin port (instance brought up before
//	    landed; re-`up` to capture)
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
	"sort"
	"strconv"
	"strings"
	"sync"
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
// attacks. Same shape as snapshot upload.
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

	// Look up the per-role admin port that records into
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
// or more `.dar` files and a set of target participant roles. The
// handler uploads each DAR to every selected participant's Admin
// API in parallel and aggregates per-participant results.
//
// Wire shape:
//
//	POST /api/instances/{name}/dar
//	multipart fields:
//	  roles — REPEATED; one or more of "app-user", "app-provider", "sv".
//	          If omitted, falls back to single `role` (back-compat with
//	          the original single-target endpoint).
//	  role  — single role; only honoured when `roles` is absent.
//	  file  — repeatable; each is one DAR.
//
//	→ 200 {
//	    schema_version, instance,
//	    results: [
//	      {role, ok: true,  dar_ids: [...], count: N},
//	      {role, ok: false, error: "..."},
//	    ],
//	    total_uploaded: <sum of dar_ids across ok=true entries>
//	  }
//	→ 4xx on outer-level validation; partial gRPC failures land in
//	  per-role `ok:false` entries with a 200 envelope.
//
// VetAllPackages + SynchronizeVetting are forced ON: the dev-flow
// expectation is "I dropped this DAR, please make it usable on
// every participant I picked". Per-package vetting toggles are a
// separate, deeper screen (tracked as a follow-up).
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

	// Resolve target roles: `roles` (repeated) preferred, else fall
	// back to single `role`. Validate + de-dup.
	rawRoles := r.MultipartForm.Value["roles"]
	if len(rawRoles) == 0 {
		if single := strings.TrimSpace(r.FormValue("role")); single != "" {
			rawRoles = []string{single}
		} else {
			rawRoles = []string{"app-user"}
		}
	}
	seen := map[string]bool{}
	roles := make([]string, 0, len(rawRoles))
	for _, raw := range rawRoles {
		for _, v := range strings.Split(raw, ",") {
			v = strings.TrimSpace(v)
			if v == "" || seen[v] {
				continue
			}
			if !validRole[v] {
				writeErrorWithCode(w, http.StatusBadRequest,
					ErrCodeInvalidRequest,
					"invalid role: "+v,
					"roles must be a subset of app-user, app-provider, sv")
				return
			}
			seen[v] = true
			roles = append(roles, v)
		}
	}
	if len(roles) == 0 {
		writeErrorWithCode(w, http.StatusBadRequest,
			ErrCodeInvalidRequest,
			"no target participants selected",
			"pass `roles` (repeated) or `role` with one of app-user, app-provider, sv")
		return
	}
	sort.Strings(roles)

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

	// Read each uploaded file into memory ONCE. The same byte slice
	// is shared across the per-role goroutines below — proto's
	// UploadDarData.Bytes is read-only and gRPC's HTTP/2 frame
	// encoder doesn't mutate the slice, so this is safe.
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

	type roleResult struct {
		Role   string   `json:"role"`
		OK     bool     `json:"ok"`
		DarIDs []string `json:"dar_ids,omitempty"`
		Count  int      `json:"count"`
		Error  string   `json:"error,omitempty"`
	}

	// Fan out: one goroutine per role. Each does its own dial,
	// upload, and close. The aggregate response carries per-role
	// success/failure so a partial failure (e.g. one participant
	// down) doesn't masquerade as a total failure.
	results := make([]roleResult, len(roles))
	var wg sync.WaitGroup
	for i, role := range roles {
		wg.Add(1)
		go func(i int, role string) {
			defer wg.Done()
			res := roleResult{Role: role}
			portKey := "participant_admin_" + role
			adminPort, hasPort := state.Ports[portKey]
			if !hasPort || adminPort == 0 {
				res.Error = "participant_admin port not recorded for role " + role +
					" — restart the instance to capture Canton API ports"
				results[i] = res
				return
			}
			cred, hasCred := state.Credentials[role]
			if !hasCred {
				res.Error = "no JWT recorded for role " + role
				results[i] = res
				return
			}
			client, err := admin.Connect(ctx, admin.Config{
				Host:     "localhost:" + strconv.Itoa(adminPort),
				Token:    cred.JWT,
				Insecure: true,
			})
			if err != nil {
				res.Error = "dial canton admin: " + err.Error()
				results[i] = res
				return
			}
			defer func() { _ = client.Close() }()
			resp, err := client.Package.UploadDar(ctx, &adminproto.UploadDarRequest{
				Dars:               uploads,
				VetAllPackages:     true,
				SynchronizeVetting: true,
			})
			if err != nil {
				res.Error = "upload dar: " + err.Error()
				results[i] = res
				return
			}
			res.OK = true
			res.DarIDs = resp.GetDarIds()
			res.Count = len(res.DarIDs)
			results[i] = res
		}(i, role)
	}
	wg.Wait()

	total := 0
	for _, r := range results {
		if r.OK {
			total += r.Count
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": 1,
		"instance":       name,
		"results":        results,
		"total_uploaded": total,
	})
}

// darUploadTimeout caps the per-upload deadline. DAR uploads are
// usually <2s; 30s is comfortable for multi-module bundles or a
// briefly-slow participant.
const darUploadTimeout = 30 * time.Second
