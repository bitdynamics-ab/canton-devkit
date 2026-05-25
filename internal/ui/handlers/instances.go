// Package handlers implements the REST endpoints mounted at /api/*.
//
// Each file in this package owns one resource:
//
//	instances.go — registry-backed instance list + detail (this file)
//	(future) jwt.go, appconfig.go — auth/credential surfaces (BIT-131 follow-on)
//	(future) packages.go         — DAR list (depends on BIT-127 backend)
//	(future) metrics.go          — Prometheus passthrough (depends on BIT-134)
//	(future) logs.go             — last-N docker logs
//	(future) acs.go, tx.go       — ledger views (depend on BIT-132 client)
//
// # Why "registry-backed" lands first
//
// The instance list and detail responses can be built from on-disk
// registry state alone — no docker calls, no ledger client, no
// JWT signer. That's the cheapest meaningful slice to land and review;
// it also gives the M2 frontend something to consume on day one.
//
// # Why we read the registry directly here
//
// `internal/cli/localnet/status.go` exposes a `CollectStatus` function
// intended as the single source of truth for both the CLI's status
// command and the future Web UI handler. As of this PR's branch base
// (post-mockup-refresh on m1-foundation), that function lives on the
// not-yet-merged srikanth/bit-144 branch — depending on it would
// couple this PR to a different review cycle.
//
// Instead, the handler reads registry directly with the same shape.
// When BIT-144 merges, this file should be refactored to delegate to
// localnet.CollectStatus (which also handles the docker `compose ps`
// soft-fail and the JWT redaction). Tracked by TODO(BIT-144-merge).
package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sort"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// MountInstances installs the instance-resource routes on mux. Path
// prefix is fixed at /api/instances. The handlers are stateless;
// every call re-reads from registry, which is cheap (small files)
// and means a concurrent `up`/`down` change is visible immediately.
func MountInstances(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instances", handleList)
	mux.HandleFunc("GET /api/instances/{name}", handleDetail)
}

// handleList: GET /api/instances → types.ListResponse.
//
// Output shape mirrors `localnet list --json` exactly; the Web UI
// dashboard reads this on initial load before subscribing to SSE
// updates.
func handleList(w http.ResponseWriter, _ *http.Request) {
	idx, err := registry.ReadIndex()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read registry index", err)
		return
	}
	resp := types.ListResponse{
		SchemaVersion: types.SchemaVersion,
		Instances:     make([]types.InstanceSummary, 0, len(idx.Entries)),
	}
	var unreadable []string
	for _, e := range idx.Entries {
		row := types.InstanceSummary{
			Name:          e.Name,
			Status:        string(e.Status),
			SpliceVersion: e.SpliceVersion,
			StartedAgo:    "", // computed by the renderer, not the API
		}
		// Per-row state.json read for the port range. Same "best-
		// effort" semantics as `localnet list`: a corrupt state
		// file is reported in the response warning, not as a
		// fatal error.
		if s, err := registry.Read(e.Name); err == nil {
			row.Ports = formatPortRange(s.Ports)
		} else {
			row.Ports = formatPortRange(nil)
			unreadable = append(unreadable, e.Name)
		}
		resp.Instances = append(resp.Instances, row)
	}
	sort.Slice(resp.Instances, func(i, j int) bool {
		return resp.Instances[i].Name < resp.Instances[j].Name
	})
	if len(unreadable) > 0 {
		resp.Warning = "unreadable per-instance state files: " +
			joinComma(unreadable)
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleDetail: GET /api/instances/{name} → types.Instance.
//
// Returns 404 if the instance isn't registered. The 400 vs 404
// distinction matters for the frontend: 400 = malformed name (user
// typed garbage); 404 = well-formed name not currently known.
func handleDetail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := registry.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance name", err)
		return
	}
	s, err := registry.Read(name)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			writeError(w, http.StatusNotFound, "instance not registered", err)
			return
		}
		writeError(w, http.StatusInternalServerError, "read state", err)
		return
	}
	inst := types.Instance{
		SchemaVersion:   types.SchemaVersion,
		Name:            s.Name,
		SpliceVersion:   s.SpliceVersion,
		Status:          string(s.Status),
		CreatedAt:       s.CreatedAt,
		ComposeProject:  s.ComposeProject,
		DockerNetwork:   s.DockerNetwork,
		ContainerPrefix: s.ContainerPrefix,
		ProjectDir:      s.ProjectDir,
		DataDir:         s.DataDir,
	}
	// TODO(BIT-144-merge): once status.go's CollectStatus lands,
	// delegate to it for the Endpoints/Credentials/Services
	// projection (also handles JWT redaction). For now, we surface
	// the cheap fields only — the frontend's dashboard tile renders
	// fine without Services until the live probe lands.
	writeJSON(w, http.StatusOK, inst)
}

// formatPortRange compresses a Ports map (logical → host) into the
// "min–max" shape the dashboard expects. Mirrors
// internal/cli/localnet/list.go's formatPortRange — see the godoc
// there for the allowlist rationale.
//
// TODO(BIT-146-merge): once list.go merges, this should be
// extracted to a shared helper rather than duplicated.
func formatPortRange(ports map[string]int) string {
	if len(ports) == 0 {
		return "—"
	}
	allowlist := []string{
		"app_user_ui", "app_provider_ui", "sv_ui",
		"swagger_ui", "postgres",
	}
	var lo, hi int
	first := true
	for _, k := range allowlist {
		p, ok := ports[k]
		if !ok || p <= 0 {
			continue
		}
		if first || p < lo {
			lo = p
		}
		if first || p > hi {
			hi = p
		}
		first = false
	}
	if first {
		return "—"
	}
	if lo == hi {
		return jsonInt(lo)
	}
	return jsonInt(lo) + "–" + jsonInt(hi)
}

