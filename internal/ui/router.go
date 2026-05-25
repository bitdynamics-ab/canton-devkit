package ui

import (
	"encoding/json"
	"net/http"
)

// NewRouter wires the M2 Web UI HTTP surface:
//
//	/healthz       — liveness probe (200 OK, no body); cheap, no docker calls
//	/api/version   — server identity + schema versions for handshake
//	/api/*         — REST handlers (added by BIT-131 in a follow-on PR)
//	/events        — SSE stream (added by BIT-130 in a follow-on PR)
//	/              — embedded Vite bundle with SPA fallback (assets.go)
//
// The skeleton landing in BIT-129 deliberately ships /healthz, /api/version,
// and the asset handler only. The empty space for /api/* and /events is
// where the follow-on PRs slot in — keeping each PR a reviewable size.
//
// We use net/http.ServeMux (stdlib) rather than chi/gorilla. Go 1.22's mux
// added method+path patterns ("GET /api/version"), which covers everything
// the current surface needs. Pulling chi for one router would add a
// transitive dep for no functional gain; if middleware composition gets
// hairy (rate limiting, auth, request logging) the swap is mechanical.
func NewRouter(assets http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.HandleFunc("GET /api/version", handleVersion)
	// Reviewer pin (PR #41 #c): the SPA fallback used to swallow
	// /api/<typo> requests and return the React index with 200,
	// hiding routing bugs (a misspelled handler path on the
	// frontend looked like "the API returned HTML, weird"). We
	// now have an explicit 404 handler for /api/* AND /events/*
	// that fires when no specific pattern matches. Anything else
	// (genuine SPA route) falls through to the asset handler.
	mux.HandleFunc("/api/", apiNotFound)
	mux.HandleFunc("/events/", apiNotFound) // sub-paths only; /events is added by BIT-130
	// Catch-all for everything else. Method intentionally
	// unconstrained so an unknown POST hits the asset handler
	// (which writes a 405 internally) rather than colliding with
	// the /api/ method pattern under Go 1.22's mux conflict rules.
	mux.Handle("/", assets)
	// withOriginCheck protects credential-issuing routes from
	// cross-origin POST. Skipped for safe methods; see
	// internal/ui/csrf.go for the rationale.
	return withCommonHeaders(withOriginCheck(mux))
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
