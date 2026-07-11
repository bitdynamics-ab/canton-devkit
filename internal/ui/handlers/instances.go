// Package handlers implements the REST endpoints mounted at /api/*.
// Each file in this package owns one resource.
//
// The CLI status command and Web UI detail endpoint share
// localnet.CollectStatus so JSON shape, Docker soft-fail handling,
// endpoint projection, and JWT redaction do not drift. See
// CONTRIBUTING.md "CLI ↔ Web UI parity".
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/containers"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/httpsec"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/progress"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/stream"
)

// upBodyMax caps the POST /api/instances request body. Defence-in-
// depth against a malicious-or-buggy client feeding us a 100 MiB
// JSON blob — the handler only needs a tiny request shape.
const upBodyMax = 4 << 10 // 4 KiB

// upJobTimeout is the hard ceiling on a single create-instance
// goroutine. It must outlast the internal docker readinessTimeout
// (25 min, see internal/docker/compose.go WaitForHealthy) plus
// Splice fetch (~5 min fresh) and compose up — a first-run bring-up
// with no cached images can take 18+ minutes; a shorter cap fires
// before WaitForHealthy's own deadline and surfaces a misleading
// timeout while containers are still progressing.
//
// This is NOT the HTTP request timeout — the POST returns 202
// immediately; the goroutine runs on its own context, cancellable
// via DELETE.
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
		mux.HandleFunc("POST /api/instances/{name}/up", handleResumeInstance(hub))
		mux.HandleFunc("POST /api/instances/{name}/recreate", handleRecreateInstance(hub))
		mux.HandleFunc("POST /api/instances/{name}/observability", handleObservabilityToggle())
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
		mux.HandleFunc("POST /api/instances/{name}/up", stub)
		mux.HandleFunc("POST /api/instances/{name}/recreate", stub)
	}
	// Container probe + log tail + restart are hub-independent
	// (pure docker calls) — mount them for every deployment.
	mux.HandleFunc("GET /api/instances/{name}/containers", handleInstanceContainers())
	mux.HandleFunc("GET /api/instances/{name}/containers/{container}/logs", handleContainerLogs())
	mux.HandleFunc("POST /api/instances/{name}/containers/{container}/restart", handleContainerRestart())
	// Pause / resume — docker compose pause/unpause; hub-
	// independent. CLI counterpart: `localnet pause` / `localnet resume`.
	mux.HandleFunc("POST /api/instances/{name}/pause", handlePauseInstance(true))
	mux.HandleFunc("POST /api/instances/{name}/resume", handlePauseInstance(false))
	// Stop — docker compose stop; hub-independent, synchronous
	// (near-instant, containers kept). CLI counterpart: `localnet stop`.
	mux.HandleFunc("POST /api/instances/{name}/stop", handleStopInstance())
	// Start — the fast compose-start path is synchronous (204), but a
	// fall-through to a full bring-up needs the hub's SSE machinery,
	// so it's only mounted when the hub is present (stubbed above).
	if hub != nil {
		mux.HandleFunc("POST /api/instances/{name}/start", handleStartInstance(hub))
	} else {
		mux.HandleFunc("POST /api/instances/{name}/start", func(w http.ResponseWriter, _ *http.Request) {
			writeErrorWithCode(w, http.StatusServiceUnavailable,
				"SSE_DISABLED",
				"start flow disabled (no event hub configured)",
				"start the server with the default config; --no-hub is a test seam")
		})
	}
}

// handleStopInstance: POST /api/instances/{name}/stop.
// Synchronous wrapper around localnet.RunStop (docker compose stop) —
// containers are kept for a fast `start`. 204 on success; mapped error
// codes on failure. CLI counterpart: `localnet stop`.
func handleStopInstance() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := registry.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), downTimeout)
		defer cancel()

		var outBuf, errBuf bytes.Buffer
		exit := localnet.RunStop(ctx, &outBuf, &errBuf, &localnet.StopOptions{Name: name})
		if exit == localnet.ExitSuccess {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		status := http.StatusInternalServerError
		switch exit {
		case localnet.ExitUserError:
			status = http.StatusBadRequest
		case localnet.ExitTimeout:
			status = http.StatusRequestTimeout
		}
		cause := firstNonWarningLine(errBuf.String())
		if cause == "" {
			cause = "operation failed with exit code " + strconv.FormatUint(uint64(exit), 10)
		}
		writeErrorWithCode(w, status, "STOP_FAILED", cause)
	}
}

