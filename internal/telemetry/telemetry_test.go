package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func sandbox(t *testing.T) {
	t.Helper()
	t.Setenv("CANTON_DEVKIT_TELEMETRY_DIR", t.TempDir())
	// Clear inherited disables so tests control state explicitly.
	t.Setenv(envDisable, "")
	t.Setenv(envDoNotTrack, "")
	t.Setenv(envEndpoint, "")
}

func TestEnabledByDefault_OptOut(t *testing.T) {
	sandbox(t)
	if !Enabled() {
		t.Fatal("telemetry should be ON by default (opt-out)")
	}
}

func TestSetEnabled_Persists(t *testing.T) {
	sandbox(t)
	if err := SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if Enabled() {
		t.Error("expected disabled after SetEnabled(false)")
	}
	if err := SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if !Enabled() {
		t.Error("expected enabled after SetEnabled(true)")
	}
}

func TestEnvOverrides_ForceDisable(t *testing.T) {
	sandbox(t)
	t.Setenv(envDisable, "0")
	if Enabled() {
		t.Error("CANTON_DEVKIT_TELEMETRY=0 must disable")
	}
	t.Setenv(envDisable, "")
	t.Setenv(envDoNotTrack, "1")
	if Enabled() {
		t.Error("DO_NOT_TRACK=1 must disable")
	}
}

func TestRecordCommand_AppendsZeroPII(t *testing.T) {
	sandbox(t)
	RecordCommand("1.2.3", "localnet token mint", 0, 1500*time.Millisecond)
	events, err := RecentEvents()
	if err != nil || len(events) != 1 {
		t.Fatalf("want 1 spooled event, got %d (%v)", len(events), err)
	}
	e := events[0]
	if e.Command != "localnet token mint" || e.ExitCode != 0 || e.DurationBucket != "500ms-2s" {
		t.Errorf("event fields wrong: %+v", e)
	}
	if e.InstallID == "" || e.ToolVersion != "1.2.3" {
		t.Errorf("missing install id / version: %+v", e)
	}
	// Zero-PII guard: serialize and assert none of the forbidden tokens
	// appear (the struct simply has no field for them).
	b, _ := json.Marshal(e)
	for _, bad := range []string{"--name", "::", "/Users/", "party", "jwt", "token=", "instance_name"} {
		if strings.Contains(strings.ToLower(string(b)), strings.ToLower(bad)) {
			t.Errorf("event leaked %q: %s", bad, b)
		}
	}
}

func TestRecordCommand_NoOpWhenDisabled(t *testing.T) {
	sandbox(t)
	_ = SetEnabled(false)
	RecordCommand("1.0.0", "localnet up", 0, time.Second)
	if events, _ := RecentEvents(); len(events) != 0 {
		t.Errorf("disabled telemetry must not spool, got %d", len(events))
	}
}

func TestFlush_PostsBatchAndClears(t *testing.T) {
	sandbox(t)
	var mu sync.Mutex
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv(envEndpoint, srv.URL)

	RecordCommand("1.0.0", "localnet up", 0, time.Second)
	RecordCommand("1.0.0", "localnet down", 0, time.Second)
	if err := Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	mu.Lock()
	evs, _ := got["events"].([]any)
	mu.Unlock()
	if len(evs) != 2 {
		t.Errorf("collector received %d events, want 2", len(evs))
	}
	if left, _ := RecentEvents(); len(left) != 0 {
		t.Errorf("spool should be cleared after a 2xx flush, %d left", len(left))
	}
}

func TestFlush_NoEndpoint_RetainsSpool(t *testing.T) {
	sandbox(t) // endpoint cleared
	RecordCommand("1.0.0", "localnet up", 0, time.Second)
	if err := Flush(context.Background()); err != nil {
		t.Fatalf("flush with no endpoint should be a no-op, got %v", err)
	}
	if left, _ := RecentEvents(); len(left) != 1 {
		t.Errorf("spool must be retained when no endpoint, got %d", len(left))
	}
}

func TestFirstRunNotice_ShownOnce(t *testing.T) {
	sandbox(t)
	var b strings.Builder
	MaybeFirstRunNotice(&b)
	if !strings.Contains(b.String(), "anonymous") {
		t.Error("first-run notice not printed")
	}
	var b2 strings.Builder
	MaybeFirstRunNotice(&b2)
	if b2.Len() != 0 {
		t.Error("notice should print only once")
	}
}
