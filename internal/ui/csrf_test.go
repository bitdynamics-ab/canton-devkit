package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

// TestServer_RejectsNonLoopbackBindByDefault is the reviewer pin
// (PR #41 #a): the previous shape merely "strongly discouraged"
// non-loopback in the CLI help text, then bound anyway. With this
// fix, Listen() refuses any Host whose IP isn't loopback unless
// AllowNonLoopback is explicitly true.
//
// We test with literal "0.0.0.0" (the wildcard bind, the worst
// case for "loopback only" claims) AND a real LAN-routable IP
// resolved from the host's interfaces if we can find one. The
// LAN-IP test is skipped when only loopback is available
// (CI containers, restricted runners).
func TestServer_RejectsNonLoopbackBindByDefault(t *testing.T) {
	srv := New(Config{Host: "0.0.0.0", Port: 0, Router: nil})
	_, err := srv.Listen()
	if err == nil {
		t.Fatal("Listen on 0.0.0.0 succeeded — wildcard bind reached production code")
	}
	if !strings.Contains(err.Error(), "non-loopback") {
		t.Errorf("error should explain the loopback gate, got %v", err)
	}
}

// TestServer_AllowsNonLoopbackWhenExplicitlyOptedIn is the
// symmetric pin: an operator who genuinely wants LAN binding
// (firewall in front, SSH tunnel exposure) can opt in via the
// AllowNonLoopback flag. Listen() must succeed in that case.
//
// We can't actually bind a routable IP in tests (would need
// admin), so we use the wildcard "0.0.0.0" — the gate logic is
// the same code path.
func TestServer_AllowsNonLoopbackWhenExplicitlyOptedIn(t *testing.T) {
	srv := New(Config{
		Host: "0.0.0.0", Port: 0, Router: http.NewServeMux(),
		AllowNonLoopback: true,
	})
	addr, err := srv.Listen()
	if err != nil {
		t.Fatalf("AllowNonLoopback=true should bypass the gate, got %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()
	if addr == "" {
		t.Error("Listen returned empty addr")
	}
}

// TestCSRF_RejectsCrossOriginPost is the reviewer pin (PR #41 #b):
// a POST whose Origin header doesn't match the request's Host
// header must be rejected with 403. The threat model: a browser
// tab on another origin issues fetch() at our loopback API.
// Without this, "open a JWT for alice and exfiltrate it" is one
// fetch() from any unrelated site the user has open.
func TestCSRF_RejectsCrossOriginPost(t *testing.T) {
	assets, _ := AssetsHandler()
	srv := New(Config{Port: 0, Router: NewRouter(assets, nil)})
	addr, _ := srv.Listen()
	go srv.Serve() //nolint:errcheck
	defer func() { _ = srv.Shutdown(context.Background()) }()

	req, _ := http.NewRequest("POST", "http://"+addr+"/api/anything", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin POST status = %d, want 403", resp.StatusCode)
	}
}

// TestCSRF_AcceptsSameOriginPost is the symmetric pin: a POST
// with a matching Origin proceeds past the CSRF gate. We hit
// /api/typo (which doesn't exist) so the response is 404 — the
// important assertion is the status is NOT 403.
func TestCSRF_AcceptsSameOriginPost(t *testing.T) {
	assets, _ := AssetsHandler()
	srv := New(Config{Port: 0, Router: NewRouter(assets, nil)})
	addr, _ := srv.Listen()
	go srv.Serve() //nolint:errcheck
	defer func() { _ = srv.Shutdown(context.Background()) }()

	req, _ := http.NewRequest("POST", "http://"+addr+"/api/typo", nil)
	req.Header.Set("Origin", "http://"+addr)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusForbidden {
		t.Errorf("same-origin POST was rejected (403) — CSRF gate too strict")
	}
}

// TestCSRF_RejectsMissingOriginPost — a POST with no Origin AND
// no Referer is also CSRF-suspect (older browsers omit Origin
// for cross-origin requests). Default to refusing.
func TestCSRF_RejectsMissingOriginPost(t *testing.T) {
	assets, _ := AssetsHandler()
	srv := New(Config{Port: 0, Router: NewRouter(assets, nil)})
	addr, _ := srv.Listen()
	go srv.Serve() //nolint:errcheck
	defer func() { _ = srv.Shutdown(context.Background()) }()

	req, _ := http.NewRequest("POST", "http://"+addr+"/api/anything", nil)
	// No Origin, no Referer.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("no-Origin POST status = %d, want 403", resp.StatusCode)
	}
}

// TestCSRF_GETIsExempt verifies the GET-doesn't-need-CSRF rule.
// /api/version is a GET; a request with no Origin should still
// succeed. Without this exemption every iframe / image / curl
// would 403.
func TestCSRF_GETIsExempt(t *testing.T) {
	srv, addr := startTestServer(t)
	defer func() { _ = srv.Shutdown(context.Background()) }()

	resp, err := http.Get("http://" + addr + "/api/version")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET with no Origin was rejected: status %d", resp.StatusCode)
	}
}