// handleStartInstance: POST /api/instances/{name}/start.
//
// Mirrors the CLI's intelligent `localnet start`:
//
//   - stopped + containers present → fast `docker compose start`,
//     synchronous, returns 204
//   - stopped + containers removed, or failed/partial → falls back to a
//     full bring-up, returns 202 + an events_url the UI subscribes to
//     (reusing the resume/create SSE machinery)
//   - not registered → 404 (the UI's Start button only shows for
//     registered rows; use POST /api/instances to create)
//   - running → 204 (idempotent no-op)
//   - paused → 409 (use /resume)
//
// The 204-vs-202 split lets the frontend branch: 204 → just refetch;
// 202 → open the existing create-progress modal.
func handleStartInstance(hub *stream.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := localnet.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}

		state, err := registry.Read(name)
		if err != nil {
			if errors.Is(err, registry.ErrNotFound) {
				writeErrorWithCode(w, http.StatusNotFound, ErrCodeNotFound,
					"instance "+name+" not registered",
					"create it first via POST /api/instances")
				return
			}
			writeError(w, http.StatusInternalServerError, "read state", err)
			return
		}
		switch state.Status {
		case registry.StatusRunning:
			w.WriteHeader(http.StatusNoContent) // idempotent
			return
		case registry.StatusPaused:
			writeErrorWithCode(w, http.StatusConflict, "INSTANCE_PAUSED",
				"instance "+name+" is paused",
				"resume it via POST /api/instances/"+name+"/resume")
			return
		}

		// stopped / failed / partial: fast-start iff the containers
		// still exist; otherwise fall back to a full bring-up.
		existing, listErr := containers.List(r.Context(), state.ComposeProject)
		if listErr == nil && len(existing) > 0 {
			ctx, cancel := context.WithTimeout(r.Context(), downTimeout)
			defer cancel()
			var outBuf, errBuf bytes.Buffer
			// SkipWait: return quickly; the reconciler promotes to
			// running once healthy (same precedent as restart no-wait).
			// The Progress sink is only used on RunStart's up-fallback
			// path, which the containers-present branch can't reach, so
			// a discard sink is safe here.
			exit := localnet.RunStart(ctx,
				localnet.NewTextProgress(io.Discard, io.Discard),
				&outBuf, &errBuf,
				&localnet.StartOptions{Name: name, SkipWait: true})
			if exit == localnet.ExitSuccess {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			status := http.StatusInternalServerError
			switch exit {
			case localnet.ExitUserError:
				status = http.StatusBadRequest
			case localnet.ExitTimeout:
				status = http.StatusRequestTimeout
			}
			cause := firstNonWarningLine(errBuf.String())
			if cause == "" {
				cause = "operation failed with exit code " + strconv.FormatUint(uint64(exit), 10)
			}
			writeErrorWithCode(w, status, "START_FAILED", cause)
			return
		}

		// Containers gone → full bring-up via the async SSE path.
		startBringUp(w, name, state.SpliceVersion, state.Profiles, hub)
	}
}

// startBringUp kicks off a full `up` for an existing (registered but
// container-less) instance and returns 202 + events_url. Extracted so
// handleStartInstance can reuse the same job/SSE machinery the resume
// and create handlers use.
func startBringUp(w http.ResponseWriter, name, version string, profiles []string, hub *stream.Hub) {
	jobCtx, cancelJob := context.WithTimeout(context.Background(), upJobTimeout)
	if !jobs.Register(name, cancelJob) {
		cancelJob()
		writeErrorWithCode(w, http.StatusConflict,
			"INSTANCE_CREATING",
			"instance "+name+" is already being brought up",
			"open /api/instances/"+name+"/events to watch the existing run")
		return
	}

	topic := progress.TopicFor(name)
	hub.EnableBuffering(topic, progressBufferCap)

	opts := &localnet.UpOptions{Name: name, Version: version, Profiles: profiles}
	go func() {
		defer cancelJob()
		defer hub.ClearBuffer(topic)
		defer jobs.Unregister(name)
		prog := progress.New(hub, name)
		exitCode := localnet.RunUp(jobCtx, prog, opts)
		log.Printf("start (bring-up) instance %q: exit_code=%d", name, exitCode)
	}()

	writeJSON(w, http.StatusAccepted, upAcceptedResponse{
		SchemaVersion: types.SchemaVersion,
		Instance:      name,
		EventsURL:     "/api/instances/" + name + "/events",
	})
}

// handlePauseInstance: POST /api/instances/{name}/pause|resume.
// Synchronous wrapper around localnet.RunPause / RunResume — both are
// near-instant (a SIGSTOP/SIGCONT signal to the containers), so no async
// job is needed. 204 on success; the run's stderr is surfaced on failure.
func handlePauseInstance(pause bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := registry.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), downTimeout)
		defer cancel()

		var outBuf, errBuf bytes.Buffer
		opts := &localnet.PauseOptions{Name: name}
		var exit int
		if pause {
			exit = localnet.RunPause(ctx, &outBuf, &errBuf, opts)
		} else {
			exit = localnet.RunResume(ctx, &outBuf, &errBuf, opts)
		}
		if exit == localnet.ExitSuccess {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		status := http.StatusInternalServerError
		switch exit {
		case localnet.ExitUserError:
			status = http.StatusBadRequest
		case localnet.ExitTimeout:
			status = http.StatusRequestTimeout
		}
		cause := firstNonWarningLine(errBuf.String())
		if cause == "" {
			cause = "operation failed with exit code " + strconv.FormatUint(uint64(exit), 10)
		}
		writeErrorWithCode(w, status, "PAUSE_FAILED", cause)
	}
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
		// Best-effort, matching `localnet list`: a corrupt state
		// file becomes a response warning, not a fatal error.
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
			strings.Join(unreadable, ", ")
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
	inst, err := localnet.CollectStatus(r.Context(), name, true, false)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			writeError(w, http.StatusNotFound, "instance not registered", err)
			return
		}
		writeError(w, http.StatusInternalServerError, "read state", err)
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

// formatPortRange compresses a Ports map (logical → host) into the
// "min–max" shape the dashboard expects. Mirrors
// internal/cli/localnet/list.go's formatPortRange — see the godoc
// there for the allowlist rationale.
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
		return strconv.Itoa(lo)
	}
	return strconv.Itoa(lo) + "–" + strconv.Itoa(hi)
}

// writeJSON is the shared JSON-response helper. Indented for
// human-readability (browsers and `curl | jq` both prefer it).
//
// Every API JSON response carries no-store: without it, browsers and
// HTTP proxies can cache responses that include credentials (the
// JWT endpoint, app-config) or that change frequently (instance
// list). The Vite bundle (assets.go) opts into hashed-file caching
// separately.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(body)
}

// errorBody is the canonical error response shape — aligned with the
// FriendlyError taxonomy in internal/localnet/friendly_errors.go.
//
// The CLI's friendly_errors carries (Code, Summary, Remediation[]) and
// the frontend already knows how to render that triple. Mirroring keeps
// one error taxonomy across CLI and UI surfaces.
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
	ErrCodeForbidden       = "FORBIDDEN"
	ErrCodeNotFound        = "NOT_FOUND"
	ErrCodeInternal        = "INTERNAL"
	ErrCodeRegistry        = "REGISTRY_READ_FAILED"
	ErrCodeUnknownRole     = "UNKNOWN_ROLE"
	ErrCodeUnknownFormat   = "UNKNOWN_FORMAT"
	ErrCodeRequestTooLarge = "REQUEST_TOO_LARGE"
)

