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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/progress"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/stream"
)

// upBodyMax caps the POST /api/instances request body. Defence-in-
// depth against a malicious-or-buggy client feeding us a 100 MiB
// JSON blob — the handler only needs a tiny request shape.
const upBodyMax = 4 << 10 // 4 KiB

// upJobTimeout is the hard ceiling on a single create-instance
// goroutine. Sized to comfortably outlast the internal docker
// readinessTimeout (25 min, see internal/docker/compose.go
// WaitForHealthy) plus slack for Splice fetch + compose up.
//
// First-run Splice 0.6.4 with no cached images was observed
// taking 18+ minutes — splice container in `health: starting`
// long after canton/participants reached healthy. The earlier
// 10/20-minute caps fired before WaitForHealthy's own deadline
// and surfaced a misleading "Timed out waiting for services"
// while containers were actually still progressing.
//
// 30 minutes total outer cap accommodates: fetch (~1 min cached
// / ~5 min fresh) + docker up (~30s) + WaitForHealthy (~25 min
// worst case) + capture JWTs (~5s) + slack.
//
// This is NOT the HTTP request timeout — the POST returns 202
// immediately; the goroutine runs on its own context, independent
// of the request. Cancellation (BIT-163e) passes a CancelFunc
// into the goroutine's context that DELETE invokes.
const upJobTimeout = 30 * time.Minute

// progressBufferCap is the per-instance topic ring size. Sized
// for a normal up: 8 step.started + 8 step.finished + a few
// step.progress + a few warnings + the done event = ~32 events.
// 128 leaves headroom for verbose compose-log forwarding without
// the oldest events evicting during a 90-second up.
const progressBufferCap = 128

// MountInstances installs the instance-resource routes on mux.
// hub may be nil for callers that don't want the create-instance
// flow (e.g. read-only deployments); in that case POST and the
// SSE endpoint return 503.
//
// Path prefix is fixed at /api/instances. The GET handlers are
// stateless — every call re-reads from registry. The POST handler
// spawns a long-running goroutine that publishes progress events
// to a per-instance topic on hub.
func MountInstances(mux *http.ServeMux, hub *stream.Hub) {
	mux.HandleFunc("GET /api/instances", handleList)
	mux.HandleFunc("GET /api/instances/{name}", handleDetail)
	if hub != nil {
		mux.HandleFunc("POST /api/instances", handleCreate(hub))
		mux.HandleFunc("GET /api/instances/{name}/events", handleInstanceEvents(hub))
		mux.HandleFunc("DELETE /api/instances/{name}/up", handleCancelUp(hub))
		mux.HandleFunc("DELETE /api/instances/{name}", handleScrubInstance(hub))
		mux.HandleFunc("POST /api/instances/{name}/down", handleDownInstance())
	} else {
		// Stub so a misconfigured deployment fails loudly
		// rather than 404 (which the frontend would mistake
		// for a missing endpoint).
		stub := func(w http.ResponseWriter, _ *http.Request) {
			writeErrorWithCode(w, http.StatusServiceUnavailable,
				"SSE_DISABLED",
				"create-instance flow disabled (no event hub configured)",
				"start the server with the default config; --no-hub is a test seam")
		}
		mux.HandleFunc("POST /api/instances", stub)
		mux.HandleFunc("GET /api/instances/{name}/events", stub)
		mux.HandleFunc("DELETE /api/instances/{name}/up", stub)
		mux.HandleFunc("DELETE /api/instances/{name}", stub)
		mux.HandleFunc("POST /api/instances/{name}/down", stub)
	}
	// Container probe + log tail are hub-independent (pure
	// docker reads) — mount them for every deployment.
	mux.HandleFunc("GET /api/instances/{name}/containers", handleInstanceContainers())
	mux.HandleFunc("GET /api/instances/{name}/containers/{container}/logs", handleContainerLogs())
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
	case status == http.StatusConflict:
		return "INSTANCE_EXISTS"
	case status >= 500:
		return ErrCodeInternal
	default:
		return ErrCodeInvalidRequest
	}
}

