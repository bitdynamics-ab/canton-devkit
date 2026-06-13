package ui

import (
	"context"
	"net/http"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/ui/stream"
)

// TestHostCheck_RejectsRebindingHost is the DNS-rebinding regression
// pin (#64). It models the attack precisely: the victim's browser
// dials the loopback server (the connection terminates on 127.0.0.1)
// but — because the attacker rebound evil.example.com's A record to
// 127.0.0.1 — sends Origin AND Host as evil.example.com:7777.
//
// Origin == Host, so the CSRF middleware (withOriginCheck) is happy
// and the loopback bind doesn't help. Only the Host allowlist
// (withHostCheck) closes the hole. We drive a GET (the worst case:
// GET /api/instances/{name}/app-config leaks party IDs + endpoints
// and is exempt from the state-changing CSRF gate) so the test fails
// if the Host check were ever scoped to mutating methods only.
func TestHostCheck_RejectsRebindingHost(t *testing.T) {
	srv, addr := startTestServer(t)
	defer func() { _ = srv.Shutdown(context.Background()) }()

	// Dial the real loopback addr, but lie about Host+Origin the way
	// a rebound browser would. req.Host overrides the on-the-wire
	// Host header without changing the dial target.
	req, _ := http.NewRequest("GET", "http://"+addr+"/api/instances/demo", nil)
	req.Host = "evil.example.com:7777"
	req.Header.Set("Origin", "http://evil.example.com:7777")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("rebinding GET (Host=Origin=evil) status = %d, want 403 — Host allowlist not enforced",
			resp.StatusCode)
	}
}

// TestHostCheck_RejectsRebindingPOST is the mutating-method companion:
// the same rebinding shape on a value-moving POST (token mint, instance
// delete) must also 403. Origin==Host means withOriginCheck passes;
// withHostCheck is the backstop.
func TestHostCheck_RejectsRebindingPOST(t *testing.T) {
	srv, addr := startTestServer(t)
	defer func() { _ = srv.Shutdown(context.Background()) }()

	req, _ := http.NewRequest("POST", "http://"+addr+"/api/tokens/RTK/mint", nil)
	req.Host = "evil.example.com:7777"
	req.Header.Set("Origin", "http://evil.example.com:7777")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("rebinding POST status = %d, want 403", resp.StatusCode)
	}
}

// TestHostCheck_AllowsLoopbackHost is the symmetric pin: a normal
// loopback GET (Host = 127.0.0.1:port, the real connection host) is
// NOT blocked by the Host allowlist. We hit /api/version (always 200)
// and assert the status is 200, proving the allowlist doesn't
// false-refuse legitimate local traffic.
func TestHostCheck_AllowsLoopbackHost(t *testing.T) {
	srv, addr := startTestServer(t)
	defer func() { _ = srv.Shutdown(context.Background()) }()

	resp, err := http.Get("http://" + addr + "/api/version")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("loopback GET /api/version status = %d, want 200 — Host allowlist too strict",
			resp.StatusCode)
	}
}

// TestHostCheck_LocalhostNameAllowed pins that the literal name
// "localhost" (not just the 127.0.0.1 IP) is on the allowlist — the
// frontend is commonly opened at http://localhost:7777, so refusing it
// would break the default workflow.
func TestHostCheck_LocalhostNameAllowed(t *testing.T) {
	srv, addr := startTestServer(t)
	defer func() { _ = srv.Shutdown(context.Background()) }()

	req, _ := http.NewRequest("GET", "http://"+addr+"/api/version", nil)
	req.Host = "localhost:7777"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Host=localhost status = %d, want 200", resp.StatusCode)
	}
}

// TestHostCheck_WithAllowedHostsOptIn pins the --allow-non-loopback
// wiring: when NewRouter is built with WithAllowedHosts, a request
// whose Host names that explicitly-allowed (non-loopback) host passes
// the Host allowlist. Without the option the same Host would be a
// rebinding 403 (covered above), so this proves the opt-in widens the
// allowlist rather than disabling it.
func TestHostCheck_WithAllowedHostsOptIn(t *testing.T) {
	assets, err := AssetsHandler()
	if err != nil {
		t.Fatalf("AssetsHandler: %v", err)
	}
	srv := New(Config{
		Port:   0,
		Router: NewRouter(assets, nil, WithAllowedHosts("devbox.lan")),
	})
	addr, err := srv.Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go func() { _ = srv.Serve() }()
	defer func() { _ = srv.Shutdown(context.Background()) }()

	req, _ := http.NewRequest("GET", "http://"+addr+"/api/version", nil)
	req.Host = "devbox.lan:7777"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("allowed-host GET status = %d, want 200 — WithAllowedHosts not honoured",
			resp.StatusCode)
	}

	// And an UNlisted non-loopback host is still refused on the same
	// server — the opt-in is exact, not a blanket disable.
	req2, _ := http.NewRequest("GET", "http://"+addr+"/api/version", nil)
	req2.Host = "evil.example.com:7777"
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("unlisted host status = %d, want 403", resp2.StatusCode)
	}
}

// TestHostCheck_GlobalEventsRebinding pins that the global /events SSE
// stream is also covered by the Host allowlist (it runs in the same
// middleware chain). A rebound EventSource open must 403 before the
// stream starts.
func TestHostCheck_GlobalEventsRebinding(t *testing.T) {
	hub := stream.New()
	assets, _ := AssetsHandler()
	srv := New(Config{Port: 0, Router: NewRouter(assets, hub)})
	addr, _ := srv.Listen()
	go func() { _ = srv.Serve() }()
	defer func() { _ = srv.Shutdown(context.Background()) }()

	req, _ := http.NewRequest("GET", "http://"+addr+"/events", nil)
	req.Host = "evil.example.com:7777"
	req.Header.Set("Origin", "http://evil.example.com:7777")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("rebinding /events status = %d, want 403", resp.StatusCode)
	}
}