// writeError emits a structured error.
//
// The cause string is never shipped to the client for 5xx errors:
// including it would leak filesystem paths (e.g.
// "read /home/user/.canton-devkit/...") into the response body — visible
// to anyone on the loopback AND to anyone who can screenshot a JS error.
// Instead:
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
// default. Used across this and sibling handlers (snapshots,
// metrics, …).
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

// ── async create-instance flow ────────────────────────────

// upRequest is the body shape for POST /api/instances. Mirrors
// `dpm localnet up` flags exactly so CLI and Web UI surface the
// same controls.
type upRequest struct {
	Name           string   `json:"name"`
	Version        string   `json:"version,omitempty"`         // empty → "latest" server-side
	AllowUncurated bool     `json:"allow_uncurated,omitempty"` // resolve unknown tags upstream
	Profiles       []string `json:"profiles,omitempty"`        // docker-compose profiles; e.g. ["observability"]
	PortBase       int      `json:"port_base,omitempty"`       // >0 → deterministic ports from this base (CLI --port-base parity)
}

// allowedProfiles is derived from localnet.KnownProfiles() so it can
// never drift from the CLI's set — the create handler validates through
// localnet.ValidateProfiles directly; this map is a lookup the
// handler-package tests pin the accepted set against. A single source
// of truth lives in internal/localnet (CONTRIBUTING.md "mirror the
// guards").
var allowedProfiles = func() map[string]bool {
	m := make(map[string]bool, len(localnet.KnownProfiles()))
	for _, p := range localnet.KnownProfiles() {
		m[p] = true
	}
	return m
}()

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
// Validation runs cheapest-first (body decode + size cap, DNS-label
// name, duplicate-name check against both the registry and in-flight
// jobs) before any work; then the handler enables SSE buffering,
// registers a cancellable job context, spawns the RunUp goroutine,
// and returns 202 with {instance, events_url}.
//
// The goroutine's deferred cleanup runs Unregister BEFORE ClearBuffer
// so a fast-reconnecting browser that races the cleanup sees either
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
				"pick a different name, or stop the existing one first via `dpm localnet down "+req.Name+"`")
			return
		}
		if jobs.Active(req.Name) {
			writeErrorWithCode(w, http.StatusConflict,
				"INSTANCE_CREATING",
				"instance "+req.Name+" is already being created",
				"open the progress stream at /api/instances/"+req.Name+"/events to watch the existing run")
			return
		}

		// Resolve the version up front so the synchronous preflight
		// gate uses the same Splice version RunUp will. Curated tags
		// resolve offline; uncurated tags (allow_uncurated) resolve
		// against upstream so the gate can apply the Major-aware
		// memory floor (see splice.MinMemoryFor) — otherwise the Web
		// UI could 202 a host that then OOM-loops mid bring-up. If
		// upstream resolution itself fails (network blip, bad tag) we
		// DON'T fail the create here — RunUp surfaces that in-stream
		// with the proper error code.
		if gateVersion, ok := resolveForGate(r.Context(), req.Version, req.AllowUncurated); ok {
			report := runPreflightForVersion(r.Context(), gateVersion)
			if !report.OK {
				w.Header().Set("X-Preflight-Failed", "1")
				writeJSON(w, http.StatusUnprocessableEntity, report)
				return
			}
		} else if !req.AllowUncurated {
			// Curated resolution failed and the caller didn't opt into
			// uncurated — same shape RunUp would produce for this input.
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"unknown splice version: "+req.Version,
				"call GET /api/splice/versions for the curated list, or set allow_uncurated to bypass")
			return
		}

		topic := progress.TopicFor(req.Name)
		hub.EnableBuffering(topic, progressBufferCap)

		// Detached context: the request returns 202 immediately,
		// but the goroutine runs until RunUp completes (or the
		// upJobTimeout ceiling fires).
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

		// Validate any caller-supplied profiles against the shared
		// allowlist (localnet.ValidateProfiles — the SAME guard RunUp
		// enforces, so the two surfaces can't drift; CONTRIBUTING.md
		// "mirror the guards"). Unknown profiles are an explicit 400
		// rather than a silent drop — a typo'd "observabilty" should
		// NOT quietly produce an instance without Prometheus.
		if err := localnet.ValidateProfiles(req.Profiles); err != nil {
			cancelJob()
			hub.ClearBuffer(topic)
			jobs.Unregister(req.Name)
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				err.Error(),
				"supported profiles: "+strings.Join(localnet.KnownProfiles(), ", "))
			return
		}

		// port_base, when supplied, must fit a usable block (0 = auto).
		// Validate the FULL range up front with a 400 — both too-low and
		// too-high — rather than letting a bad value fail later inside
		// DeriveUIPorts. The upper bound mirrors up.go's effective
		// env-var block (the observability profile adds two ports), so
		// the gate matches exactly what RunUp will try to allocate.
		if req.PortBase != 0 {
			nPorts := len(localnet.UIPortEnvVars())
			for _, p := range req.Profiles {
				if p == localnet.ObservabilityProfileName {
					nPorts += len(localnet.ObservabilityPortEnvVars())
				}
			}
			maxBase := 65535 - nPorts
			if req.PortBase < localnet.MinPortBase || req.PortBase > maxBase {
				cancelJob()
				hub.ClearBuffer(topic)
				jobs.Unregister(req.Name)
				writeErrorWithCode(w, http.StatusBadRequest,
					ErrCodeInvalidRequest,
					fmt.Sprintf("port_base %d is out of range", req.PortBase),
					fmt.Sprintf("use 0 (auto) or a base in %d..%d", localnet.MinPortBase, maxBase))
				return
			}
		}

		opts := &localnet.UpOptions{
			Name:           req.Name,
			Version:        req.Version,
			AllowUncurated: req.AllowUncurated,
			Profiles:       req.Profiles,
			PortBase:       req.PortBase,
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
// future per-instance down options).
type downRequest struct{}

// handleResumeInstance: POST /api/instances/{name}/up.
//
// Brings a previously-stopped instance back up, reusing its
// recorded Splice version and ports. Symmetric counterpart to
// `POST /api/instances/{name}/down`. The general create flow
// (`POST /api/instances`) refuses when the name already exists,
// so the UI needs this dedicated verb to surface a "Start"
// button on the stopped row.
//
// Path-existence semantics:
//
//   - 404 — name not in the registry (user typoed)
//   - 409 INSTANCE_RUNNING — already running; use /down first
//   - 409 INSTANCE_CREATING — a bring-up is in flight; subscribe
//     to the existing events stream instead
//   - 202 — kicked off; events stream at /api/instances/{name}/events
//
// Uses the same goroutine / SSE shape as handleCreate so the
// frontend reuses its create-progress modal verbatim.
func handleResumeInstance(hub *stream.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := localnet.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}
		// Invert read-then-acquire order to close a TOCTOU race.
		// Reading state, deciding to proceed, then registering the
		// job leaves a window where a concurrent /up could register
		// first, get the lock, and flip the instance to running —
		// leaving us racing to start a duplicate. Instead: reserve
		// the job slot FIRST, then read state, recheck status. If
		// anything changed after we won the slot we release and
		// surface the right 409.
		jobCtx, cancelJob := context.WithTimeout(context.Background(), upJobTimeout)
		if !jobs.Register(name, cancelJob) {
			cancelJob()
			writeErrorWithCode(w, http.StatusConflict,
				"INSTANCE_CREATING",
				"instance "+name+" is already being brought up",
				"open /api/instances/"+name+"/events to watch the existing run")
			return
		}

		prior, err := registry.Read(name)
		if err != nil {
			jobs.Unregister(name)
			cancelJob()
			if errors.Is(err, registry.ErrNotFound) {
				writeErrorWithCode(w, http.StatusNotFound,
					ErrCodeNotFound,
					"instance "+name+" not registered",
					"create it first via POST /api/instances")
				return
			}
			writeError(w, http.StatusInternalServerError, "read state", err)
			return
		}
		if prior.Status == registry.StatusRunning {
			jobs.Unregister(name)
			cancelJob()
			writeErrorWithCode(w, http.StatusConflict,
				"INSTANCE_RUNNING",
				"instance "+name+" is already running",
				"to restart, stop it first via POST /api/instances/"+name+"/down")
			return
		}

		topic := progress.TopicFor(name)
		hub.EnableBuffering(topic, progressBufferCap)

		// Reuse the recorded Splice version so a resume doesn't
		// silently upgrade. The user explicitly upgrades by going
		// through the create flow with a new --version.
		opts := &localnet.UpOptions{
			Name:    name,
			Version: prior.SpliceVersion,
		}

		go func() {
			defer cancelJob()
			defer hub.ClearBuffer(topic)
			defer jobs.Unregister(name)
			prog := progress.New(hub, name)
			exitCode := localnet.RunUp(jobCtx, prog, opts)
			log.Printf("resume instance %q: exit_code=%d", name, exitCode)
		}()

		writeJSON(w, http.StatusAccepted, upAcceptedResponse{
			SchemaVersion: types.SchemaVersion,
			Instance:      name,
			EventsURL:     "/api/instances/" + name + "/events",
		})
	}
}