// ── BIT-163d: async create-instance flow ──────────────────────────

// upRequest is the body shape for POST /api/instances. Mirrors
// `dpm localnet up` flags exactly so CLI and Web UI surface the
// same controls.
type upRequest struct {
	Name           string `json:"name"`
	Version        string `json:"version,omitempty"`         // empty → "latest" server-side
	AllowUncurated bool   `json:"allow_uncurated,omitempty"` // resolve unknown tags upstream
}

// upAcceptedResponse is the 202 body the POST returns. The frontend
// uses events_url to open the EventSource for progress streaming;
// instance is echoed so a client that auto-navigated can pick
// it up from the response without parsing the URL.
type upAcceptedResponse struct {
	SchemaVersion int    `json:"schema_version"`
	Instance      string `json:"instance"`
	EventsURL     string `json:"events_url"`
}

// handleCreate: POST /api/instances → 202 + spawns goroutine.
//
// Validation order (cheapest first; each rejection fails the
// request before any work):
//
//	1. body decode + size cap
//	2. RFC 1123 DNS-label name validation
//	3. duplicate-name check (registry has an entry OR jobs
//	   registry has an in-flight goroutine)
//
// Then:
//
//	4. hub.EnableBuffering(topic, 128)
//	5. context.WithCancel — cancel stored in jobs registry for
//	   the future DELETE handler (BIT-163e); context.WithTimeout
//	   wraps that with the 10-minute job ceiling
//	6. spawn goroutine → RunUp(ctx, SSEProgress, opts)
//	7. return 202 with {instance, events_url}
//
// The goroutine's deferred cleanup:
//
//	defer jobs.Unregister(name)
//	defer hub.ClearBuffer(topic)
//	defer ctxCancel() (releases context resources)
//
// Order matters: Unregister BEFORE ClearBuffer so a fast-
// reconnecting browser that races the cleanup sees either
// (a) the buffer still present and replays final events, or
// (b) the registry already cleared and gets a fresh 404 from
// the SSE endpoint — never an inconsistent state where the
// buffer is gone but the job is still listed.
func handleCreate(hub *stream.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Body cap first.
		r.Body = http.MaxBytesReader(w, r.Body, upBodyMax)

		var req upRequest
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				writeErrorWithCode(w, http.StatusRequestEntityTooLarge,
					ErrCodeRequestTooLarge,
					"request body too large",
					"the create-instance body should be tiny — check you didn't paste binary data")
				return
			}
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid request body",
				"the body must be JSON: {\"name\":\"<dns-label>\", \"version\":\"<tag>\"?}")
			return
		}

		if err := localnet.ValidateName(req.Name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error(),
				"names must be lowercase DNS labels (a-z, 0-9, hyphen); 1–63 chars; can't start or end with hyphen")
			return
		}

		// Reject duplicates against BOTH the registry (an
		// already-running instance) AND the jobs registry (a
		// bring-up that hasn't finished yet). The two failure
		// modes are distinct UX cases — the frontend renders
		// "instance exists, switch to it" vs "instance is
		// being created, watch progress" — but both serve as a
		// reason to refuse the new POST.
		if _, err := registry.Read(req.Name); err == nil {
			writeErrorWithCode(w, http.StatusConflict,
				"INSTANCE_EXISTS",
				"instance "+req.Name+" already exists",
				"pick a different name, or stop the existing one first via `dpm localnet down --name "+req.Name+"`")
			return
		}
		if jobs.Active(req.Name) {
			writeErrorWithCode(w, http.StatusConflict,
				"INSTANCE_CREATING",
				"instance "+req.Name+" is already being created",
				"open the progress stream at /api/instances/"+req.Name+"/events to watch the existing run")
			return
		}

		topic := progress.TopicFor(req.Name)
		hub.EnableBuffering(topic, progressBufferCap)

		// Detached context: the request returns 202 immediately,
		// but the goroutine runs until RunUp completes (or the
		// 10-minute ceiling fires). WithCancel comes outside
		// WithTimeout so the DELETE handler's cancel wins
		// regardless of the timeout's state.
		jobCtx, cancelJob := context.WithTimeout(context.Background(), upJobTimeout)
		// Register BEFORE spawning so a racing second POST
		// loses (sees the entry already present).
		if !jobs.Register(req.Name, cancelJob) {
			// Lost the race with another POST that just won.
			cancelJob()
			hub.ClearBuffer(topic)
			writeErrorWithCode(w, http.StatusConflict,
				"INSTANCE_CREATING",
				"instance "+req.Name+" is already being created",
				"open /api/instances/"+req.Name+"/events to watch the existing run")
			return
		}

		opts := &localnet.UpOptions{
			Name:           req.Name,
			Version:        req.Version,
			AllowUncurated: req.AllowUncurated,
		}

		go func() {
			// Cleanup order matters — see handler godoc.
			defer cancelJob()
			defer hub.ClearBuffer(topic)
			defer jobs.Unregister(req.Name)

			prog := progress.New(hub, req.Name)
			exitCode := localnet.RunUp(jobCtx, prog, opts)
			log.Printf("create instance %q: exit_code=%d", req.Name, exitCode)
		}()

		writeJSON(w, http.StatusAccepted, upAcceptedResponse{
			SchemaVersion: types.SchemaVersion,
			Instance:      req.Name,
			EventsURL:     "/api/instances/" + req.Name + "/events",
		})
	}
}

