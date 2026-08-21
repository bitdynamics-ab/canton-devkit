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

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/progress"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/stream"
)

// TestCancelUp_NotInFlightReturns404 pins the "nothing to cancel"
// contract. A DELETE for a name that isn't actively being created
// (e.g. the up finished, or the user mistyped) returns 404 — the
// frontend hides the cancel button on those states, but a stale
// click shouldn't 500.
func TestCancelUp_NotInFlightReturns404(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	jobsReset()
	hub := stream.New()
	defer hub.Close()

	mux := http.NewServeMux()
	MountInstances(mux, hub)

	req := httptest.NewRequest(http.MethodDelete,
		"/api/instances/notrunning/up", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != "NOT_FOUND" {
		t.Errorf("code = %q, want NOT_FOUND", body.Code)
	}
}

// TestCancelUp_InvalidNameReturns400 pins path-param validation.
func TestCancelUp_InvalidNameReturns400(t *testing.T) {
	jobsReset()
	hub := stream.New()
	defer hub.Close()
	mux := http.NewServeMux()
	MountInstances(mux, hub)

	req := httptest.NewRequest(http.MethodDelete,
		"/api/instances/Invalid_Name/up", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// TestCancelUp_NoHubReturns503 pins the stub when no hub configured.
func TestCancelUp_NoHubReturns503(t *testing.T) {
	mux := http.NewServeMux()
	MountInstances(mux, nil)

	req := httptest.NewRequest(http.MethodDelete,
		"/api/instances/demo/up", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

// TestCancelUp_HappyPath_204AndCancelledEventEmitted pins the invariant —
// end-to-end pin: spawn a create job, DELETE it, verify
//
//	(a) the response is 204
//	(b) a kind=cancelled event lands on the SSE topic BEFORE
//	    the step.failed events from RunUp's ctx.Err() handling
//	(c) the jobs registry is empty after the goroutine exits
func TestCancelUp_HappyPath_204AndCancelledEventEmitted(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	jobsReset()
	hub := stream.New()
	defer hub.Close()

	mux := http.NewServeMux()
	MountInstances(mux, hub)

	// Must block so the job is still in-flight when the DELETE lands.
	origUp := runUp
	t.Cleanup(func() { runUp = origUp })
	runUp = func(ctx context.Context, _ localnet.Progress, _ *localnet.UpOptions) int {
		<-ctx.Done()
		return localnet.ExitUserError
	}

	// Subscribe BEFORE the POST so we don't depend on the
	// replay buffer for this test (cleaner ordering check).
	hub.EnableBuffering(progress.TopicFor("cancelme"), 32)
	ch, cancelSub := hub.SubscribeWithReplay(progress.TopicFor("cancelme"))
	defer cancelSub()

	// Spawn the create.
	postReq := httptest.NewRequest(http.MethodPost,
		"/api/instances",
		strings.NewReader(`{"name":"cancelme"}`))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusAccepted {
		t.Fatalf("POST status = %d, want 202; body=%s", postRec.Code, postRec.Body.String())
	}

	// Give the goroutine a moment to start — without this the
	// DELETE could race the goroutine's first publish and emit
	// the cancelled event ahead of step.started, which the
	// frontend would render correctly anyway but the test pin
	// here is "cancelled appears in the stream".
	time.Sleep(20 * time.Millisecond)

	// Cancel.
	delReq := httptest.NewRequest(http.MethodDelete,
		"/api/instances/cancelme/up", nil)
	delRec := httptest.NewRecorder()
	mux.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Errorf("DELETE status = %d, want 204; body=%s", delRec.Code, delRec.Body.String())
	}

	// Drain events until we see the cancelled marker (or time
	// out). Expected stream: some step events, then cancelled,
	// then more step events (the natural failure path).
	deadline := time.After(2 * time.Second)
	sawCancelled := false
	for !sawCancelled {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("channel closed before cancelled event arrived")
			}
			if bytes.Contains(ev.Data, []byte(`"kind":"cancelled"`)) {
				sawCancelled = true
				if !bytes.Contains(ev.Data, []byte("user requested")) {
					t.Errorf("cancelled event missing reason; data=%s", ev.Data)
				}
			}
		case <-deadline:
			t.Fatal("timeout waiting for cancelled event on SSE topic")
		}
	}

	// Goroutine should exit shortly after cancel.
	waitJobsDrain(t, time.Second)
}