// profilesFromComposeFiles infers the docker-compose profile set
// the instance was originally created with by inspecting its
// recorded ComposeFiles. The observability and tokens-v2 overlays
// each ship a distinctive filename; presence of the overlay path
// in state.ComposeFiles is a reliable signal the corresponding
// profile is in effect.
//
// Used by the restart flow so a down → up cycle re-enables the
// same `--profile <name>` flags the original `localnet up` was
// invoked with. Without this, restarting an instance that was
// created with `--profile observability` would silently lose its
// Prometheus + Grafana sidecars.
func profilesFromComposeFiles(files []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range files {
		// Match by basename so changes to the overlay's parent
		// dir layout don't break detection.
		base := f
		if i := strings.LastIndexByte(f, '/'); i >= 0 {
			base = f[i+1:]
		}
		switch base {
		case "observability.yaml":
			if !seen[localnet.ObservabilityProfileName] {
				out = append(out, localnet.ObservabilityProfileName)
				seen[localnet.ObservabilityProfileName] = true
			}
		case "tokens-v2.yaml":
			if !seen[localnet.TokensV2ProfileName] {
				out = append(out, localnet.TokensV2ProfileName)
				seen[localnet.TokensV2ProfileName] = true
			}
		}
	}
	return out
}

// handleRecreateInstance: POST /api/instances/{name}/recreate.
//
// Full down → up cycle for an existing instance, reusing the
// recorded SpliceVersion + inferred profile set. The UI surfaces
// this as a single "Restart" button so users don't have to
// manually compose a Stop followed by a Start.
//
// Verb semantics:
//
//   - 404 — name not in the registry
//   - 400 — invalid name (DNS-label validation)
//   - 409 INSTANCE_CREATING — a bring-up / down / restart goroutine
//     is already in flight for this name (jobs.Register loses the
//     race); caller should subscribe to the existing events stream
//   - 202 — kicked off; events stream at /api/instances/{name}/events
//
// Concurrency model:
//
// The handler reserves the per-name slot in `jobs` BEFORE doing
// any state read (same ordering as handleResumeInstance —
// inverting read-then-acquire avoids a window where a concurrent
// /up/restart could win the lock between our read and our spawn).
// Once registered, the goroutine runs RunDown then RunUp serially;
// each of those takes the per-instance `registry.Lock` internally,
// so the restart never bypasses the lock pattern the down/up
// handlers depend on.
//
// Idempotency: a second POST while the first is running loses the
// jobs.Register race and gets 409 INSTANCE_CREATING — it does NOT
// double-execute the down/up cycle.
//
// What the restart preserves:
//
//   - SpliceVersion (read from state.json before the down call)
//   - Profiles (derived from ComposeFiles via profilesFromComposeFiles)
//   - Overlay env, credentials, DSO state, ports — all preserved
//     because RunDown leaves them on disk and RunUp re-reads them
//
// What it does NOT do: regenerate credentials, recreate the
// registry entry, allocate new ports, or upgrade the Splice version.
func handleRecreateInstance(hub *stream.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := localnet.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}

		// Reserve the job slot first (same ordering as resume).
		// Outer context is detached so the goroutine survives the
		// HTTP response cycle; the timeout combines down (3min) +
		// up (30min) with some slack.
		jobCtx, cancelJob := context.WithTimeout(context.Background(),
			upJobTimeout+downTimeout)
		if !jobs.Register(name, cancelJob) {
			cancelJob()
			writeErrorWithCode(w, http.StatusConflict,
				"INSTANCE_CREATING",
				"instance "+name+" is already being created or restarted",
				"open /api/instances/"+name+"/events to watch the existing run")
			return
		}

		// Read state AFTER claiming the job slot. If the name isn't
		// registered or read fails, release the slot before we
		// surface the error.
		prior, err := registry.Read(name)
		if err != nil {
			jobs.Unregister(name)
			cancelJob()
			if errors.Is(err, registry.ErrNotFound) {
				writeErrorWithCode(w, http.StatusNotFound,
					ErrCodeNotFound,
					"instance "+name+" not registered",
					"create it first via POST /api/instances")
				return
			}
			writeError(w, http.StatusInternalServerError, "read state", err)
			return
		}

		topic := progress.TopicFor(name)
		hub.EnableBuffering(topic, progressBufferCap)

		// Capture identity values BEFORE the goroutine spawns so we
		// don't race the goroutine reading `prior` after the HTTP
		// response has already returned.
		recordedVersion := prior.SpliceVersion
		// Prefer the authoritative persisted profile set; fall
		// back to sniffing the compose overlay filenames for instances
		// created before the Profiles field existed.
		recordedProfiles := prior.Profiles
		if len(recordedProfiles) == 0 {
			recordedProfiles = profilesFromComposeFiles(prior.ComposeFiles)
		}

		go func() {
			defer cancelJob()
			defer hub.ClearBuffer(topic)
			defer jobs.Unregister(name)
			recreateWork(jobCtx, hub, name, recordedVersion, recordedProfiles)
		}()

		writeJSON(w, http.StatusAccepted, upAcceptedResponse{
			SchemaVersion: types.SchemaVersion,
			Instance:      name,
			EventsURL:     "/api/instances/" + name + "/events",
		})
	}
}

