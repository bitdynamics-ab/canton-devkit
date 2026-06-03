package ui

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/ui/handlers"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/stream"
)

// NewRouter wires the M2 Web UI HTTP surface:
//
//	/healthz       — liveness probe (200 OK, no body); cheap, no docker calls
//	/api/version   — server identity + schema versions for handshake
//	/api/*         — REST handlers (added by BIT-131 in a follow-on PR)
//	/events        — SSE stream (added by BIT-130)
//	/              — embedded Vite bundle with SPA fallback (assets.go)
//
// `hub` may be nil for callers that don't want SSE (test wiring,
// future "read-only" mode); in that case /events is mounted as a
// 503-returning stub. Production callers pass a real hub from
// internal/ui/stream.
//
// We use net/http.ServeMux (stdlib) rather than chi/gorilla. Go 1.22's mux
// added method+path patterns ("GET /api/version"), which covers everything
// the current surface needs. Pulling chi for one router would add a
// transitive dep for no functional gain; if middleware composition gets
// hairy (rate limiting, auth, request logging) the swap is mechanical.
func NewRouter(assets http.Handler, hub *stream.Hub) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.HandleFunc("GET /api/version", handleVersion)
	// REST handlers under /api/* (BIT-131). Each handler package
	// owns one resource; new resources mount themselves here as
	// they land.
	handlers.MountInstances(mux, hub)
	handlers.MountAuth(mux)
	handlers.MountSpliceVersions(mux)
	handlers.MountPreflight(mux)
	handlers.MountMetrics(mux)
	handlers.MountSnapshots(mux)    // BIT-184 — UI parity for snapshot/restore
	handlers.MountDAR(mux)          // BIT-187 — DAR Manager (list uploaded DARs)
	handlers.MountContracts(mux)    // BIT-186 — Explorer (ACS snapshot)
	handlers.MountTransactions(mux) // BIT-186 — Explorer (Transactions/Timeline)
	handlers.MountSkills(mux)       // BIT-189 — Agent Skills (browse + install)
	// /events (SSE) — added by BIT-130. Handler does its own
	// Origin check (sse.go) since EventSource sends GET and the
	// global CSRF middleware is GET-exempt.
	if hub != nil {
		mux.Handle("GET /events", sseHandler(hub))
	} else {
		mux.HandleFunc("GET /events", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "SSE disabled (no hub configured)",
				http.StatusServiceUnavailable)
		})
	}
	// PR #41 #c: explicit 404 for /api/<typo> and /events/<typo>
	// so a misspelled handler path doesn't fall through to the
	// SPA index. Frontend fetch() consumers get a structured
	// error instead of "the API returned HTML, weird".
	mux.HandleFunc("/api/", apiNotFound)
	mux.HandleFunc("/events/", apiNotFound)
	// Catch-all for everything else. Method intentionally
	// unconstrained so an unknown POST hits the asset handler
	// rather than colliding with the /api/ method pattern under
	// Go 1.22's mux conflict rules.
	mux.Handle("/", assets)
	// Middleware chain (outer → inner): access log first so it
	// wraps everything including 403s from CSRF and 404s from
	// /api/typo. Common headers + Origin gate run per request
	// inside the logged span.
	return withAccessLog(withCommonHeaders(withOriginCheck(mux)))
}

// statusRecorder wraps http.ResponseWriter to capture the final
// status code for the access log. The stdlib type has no status-
// read API; the standard pattern is to override WriteHeader.
//
// Flush pass-through: SSE handlers require http.Flusher. Without
// the explicit forward, the type assertion in sse.go fails and
// every /events request 500s. The stdlib provides the Flusher
// on the underlying writer; we just need to expose it.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush forwards to the underlying writer's Flusher if present.
// SSE depends on this — without the forward, sseHandler's
// `w.(http.Flusher)` assertion fails through this wrapper.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// withAccessLog emits one line per request via log.Default():
//
//	access: 200 GET /api/version  127.0.0.1  3.2ms
//
// Reviewer pin (PR #41 #3). Format is stable and parseable.
//
// We deliberately do NOT log query strings — they can carry
// credentials (?include_jwt=true today, future auth tokens). The
// path-only line is enough for traffic-shape diagnostics.
//
// Routed through log.Default so audit/access/app logs share one
// stream — operators redirect the logger once and get everything.
// Future structured-logging migration is one-line change here.
func withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Printf("access: %d %s %s  %s  %s",
			rec.status, r.Method, r.URL.Path,
			clientIP(r),
			time.Since(start).Round(time.Microsecond),
		)
	})
}

// clientIP extracts the loopback IP without the ephemeral port.
// Loopback-only server so we don't need X-Forwarded-For chains.
func clientIP(r *http.Request) string {
	if r.RemoteAddr == "" {
		return "?"
	}
	for i := len(r.RemoteAddr) - 1; i >= 0; i-- {
		if r.RemoteAddr[i] == ':' {
			return r.RemoteAddr[:i]
		}
	}
	return r.RemoteAddr
}

// apiNotFound is the explicit 404 for unknown /api/* and /events/*
// paths. Returns JSON so a frontend fetch() consumer sees a
// structured error instead of an HTML SPA-index 200.
func apiNotFound(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"error":"not found"}` + "\n"))
}

// handleHealthz is the cheapest possible liveness probe — no docker
// query, no registry read, no allocation beyond the response. Used by
// orchestrators (and the eventual `dpm localnet ui` startup wait) to
// confirm the server is listening before opening the browser.
func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// versionPayload is the handshake the Web UI uses on first load to
// confirm it's talking to a compatible backend. SchemaVersion mirrors
// internal/api/types.SchemaVersion so a frontend bundled for v1 can
// refuse to talk to a v2 backend (or vice versa) with a clear error
// instead of silently mis-decoding responses.
//
// `Built` is intentionally not embedded yet — we don't have a stable
// build-info plumbing on M1 foundation. Added in M2 finalisation when
// the release pipeline lands (BIT-141 umbrella follow-up).
type versionPayload struct {
	Name          string `json:"name"`
	SchemaVersion int    `json:"schema_version"`
}

// handleVersion is the schema-handshake endpoint. Returns the server's
// API SchemaVersion as a plain JSON document; the frontend reads this
// on bootstrap and refuses to render if it doesn't match its compiled
// expectation.
func handleVersion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	// Imported lazily via blank var to avoid an import cycle here —
	// the types package's reverse import is on the api side, but
	// the handler in ui shouldn't pull a structural dep on types
	// for one int. Inline the constant; keep them in sync via the
	// versionParity test in router_test.go.
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(versionPayload{
		Name:          "canton-devkit",
		SchemaVersion: schemaVersion,
	})
}

// schemaVersion mirrors types.SchemaVersion. A test in router_test.go
// asserts the two stay in sync — see TestVersion_MatchesTypesSchema.
// Inlined rather than imported to keep this package free of an upward
// import on api/types (the dependency direction stays handlers→types,
// not router→types).
const schemaVersion = 1

// withCommonHeaders is the only middleware the skeleton ships with.
// Two header policies that apply to EVERY response:
//
//   - X-Content-Type-Options: nosniff — prevents browsers from
//     interpreting a JSON response as HTML or script if a buggy
//     handler ever forgets the Content-Type header.
//   - Server: dpm — short identification banner. Better than the
//     empty default; intentionally short to avoid fingerprinting.
//
// Auth headers, CORS, request logging — all added later in their own
// commits with their own tests. Keeping the middleware stack minimal
// here means BIT-130/131 review can focus on their own surfaces.
func withCommonHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Server", "dpm")
		next.ServeHTTP(w, r)
	})
}