// downTimeout caps a single down operation. `docker compose down`
// is usually fast (10-30s) but a stuck container can extend it
// well past the default 10s SIGTERM grace per container × N
// services. 3 minutes is generous.
const downTimeout = 3 * time.Minute

// downRequest is the body shape (currently empty; reserved for
// future --keep-data flag once the frontend has a UI for it).
type downRequest struct {
	KeepData bool `json:"keep_data,omitempty"`
}

// handleDownInstance: POST /api/instances/{name}/down.
//
// Synchronous wrapper around localnet.RunDown. Down is fast
// enough that streaming progress over SSE is overkill — the
// modal would barely have time to render before completion.
// (Compare to /up which is minutes-long; that one warrants the
// full SSE choreography.)
//
// Returns 204 on success, 5xx with the captured output on
// failure. Body is application/json; an empty body or
// {"keep_data": true} are both valid.
//
// Refuses on `creating` (409) — a goroutine is mid-up; the
// caller should DELETE /up to cancel first, then this endpoint
// can scrub what's left.
func handleDownInstance() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := registry.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}

		// Body parse. Empty body is fine — treat as defaults.
		r.Body = http.MaxBytesReader(w, r.Body, upBodyMax)
		var req downRequest
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil && err != io.EOF {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid request body",
				"the body should be empty or {\"keep_data\": bool}")
			return
		}

		if jobs.Active(name) {
			writeErrorWithCode(w, http.StatusConflict,
				"INSTANCE_CREATING",
				"instance "+name+" is being created — cancel the bring-up first",
				"call DELETE /api/instances/"+name+"/up to cancel, then retry")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), downTimeout)
		defer cancel()

		// Capture out/err for the failure-response body. The
		// success path discards them — 204 has no body.
		var outBuf, errBuf bytes.Buffer
		exit := localnet.RunDown(ctx, &outBuf, &errBuf, &localnet.DownOptions{
			Name:     name,
			KeepData: req.KeepData,
		})

		if exit == localnet.ExitSuccess {
			log.Printf("down instance %q: ok", name)
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Non-zero exit → 5xx with the cause from stderr.
		// RunDown writes friendly errors to errw; surface those
		// directly so the frontend can render them.
		status := http.StatusInternalServerError
		if exit == localnet.ExitUserError {
			status = http.StatusBadRequest
		} else if exit == localnet.ExitTimeout {
			status = http.StatusRequestTimeout
		}
		cause := errBuf.String()
		if cause == "" {
			cause = "down failed with exit code " + uintToString(uint64(exit))
		}
		log.Printf("down instance %q: exit=%d err=%s", name, exit, cause)
		writeErrorWithCode(w, status,
			"DOWN_FAILED",
			"failed to stop "+name+": "+firstLine(cause),
			"the docker compose down output is in the server log; "+
				"try `dpm localnet down --name "+name+"` from a terminal for full output")
	}
}