// recreateWork runs the down→up cycle for a restart. It's a package
// var, not an inline closure, so tests can substitute a deterministic
// fake and assert the jobs.Register idempotency contract without the
// detached goroutine shelling out to a real `docker compose`.
// Production wiring is realRecreateWork.
var recreateWork = realRecreateWork

// realRecreateWork is the production restart worker: RunDown then RunUp,
// reusing the recorded version + profiles so a restart neither upgrades
// nor sheds overlays.
func realRecreateWork(ctx context.Context, hub *stream.Hub, name, version string, profiles []string) {
	prog := progress.New(hub, name)

	// Phase 1: down. RunDown takes the per-instance lock itself; we
	// don't pre-acquire one here or we'd deadlock. Capture stderr so a
	// down failure can be logged (the user sees the failure event via
	// SSE progress; this is for the operator-side log only).
	var downOut, downErr bytes.Buffer
	downExit := localnet.RunDown(ctx, &downOut, &downErr,
		&localnet.DownOptions{Name: name})
	if downExit != localnet.ExitSuccess {
		log.Printf("restart instance %q: down phase failed exit=%d err=%s",
			name, downExit, downErr.String())
		// Surface as a synthetic warning on the progress stream so the
		// UI sees something even though RunDown writes its own stderr to
		// buffers, not the hub.
		prog.Warn("down phase failed during restart: " +
			firstNonWarningLine(downErr.String()))
		// Continue to up anyway — RunDown is idempotent and a
		// partial-down state still lets RunUp try to reconcile.
	}

	// Phase 2: up. Reuses the recorded version + profiles so the restart
	// doesn't silently upgrade or shed overlays. RunUp emits the full
	// step-event sequence to prog that the create flow uses.
	upOpts := &localnet.UpOptions{
		Name:     name,
		Version:  version,
		Profiles: profiles,
	}
	exitCode := localnet.RunUp(ctx, prog, upOpts)
	log.Printf("restart instance %q: down_exit=%d up_exit=%d",
		name, downExit, exitCode)
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
// failure. Body is application/json; an empty body is valid.
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
				"the body should be empty or {}")
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
			Name: name,
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
		switch exit {
		case localnet.ExitUserError:
			status = http.StatusBadRequest
		case localnet.ExitTimeout:
			status = http.StatusRequestTimeout
		}
		cause := errBuf.String()
		if cause == "" {
			cause = "down failed with exit code " + strconv.FormatUint(uint64(exit), 10)
		}
		log.Printf("down instance %q: exit=%d err=%s", name, exit, cause)
		// RunDown emits non-fatal `Warning:` lines before the actual
		// fatal cause; firstNonWarningLine skips them so the summary
		// reflects the real failure. Full output stays in the server log.
		writeErrorWithCode(w, status,
			"DOWN_FAILED",
			"failed to stop "+name+": "+firstNonWarningLine(cause),
			"the docker compose down output is in the server log; "+
				"try `dpm localnet down "+name+"` from a terminal for full output")
	}
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

