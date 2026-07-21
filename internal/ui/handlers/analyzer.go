// Web UI daml-analyzer handlers.
//
// Wraps internal/analyzer (which runs the Certora daml-analyzer Docker
// image) so the browser can run a cross-package interaction
// analysis on a compiled .dar. Three surfaces:
//
//	GET  /api/analyzer/status
//	  → 200 {schema_version, available, docker_found, image_present,
//	         image?, detail?}  (instance-independent)
//
//	POST /api/analyzer/analyze          (multipart; file field `dar`)
//	  → 200 {schema_version, dar_name, report}  (instance-independent)
//	  → 413 REQUEST_TOO_LARGE past the body cap
//	  → 503 ANALYZER_UNAVAILABLE if Docker/the image isn't available
//
//	GET  /api/instances/{name}/analyzer/{id}?role=<app-user|…>
//	  → 200 {schema_version, instance, package_id, dar_name, report}
//	  → 503 ANALYZER_UNAVAILABLE if Docker/the image isn't available
//
// The last one analyses an already-deployed DAR by fetching its bytes
// from the participant's admin API (GetDar), mirroring dar_inspect.go.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/analyzer"
	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/admin"
	adminproto "github.com/bitdynamics-ab/canton-devkit/internal/canton/admin/proto"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// ErrCodeAnalyzerUnavailable is the stable token the frontend branches
// on to render a clean "analyzer not configured" state (install Java /
// install Docker) instead of a generic upstream error.
const ErrCodeAnalyzerUnavailable = "ANALYZER_UNAVAILABLE"

// analyzerUploadMax caps the multipart upload body. DARs are larger
// than the norm this endpoint accepts elsewhere (the analyzer runs on
// full dependency bundles), so 96 MiB — comfortably above any real
// package while still bounding a "fill the disk" upload.
const analyzerUploadMax = 96 << 20

// analyzerRequestTimeout caps the deployed-DAR path: one GetDar
// round-trip plus the JVM analysis. The analyzer walks deep package
// graphs, so this is generous.
const analyzerRequestTimeout = 60 * time.Second

// MountAnalyzer installs the analyzer endpoints on mux. Instance-
// independent status + upload plus the per-instance deployed-DAR path.
func MountAnalyzer(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/analyzer/status", handleAnalyzerStatus)
	mux.HandleFunc("POST /api/analyzer/analyze", handleAnalyzerUpload)
	mux.HandleFunc("GET /api/instances/{name}/analyzer/{id}", handleInstanceAnalyze)
}

func handleAnalyzerStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, analyzer.Status(r.Context()))
}

// handleAnalyzerUpload analyses an uploaded .dar (multipart field
// `dar`). Instance-independent — no participant involved.
func handleAnalyzerUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, analyzerUploadMax)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeErrorWithCode(w, http.StatusRequestEntityTooLarge,
				ErrCodeRequestTooLarge,
				fmt.Sprintf("upload exceeds %d MiB cap", analyzerUploadMax>>20))
			return
		}
		writeError(w, http.StatusBadRequest, "parse multipart", err)
		return
	}
	f, hdr, err := r.FormFile("dar")
	if err != nil {
		writeError(w, http.StatusBadRequest, "no dar file uploaded (field `dar`)", nil)
		return
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read uploaded file", err)
		return
	}

	rep, err := analyzer.AnalyzeBytes(r.Context(), b)
	if err != nil {
		if writeAnalyzerUnavailable(w, r.Context(), err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "analyze dar", err)
		return
	}

	writeJSON(w, http.StatusOK, types.AnalyzerResponse{
		SchemaVersion: types.SchemaVersion,
		DarName:       hdr.Filename,
		Report:        rep,
	})
}

// handleInstanceAnalyze analyses a DEPLOYED DAR by its main package id:
// fetch the bytes from the participant's admin API, then run the
// analyzer on them. Mirrors dar_inspect.go's GetDar path.
func handleInstanceAnalyze(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := registry.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance name", err)
		return
	}
	id := r.PathValue("id")
	if !looksLikePackageID(id) {
		writeErrorWithCode(w, http.StatusBadRequest,
			ErrCodeInvalidRequest,
			"invalid package id",
			"package id must be 64 hex chars")
		return
	}
	role, ok := resolveRoleParam(w, r)
	if !ok {
		return
	}

	state, err := registry.Read(name)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			writeErrorWithCode(w, http.StatusNotFound, ErrCodeNotFound,
				"instance "+name+" not registered")
			return
		}
		writeError(w, http.StatusInternalServerError, "read state", err)
		return
	}
	cfg, ok := requireParticipantAccess(w, state, name, role)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), analyzerRequestTimeout)
	defer cancel()

	client, err := admin.Connect(ctx, cfg)
	if err != nil {
		writeError(w, http.StatusBadGateway, "dial canton admin", err)
		return
	}
	defer func() { _ = client.Close() }()

	resp, err := client.Package.GetDar(ctx, &adminproto.GetDarRequest{MainPackageId: id})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not_found") ||
			strings.Contains(strings.ToLower(err.Error()), "notfound") {
			writeErrorWithCode(w, http.StatusNotFound, ErrCodeNotFound,
				"no DAR with main package id "+id+" on participant "+role)
			return
		}
		writeError(w, http.StatusBadGateway, "fetch dar", err)
		return
	}
	payload := resp.GetPayload()
	if len(payload) == 0 {
		writeError(w, http.StatusBadGateway, "empty DAR payload from participant", nil)
		return
	}

	rep, err := analyzer.AnalyzeBytes(ctx, payload)
	if err != nil {
		if writeAnalyzerUnavailable(w, ctx, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "analyze dar", err)
		return
	}

	writeJSON(w, http.StatusOK, types.AnalyzerResponse{
		SchemaVersion: types.SchemaVersion,
		Instance:      name,
		PackageID:     id,
		DarName:       darNameFrom(resp.GetData()),
		Report:        rep,
	})
}

// writeAnalyzerUnavailable maps the analyzer's not-configured sentinels
// onto a 503 carrying the same remediation Status() would render.
// Returns true when it handled err (503 written), false otherwise.
func writeAnalyzerUnavailable(w http.ResponseWriter, ctx context.Context, err error) bool {
	if !errors.Is(err, analyzer.ErrDockerNotFound) {
		return false
	}
	detail := analyzer.Status(ctx).Detail
	if detail == "" {
		detail = "daml-analyzer is not available in this environment"
	}
	writeErrorWithCode(w, http.StatusServiceUnavailable, ErrCodeAnalyzerUnavailable, detail)
	return true
}

// darNameFrom derives a display filename from the participant's DAR
// metadata (name-version.dar), falling back to empty when unset.
func darNameFrom(d *adminproto.DarDescription) string {
	if d == nil {
		return ""
	}
	name, ver := d.GetName(), d.GetVersion()
	switch {
	case name != "" && ver != "":
		return name + "-" + ver + ".dar"
	case name != "":
		return name + ".dar"
	default:
		return ""
	}
}