// uintToString — stdlib-free integer to ASCII. Duplicated here
// to keep this file's strconv-free convention.
func uintToString(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// firstLine returns just the first line of s. Used to keep error
// summaries one-line-y; the full output goes to the server log.
func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

// ContainerHealth is one row in the per-instance container
// table the UI's ContainerHealth panel renders. Mirrors what
// `docker compose ps --all --format json` emits, narrowed to the
// fields the UI actually needs.
//
// State + Health are the high-leverage diagnostic pair:
//   State    = docker container state — running, restarting,
//              exited, dead, created, paused
//   Health   = docker healthcheck verdict — healthy, unhealthy,
//              starting, "" (no healthcheck defined)
//
// The user's frustration we're addressing: registry's hard-coded
// `running|stopped|failed|...` enum hides truth like "canton is
// in a restart loop while postgres is healthy and splice is
// stuck waiting on canton's admin API." With this list the UI
// can render the per-container truth.
type ContainerHealth struct {
	Name    string `json:"name"`
	Service string `json:"service"`
	State   string `json:"state"`
	Health  string `json:"health,omitempty"`
	Status  string `json:"status"` // raw human string from docker (e.g. "Up 4 minutes (health: starting)")
	Image   string `json:"image,omitempty"`
}

type ContainersResponse struct {
	SchemaVersion int               `json:"schema_version"`
	Instance      string            `json:"instance"`
	Containers    []ContainerHealth `json:"containers"`
	// Counters the frontend uses for a one-glance summary pill.
	HealthyCount  int `json:"healthy_count"`
	StartingCount int `json:"starting_count"`
	UnhealthyCount int `json:"unhealthy_count"`
	RestartingCount int `json:"restarting_count"`
	ExitedCount   int `json:"exited_count"`
}

// handleInstanceContainers: GET /api/instances/{name}/containers.
//
// Runs `docker compose -p <project> ps --all --format json`
// against the registered compose project, parses each line as a
// container record, and returns the narrow shape the
// ContainerHealth panel renders.
//
// 200 with empty containers list if the project name resolves
// but docker has no containers (the instance was scrubbed at the
// docker level but state.json still names a project) — better
// than 404 because the frontend can show "no containers" and
// offer cleanup actions.
//
// Caching: no-store (covered by writeJSON). The frontend polls
// every ~3s while a CreatingPanel or stuck-state row is visible.
func handleInstanceContainers() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := registry.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
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

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		containers, runErr := composePs(ctx, state.ComposeProject)
		if runErr != nil {
			// Docker call failed (daemon down? project label
			// doesn't exist?). Surface the failure as a 503
			// with the underlying error so the UI can render a
			// degraded-mode message.
			writeErrorWithCode(w, http.StatusServiceUnavailable,
				"DOCKER_PROBE_FAILED",
				"could not query docker for compose project "+state.ComposeProject,
				runErr.Error())
			return
		}

		resp := ContainersResponse{
			SchemaVersion: types.SchemaVersion,
			Instance:      name,
			Containers:    containers,
		}
		for _, c := range containers {
			switch c.Health {
			case "healthy":
				resp.HealthyCount++
			case "starting":
				resp.StartingCount++
			case "unhealthy":
				resp.UnhealthyCount++
			}
			switch c.State {
			case "restarting":
				resp.RestartingCount++
			case "exited", "dead":
				resp.ExitedCount++
			}
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// composePs runs `docker compose -p <project> ps --all --format
// json` and parses each NDJSON line into a ContainerHealth.
// Returns an empty slice (no error) when docker reports no
// containers for the project — the project may have been torn
// down out-of-band, or never created.
//
// Error path is reserved for docker-side failures (daemon down,
// compose binary missing) — the handler surfaces those as 503.
func composePs(ctx context.Context, project string) ([]ContainerHealth, error) {
	cmd := exec.CommandContext(ctx,
		"docker", "compose", "-p", project,
		"ps", "--all", "--format", "json")
	out, err := cmd.Output()
	if err != nil {
		// Empty output + non-zero exit usually means "no such
		// project" which we treat as empty-list success. Other
		// errors (daemon down) propagate.
		if exitErr, ok := err.(*exec.ExitError); ok && len(out) == 0 {
			stderrText := string(exitErr.Stderr)
			if strings.Contains(stderrText, "no such") ||
				strings.Contains(stderrText, "not exist") {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("docker compose ps: %w", err)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, nil
	}

	// docker compose ps --format json emits a single JSON ARRAY
	// in newer versions (>= v2.21) or NDJSON in older. Handle
	// both: if the first non-whitespace byte is '[', parse as
	// array; otherwise parse line-by-line.
	trimmed := bytes.TrimLeft(out, " \t\r\n")
	containers := []ContainerHealth{}
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var arr []dockerComposePsEntry
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return nil, fmt.Errorf("parse compose ps JSON array: %w", err)
		}
		for _, e := range arr {
			containers = append(containers, e.toHealth())
		}
		return containers, nil
	}
	// NDJSON fallback.
	scanner := bufio.NewScanner(bytes.NewReader(trimmed))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var e dockerComposePsEntry
		if err := json.Unmarshal(line, &e); err != nil {
			// Best-effort: skip a malformed line rather than
			// fail the whole call.
			continue
		}
		containers = append(containers, e.toHealth())
	}
	return containers, nil
}

// handleContainerLogs: GET /api/instances/{name}/containers/{container}/logs.
//
// Returns the last N lines (default 200, capped at 2000) of the
// named container's logs as text/plain. The frontend renders
// the body in a <pre> with terminal styling.
//
// Container name is path-param-validated against the live
// docker-compose-ps output for the instance's project, so
// arbitrary names can't be passed in (defence-in-depth against
// a misconfigured proxy that forwards untrusted path segments).
//
// Query params:
//
//	tail=<n>  — max lines to return; clamped to [10, 2000].
//	            Default 200.
//	since=<duration> — docker --since flag, e.g. "5m", "30s".
//	            Default empty (whole log).
//
// 404 if the named container isn't in the instance's compose
// project (or doesn't exist in docker at all).
// 503 if docker itself can't be reached.
func handleContainerLogs() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		container := r.PathValue("container")
		if err := registry.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}
		if !validContainerName(container) {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid container name",
				"container names are alphanumeric + hyphens; arbitrary shell metachars rejected")
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

		// Verify the container actually belongs to this
		// instance's project. Without this check, anyone could
		// fetch logs from any container on the host by passing
		// a foreign name. The check is cheap (one
		// docker compose ps) and the defence-in-depth is worth
		// the extra round trip.
		probeCtx, cancelProbe := context.WithTimeout(r.Context(), 5*time.Second)
		containers, probeErr := composePs(probeCtx, state.ComposeProject)
		cancelProbe()
		if probeErr != nil {
			writeErrorWithCode(w, http.StatusServiceUnavailable,
				"DOCKER_PROBE_FAILED",
				"could not query docker", probeErr.Error())
			return
		}
		found := false
		for _, c := range containers {
			if c.Name == container {
				found = true
				break
			}
		}
		if !found {
			writeErrorWithCode(w, http.StatusNotFound,
				ErrCodeNotFound,
				"container "+container+" not in compose project "+state.ComposeProject)
			return
		}

		// tail / since query params with sane bounds.
		tail := 200
		if t := r.URL.Query().Get("tail"); t != "" {
			if n, err := parseClampedInt(t, 10, 2000); err == nil {
				tail = n
			}
		}
		since := r.URL.Query().Get("since")
		// Validate `since` against a strict subset of docker's
		// duration format (digits + h/m/s) to avoid passing
		// shell-y characters through to the docker CLI.
		if since != "" && !validDuration(since) {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid since duration",
				"format: <number><h|m|s>, e.g. 5m, 30s, 2h")
			return
		}

		args := []string{"logs", "--tail", strconv.Itoa(tail)}
		if since != "" {
			args = append(args, "--since", since)
		}
		args = append(args, container)

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "docker", args...)
		// Docker logs are written to stderr for the bootstrap +
		// stdout for application output; merge for the user.
		var combined bytes.Buffer
		cmd.Stdout = &combined
		cmd.Stderr = &combined
		if err := cmd.Run(); err != nil {
			// If we got SOME output, surface it anyway — log
			// tails often partial-fail when the container
			// rotates mid-fetch.
			if combined.Len() == 0 {
				writeErrorWithCode(w, http.StatusServiceUnavailable,
					"DOCKER_LOGS_FAILED",
					"could not tail container logs", err.Error())
				return
			}
		}

		// Plain-text response so the frontend can render in a
		// <pre> without a JSON decode + escape round-trip.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(combined.Bytes())
	}
}