// firstNonWarningLine returns the first line of s that does not
// look like a `Warning:` / `WARN:` notice. RunDown emits warning
// lines for non-fatal side issues before the actual fatal cause,
// so a naive firstLine would surface a Warning as the failure
// summary. CRLF safe. If every line is a warning OR the string is
// empty, returns the raw first line so the caller still has
// *something* to show.
func firstNonWarningLine(s string) string {
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '\n' {
			line := s[start:i]
			// Trim a trailing \r so CRLF line endings (Windows
			// docker-compose stderr) don't defeat the prefix
			// match below.
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			if !looksLikeWarningLine(line) && line != "" {
				return line
			}
			start = i + 1
		}
	}
	return firstLine(s)
}

// looksLikeWarningLine matches lines to skip when looking for the
// surfaced failure cause. Recognizes the fixed set of case variants
// RunDown and docker actually emit (Warning/warning/WARNING/WARN/
// warn/Warn) rather than matching truly case-insensitively.
func looksLikeWarningLine(line string) bool {
	// Trim leading whitespace defensively — docker-compose's
	// formatted output sometimes indents continuation lines.
	for len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
		line = line[1:]
	}
	switch {
	case len(line) >= 8 && (line[:8] == "Warning:" || line[:8] == "warning:" || line[:8] == "WARNING:"):
		return true
	case len(line) >= 5 && (line[:5] == "WARN:" || line[:5] == "warn:" || line[:5] == "Warn:"):
		return true
	}
	return false
}

