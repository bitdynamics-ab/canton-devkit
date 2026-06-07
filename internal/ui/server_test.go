package ui

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestServer_BindsToLoopbackByDefault is the security pin: the default
// Config MUST bind a loopback address. Regression here would
// broadcast credentials (JWTs, party IDs) on the LAN. The Server
// docstring describes this as load-bearing; this test enforces it.
func TestServer_BindsToLoopbackByDefault(t *testing.T) {
	assets, err := AssetsHandler()
	if err != nil {
		t.Fatalf("assets: %v", err)
	}
	srv := New(Config{Port: 0, Router: NewRouter(assets, nil)}) // default host
	addr, err := srv.Listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split addr %q: %v", addr, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		t.Errorf("default bind is %q, want loopback — exposing the UI on a non-loopback iface would broadcast JWTs", host)
	}
}

// TestConfig_WithDefaultsKeepsPortZero is the lock-in for the
// Port=0 contract: Port has NO default substitution in
// withDefaults(). 0 means "OS-assigned" and is used by `--port 0`,
// tests, and CI smoke checks. Without this pin, a contributor who
// adds `if c.Port == 0 { c.Port = 7777 }` would silently break
// every Port=0 caller (test pool collisions, "port already in
// use" on `--port 0`, etc.). The CLI flag owns the 7777 human
// default — see internal/cli/localnet/ui.go.
func TestConfig_WithDefaultsKeepsPortZero(t *testing.T) {
	got := Config{Port: 0}.withDefaults()
	if got.Port != 0 {
		t.Errorf("withDefaults() upgraded Port=0 to %d — breaks OS-assigned-port callers", got.Port)
	}
}

// TestServer_PortZeroAssignsFreePort verifies the OS-assigned-port
// path (used by `--port 0` in the CLI and by every test that needs
// a non-colliding port). The resolved Addr() must report a real
// non-zero port.
func TestServer_PortZeroAssignsFreePort(t *testing.T) {
	assets, _ := AssetsHandler()
	srv := New(Config{Port: 0, Router: NewRouter(assets, nil)})
	addr, err := srv.Listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	_, portStr, _ := net.SplitHostPort(addr)
	if portStr == "0" || portStr == "" {
		t.Errorf("OS-assigned port did not resolve; addr=%q", addr)
	}
}

// TestServer_ServeBeforeListenErrors is a contract pin: calling
// Serve() without Listen() should fail loudly, not block on a nil
// listener and hang forever. The CLI flow guarantees the order, but
// future callers (Web UI handler tests, an in-process embed) need
// the bare-Serve path to fail fast.
func TestServer_ServeBeforeListenErrors(t *testing.T) {
	assets, _ := AssetsHandler()
	srv := New(Config{Port: 0, Router: NewRouter(assets, nil)})
	err := srv.Serve()
	if err == nil {
		t.Fatal("Serve before Listen should return an error, not block")
	}
	if !strings.Contains(err.Error(), "before Listen") {
		t.Errorf("error message should mention Listen, got %q", err)
	}
}

// TestServer_ShutdownStopsServe pins the lifecycle: Shutdown() must
// cause an in-flight Serve() to return cleanly (no error). This is
// the path SIGINT triggers in ui.go.
func TestServer_ShutdownStopsServe(t *testing.T) {
	assets, _ := AssetsHandler()
	srv := New(Config{Port: 0, Router: NewRouter(assets, nil)})
	if _, err := srv.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- srv.Serve() }()
	// Tiny pause to let Serve actually start. Without it Shutdown
	// races Serve and the test passes for the wrong reason.
	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned non-nil after Shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after Shutdown")
	}
}

// TestServer_ReadHeaderTimeoutSet pins the slowloris defence: the
// underlying *http.Server MUST carry a non-zero ReadHeaderTimeout.
// Default Go *http.Server has none, which means a dribble-headers
// attack can pin a goroutine indefinitely. Even on loopback this
// matters (a runaway browser tab does the same).
func TestServer_ReadHeaderTimeoutSet(t *testing.T) {
	srv := New(Config{Port: 0, Router: http.NewServeMux()})
	if srv.http.ReadHeaderTimeout == 0 {
		t.Error("ReadHeaderTimeout = 0 — slowloris defence regressed")
	}
}

// TestServer_HealthzReturnsOK exercises the end-to-end stack:
// bind → serve → real HTTP GET → 200 from the healthz handler.
// Smoke-tests the assembled pipeline at one of the cheapest endpoints.
func TestServer_HealthzReturnsOK(t *testing.T) {
	assets, _ := AssetsHandler()
	srv := New(Config{Port: 0, Router: NewRouter(assets, nil)})
	addr, err := srv.Listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	// Errors checked below via Shutdown.
	go func() { _ = srv.Serve() }()
	defer func() { _ = srv.Shutdown(context.Background()) }()

	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}
}

// TestServer_IdleTimeoutSet pins:
// every server
// MUST have a non-zero IdleTimeout. Without it, keep-alive
// connections from sleeping browser tabs pin server-side
// goroutines indefinitely; over hours of use that accumulates
// into a leak. 60s is the chosen value (twice the SSE heartbeat
// so a healthy stream's keepalives reset the timer cleanly).
func TestServer_IdleTimeoutSet(t *testing.T) {
	srv := New(Config{Port: 0, Router: http.NewServeMux()})
	if srv.http.IdleTimeout == 0 {
		t.Error("IdleTimeout = 0 — keep-alive connections will pin goroutines on sleeping tabs")
	}
	if srv.http.IdleTimeout < 30*time.Second {
		t.Errorf("IdleTimeout = %v, too aggressive — would close healthy SSE streams between heartbeats",
			srv.http.IdleTimeout)
	}
}