// validContainerName allows alphanumeric, dot, underscore,
// hyphen — the character set docker actually uses for container
// names. Rejects path separators, shell metachars, spaces.
func validContainerName(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.'
		if !ok {
			return false
		}
	}
	return true
}

// validDuration accepts the narrow docker --since format:
// one-or-more digits followed by exactly one unit letter (h/m/s).
// Rejects compound expressions ("1h30m") and shell metachars.
func validDuration(s string) bool {
	if len(s) < 2 || len(s) > 6 {
		return false
	}
	last := s[len(s)-1]
	if last != 'h' && last != 'm' && last != 's' {
		return false
	}
	for i := 0; i < len(s)-1; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// parseClampedInt parses s as an integer and clamps to [lo, hi].
// Returns an error only on non-integer input — out-of-range
// values get silently clamped because the caller's intent is
// clearer that way ("tail=99999" → return the max we'll allow).
func parseClampedInt(s string, lo, hi int) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	if n < lo {
		return lo, nil
	}
	if n > hi {
		return hi, nil
	}
	return n, nil
}

// dockerComposePsEntry mirrors the JSON fields `docker compose ps
// --format json` emits. Only the fields we render are mapped;
// unknown extras (e.g. ExitCode, RunningFor) are ignored so a
// docker version bump that adds new fields doesn't break us.
type dockerComposePsEntry struct {
	Name    string `json:"Name"`
	Service string `json:"Service"`
	State   string `json:"State"`
	Health  string `json:"Health"`
	Status  string `json:"Status"`
	Image   string `json:"Image"`
}

