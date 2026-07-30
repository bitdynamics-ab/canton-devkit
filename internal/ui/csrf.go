package ui

import (
	"net/http"

	"github.com/bitdynamics-ab/canton-devkit/internal/ui/httpsec"
)

// withOriginCheck is the CSRF-protection middleware: state-changing
// methods (POST/PUT/PATCH/DELETE) MUST carry an Origin (or Referer)
// header whose host matches the server's own Host header; mismatches
// return 403.
//
// A loopback bind is NOT a CSRF defence: any unrelated website open in
// a local browser can fetch() http://127.0.0.1:7777/... and reach this
// server. The check lives in router-level middleware, not per-handler,
// because per-handler checks drift and one forgotten handler is one
// credential leak.
//
// GET/HEAD/OPTIONS are intentionally exempt — they don't trigger CSRF
// in the browser security model, and gating them would 403 innocuous
// reads. That also exempts SSE (EventSource only sends GET); sse.go
// does its own Origin check.
//
// Origin==Host alone does NOT stop DNS rebinding (Origin and Host are
// then BOTH the attacker's name). That hole is closed by withHostCheck,
// which runs on ALL methods including GET.
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

// withSkipOriginCheck, when enabled, rewrites the request Origin to
// match r.Host so every Origin==Host gate (CSRF middleware, SSE
// handlers, per-route checks) passes. No-op when disabled. The Host
// allowlist still runs outside this middleware.
//
// Opt-in only via `dpm localnet ui --insecure-skip-origin-check` for
// frontend-dev setups that cannot keep Origin aligned with Host.
func withSkipOriginCheck(enabled bool, next http.Handler) http.Handler {
	if !enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "" {
			r.Header.Set("Origin", "http://"+r.Host)
		}
		next.ServeHTTP(w, r)
	})
}

// withHostCheck rejects any request whose Host header host-part is not
// a loopback name ({127.0.0.0/8, ::1, localhost}, plus any extra hosts
// the operator explicitly allowed via --allow-non-loopback). Runs on
// EVERY method, including GET — unlike withOriginCheck.
//
// It exists to defeat DNS rebinding, which neither the Origin check nor
// the loopback bind stops: the attacker rebinds evil.example.com's A
// record to 127.0.0.1, so the victim's browser sends BOTH Origin and
// Host as evil.example.com:7777 — Origin == Host passes, and the
// browser itself makes the loopback connection. The blast radius is
// real: token mint/transfer/burn move value, instance endpoints tear
// down LocalNets, and GET app-config leaks party IDs. A Host allowlist
// is the standard mitigation (webpack-dev-server, Grafana, etc.).
//
// A missing Host header fails closed too — real browsers always send
// one; its absence is anomalous.
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
// httpsec.CheckOriginAgainstHost so this middleware and the
// package-handlers SSE endpoint share one implementation.
func checkOriginAgainstHost(r *http.Request) error {
	return httpsec.CheckOriginAgainstHost(
		r.Header.Get("Origin"), r.Header.Get("Referer"), r.Host)
}

// hostsMatch aliases the shared helper so TestCSRF_HostsMatch keeps
// exercising the comparison rules from this package.
func hostsMatch(a, b string) bool { return httpsec.HostsMatch(a, b) }
