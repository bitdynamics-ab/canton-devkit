package telemetry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestUploadPeriod_RetriesWithoutInstallIDOn400 covers the
// collector-rollout safety net: a collector predating install_id rejects
// the unknown field with 400 (DisallowUnknownFields). uploadPeriod must
// retry ONCE without the token so the counters still land — losing only
// the unique-install dedup, not the counts.
func TestUploadPeriod_RetriesWithoutInstallIDOn400(t *testing.T) {
	sandbox(t)
	t.Setenv("CI", "") // non-CI so installID() mints a token

	var mu sync.Mutex
	var sawToken, sawNoToken bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, hasID := body["install_id"]
		mu.Lock()
		if hasID {
			sawToken = true
		} else {
			sawNoToken = true
		}
		mu.Unlock()
		if hasID {
			w.WriteHeader(http.StatusBadRequest) // old collector behaviour
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	agg := &PeriodAggregate{
		SchemaVersion: SchemaVersion, Period: "2000-01-01", Granularity: "daily",
		Counters: map[string]map[string]int{"dpm/command": {"up": 1}},
	}
	if err := uploadPeriod(srv.URL, agg); err != nil {
		t.Fatalf("uploadPeriod should succeed via the no-token retry, got %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawToken {
		t.Error("first attempt should have carried install_id")
	}
	if !sawNoToken {
		t.Error("fallback attempt without install_id never happened")
	}
}

// TestUploadPeriod_No400RetryLoopInCI verifies that with no token (CI),
// a 400 surfaces as an error and is NOT retried — the fallback is gated on
// a token having been sent.
func TestUploadPeriod_No400RetryLoopInCI(t *testing.T) {
	sandbox(t)
	t.Setenv("CI", "true") // installID() returns "" → no token in the body

	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	agg := &PeriodAggregate{
		SchemaVersion: SchemaVersion, Period: "2000-01-01", Granularity: "daily",
		Counters: map[string]map[string]int{"dpm/command": {"up": 1}},
	}
	if err := uploadPeriod(srv.URL, agg); err == nil {
		t.Fatal("expected a 400 to surface as an error when no token was sent")
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("token-less 400 retried %d times, want exactly 1 attempt", calls)
	}
}
