package ui

import (
	"net/http"
	"strings"
)

// withOriginCheck is the CSRF-protection middleware for credential-
// issuing routes. For state-changing methods (POST/PUT/PATCH/DELETE),
// the request MUST carry an Origin (or Referer) header whose host
// matches the server's own Host header. Mismatches return 403.
//
// # Why we need this on a loopback server
//
// Reviewer pin (PR #41 #b): "loopback-only" is NOT a CSRF defence.
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
// # SSE caveat
//
// EventSource (the browser SSE client) only sends GET, so SSE is
// covered by the GET-exempt rule. If a future handler issues
// credentials via GET (don't), this middleware won't gate it —
// that's an anti-pattern flagged in the handler's review.
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
// Referer, fallback) header host matches r.Host. Treats missing
// Origin+Referer as a failure (browsers always send one on
// cross-origin fetch; curl users can opt-in by setting Origin to
// the server's URL).
//
// Returns nil on match, an explanatory error otherwise. The error
// text is returned to the client as the 403 body — useful for
// debugging during dev, deliberately vague about the comparison
// (no "expected X got Y" so a fuzzer can't binary-search).
func checkOriginAgainstHost(r *http.Request) error {
	host := r.Host
	if host == "" {
		return errCSRFMissingHost
	}
	// Origin header wins if present (per RFC 6454). Falls back to
	// Referer (still useful but easier to spoof in older browsers).
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		return errCSRFMissingOrigin
	}
	// Extract host from the Origin URL. We don't pull net/url for
	// this — we already strip the scheme and any path so a string-
	// match against r.Host is correct and faster.
	originHost := stripOriginToHost(origin)
	if originHost == "" {
		return errCSRFMalformedOrigin
	}
	if !hostsMatch(originHost, host) {
		return errCSRFOriginMismatch
	}
	return nil
}

// stripOriginToHost extracts the host portion of an Origin URL
// ("http://127.0.0.1:7777/foo" → "127.0.0.1:7777"). Returns ""
// for inputs that don't look like a URL with a scheme separator.
func stripOriginToHost(origin string) string {
	const sep = "://"
	i := strings.Index(origin, sep)
	if i < 0 {
		return ""
	}
	rest := origin[i+len(sep):]
	if j := strings.IndexAny(rest, "/?"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// hostsMatch compares two host strings, tolerating an implicit
// port (Origin without ":port" vs r.Host with ":port"). Strict
// otherwise — we don't normalise case (hosts are case-insensitive
// but loopback IPs are ASCII numerics, so case is a non-issue).
func hostsMatch(a, b string) bool {
	if a == b {
		return true
	}
	// Allow "127.0.0.1" to match "127.0.0.1:7777" only if the
	// HOST side carries the port. Origin without port → port 80
	// (HTTP default), which we'd never bind for the UI; this
	// fallback is for the rare curl case.
	if strings.HasPrefix(b, a+":") {
		return true
	}
	if strings.HasPrefix(a, b+":") {
		return true
	}
	return false
}

// Error sentinels. Distinct types so a debugging test can branch
// on the specific failure mode without scraping strings.
type csrfErr string

func (e csrfErr) Error() string { return string(e) }

const (
	errCSRFMissingHost     = csrfErr("request has no Host header")
	errCSRFMissingOrigin   = csrfErr("missing Origin and Referer headers")
	errCSRFMalformedOrigin = csrfErr("Origin/Referer header malformed")
	errCSRFOriginMismatch  = csrfErr("Origin/Referer host does not match server")
)
