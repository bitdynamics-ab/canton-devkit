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
//	    per-role admin port (instance brought up before per-role
//	    port capture landed; re-`up` to capture)
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
	"strings"
	"sync"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/admin"
	adminproto "github.com/bitdynamics-ab/canton-devkit/internal/canton/admin/proto"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/darops"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// darRequestTimeout caps each DAR-list request. Canton's ListDars
// returns inline data (no pagination on the wire), so 8 s is a
// generous ceiling — typical responses come back in <100 ms.
const darRequestTimeout = 8 * time.Second

// MountDAR installs the DAR endpoints on mux. The hub is used only
// by the dar-watch SSE bridge; the read endpoints are
// pure gRPC. Passing nil disables the watch SSE surface.
func MountDAR(mux *http.ServeMux, hub watchHub) {
	mux.HandleFunc("GET /api/instances/{name}/dar", handleDARList)
	mux.HandleFunc("POST /api/instances/{name}/dar", handleDARUpload)

	// Package-tree inspect.
	mux.HandleFunc("GET /api/instances/{name}/dar/{id}/inspect", handleDARInspect)
	// Structural diff.
	mux.HandleFunc("GET /api/instances/{name}/dar/diff", handleDARDiff)
	// Per-participant vetting state + toggle.
	mux.HandleFunc("GET /api/instances/{name}/dar/{id}/vetting", handleDARVettingList)
	mux.HandleFunc("POST /api/instances/{name}/dar/{id}/vetting/{role}", handleDARVettingToggle)

	// Hot-deploy indicator. The publish endpoint is
	// the cross-process bridge: a `dpm localnet dar watch` process
	// POSTs lifecycle events to it; the SSE subscriber tails them
	// for the Web UI's "watching" badge.
	if hub != nil {
		mux.Handle("POST /api/dar/watch/publish", handleDARWatchPublish(hub))
		mux.Handle("GET /api/dar/watch/events", handleDARWatchEvents(hub))
	}
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
// Derived from darops.Roles so the UI membership check and the shared
// darops.ValidateRole guard (used by the CLI) cannot drift.
var validRole = func() map[string]bool {
	m := make(map[string]bool, len(darops.Roles))
	for _, r := range darops.Roles {
		m[r] = true
	}
	return m
}()

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

	// Resolve the per-role admin port + JWT via the shared darops
	// lookup. Older instances brought up before per-role port capture
	// landed surface as ErrPortNotRecorded → a clear 503 with the
	// remediation rather than crashing or pretending the API is down.
	cfg, ok := requireParticipantAccess(w, state, name, role)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), darRequestTimeout)
	defer cancel()

	client, err := admin.Connect(ctx, cfg)
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

	// Project the proto into the shared types.DARRow shape (also used
	// by the CLI's `dar list --format json`). We deliberately don't
	// pass the protobuf JSON straight through — the proto types include
	// internal `state` fields. The list endpoint is the cheap path:
	// Vetted is left nil (not probed) so a basic listing stays
	// byte-identical to the pre-shared shape; the per-package vetting
	// state is served by GET …/dar/{id}/vetting.
	rows := make([]types.DARRow, 0, len(resp.GetDars()))
	for _, d := range resp.GetDars() {
		rows = append(rows, types.DARRow{
			Main:        d.GetMain(),
			Name:        d.GetName(),
			Version:     d.GetVersion(),
			Description: d.GetDescription(),
		})
	}

	writeJSON(w, http.StatusOK, types.DARListResponse{
		SchemaVersion: types.SchemaVersion,
		Instance:      name,
		Role:          role,
		Dars:          rows,
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

	// Fan out: one goroutine per role. Each does its own dial,
	// upload, and close. The aggregate response carries per-role
	// success/failure so a partial failure (e.g. one participant
	// down) doesn't masquerade as a total failure. Per-role config
	// resolution goes through the shared darops.ResolveParticipant so
	// the port/JWT lookup matches every other DAR surface.
	results := make([]types.DARUploadRoleResult, len(roles))
	var wg sync.WaitGroup
	for i, role := range roles {
		wg.Add(1)
		go func(i int, role string) {
			defer wg.Done()
			res := types.DARUploadRoleResult{Role: role}
			cfg, err := darops.ResolveParticipant(state, role, "", true)
			if err != nil {
				res.Error = err.Error()
				results[i] = res
				return
			}
			client, err := admin.Connect(ctx, cfg)
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

	writeJSON(w, http.StatusOK, types.DARUploadResponse{
		SchemaVersion: types.SchemaVersion,
		Instance:      name,
		Results:       results,
		TotalUploaded: total,
	})
}

// darUploadTimeout caps the per-upload deadline. DAR uploads are
// usually <2s; 30s is comfortable for multi-module bundles or a
// briefly-slow participant.
const darUploadTimeout = 30 * time.Second