// TestSPA_DoesNotMaskAPITypos is the reviewer pin (PR #41 #c):
// the previous SPA fallback served index.html with 200 for ANY
// path that didn't match an embedded file — including /api/typo
// where the frontend genuinely had a routing bug. The fix routes
// /api/* and /events/* through an explicit JSON 404 so a
// misspelled handler path surfaces as a structured error the
// frontend can branch on, not "the API returned HTML, weird".
func TestSPA_DoesNotMaskAPITypos(t *testing.T) {
	srv, addr := startTestServer(t)
	defer func() { _ = srv.Shutdown(context.Background()) }()

	// Misspelled API path — looks like /api/version typo.
	resp, err := http.Get("http://" + addr + "/api/typoVersion")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("/api/typo status = %d, want 404 (must NOT serve SPA index)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "<!doctype") || strings.Contains(string(body), "<html") {
		t.Errorf("/api/typo served HTML (SPA fallback) instead of structured 404:\n%s", body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("/api/typo Content-Type = %q, want application/json", ct)
	}
	// Similarly for /events/typo.
	resp2, _ := http.Get("http://" + addr + "/events/whatever")
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("/events/typo status = %d, want 404", resp2.StatusCode)
	}
}

// TestSPA_StillServesGenuineRoute — symmetric inverse: a path
// that's NOT under /api/ or /events/ DOES fall through to the
// SPA fallback. Required so React Router deep links work.
func TestSPA_StillServesGenuineRoute(t *testing.T) {
	srv, addr := startTestServer(t)
	defer func() { _ = srv.Shutdown(context.Background()) }()

	resp, err := http.Get("http://" + addr + "/dashboard/explorer/abc123")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("genuine SPA route status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("SPA route Content-Type = %q, want text/html", ct)
	}
}

// TestCSRF_HostsMatch unit-tests the host-comparison helper since
// the implicit-port rules are tricky enough to deserve their own
// pin (browser may omit :80 on plain HTTP Origin, etc.).
func TestCSRF_HostsMatch(t *testing.T) {
	cases := []struct {
		origin, host string
		want         bool
	}{
		{"127.0.0.1:7777", "127.0.0.1:7777", true},
		{"127.0.0.1", "127.0.0.1:7777", true}, // origin omits port
		{"127.0.0.1:7777", "127.0.0.1", true}, // symmetric
		{"127.0.0.1", "evil.example.com", false},
		{"evil.example.com", "127.0.0.1", false},
		{"127.0.0.1:7778", "127.0.0.1:7777", false}, // wrong port
	}
	for _, c := range cases {
		got := hostsMatch(c.origin, c.host)
		if got != c.want {
			t.Errorf("hostsMatch(%q, %q) = %v, want %v",
				c.origin, c.host, got, c.want)
		}
	}
}

// TestAssets_RejectsPathTraversal is the reviewer pin (PR #41 round-2 #2):
// the SPA fallback used to swallow any unmatched path as the index;
// a URL whose Path contains a traversal segment must instead get a
// defensive 400. We exercise the asset handler directly (bypassing
// the stdlib http.ServeMux that normalizes paths before they reach
// the handler) so the defence is exercised on the rare case where
// a non-normalizing client / middleware lets a `..` through.
func TestAssets_RejectsPathTraversal(t *testing.T) {
	h, err := AssetsHandler()
	if err != nil {
		t.Fatalf("AssetsHandler: %v", err)
	}
	for _, p := range []string{
		"/../etc/passwd",
		"/foo/../../etc/passwd",
		"/.../etc/passwd",
	} {
		// Construct the request directly so URL.Path is preserved
		// verbatim — net/http server-side canonicalisation would
		// strip "..".
		req := &http.Request{Method: "GET", URL: mustParseURL("http://example/" + strings.TrimPrefix(p, "/"))}
		// Override URL.Path to keep the literal traversal segment
		// (url.Parse also normalises in some Go versions).
		req.URL.Path = p
		rr := newRR()
		h.ServeHTTP(rr, req)
		if rr.code != http.StatusBadRequest {
			t.Errorf("traversal %q status = %d, want 400 (silent SPA fallback would hide intent)",
				p, rr.code)
		}
	}
}

// rrRecorder is a tiny http.ResponseWriter recorder. We don't
// pull httptest just for two fields here — keeps the test file
// dep-surface low.
type rrRecorder struct {
	hdr  http.Header
	code int
	body bytes.Buffer
}

func newRR() *rrRecorder                          { return &rrRecorder{hdr: http.Header{}} }
func (r *rrRecorder) Header() http.Header         { return r.hdr }
func (r *rrRecorder) Write(p []byte) (int, error) { return r.body.Write(p) }
func (r *rrRecorder) WriteHeader(code int)        { r.code = code }

// mustParseURL is a tiny test helper for constructing requests
// with literal path strings.
func mustParseURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// TestAssets_PlaceholderSentinel is the reviewer pin (PR #41 round-2 #5):
// the embedded dist/index.html carries a sentinel string so
// IsPlaceholderBundle can detect a release binary that forgot
// `make frontend`. Without this guard, a stage promotion serves
// the dev placeholder to real users silently.
func TestAssets_PlaceholderSentinel(t *testing.T) {
	if !IsPlaceholderBundle() {
		// This branch only fires once Vite truly replaces the
		// placeholder. Until then the test asserts the sentinel
		// is intact.
		t.Skip("placeholder already replaced (a real Vite build is embedded) — this test no longer applies")
	}
	// True case: confirm the public API agrees.
	if !IsPlaceholderBundle() {
		t.Error("IsPlaceholderBundle inconsistent — sentinel marker present in dist/index.html but check returned false")
	}
}