func (e dockerComposePsEntry) toHealth() ContainerHealth {
	return ContainerHealth{
		Name:    e.Name,
		Service: e.Service,
		State:   e.State,
		Health:  e.Health,
		Status:  e.Status,
		Image:   e.Image,
	}
}

// handleScrubInstance: DELETE /api/instances/{name}.
//
// Removes the registry entry for an instance. The narrower
// /api/instances/{name}/up cancels an in-flight goroutine but
// leaves the registry entry alone (the goroutine writes its own
// status=failed before exit). This endpoint is the registry-level
// cleanup — for orphaned `creating` entries left by a server
// restart, or for instances that finished badly and the user
// wants to retry the name.
//
// Safety: refuses to scrub a `running` instance — that path
// belongs to a future DELETE /api/instances/{name}/down (which
// would do `docker compose down` + state cleanup). 409 in that
// case with a remediation hint.
//
// Also refuses to scrub if a job is actively creating — that
// would race the goroutine. 409 with a hint to call /up cancel
// first.
//
// 204 on success; the entry is gone from /api/instances next
// poll. Idempotent against a non-existent name (404 → success
// would be misleading; we keep the honest 404).
//
// Files on disk: removes the per-instance state.json + dir.
// Does NOT remove docker resources — the up may have crashed
// before docker got involved (the common zombie case is exactly
// this), and trying `docker compose down` against an unknown
// project would 5xx for no benefit.
func handleScrubInstance(hub *stream.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := registry.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}

		// Refuse if a job is actively creating — the goroutine
		// would race our cleanup. Caller should DELETE /up first.
		if jobs.Active(name) {
			writeErrorWithCode(w, http.StatusConflict,
				"INSTANCE_CREATING",
				"instance "+name+" is being created — cancel the bring-up first",
				"call DELETE /api/instances/"+name+"/up to cancel, then retry this DELETE")
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

		// Block on `running` — that needs a real `down` flow.
		if state.Status == registry.StatusRunning {
			writeErrorWithCode(w, http.StatusConflict,
				"INSTANCE_RUNNING",
				"instance "+name+" is running — stop it first",
				"run `dpm localnet down --name "+name+"` from a terminal (the Web UI's down endpoint is BIT-173)")
			return
		}

		// Clean the in-memory event buffer if any; then the
		// on-disk state + index entry.
		hub.ClearBuffer(progress.TopicFor(name))
		if err := registry.Delete(name); err != nil {
			writeError(w, http.StatusInternalServerError, "delete state", err)
			return
		}

		log.Printf("scrub instance %q via DELETE", name)
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleInstanceEvents: GET /api/instances/{name}/events.
//
// SSE endpoint for the per-instance progress stream. Subscribes
// to instance:<name> via SubscribeWithReplay so a late client
// (browser opening the EventSource ~50ms after POST returns)
// receives the events that were published before its connection
// completed.
//
// Returns 404 if no buffer exists for the topic — meaning either
// the up finished (and ClearBuffer ran) OR the name was never
// the target of a POST. The frontend distinguishes "instance
// finished" from "instance never existed" via the registry.Read
// followup.
//
// 30s heartbeat (matches the global /events handler). On the
// goroutine's done event the client closes the EventSource;
// idle connections beyond that survive on the heartbeat until
// the user navigates away.
func handleInstanceEvents(hub *stream.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := registry.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError,
				"streaming unsupported", nil)
			return
		}

		// SSE headers — same shape as the global /events handler.
		h := w.Header()
		h.Set("Content-Type", "text/event-stream; charset=utf-8")
		h.Set("Cache-Control", "no-cache, no-transform")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		topic := progress.TopicFor(name)
		ch, cancel := hub.SubscribeWithReplay(topic)
		defer cancel()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				writeInstanceEventFrame(w, ev)
				flusher.Flush()
			case <-ticker.C:
				_, _ = w.Write([]byte(": keepalive\n\n"))
				flusher.Flush()
			}
		}
	}
}

