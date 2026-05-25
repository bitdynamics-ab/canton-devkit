package ui

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/ui/stream"
)

// TestSSE_ReceivesPublishedEvent is the end-to-end smoke for the SSE
// stack: bind a server with a real hub, connect via GET /events,
// publish an event, assert the wire format on the read side.
// Catches regressions in: handler installation, wire format,
// hub→handler plumbing.
func TestSSE_ReceivesPublishedEvent(t *testing.T) {
	hub := stream.New()
	assets, _ := AssetsHandler()
	srv := New(Config{Port: 0, Router: NewRouter(assets, hub)})
	addr, _ := srv.Listen()
	go srv.Serve() //nolint:errcheck
	defer srv.Shutdown(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+addr+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}

	// Give the handler a beat to install its subscription before
	// we publish; the helper polls Stats() instead of sleeping a
	// fixed interval (PR #42 round-2 #6 — no time.Sleep in tests).
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	if !hub.WaitForSubscribers(waitCtx, 1) {
		t.Fatal("subscriber didn't register")
	}
	waitCancel()
	hub.Publish(stream.Event{Topic: "services", ID: "42", Data: []byte("hello")})

	scanner := bufio.NewScanner(resp.Body)
	var lines []string
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(lines) < 4 {
		if scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
	}
	body := strings.Join(lines, "\n")
	for _, want := range []string{"id: 42", "event: services", "data: hello"} {
		if !strings.Contains(body, want) {
			t.Errorf("SSE frame missing %q\nfull:\n%s", want, body)
		}
	}
}

// TestSSE_TopicFilter exercises ?topics=foo,bar: only events whose
// Topic appears in the list reach this subscriber. Without this,
// every browser tab gets every event and the wire/CPU cost
// multiplies.
func TestSSE_TopicFilter(t *testing.T) {
	hub := stream.New()
	assets, _ := AssetsHandler()
	srv := New(Config{Port: 0, Router: NewRouter(assets, hub)})
	addr, _ := srv.Listen()
	go srv.Serve() //nolint:errcheck
	defer srv.Shutdown(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET",
		"http://"+addr+"/events?topics=metrics", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	if !hub.WaitForSubscribers(waitCtx, 1) {
		t.Fatal("subscriber didn't register")
	}
	waitCancel()
	hub.Publish(stream.Event{Topic: "services", Data: []byte("skip-me")})
	hub.Publish(stream.Event{Topic: "metrics", Data: []byte("show-me")})

	scanner := bufio.NewScanner(resp.Body)
	var got []string
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if scanner.Scan() {
			got = append(got, scanner.Text())
		}
	}
	body := strings.Join(got, "\n")
	if strings.Contains(body, "skip-me") {
		t.Errorf("topic filter let unmatched topic through:\n%s", body)
	}
	if !strings.Contains(body, "show-me") {
		t.Errorf("topic filter blocked matching topic:\n%s", body)
	}
}

// TestSSE_DisconnectFreesSubscription pins the cleanup contract:
// when the HTTP client cancels (browser tab closed), the handler
// MUST call cancel on the subscription so the hub doesn't keep
// trying to deliver to a dead channel. We assert via Hub.Stats:
// subscriber count returns to 0 after the request context cancels.
func TestSSE_DisconnectFreesSubscription(t *testing.T) {
	hub := stream.New()
	assets, _ := AssetsHandler()
	srv := New(Config{Port: 0, Router: NewRouter(assets, hub)})
	addr, _ := srv.Listen()
	go srv.Serve() //nolint:errcheck
	defer srv.Shutdown(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+addr+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}

	// Confirm the subscription registered.
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	if !hub.WaitForSubscribers(waitCtx, 1) {
		t.Fatal("subscriber didn't register")
	}
	waitCancel()
	if got := hub.Stats().Subscribers; got != 1 {
		t.Fatalf("Stats.Subscribers = %d after connect, want 1", got)
	}

	// Disconnect.
	cancel()
	resp.Body.Close()

	// The handler's defer cancel() should fire on the next loop
	// iteration when ctx.Done is selected. Poll briefly.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if hub.Stats().Subscribers == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("Subscribers = %d after disconnect, want 0 — subscription leaked",
		hub.Stats().Subscribers)
}