// jsonInt is a tiny stringer used by formatPortRange. We don't
// pull strconv for a one-liner.
func jsonInt(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	negative := n < 0
	if negative {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// joinComma is a tiny strings.Join replacement that keeps this file
// strings-package-free. Used once for the warning string.
func joinComma(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	n := len(ss) - 1
	for _, s := range ss {
		n += len(s)
	}
	b := make([]byte, 0, n)
	for i, s := range ss {
		if i > 0 {
			b = append(b, ',', ' ')
		}
		b = append(b, s...)
	}
	return string(b)
}

// writeJSON is the shared JSON-response helper. Indented for
// human-readability (browsers and `curl | jq` both prefer it);
// the gzip middleware (future) will erase the size cost.
//
// Reviewer pin (PR #43 round-2 Cache-Control): every API JSON
// response carries no-store. Without it, browsers and HTTP
// proxies can cache responses that include credentials (the
// JWT endpoint, app-config) or that change frequently (instance
// list). The Vite bundle (handled in assets.go) opts INTO
// hashed-file caching separately; this default applies only to
// /api/* responses written through this helper.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(body)
}

// errorBody is the canonical error response shape — aligned with
// PR #36's FriendlyError taxonomy in internal/localnet/friendly_errors.go.
//
// Reviewer pin (PR #43 #e): the previous shape was a free-form
// {error, detail} pair. The CLI's friendly_errors carries
// (Code, Summary, Remediation[]) and the frontend already knows
// how to render that triple. Mirroring keeps one error taxonomy
// across CLI and UI surfaces.
//
// Fields:
//   - Code: stable token scripts/frontends branch on (e.g.
//     "INSTANCE_NOT_FOUND"). Never renamed once shipped.
//   - Error: one-line human summary (toast).
//   - Detail: cause string for the dev-tools view; populated only
//     for 5xx (server-side) so 4xx (client-input) errors don't
//     echo attacker-controlled strings.
//   - Remediation: ordered action list ("try X then Y").
type errorBody struct {
	Code        string   `json:"code"`
	Error       string   `json:"error"`
	Detail      string   `json:"detail,omitempty"`
	Remediation []string `json:"remediation,omitempty"`
}

// Stable error-code tokens. Mirror the ErrorCode constants in
// internal/localnet/friendly_errors.go where applicable; new codes
// here belong in the docs at devkit.dev/e/<CODE>.
const (
	ErrCodeInvalidRequest  = "INVALID_REQUEST"
	ErrCodeNotFound        = "NOT_FOUND"
	ErrCodeInternal        = "INTERNAL"
	ErrCodeRegistry        = "REGISTRY_READ_FAILED"
	ErrCodeUnknownRole     = "UNKNOWN_ROLE"
	ErrCodeUnknownFormat   = "UNKNOWN_FORMAT"
	ErrCodeRequestTooLarge = "REQUEST_TOO_LARGE"
)

// writeError emits a structured error.
//
// Reviewer pin (PR #43 round-2 5xx leak): the previous shape
// included the raw cause string for 5xx errors. That leaked
// filesystem paths (e.g. "read /home/user/.canton-devkit/...")
// into the response body — visible to anyone on the loopback
// AND to anyone who can screenshot a JS error. Now:
//   - 4xx: code + summary only (no cause)
//   - 5xx: code + summary only AS WELL — the cause is LOGGED
//     server-side via log.Default so the operator can diagnose,
//     but never leaves the box. A correlation ID would be
//     better than the implicit log/wire pairing; tracked for
//     a future observability pass.
//
// `summary` is the human-facing label (rendered as a toast);
// it must not include attacker-controlled strings — the caller's
// responsibility, but the helper enforces "no Detail" so a
// careless cause.Error() can't leak.
func writeError(w http.ResponseWriter, status int, summary string, cause error) {
	if cause != nil && status >= 500 {
		// Log server-side, don't ship to client.
		log.Printf("handler error: status=%d code=%s summary=%q cause=%v",
			status, codeForStatus(status), summary, cause)
	}
	writeJSON(w, status, errorBody{
		Code:  codeForStatus(status),
		Error: summary,
	})
}

// writeErrorWithCode is the variant used when the handler wants
// to pin a specific stable code rather than the status-derived
// default.
func writeErrorWithCode(w http.ResponseWriter, status int, code, summary string, remediation ...string) {
	writeJSON(w, status, errorBody{
		Code:        code,
		Error:       summary,
		Remediation: remediation,
	})
}

// codeForStatus is the default code mapping. Handlers wanting a
// more specific code use writeErrorWithCode.
func codeForStatus(status int) string {
	switch {
	case status == http.StatusNotFound:
		return ErrCodeNotFound
	case status == http.StatusBadRequest:
		return ErrCodeInvalidRequest
	case status == http.StatusRequestEntityTooLarge:
		return ErrCodeRequestTooLarge
	case status >= 500:
		return ErrCodeInternal
	default:
		return ErrCodeInvalidRequest
	}
}
