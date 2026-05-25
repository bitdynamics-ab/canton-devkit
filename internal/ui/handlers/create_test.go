package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/ui/progress"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/stream"
)

// TestCreate_ValidationRejectsBadName pins the four common
// rejection paths — empty body, malformed JSON, oversized body,
// invalid DNS-label name — and asserts on the stable error codes
// the frontend branches on.
func TestCreate_ValidationRejectsBadName(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	jobsReset()
	hub := stream.New()
	defer hub.Close()
	handler := handleCreate(hub)

	cases := []struct {
		name     string
		body     string
		wantCode int
		wantTag  string
	}{
		{
			name:     "malformed JSON",
			body:     `{"name": invalid`,
			wantCode: http.StatusBadRequest,
			wantTag:  "INVALID_REQUEST",
		},
		{
			name:     "empty name",
			body:     `{"name":""}`,
			wantCode: http.StatusBadRequest,
			wantTag:  "INVALID_REQUEST",
		},
		{
			name:     "uppercase name (not DNS label)",
			body:     `{"name":"MyStack"}`,
			wantCode: http.StatusBadRequest,
			wantTag:  "INVALID_REQUEST",
		},
		{
			name:     "underscore (not DNS label)",
			body:     `{"name":"my_stack"}`,
			wantCode: http.StatusBadRequest,
			wantTag:  "INVALID_REQUEST",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/instances",
				strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			handler(rec, req)

			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			var body errorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body: %v\nraw: %s", err, rec.Body.String())
			}
			if body.Code != tc.wantTag {
				t.Errorf("code = %q, want %q", body.Code, tc.wantTag)
			}
		})
	}
}

// TestCreate_OversizedBodyRejected pins the 4 KiB cap with the
// REQUEST_TOO_LARGE code. Mirrors the JWT endpoint's body cap;
// defence-in-depth against a malicious 100 MiB JSON.
func TestCreate_OversizedBodyRejected(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	jobsReset()
	hub := stream.New()
	defer hub.Close()
	handler := handleCreate(hub)

	// Craft a body bigger than upBodyMax (4 KiB).
	bigName := strings.Repeat("a", 5000)
	body := []byte(`{"name":"` + bigName + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/instances",
		bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413; body: %s", rec.Code, rec.Body.String())
	}
}