// writeInstanceEventFrame is a minimal SSE encoder for the
// per-instance handler.
//
// We DO NOT emit an `event:` line. The hub's Topic field is
// internal routing data (e.g. "instance:test"); when emitted on
// the wire it becomes a NAMED SSE event, which EventSource's
// onmessage handler ignores by spec (only the default-type events
// fire onmessage; named events require addEventListener(name, …)).
//
// Since the per-instance handler ALWAYS serves a single topic
// (the {name} path param IS the topic), the topic is redundant on
// the wire — the consumer already knows what stream they opened.
// Omitting the event: line means events arrive as the default
// type and the frontend's onmessage catches them as the spec
// intends.
//
// Per spec, multi-line data needs one "data:" prefix per line; we
// strip a trailing newline so the payload doesn't gain a trailing
// empty data: line.
func writeInstanceEventFrame(w http.ResponseWriter, e stream.Event) {
	if e.ID != "" {
		_, _ = fmt.Fprintf(w, "id: %s\n", e.ID)
	}
	if len(e.Data) == 0 {
		_, _ = w.Write([]byte("data:\n\n"))
		return
	}
	body := string(e.Data)
	body = trimRight(body, "\n")
	for _, line := range splitLines(body) {
		_, _ = fmt.Fprintf(w, "data: %s\n", line)
	}
	_, _ = w.Write([]byte("\n"))
}

