// Package httpsec holds the browser-facing security checks shared by
// the Web UI router (package ui) and the per-instance handlers
// (package ui/handlers).
//
// It exists to satisfy the CLI↔Web UI parity rule the other way round:
// the two HTTP surfaces (package ui's middleware + /events, and package
// handlers' /api/instances/{name}/events) must apply the SAME CSRF and
// DNS-rebinding guards. Package handlers cannot import package ui (that
// would be an import cycle — ui imports handlers in NewRouter), so the
// checks live in this neutral leaf package both can depend on.
//
// Two distinct browser threats are covered here:
//
//  1. Cross-origin fetch / EventSource (CSRF + cross-origin read).
//     A tab on evil.example.com issues fetch()/EventSource against
//     http://127.0.0.1:7777. The browser sends a truthful Origin that
//     differs from r.Host. CheckOriginAgainstHost rejects it.
//
//  2. DNS rebinding. The attacker rebinds evil.example.com's A record
//     to 127.0.0.1; the victim's browser then sends BOTH Origin and
//     Host as evil.example.com:7777, so the Origin==Host comparison
//     passes. IsLoopbackHost closes this hole by asserting r.Host
//     itself is a loopback name, independent of the Origin match.
package httpsec

import (
	"net"
	"strings"
)

// CheckOriginAgainstHost validates that the request's Origin (or
// Referer, fallback) header host matches the supplied Host header
// (r.Host). Treats missing Origin+Referer as a failure (browsers
// always send one on cross-origin fetch; curl users can opt-in by
// setting Origin to the server's URL).
//
// Returns nil on match, an explanatory error otherwise. The error
// text is safe to return to the client as a 403 body — useful for
// debugging during dev, deliberately vague about the comparison
// (no "expected X got Y" so a fuzzer can't binary-search).
//
// origin is the value of the Origin header (or "" — the caller may
// pass the Referer fallback itself, but the empty-Origin→Referer
// fallback is handled here for convenience). host is r.Host.
func CheckOriginAgainstHost(origin, referer, host string) error {
	if host == "" {
		return ErrMissingHost
	}
	// Origin header wins if present (per RFC 6454). Falls back to
	// Referer (still useful but easier to spoof in older browsers).
	if origin == "" {
		origin = referer
	}
	if origin == "" {
		return ErrMissingOrigin
	}
	// Extract host from the Origin URL. We don't pull net/url for
	// this — we already strip the scheme and any path so a string-
	// match against host is correct and faster.
	originHost := StripOriginToHost(origin)
	if originHost == "" {
		return ErrMalformedOrigin
	}
	if !HostsMatch(originHost, host) {
		return ErrOriginMismatch
	}
	return nil
}

// StripOriginToHost extracts the host portion of an Origin URL
// ("http://127.0.0.1:7777/foo" → "127.0.0.1:7777"). Returns ""
// for inputs that don't look like a URL with a scheme separator.
func StripOriginToHost(origin string) string {
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

// HostsMatch compares two host strings, tolerating an implicit
// port (Origin without ":port" vs r.Host with ":port"). Strict
// otherwise — we don't normalise case (hosts are case-insensitive
// but loopback IPs are ASCII numerics, so case is a non-issue).
func HostsMatch(a, b string) bool {
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

// IsLoopbackHost reports whether the host-part of a Host header value
// names the loopback interface — the allowlist that defeats DNS
// rebinding. It accepts:
//
//   - the IPv4 loopback block (127.0.0.0/8, e.g. 127.0.0.1),
//   - the IPv6 loopback ::1 (bare or bracketed),
//   - the name "localhost" (case-insensitive),
//
// with or without a trailing ":port", and rejects everything else
// (evil.example.com, a LAN IP, 0.0.0.0, …).
//
// extraAllowed carries the operator's explicitly-configured bind host
// for the `--allow-non-loopback` escape hatch: when the UI is bound to
// a LAN IP on purpose, requests whose Host names that IP must pass even
// though it isn't loopback. Each entry is compared host-part to
// host-part (ports stripped on both sides). Empty entries are ignored.
func IsLoopbackHost(host string, extraAllowed ...string) bool {
	h := hostPart(host)
	if h == "" {
		return false
	}
	if strings.EqualFold(h, "localhost") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		return true
	}
	for _, allowed := range extraAllowed {
		a := hostPart(allowed)
		if a == "" {
			continue
		}
		if strings.EqualFold(h, a) {
			return true
		}
	}
	return false
}

// hostPart strips a trailing ":port" (and surrounding brackets for an
// IPv6 literal) from a Host-header / authority string, returning just
// the host. Uses net.SplitHostPort when a port is present and falls
// back to bracket-stripping for the port-less IPv6 case ("[::1]").
func hostPart(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	// No port (or malformed). Strip brackets for a port-less IPv6
	// literal like "[::1]" so net.ParseIP sees "::1".
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return host[1 : len(host)-1]
	}
	return host
}

// hostErr is a small typed error so a debugging test can branch on the
// specific failure mode without scraping strings.
type hostErr string

func (e hostErr) Error() string { return string(e) }

// Error sentinels returned by CheckOriginAgainstHost.
const (
	ErrMissingHost     = hostErr("request has no Host header")
	ErrMissingOrigin   = hostErr("missing Origin and Referer headers")
	ErrMalformedOrigin = hostErr("Origin/Referer header malformed")
	ErrOriginMismatch  = hostErr("Origin/Referer host does not match server")
	// ErrHostNotAllowed is returned by the router's Host-allowlist
	// middleware (see package ui) when r.Host is not a loopback name.
	ErrHostNotAllowed = hostErr("Host header is not an allowed loopback name")
)
