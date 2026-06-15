package ui

import (
	"net/http"

	"github.com/bitdynamics-ab/canton-devkit/internal/ui/httpsec"
)

// withOriginCheck is the CSRF-protection middleware for credential-
// issuing routes. For state-changing methods (POST/PUT/PATCH/DELETE),
// the request MUST carry an Origin (or Referer) header whose host
// matches the server's own Host header. Mismatches return 403.
//
// # Why we need this on a loopback server
//
// "loopback-only" is NOT a CSRF defence.
// A browser running on the same machine — visiting any unrelated
// website — can issue a POST to http://127.0.0.1:7777/api/instances/
// demo/jwt and the request reaches the loopback server unless we
// validate the Origin. Without this middleware, "open a JWT for
// alice and exfiltrate it" is one fetch() from a malicious tab.
//
// # Why this is in middleware, not per-handler
//
// Every state-changing handler in /api/* needs this. Per-handler
// checks drift; one forgotten handler is one credential leak.
// Single chokepoint at the router level is the right shape.
//
// # GET is intentionally exempt
//
// GET / HEAD / OPTIONS don't trigger CSRF in the browser security
// model (they're "simple" requests). Skipping them keeps innocuous
// reads cheap and removes a class of false-positive 403s from
// inline-image fetches.
//
// # The DNS-rebinding gap is covered separately
//
// Origin==Host alone does NOT stop DNS rebinding (attacker rebinds
// evil.example.com → 127.0.0.1, so Origin and Host are BOTH
// evil.example.com and this check passes). That hole is closed by
// withHostCheck, which runs on ALL methods including GET. See that
// middleware's godoc.
//
// # SSE caveat
//
// EventSource (the browser SSE client) only sends GET, so SSE is
// covered by the GET-exempt rule. If a future handler issues
// credentials via GET (don't), this middleware won't gate it —
// that would be an anti-pattern.
func withOriginCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isStateChanging(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		if err := checkOriginAgainstHost(r); err != nil {
			http.Error(w, "forbidden: "+err.Error(), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withHostCheck rejects any request whose Host header host-part is not
// a loopback name ({127.0.0.0/8, ::1, localhost}, plus any extra hosts
// the operator explicitly allowed via --allow-non-loopback). Runs on
// EVERY method, including GET — unlike withOriginCheck.
//
// # Why a Host allowlist is required
//
// The CSRF middleware checks Origin == Host, and the listener binds to
// loopback. Neither stops DNS rebinding:
//
//	attacker controls evil.example.com → initially their server,
//	then rebinds the A record to 127.0.0.1. The victim's browser
//	loads evil.example.com:7777 and its fetch()es now hit OUR
//	loopback server with Origin: http://evil.example.com:7777 and
//	Host: evil.example.com:7777. Origin == Host, so withOriginCheck
//	passes; the loopback bind doesn't help because the browser (on
//	the victim's box) makes the loopback connection itself.
//
// The blast radius is real: token mint/transfer/burn move value, the
// instance create/delete endpoints stand up and tear down LocalNets,
// and GET /api/instances/{name}/app-config leaks party IDs +
// endpoints. The standard mitigation (webpack-dev-server allowedHosts,
// Grafana, etc.) is a Host allowlist; this is ours.
//
// We deliberately fail closed on a missing Host header too — a real
// browser always sends one; its absence is anomalous.
func withHostCheck(allowedExtra []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !httpsec.IsLoopbackHost(r.Host, allowedExtra...) {
			http.Error(w, "forbidden: "+httpsec.ErrHostNotAllowed.Error(),
				http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isStateChanging is the method set CSRF protection applies to.
// Mirrors the standard "safe methods" list from RFC 7231 §4.2.1.
func isStateChanging(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

// checkOriginAgainstHost validates that the request's Origin (or
// Referer, fallback) header host matches r.Host. Thin wrapper over
// httpsec.CheckOriginAgainstHost so the package-ui middleware and the
// package-handlers SSE endpoint share one implementation.
//
// Returns nil on match, an explanatory error otherwise.
func checkOriginAgainstHost(r *http.Request) error {
	return httpsec.CheckOriginAgainstHost(
		r.Header.Get("Origin"), r.Header.Get("Referer"), r.Host)
}

// hostsMatch is retained as a package-local alias of the shared helper
// so the existing TestCSRF_HostsMatch unit test (csrf_test.go) keeps
// exercising the comparison rules from this package.
func hostsMatch(a, b string) bool { return httpsec.HostsMatch(a, b) }