// trimRight / splitLines — tiny stdlib-free helpers used by
// writeInstanceEventFrame. Keeps this file strings-package-free
// for symmetry with the existing helpers (jsonInt etc).
func trimRight(s, cutset string) string {
	for len(s) > 0 {
		r := s[len(s)-1]
		found := false
		for i := 0; i < len(cutset); i++ {
			if r == cutset[i] {
				found = true
				break
			}
		}
		if !found {
			return s
		}
		s = s[:len(s)-1]
	}
	return s
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// ── BIT-163e: cancel an in-flight create-instance goroutine ───────

// handleCancelUp: DELETE /api/instances/{name}/up.
//
// The CSRF middleware in router.go protects DELETE against
// cross-origin invocations (DELETE isn't a CORS simple method,
// so a browser preflight would fire and be rejected by the
// Origin check). Loopback bind is the outer defence.
//
// Sequence:
//  1. validate name
//  2. lookup the job; 404 if no in-flight goroutine
//  3. publish a synthetic kind=cancelled event so SSE consumers
//     see the user-initiated cancellation BEFORE the natural
//     step.failed events RunUp emits when it notices ctx.Err()
//  4. invoke the registered cancel func — the goroutine's
//     ctx.Done() fires, RunUp returns ExitTimeout via its
//     existing path, the deferred ClearBuffer + Unregister run
//
// 204 No Content on success. The frontend doesn't need a body —
// the SSE stream carries the actual cancellation marker, and
// the registry change becomes visible to /api/instances on the
// goroutine's next state write.
//
// Idempotency: a second DELETE for the same name (after the
// goroutine has already exited) returns 404, NOT 200/204. The
// frontend's "cancel" button should debounce so this only
// matters for genuine retries; the 404 is the right signal that
// there's nothing to cancel.
func handleCancelUp(hub *stream.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := registry.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}

		if !jobs.Active(name) {
			writeErrorWithCode(w, http.StatusNotFound,
				ErrCodeNotFound,
				"no in-flight create job for "+name,
				"the up may have already finished — refresh the instance list to check")
			return
		}

		// Publish cancellation marker BEFORE invoking cancel.
		// Order matters: a fast subscriber reading the SSE in
		// real-time should see kind=cancelled THEN the
		// step.failed events RunUp emits as ctx.Err() propagates
		// through the orchestrator. Reverse order would surface
		// the natural failure ("Interrupted while …") without
		// the user-initiated marker, which the frontend would
		// mis-render as a generic failure.
		progress.PublishCancelled(hub, name, "user requested via DELETE")

		// Cancel returns false if the job vanished between
		// Active() and Cancel(). Treat as success — the goal
		// was achieved (no in-flight work). Don't 404 on a
		// race the user can't observe.
		jobs.Cancel(name)

		log.Printf("cancel instance %q via DELETE", name)
		w.WriteHeader(http.StatusNoContent)
	}
}