// ContainerHealth is one row in the per-instance container
// table the UI's ContainerHealth panel renders. Mirrors what
// `docker compose ps --all --format json` emits, narrowed to the
// fields the UI actually needs.
//
// State + Health are the high-leverage diagnostic pair:
//
//	State    = docker container state — running, restarting,
//	           exited, dead, created, paused
//	Health   = docker healthcheck verdict — healthy, unhealthy,
//	           starting, "" (no healthcheck defined)
//
// The registry's hard-coded `running|stopped|failed|...` enum hides
// truth like "canton is in a restart loop while postgres is healthy";
// this list lets the UI render the per-container truth.
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
	HealthyCount    int `json:"healthy_count"`
	StartingCount   int `json:"starting_count"`
	UnhealthyCount  int `json:"unhealthy_count"`
	RestartingCount int `json:"restarting_count"`
	ExitedCount     int `json:"exited_count"`
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

		health, runErr := containersList(ctx, state.ComposeProject)
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
			Containers:    health,
		}
		for _, c := range health {
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

// containersList wraps the shared containers.List (called from
// both the CLI's `dpm localnet container list` and this handler)
// and re-shapes Info → ContainerHealth so the API responses keep
// their stable JSON tags. The docker-side logic lives once in
// internal/localnet/containers — see CONTRIBUTING.md "CLI ↔ Web UI
// parity" rule.
func containersList(ctx context.Context, project string) ([]ContainerHealth, error) {
	infos, err := containers.List(ctx, project)
	if err != nil {
		return nil, err
	}
	out := make([]ContainerHealth, 0, len(infos))
	for _, c := range infos {
		out = append(out, ContainerHealth{
			Name:    c.Name,
			Service: c.Service,
			State:   c.State,
			Health:  c.Health,
			Status:  c.Status,
			Image:   c.Image,
		})
	}
	return out, nil
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
		belongsErr := containers.BelongsToProject(probeCtx, state.ComposeProject, container)
		cancelProbe()
		if belongsErr != nil {
			if errors.Is(belongsErr, containers.ErrContainerNotInProject) {
				writeErrorWithCode(w, http.StatusNotFound,
					ErrCodeNotFound,
					"container "+container+" not in compose project "+state.ComposeProject)
				return
			}
			writeErrorWithCode(w, http.StatusServiceUnavailable,
				"DOCKER_PROBE_FAILED",
				"could not query docker", belongsErr.Error())
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

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		body, err := containers.Logs(ctx, container, containers.LogsOptions{
			Tail:  tail,
			Since: since,
		})
		if err != nil && body == "" {
			writeErrorWithCode(w, http.StatusServiceUnavailable,
				"DOCKER_LOGS_FAILED",
				"could not tail container logs", err.Error())
			return
		}
		// Plain-text response so the frontend can render in a
		// <pre> without a JSON decode + escape round-trip.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}
}

// handleContainerRestart: POST /api/instances/{name}/containers/{container}/restart.
//
// Runs `docker restart <container>` against the named container.
// Synchronous (docker restart blocks until container exits then
// starts again). 30s timeout — docker's default stop grace is
// 10s plus startup, so most containers are well under this.
//
// Same path-param validation as the logs endpoint: container
// name regex + verified against the instance's compose project
// via docker compose ps so arbitrary host containers can't be
// poked.
//
// 204 on success. 5xx with the docker error on failure (most
// likely cause: the container is gone between the ps probe and
// the restart call).
func handleContainerRestart() http.HandlerFunc {
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
				"invalid container name")
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

		// Verify container belongs to this instance's project.
		// Defence-in-depth: without this, a malicious client
		// could restart arbitrary containers on the host by
		// passing a foreign container name.
		probeCtx, cancelProbe := context.WithTimeout(r.Context(), 5*time.Second)
		belongsErr := containers.BelongsToProject(probeCtx, state.ComposeProject, container)
		cancelProbe()
		if belongsErr != nil {
			if errors.Is(belongsErr, containers.ErrContainerNotInProject) {
				writeErrorWithCode(w, http.StatusNotFound,
					ErrCodeNotFound,
					"container "+container+" not in compose project "+state.ComposeProject)
				return
			}
			writeErrorWithCode(w, http.StatusServiceUnavailable,
				"DOCKER_PROBE_FAILED",
				"could not query docker", belongsErr.Error())
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := containers.Restart(ctx, container); err != nil {
			log.Printf("restart container %q: %v", container, err)
			writeErrorWithCode(w, http.StatusInternalServerError,
				"CONTAINER_RESTART_FAILED",
				"failed to restart "+container+": "+firstLine(err.Error()))
			return
		}
		log.Printf("restart container %q: ok", container)
		w.WriteHeader(http.StatusNoContent)
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

// handleScrubInstance: DELETE /api/instances/{name}.
//
// Registry-level cleanup: removes the per-instance state.json + dir
// and the index entry — for orphaned `creating` entries left by a
// server restart, or for instances that finished badly and the user
// wants to retry the name. 204 on success; honest 404 for an
// unknown name.
//
// Refuses (409) to scrub a `running` instance (stop it via POST
// /down first) and refuses while a create job is in flight (that
// would race the goroutine; cancel via DELETE /up first).
//
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

		// Acquire the per-instance flock so the read +
		// status check + index/state delete is a true CAS. Without
		// this a concurrent POST /api/instances or POST /up that
		// races the goroutine-Active probe above could re-register
		// the same name between our Read and Delete. The lock
		// excludes both create and resume paths (they take the
		// same lock around their work).
		release, lerr := registry.Lock(name)
		if lerr != nil {
			writeErrorWithCode(w, http.StatusConflict,
				"INSTANCE_BUSY",
				"instance "+name+" is busy — another operation holds the lock",
				"wait for the in-flight operation to finish, then retry")
			return
		}
		defer release()

		state, err := registry.Read(name)
		if err != nil {
			if errors.Is(err, registry.ErrNotFound) {
				// Two flavours of "not found":
				//   1. The name is unknown entirely — neither
				//      a state.json nor an index entry exists.
				//      Return 404; nothing to clean.
				//   2. ORPHAN: state.json is missing but the
				//      index.json still references the name —
				//      a previous interrupted teardown or a
				//      manual filesystem wipe. The Web UI shows
				//      this in `localnet list` and the user
				//      clicks Remove with no way to repair it.
				//      Treat Delete as idempotent here: scrub
				//      the index entry so list reflects truth.
				if idx, ierr := registry.ReadIndex(); ierr == nil {
					for _, e := range idx.Entries {
						if e.Name == name {
							hub.ClearBuffer(progress.TopicFor(name))
							if derr := registry.Delete(name); derr != nil {
								writeError(w, http.StatusInternalServerError,
									"scrub orphan index entry", derr)
								return
							}
							log.Printf("scrub orphan index entry %q via DELETE (state.json was missing)", name)
							w.WriteHeader(http.StatusNoContent)
							return
						}
					}
				}
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
				"run `dpm localnet down "+name+"` from a terminal")
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
// When no buffer exists for the topic — the up finished (and
// ClearBuffer ran) OR the name was never the target of a POST —
// the stream simply opens with nothing to replay. The frontend
// distinguishes "instance finished" from "instance never existed"
// via a registry.Read follow-up.
//
// 30s heartbeat (matches the global /events handler). On the
// goroutine's done event the client closes the EventSource;
// idle connections beyond that survive on the heartbeat until
// the user navigates away.
//
// # Origin gate (parity with the global /events handler)
//
// EventSource issues a GET, so this stream is exempt from the
// router's state-changing CSRF middleware — but a tab on another
// origin can still open EventSource("http://127.0.0.1:7777/api/
// instances/x/events") and READ the bring-up progress (instance
// name, Splice version, step status, RunUp error text). The global
// /events handler (internal/ui/sse.go) hardens against exactly this;
// we mirror the guard here so the two SSE surfaces are consistent.
// Origin is absent on direct curl, so we only enforce when it is
// present and only fail on mismatch.
func handleInstanceEvents(hub *stream.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := registry.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}

		// Mirror sse.go's Origin/Host check. Host-level rebinding is
		// blocked by the router's withHostCheck; this catches the
		// cross-origin EventSource read where Host is loopback but
		// Origin is the attacker's site.
		if origin := r.Header.Get("Origin"); origin != "" {
			if err := httpsec.CheckOriginAgainstHost(
				origin, r.Header.Get("Referer"), r.Host); err != nil {
				writeErrorWithCode(w, http.StatusForbidden,
					ErrCodeForbidden, "forbidden: "+err.Error())
				return
			}
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
	body := strings.TrimRight(string(e.Data), "\n")
	for _, line := range strings.Split(body, "\n") {
		_, _ = fmt.Fprintf(w, "data: %s\n", line)
	}
	_, _ = w.Write([]byte("\n"))
}

// ── cancel an in-flight create-instance goroutine ─────────

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

// observabilityToggleTimeout caps how long the toggle handler will
// wait on docker compose. Prometheus + Grafana cold-start in <10s
// typically; 90s leaves room for the first image pull (~250 MB)
// on a fresh machine.
const observabilityToggleTimeout = 90 * time.Second

// observabilityToggleRequest is the body for
// POST /api/instances/{name}/observability.
//
// Per-component flags (`prometheus` / `grafana`) are the canonical
// form so users can flip the two sidecars independently. The legacy
// `enabled` flag is retained as a compatibility synonym: when
// present (non-nil), it sets BOTH components to the same value so
// clients that haven't been updated keep working. Per-component
// flags win over `enabled` when both are sent in the same body.
type observabilityToggleRequest struct {
	Prometheus *bool `json:"prometheus,omitempty"`
	Grafana    *bool `json:"grafana,omitempty"`
	Enabled    *bool `json:"enabled,omitempty"`
}

// resolveTargets folds the three optional fields into the
// (prometheus, grafana) target booleans. Returns the resolved
// targets plus a flag indicating whether any field was set. The
// per-component flags take precedence over `enabled`.
func (r observabilityToggleRequest) resolveTargets() (prom, graf bool, ok bool) {
	if r.Enabled != nil {
		prom = *r.Enabled
		graf = *r.Enabled
		ok = true
	}
	if r.Prometheus != nil {
		prom = *r.Prometheus
		ok = true
	}
	if r.Grafana != nil {
		graf = *r.Grafana
		ok = true
	}
	return
}

// handleObservabilityToggle: POST /api/instances/{name}/observability.
//
// Toggles the Prometheus + Grafana sidecars on a RUNNING instance
// without disturbing canton/splice. The endpoint exists so users
// who created an instance without `--profile observability` (or
// without the checkbox in the Create modal) can flip metrics on
// after the fact instead of having to down + up.
//
// CLI parity: mirrored by `dpm localnet observability enable|disable|
// status` (internal/cli/localnet/observability.go) — both surfaces call
// the SAME neutral localnet.SetObservability, so there is no per-surface
// docker-compose drift. The sidecars are still PER-INSTANCE rather than
// a single host-level stack (see docs/limitations.md "Shared
// observability stack").
//
// Body: {"enabled": true|false}
//
//	enabled=true  → MaterializeObservabilityOverlay into dataDir,
//	                append the overlay to state.ComposeFiles if
//	                absent, then bring up each requested sidecar with
//	                its own `docker compose ... up -d --no-deps <svc>`
//	                under the profile set the service needs (grafana
//	                also activates the prometheus profile so its
//	                `depends_on: prometheus` resolves). Discover the
//	                new host port, persist into state.Ports[...].
//	enabled=false → `docker compose ... stop prometheus grafana`
//	                then `... rm -f prometheus grafana`. Clear the
//	                port from state.json. Canton + splice are
//	                untouched.
//
// Failure modes:
//
//	404 INSTANCE_NOT_FOUND       — name unknown
//	409 INSTANCE_NOT_RUNNING     — toggle requires a live stack
//	409 INSTANCE_BUSY            — another op holds the lock
//	502 OBSERVABILITY_TOGGLE_FAIL — docker compose returned non-zero
func handleObservabilityToggle() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := localnet.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}
		var req observabilityToggleRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid request body",
				`expected {"prometheus": true|false, "grafana": true|false} `+
					`(legacy {"enabled": true|false} also accepted)`)
			return
		}
		wantProm, wantGraf, ok := req.resolveTargets()
		if !ok {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"request body must set at least one of "+
					`"prometheus", "grafana", or "enabled"`)
			return
		}

		// Per-instance lock so we can't race a concurrent down/up
		// or another toggle. The lock is the same one
		// `localnet up` / snapshot / restore hold — uniform CAS.
		release, lerr := registry.Lock(name)
		if lerr != nil {
			writeErrorWithCode(w, http.StatusConflict,
				"INSTANCE_BUSY",
				"instance "+name+" is busy — another op holds the lock",
				"retry once the in-flight operation finishes")
			return
		}
		defer release()

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
		if state.Status != registry.StatusRunning {
			writeErrorWithCode(w, http.StatusConflict,
				"INSTANCE_NOT_RUNNING",
				"instance "+name+" is not running (status="+string(state.Status)+")",
				"bring it up first via POST /api/instances/"+name+"/up")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), observabilityToggleTimeout)
		defer cancel()

		// Delegate the docker-compose orchestration + port persistence
		// to the NEUTRAL localnet.SetObservability so the CLI verb
		// (`dpm localnet observability`) and this handler share one
		// code path (CONTRIBUTING.md "share the business logic"). We
		// hold the per-instance lock for the whole handler, satisfying
		// SetObservability's caller contract.
		var logBuf bytes.Buffer
		res, err := localnet.SetObservability(ctx, state, wantProm, wantGraf, &logBuf)
		if logBuf.Len() > 0 {
			log.Printf("observability toggle %q: %s", name, strings.TrimSpace(logBuf.String()))
		}
		if err != nil {
			log.Printf("observability toggle %q failed: %v", name, err)
			// Point the docker-logs hint at the container that actually
			// failed (prometheus vs grafana) instead of always naming
			// prometheus — SetObservability wraps the failure with a
			// component-specific prefix we can key off.
			svc := failedObservabilityService(err)
			writeErrorWithCode(w, http.StatusBadGateway,
				"OBSERVABILITY_TOGGLE_FAIL",
				"failed to apply observability toggle: "+truncateForUser(err.Error()),
				"check `docker compose -p "+state.ComposeProject+" ps` and `docker logs "+state.ComposeProject+"-"+svc+"`")
			return
		}

		resp := map[string]any{
			"schema_version": types.SchemaVersion,
			"instance":       name,
			"prometheus":     res.Prometheus,
			"grafana":        res.Grafana,
			// `enabled` retained for legacy clients: true iff BOTH are on.
			"enabled": res.Prometheus && res.Grafana,
		}
		if res.PrometheusPort != 0 {
			resp["prometheus_ui"] = res.PrometheusPort
		}
		if res.GrafanaPort != 0 {
			resp["grafana_ui"] = res.GrafanaPort
		}
		if res.Warning != "" {
			resp["warning"] = res.Warning
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// failedObservabilityService maps a SetObservability error to the
// sidecar the remediation hint should name. SetObservability wraps every
// component failure with a leading "enable|disable <component>: ..."
// prefix, so we match that PREFIX rather than searching the whole error:
// the wrapped docker output (the "...\noutput:\n<stderr>" tail) can
// mention the other component (e.g. a compose error listing both
// services), which a substring match would mis-attribute. A grafana
// prefix yields "grafana"; the prometheus path and any non-component
// error fall back to "prometheus".
func failedObservabilityService(err error) string {
	if err != nil {
		msg := err.Error()
		if strings.HasPrefix(msg, "enable grafana") || strings.HasPrefix(msg, "disable grafana") {
			return "grafana"
		}
	}
	return "prometheus"
}

// truncateForUser keeps error messages from leaking the full docker
// compose stderr (sometimes 10+ KB) into the user-facing response.
// 400 chars is enough for the typical "no such service" / "address
// already in use" lines without dumping the entire trace.
func truncateForUser(s string) string {
	const max = 400
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