// TestSSE_NilHubReturns503 pins the fallback wiring: when the
// router is constructed with hub=nil (test mode, future read-only
// mode), /events serves 503 rather than crashing or 404-ing.
func TestSSE_NilHubReturns503(t *testing.T) {
	assets, _ := AssetsHandler()
	srv := New(Config{Port: 0, Router: NewRouter(assets, nil)})
	addr, _ := srv.Listen()
	go srv.Serve() //nolint:errcheck
	defer srv.Shutdown(context.Background())

	resp, err := http.Get("http://" + addr + "/events")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

// TestSSE_MultiLineDataGetsPerLinePrefix pins the SSE spec
// detail: multi-line Data must produce one `data:` line per
// source line. A single `data:` containing embedded newlines is
// invalid SSE and would be interpreted as the end of the event by
// some clients.
func TestSSE_MultiLineDataGetsPerLinePrefix(t *testing.T) {
	hub := stream.New()
	assets, _ := AssetsHandler()
	srv := New(Config{Port: 0, Router: NewRouter(assets, hub)})
	addr, _ := srv.Listen()
	go srv.Serve() //nolint:errcheck
	defer srv.Shutdown(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+addr+"/events", nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	if !hub.WaitForSubscribers(waitCtx, 1) {
		t.Fatal("subscriber didn't register")
	}
	waitCancel()
	hub.Publish(stream.Event{Topic: "x", Data: []byte("line1\nline2\nline3")})

	scanner := bufio.NewScanner(resp.Body)
	var got []string
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) && len(got) < 6 {
		if scanner.Scan() {
			got = append(got, scanner.Text())
		}
	}
	body := strings.Join(got, "\n")
	for _, want := range []string{"data: line1", "data: line2", "data: line3"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}
// TestSSE_RejectsCrossOriginConnection is the reviewer pin
// (PR #42 #c): a browser tab on evil.example.com can open
// EventSource("http://127.0.0.1:7777/events") and read our event
// stream — EventSource always sends GET (CSRF-exempt globally),
// so the SSE handler must do its own Origin check.
func TestSSE_RejectsCrossOriginConnection(t *testing.T) {
	hub := stream.New()
	assets, _ := AssetsHandler()
	srv := New(Config{Port: 0, Router: NewRouter(assets, hub)})
	addr, _ := srv.Listen()
	go srv.Serve() //nolint:errcheck
	defer srv.Shutdown(context.Background())

	req, _ := http.NewRequest("GET", "http://"+addr+"/events", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin /events status = %d, want 403", resp.StatusCode)
	}
}

// TestSSE_NoOriginAllowedForCurl is the symmetric pin: a CLI tool
// (curl, EventSource client outside a browser) that doesn't send
// Origin at all should still be allowed — there's no CSRF risk
// without a browser. Catches the regression class where the
// gate over-tightens and refuses legitimate non-browser clients.
func TestSSE_NoOriginAllowedForCurl(t *testing.T) {
	hub := stream.New()
	assets, _ := AssetsHandler()
	srv := New(Config{Port: 0, Router: NewRouter(assets, hub)})
	addr, _ := srv.Listen()
	go srv.Serve() //nolint:errcheck
	defer srv.Shutdown(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+addr+"/events", nil)
	// No Origin header (curl default).
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		t.Errorf("/events with no Origin returned 403 — curl/CLI clients blocked")
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/events with no Origin status = %d, want 200", resp.StatusCode)
	}
}

// TestSSE_ContentTypeCharsetUTF8 is the reviewer pin (PR #42
// round-2 #4): the SSE Content-Type must include charset=utf-8.
// Without it, older Safari + some proxies interpret the stream
// as Latin-1 and mangle multi-byte event data.
func TestSSE_ContentTypeCharsetUTF8(t *testing.T) {
	hub := stream.New()
	assets, _ := AssetsHandler()
	srv := New(Config{Port: 0, Router: NewRouter(assets, hub)})
	addr, _ := srv.Listen()
	go srv.Serve() //nolint:errcheck
	defer srv.Shutdown(context.Background())

	resp, err := http.Get("http://" + addr + "/events")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "charset=utf-8") {
		t.Errorf("Content-Type = %q, want charset=utf-8 — non-UTF-8 clients mangle multi-byte data",
			ct)
	}
}

// TestSSE_TrailingNewlineStripped is the reviewer pin (PR #42
// round-2 #4): a Data payload ending in "\n" must not produce
// a spurious empty `data:` line that changes the event payload
// from "msg" to "msg\n" on the client side.
func TestSSE_TrailingNewlineStripped(t *testing.T) {
	hub := stream.New()
	assets, _ := AssetsHandler()
	srv := New(Config{Port: 0, Router: NewRouter(assets, hub)})
	addr, _ := srv.Listen()
	go srv.Serve() //nolint:errcheck
	defer srv.Shutdown(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+addr+"/events", nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	waitCtx, wc := context.WithTimeout(context.Background(), time.Second)
	if !hub.WaitForSubscribers(waitCtx, 1) {
		t.Fatal("subscriber didn't register")
	}
	wc()

	hub.Publish(stream.Event{Topic: "x", Data: []byte("msg\n")})

	scanner := bufio.NewScanner(resp.Body)
	var got []string
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) && len(got) < 4 {
		if scanner.Scan() {
			got = append(got, scanner.Text())
		}
	}
	body := strings.Join(got, "\n")
	// Must have ONE `data: msg` and NO trailing `data:` empty
	// (the trailing-newline bug emitted "data: msg\ndata: \n\n").
	if !strings.Contains(body, "data: msg") {
		t.Errorf("missing 'data: msg' in:\n%s", body)
	}
	if strings.Contains(body, "data: \n") || strings.Contains(body, "data:\n\n") {
		t.Errorf("trailing empty data line present (trailing-newline bug):\n%s", body)
	}
}
