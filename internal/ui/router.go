package ui

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/telemetry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/handlers"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/stream"
)

// NewRouter wires the Web UI HTTP surface:
//
//	/healthz       — liveness probe (200 OK, no body); cheap, no docker calls
//	/api/version   — server identity + schema versions for handshake
//	/api/*         — REST handlers
//	/events        — SSE stream
//	/              — embedded Vite bundle with SPA fallback (assets.go)
//
// `hub` may be nil for callers that don't want SSE; in that case
// /events is mounted as a 503-returning stub. Production callers pass a
// real hub from internal/ui/stream.
//
// The stdlib ServeMux (Go 1.22 method+path patterns) covers everything
// the current surface needs; a third-party router would add a
// transitive dep for no functional gain.
//
// Variadic opts keep the common two-arg call site unchanged while
// letting `dpm localnet ui --allow-non-loopback` widen the Host
// allowlist — see RouterOption / WithAllowedHosts.
func NewRouter(assets http.Handler, hub *stream.Hub, opts ...RouterOption) http.Handler {
	var cfg routerConfig
	for _, o := range opts {
		o(&cfg)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.HandleFunc("GET /api/version", handleVersion)
	// REST handlers under /api/*; each handler package owns one resource.
	handlers.MountInstances(mux, hub)
	handlers.MountAuth(mux)
	handlers.MountSpliceVersions(mux)
	handlers.MountPreflight(mux)
	handlers.MountDoctor(mux)
	handlers.MountMetrics(mux)
	handlers.MountSnapshots(mux)
	handlers.MountDAR(mux, hub)
	handlers.MountContracts(mux)
	handlers.MountTransactions(mux)
	handlers.MountSkills(mux)
	handlers.MountTokens(mux, hub)
	// /events (SSE). Handler does its own Origin check (sse.go)
	// since EventSource sends GET and the global CSRF middleware
	// is GET-exempt.
	if hub != nil {
		mux.Handle("GET /events", sseHandler(hub))
	} else {
		mux.HandleFunc("GET /events", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "SSE disabled (no hub configured)",
				http.StatusServiceUnavailable)
		})
	}
	// Explicit 404 for /api/<typo> and /events/<typo> so a misspelled
	// path doesn't fall through to the SPA index — frontend fetch()
	// consumers get a structured error, not HTML.
	mux.HandleFunc("/api/", apiNotFound)
	mux.HandleFunc("/events/", apiNotFound)
	// Catch-all for everything else. Method intentionally
	// unconstrained so an unknown POST hits the asset handler
	// rather than colliding with the /api/ method pattern under
	// Go 1.22's mux conflict rules.
	mux.Handle("/", assets)
	// Middleware chain (outer → inner): access log first so it wraps
	// everything, including 403s from CSRF/Host and 404s. The Host
	// allowlist runs before the Origin gate because it applies to
	// EVERY method (GET reads leak party IDs too) and is the
	// DNS-rebinding defence the Origin check can't provide. When
	// skipOriginCheck is set, rewrite Origin to match Host before
	// any gate so CSRF + SSE + per-handler Origin checks all pass;
	// Host allowlisting still applies. Feature telemetry sits
	// innermost so it only records requests that reached a real
	// handler.
	return withAccessLog(
		withHostCheck(cfg.allowedHosts,
			withSkipOriginCheck(cfg.skipOriginCheck,
				withCommonHeaders(
					withOriginCheck(
						withFeatureTelemetry(mux))))))
}

// routerConfig accumulates NewRouter options.
type routerConfig struct {
	// allowedHosts are extra Host-header host-parts the Host allowlist
	// accepts beyond the built-in loopback names; empty in the default
	// loopback-only posture.
	allowedHosts []string
	// skipOriginCheck disables the CSRF / SSE Origin==Host gate.
	// Used only on the `dpm localnet ui --insecure-skip-origin-check`
	// path for Vite (or other) frontend-dev setups that cannot keep
	// Origin aligned with Host.
	skipOriginCheck bool
}

// RouterOption configures NewRouter without breaking its two-arg
// call site. See WithAllowedHosts.
type RouterOption func(*routerConfig)

// WithAllowedHosts widens the Host-header allowlist (withHostCheck) to
// include the given hosts in addition to the built-in loopback names.
// Used only on the `dpm localnet ui --allow-non-loopback` path: when an
// operator deliberately binds the UI to a LAN IP, requests whose Host
// names that IP would otherwise be rejected as a rebinding attempt.
// Empty entries are ignored.
func WithAllowedHosts(hosts ...string) RouterOption {
	return func(c *routerConfig) {
		for _, h := range hosts {
			if h != "" {
				c.allowedHosts = append(c.allowedHosts, h)
			}
		}
	}
}

