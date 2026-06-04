package telemetry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
)

// TestFlushNow_ShipsCurrentAndPast verifies FlushNow uploads EVERY period
// file — including the current, still-open period (the differentiator
// from tryUpload, which skips the current period) — and removes each on
// success.
func TestFlushNow_ShipsCurrentAndPast(t *testing.T) {
	sandbox(t)
	var mu sync.Mutex
	got := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		if p, ok := body["period"].(string); ok {
			got[p] = true
		}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv(envEndpoint, srv.URL)

	// One completed past period + the current (open) period.
	past := &PeriodAggregate{SchemaVersion: SchemaVersion, Period: "2000-01-01", Granularity: "daily",
		Counters: map[string]map[string]int{"dpm/command": {"up": 1}}}
	if err := savePeriod(past); err != nil {
		t.Fatal(err)
	}
	cur := currentPeriod()
	curAgg := &PeriodAggregate{SchemaVersion: SchemaVersion, Period: cur, Granularity: "daily",
		Counters: map[string]map[string]int{"dpm/command": {"status": 2}}}
	if err := savePeriod(curAgg); err != nil {
		t.Fatal(err)
	}

	n, err := FlushNow()
	if err != nil {
		t.Fatalf("FlushNow: %v", err)
	}
	if n != 2 {
		t.Errorf("flushed %d periods, want 2 (past + current)", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if !got["2000-01-01"] {
		t.Error("past period was not shipped")
	}
	if !got[cur] {
		t.Error("current (open) period was not shipped — FlushNow must include it")
	}
	// Both files removed after success.
	if _, err := os.Stat(periodFilePath("2000-01-01")); !os.IsNotExist(err) {
		t.Error("past file not removed after flush")
	}
	if _, err := os.Stat(periodFilePath(cur)); !os.IsNotExist(err) {
		t.Error("current file not removed after flush")
	}
}

// TestFlushNow_NoCollector returns ErrNoCollector so the CLI can advise
// the user instead of silently no-op'ing.
func TestFlushNow_NoCollector(t *testing.T) {
	sandbox(t)
	t.Setenv(envEndpoint, "") // explicitly no endpoint
	endpoint = ""             // and no baked default
	if _, err := FlushNow(); err != ErrNoCollector {
		t.Errorf("FlushNow with no endpoint = %v, want ErrNoCollector", err)
	}
}