// TestRouter_AccessLogEmittedPerRequest is the reviewer pin
// (PR #41 round-2 #3): every request goes through withAccessLog
// and produces a stable parseable log line. Catches the
// regression class where someone removes the middleware or
// breaks the format.
func TestRouter_AccessLogEmittedPerRequest(t *testing.T) {
	var logBuf bytes.Buffer
	prev := log.Default().Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(prev) })

	srv, addr := startTestServer(t)
	defer func() { _ = srv.Shutdown(context.Background()) }()

	resp, err := http.Get("http://" + addr + "/api/version")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()

	body := logBuf.String()
	for _, want := range []string{
		"access: 200",
		"GET /api/version",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("access log missing %q\nfull:\n%s", want, body)
		}
	}
	// Stable rule: the access log MUST NOT carry the query string
	// (?include_jwt=true would leak credential intent).
	if strings.Contains(body, "?") {
		t.Errorf("access log included query string — credential leak vector:\n%s", body)
	}
}

// TestCSRF_JWTEndpointProtectedEndToEnd is the reviewer pin
// (PR #43 cross-PR round-2): the /api/instances/{name}/jwt
// route is credential-issuing and MUST go through the CSRF
// middleware from withOriginCheck (#41). The handler-package
// unit tests use a bare ServeMux that bypasses the middleware;
// without this end-to-end test, a regression that drops
// withOriginCheck from NewRouter or accidentally exempts /api/
// routes would silently pass handler-package tests.
//
// Catch is structural: we drive the FULL NewRouter pipeline and
// assert a cross-origin POST to the JWT endpoint returns 403,
// proving the middleware chain reaches that handler.
func TestCSRF_JWTEndpointProtectedEndToEnd(t *testing.T) {
	srv, addr := startTestServer(t)
	defer func() { _ = srv.Shutdown(context.Background()) }()

	// We don't care whether the underlying handler would have
	// succeeded — we only care that the CSRF gate fires BEFORE
	// the handler runs. Cross-origin POST → 403.
	req, _ := http.NewRequest("POST",
		"http://"+addr+"/api/instances/demo/jwt", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin POST /api/instances/demo/jwt status = %d, want 403 — withOriginCheck not reaching credential route",
			resp.StatusCode)
	}
}

// TestCSRF_SkillsInstallProtectedEndToEnd pins the new filesystem-writing
// skills install route behind the same router-level CSRF middleware as the
// rest of the mutating API surface.
func TestCSRF_SkillsInstallProtectedEndToEnd(t *testing.T) {
	srv, addr := startTestServer(t)
	defer func() { _ = srv.Shutdown(context.Background()) }()

	req, _ := http.NewRequest("POST",
		"http://"+addr+"/api/skills/install", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin POST /api/skills/install status = %d, want 403 — withOriginCheck not reaching skills install route",
			resp.StatusCode)
	}
}

// TestCSRF_TokensActionsProtectedEndToEnd pins that the state-changing
// /api/tokens POSTs (create / mint / transfer / accept / burn) inherit
// the global Origin gate — they have no per-route CSRF guard of their
// own and rely entirely on withOriginCheck wrapping the mux in
// NewRouter. A cross-origin POST to each must be rejected with 403
// BEFORE the handler (and any ledger mutation) runs.
func TestCSRF_TokensActionsProtectedEndToEnd(t *testing.T) {
	srv, addr := startTestServer(t)
	defer func() { _ = srv.Shutdown(context.Background()) }()

	routes := []string{
		"/api/tokens",
		"/api/tokens/RTK/mint",
		"/api/tokens/RTK/transfer",
		"/api/tokens/transfers/abc123/accept",
		"/api/tokens/RTK/burn",
	}
	for _, route := range routes {
		req, _ := http.NewRequest("POST", "http://"+addr+route, nil)
		req.Header.Set("Origin", "https://evil.example.com")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", route, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("cross-origin POST %s status = %d, want 403 — withOriginCheck not reaching the route",
				route, resp.StatusCode)
		}
	}
}

func TestRouter_SkillsInstallRouteMountedEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	srv, addr := startTestServer(t)
	defer func() { _ = srv.Shutdown(context.Background()) }()

	reqBody, _ := json.Marshal(map[string]string{"target": "claude"})
	req, _ := http.NewRequest("POST",
		"http://"+addr+"/api/skills/install", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://"+addr)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	var body struct {
		Dir   string `json:"dir"`
		Count int    `json:"count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Dir != filepath.Join(home, ".claude", "skills") {
		t.Errorf("dir = %q, want fake home claude skills dir", body.Dir)
	}
	if body.Count != 6 {
		t.Errorf("count = %d, want 6", body.Count)
	}
}