// WithSkipOriginCheck disables the Origin==Host CSRF / SSE gate for
// every request. The Host allowlist (DNS-rebinding defence) still
// runs. Used only behind `dpm localnet ui --insecure-skip-origin-check`.
func WithSkipOriginCheck(skip bool) RouterOption {
	return func(c *routerConfig) {
		c.skipOriginCheck = skip
	}
}

// withFeatureTelemetry records which Web UI screens a `localnet ui`
// session touched (adoption signal). Recorded once per feature per
// process via telemetry.IncOnce, so the Explorer/Metrics polling loops
// don't inflate the count — the signal is "this screen was used this
// session," not request volume. Maps the request path prefix to a
// coarse, allow-listed feature name; unknown paths record nothing.
func withFeatureTelemetry(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f := featureForPath(r.URL.Path); f != "" {
			telemetry.IncOnce("dpm/ui_feature", f)
		}
		next.ServeHTTP(w, r)
	})
}

// featureForPath maps an /api/* request path to the Web UI feature it
// belongs to. Returns "" for infra endpoints (version, auth, healthz,
// preflight, SSE) and the asset bundle, which aren't adoption signals.
//
// Most feature endpoints live under /api/instances/{name}/<sub>/...
// (handlers use Go 1.22 ServeMux path-value patterns), so the per-
// instance sub-resource is checked BEFORE the /api/instances
// catch-all — otherwise DAR/Explorer/etc. calls would be silently
// counted as "instances" and underreport their feature adoption.
func featureForPath(path string) string {
	if sub := instanceSubResource(path); sub != "" {
		switch sub {
		case "dar":
			return "dar"
		case "contracts", "transactions":
			return "explorer"
		case "metrics":
			return "metrics"
		case "tokens":
			return "tokens"
		case "skills":
			return "skills"
		case "snapshots", "backup":
			return "backup"
		}
		// Anything else under an instance (status, env, logs,
		// containers, down, up, recreate, …) is instance lifecycle.
		return "instances"
	}
	switch {
	case strings.HasPrefix(path, "/api/dars"), strings.HasPrefix(path, "/api/dar/"):
		return "dar"
	case strings.HasPrefix(path, "/api/contracts"), strings.HasPrefix(path, "/api/transactions"):
		return "explorer"
	case strings.HasPrefix(path, "/api/metrics"):
		return "metrics"
	case strings.HasPrefix(path, "/api/tokens"):
		return "tokens"
	case strings.HasPrefix(path, "/api/skills"):
		return "skills"
	case strings.HasPrefix(path, "/api/doctor"):
		return "doctor"
	case strings.HasPrefix(path, "/api/snapshots"):
		return "backup"
	case strings.HasPrefix(path, "/api/instances"):
		return "instances"
	}
	return ""
}

// instanceSubResource returns the sub-resource segment for paths of
// the form /api/instances/{name}/<sub-resource>... For
// /api/instances/demo/dar/abc/inspect it returns "dar"; for
// /api/instances/demo (no sub-resource) it returns ""; for
// anything not under /api/instances/ it returns "".
func instanceSubResource(path string) string {
	const prefix = "/api/instances/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := path[len(prefix):]
	// Skip the {name} segment.
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return ""
	}
	sub := rest[slash+1:]
	if slash2 := strings.IndexByte(sub, '/'); slash2 >= 0 {
		return sub[:slash2]
	}
	return sub
}

// statusRecorder wraps http.ResponseWriter to capture the final status
// code for the access log (the stdlib type has no status-read API).
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
// `w.(http.Flusher)` assertion fails through this wrapper and
// every /events request 500s.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// withAccessLog emits one stable, parseable line per request via
// log.Default() (so access and app logs share one stream):
//
//	access: 200 GET /api/version  127.0.0.1  3.2ms
//
// Query strings are deliberately NOT logged because they may carry
// credentials in future endpoints.
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

// apiNotFound is the explicit JSON 404 for unknown /api/* and /events/*
// paths, so a frontend fetch() consumer sees a structured error instead
// of an HTML SPA-index 200.
func apiNotFound(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"error":"not found"}` + "\n"))
}

// handleHealthz is the cheapest possible liveness probe — no docker
// query, no registry read. Used to confirm the server is listening
// before opening the browser.
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
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(versionPayload{
		Name:          "canton-devkit",
		SchemaVersion: schemaVersion,
	})
}

// schemaVersion mirrors types.SchemaVersion, inlined rather than
// imported to keep this package free of an upward import on api/types.
// TestVersion_MatchesTypesSchema asserts the two stay in sync.
const schemaVersion = 1

// withCommonHeaders sets two header policies that apply to EVERY
// response:
//
//   - X-Content-Type-Options: nosniff — prevents browsers from
//     interpreting a JSON response as HTML or script if a buggy
//     handler ever forgets the Content-Type header.
//   - Server: dpm — short identification banner; intentionally
//     short to avoid fingerprinting.
func withCommonHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Server", "dpm")
		next.ServeHTTP(w, r)
	})
}
