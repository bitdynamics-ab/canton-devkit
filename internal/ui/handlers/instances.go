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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
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
// goroutine. RunUp does Splice fetch + docker compose up + health
// probe, which can take ~2 minutes on a fresh box. 10 minutes
// gives headroom for slow networks without hanging an orphaned
// goroutine indefinitely when the browser closes the SSE.
//
// This is NOT the HTTP request timeout — the POST returns 202
// immediately; the goroutine runs on its own context, independent
// of the request. Cancellation (BIT-163e) passes a CancelFunc
// into the goroutine's context that DELETE invokes.
const upJobTimeout = 10 * time.Minute

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
// per-instance handler. Mirrors the writeSSEFrame in
// internal/ui/sse.go (kept private there); duplicated here so the
// handlers package doesn't import from internal/ui.
//
// Per spec, multi-line data needs one "data:" prefix per line; we
// strip a trailing newline so the payload doesn't gain a trailing
// empty data: line.
func writeInstanceEventFrame(w http.ResponseWriter, e stream.Event) {
	if e.ID != "" {
		_, _ = fmt.Fprintf(w, "id: %s\n", e.ID)
	}
	if e.Topic != "" {
		_, _ = fmt.Fprintf(w, "event: %s\n", e.Topic)
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