// TestCreate_NoHubReturns503 pins the stub behaviour for
// deployments without an event hub. Without this, a misconfigured
// server would 404 and the frontend would mistake it for "endpoint
// missing" rather than "feature disabled."
func TestCreate_NoHubReturns503(t *testing.T) {
	mux := http.NewServeMux()
	MountInstances(mux, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/instances",
		strings.NewReader(`{"name":"demo"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != "SSE_DISABLED" {
		t.Errorf("code = %q, want SSE_DISABLED", body.Code)
	}
}

// TestCreate_AcceptedReturns202WithEventsURL pins the success
// path. We don't let the goroutine actually run RunUp (that would
// require docker); cancel via the jobs registry the moment we see
// the 202 so the test exits clean.
func TestCreate_AcceptedReturns202WithEventsURL(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	jobsReset()
	hub := stream.New()
	defer hub.Close()
	handler := handleCreate(hub)

	req := httptest.NewRequest(http.MethodPost, "/api/instances",
		strings.NewReader(`{"name":"democreate"}`))
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", rec.Code, rec.Body.String())
	}

	var resp upAcceptedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Instance != "democreate" {
		t.Errorf("instance = %q, want democreate", resp.Instance)
	}
	if resp.EventsURL != "/api/instances/democreate/events" {
		t.Errorf("events_url = %q, want /api/instances/democreate/events", resp.EventsURL)
	}

	// Cancel the goroutine so the test exits clean without
	// waiting on the 10-minute job timeout. Cancel is idempotent
	// against the goroutine's own deferred cancelJob.
	jobs.Cancel("democreate")
	// Best-effort wait for the goroutine to finish so it doesn't
	// leak into the next test.
	waitJobsDrain(t, time.Second)
}

// TestCreate_DuplicateNameReturns409 pins the in-flight-job
// rejection. A second POST while the first goroutine is still
// running must get INSTANCE_CREATING; the frontend uses this to
// auto-attach to the existing stream rather than starting a new
// one.
func TestCreate_DuplicateNameReturns409(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	jobsReset()
	hub := stream.New()
	defer hub.Close()
	handler := handleCreate(hub)

	req := httptest.NewRequest(http.MethodPost, "/api/instances",
		strings.NewReader(`{"name":"dupcreate"}`))
	rec1 := httptest.NewRecorder()
	handler(rec1, req)
	if rec1.Code != http.StatusAccepted {
		t.Fatalf("first POST status = %d, want 202", rec1.Code)
	}

	// Second POST for the same name while the first is in-flight.
	req2 := httptest.NewRequest(http.MethodPost, "/api/instances",
		strings.NewReader(`{"name":"dupcreate"}`))
	rec2 := httptest.NewRecorder()
	handler(rec2, req2)
	if rec2.Code != http.StatusConflict {
		t.Errorf("second POST status = %d, want 409", rec2.Code)
	}
	var body errorBody
	if err := json.Unmarshal(rec2.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != "INSTANCE_CREATING" {
		t.Errorf("code = %q, want INSTANCE_CREATING", body.Code)
	}

	jobs.Cancel("dupcreate")
	waitJobsDrain(t, time.Second)
}

// TestInstanceEvents_StreamsBufferedEvents pins the late-subscriber
// contract end-to-end: simulate the create-flow by enabling the
// buffer + publishing two SSEProgress events, then opening the SSE
// handler. The handler must drain the buffered events into the
// response body before the request ends.
func TestInstanceEvents_StreamsBufferedEvents(t *testing.T) {
	hub := stream.New()
	defer hub.Close()
	hub.EnableBuffering(progress.TopicFor("alpha"), 32)

	prog := progress.New(hub, "alpha")
	prog.Warn("first")
	prog.Warn("second")

	// Build the route via MountInstances so we exercise the real
	// path mux and pattern matching.
	mux := http.NewServeMux()
	MountInstances(mux, hub)

	// Use a server with an actual TCP listener so http.ResponseRecorder
	// doesn't swallow streaming behaviour. We close the connection
	// from the client side after seeing both events.
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		srv.URL+"/api/instances/alpha/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream prefix", ct)
	}

	// Read until both events appear (or the request ctx deadline
	// fires). The req has a 2-second deadline (set via
	// http.NewRequestWithContext); ctx-cancel closes the body
	// once the deadline expires so the Read loop exits.
	buf := make([]byte, 1024)
	body := []byte{}
	for !bytes.Contains(body, []byte("second")) {
		n, err := resp.Body.Read(buf)
		body = append(body, buf[:n]...)
		if err != nil {
			break
		}
	}
	if !bytes.Contains(body, []byte("first")) || !bytes.Contains(body, []byte("second")) {
		t.Errorf("expected both events in body, got:\n%s", body)
	}
}

// TestInstanceEvents_InvalidName400 pins the path-param validation.
func TestInstanceEvents_InvalidName400(t *testing.T) {
	hub := stream.New()
	defer hub.Close()
	mux := http.NewServeMux()
	MountInstances(mux, hub)

	req := httptest.NewRequest(http.MethodGet,
		"/api/instances/Invalid_Name/events", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for invalid DNS label", rec.Code)
	}
}

// waitJobsDrain polls the jobs registry until it's empty or
// timeout fires. Used by tests that cancel a job to ensure the
// goroutine has actually exited before the test returns.
func waitJobsDrain(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		jobs.mu.Lock()
		empty := len(jobs.jobs) == 0
		jobs.mu.Unlock()
		if empty {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Log("warning: jobs registry didn't drain within timeout (may indicate leaked goroutine)")
}
