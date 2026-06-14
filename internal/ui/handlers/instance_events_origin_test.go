package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/ui/stream"
)

// hubServingMux mounts the instance handlers with a real hub so the
// per-instance SSE route (handleInstanceEvents) is the live handler,
// not the no-hub stub.
func hubServingMux(t *testing.T) (*httptest.Server, *stream.Hub) {
	t.Helper()
	hub := stream.New()
	mux := http.NewServeMux()
	MountInstances(mux, hub)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Cleanup(hub.Close)
	return srv, hub
}

// TestInstanceEvents_RejectsCrossOrigin is the regression pin: a
// cross-origin EventSource open against the per-instance progress
// stream must be rejected with 403, mirroring the global /events
// handler. Before the fix this stream had NO Origin check, so a tab on
// evil.example.com could read another instance's bring-up progress
// (name, Splice version, step status, RunUp error text).
func TestInstanceEvents_RejectsCrossOrigin(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv, _ := hubServingMux(t)

	req, _ := http.NewRequest("GET", srv.URL+"/api/instances/demo/events", nil)
	// Host here is the httptest loopback addr (passes the loopback
	// allowlist conceptually); Origin is the attacker's site. This is
	// the cross-origin-read vector the Host allowlist does NOT catch.
	req.Header.Set("Origin", "https://evil.example.com")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin EventSource status = %d, want 403 — per-instance SSE Origin gate missing",
			resp.StatusCode)
	}
	// The 403 must fire BEFORE SSE headers are written, otherwise the
	// stream already leaked its Content-Type and a body could follow.
	if ct := resp.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("rejected request still sent SSE Content-Type %q — gate ran too late", ct)
	}
}

// TestInstanceEvents_AllowsSameOrigin is the symmetric pin: an
// EventSource open whose Origin matches the request Host is NOT blocked
// by the gate. We open the stream with a cancellable context, assert
// the response opens 200 with the SSE Content-Type, then cancel to
// unblock the handler.
func TestInstanceEvents_AllowsSameOrigin(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv, _ := hubServingMux(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET",
		srv.URL+"/api/instances/demo/events", nil)
	// Same-origin: Origin host == request Host (httptest server addr).
	req.Header.Set("Origin", "http://"+req.Host)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("same-origin EventSource status = %d, want 200 — gate too strict", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	cancel()
	// Give the handler a beat to observe ctx cancellation and return.
	time.Sleep(20 * time.Millisecond)
}

// TestInstanceEvents_NoOriginProceeds pins the curl-friendly behaviour:
// a request with NO Origin header (a direct curl, not a browser) is not
// rejected — matching sse.go's "only enforce when Origin is present"
// rule. Asserts the stream opens 200 rather than 403.
func TestInstanceEvents_NoOriginProceeds(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv, _ := hubServingMux(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET",
		srv.URL+"/api/instances/demo/events", nil)
	// No Origin, no Referer.

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusForbidden {
		t.Errorf("no-Origin EventSource was rejected (403) — curl path should proceed")
	}
	cancel()
	time.Sleep(20 * time.Millisecond)
}
