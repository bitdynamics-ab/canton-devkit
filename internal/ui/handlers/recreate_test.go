package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/stream"
)

// restartMux mounts the instance handlers with a live hub — the
// restart flow needs the hub to publish progress events to a
// per-instance topic just like the create/resume flows.
func restartMux(t *testing.T) (*httptest.Server, *stream.Hub) {
	t.Helper()
	jobsReset()
	hub := stream.New()
	t.Cleanup(func() { hub.Close() })
	mux := http.NewServeMux()
	MountInstances(mux, hub)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, hub
}

// TestRestart_StoppedInstance202 — happy-path acceptance test.
// A registered instance accepts the restart POST and returns the
// async 202 + events_url shape so the frontend can subscribe.
//
// We deliberately don't assert on the goroutine reaching real
// docker work — RunDown / RunUp are exercised in their own
// packages. This test pins the HTTP contract.
func TestRestart_StoppedInstance202(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "pebble", "0.6.4",
		map[string]int{"app_user_ui": 44440}, registry.StatusStopped)
	srv, _ := restartMux(t)

	resp, err := http.Post(srv.URL+"/api/instances/pebble/recreate",
		"application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if h := resp.Header.Get("Content-Type"); h != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", h)
	}
	var body upAcceptedResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Instance != "pebble" {
		t.Errorf("instance = %q, want pebble", body.Instance)
	}
	if body.EventsURL != "/api/instances/pebble/events" {
		t.Errorf("events_url = %q, want /api/instances/pebble/events",
			body.EventsURL)
	}
}

// TestRestart_RunningInstance202 — restart accepts a running
// instance too (the whole point: down → up cycle). The resume
// endpoint refuses running with 409 because resume is one-shot
// (Start); restart is symmetric and applies to either state.
func TestRestart_RunningInstance202(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo", "0.6.4",
		map[string]int{"app_user_ui": 44440}, registry.StatusRunning)
	srv, _ := restartMux(t)

	resp, err := http.Post(srv.URL+"/api/instances/demo/recreate",
		"application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
}

// TestRestart_UnknownInstance404 — a name that's not in the
// registry can't be restarted. The handler must release the jobs
// slot before returning 404 so a later legitimate create of the
// same name isn't blocked by a stale "INSTANCE_CREATING" lock.
func TestRestart_UnknownInstance404(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv, _ := restartMux(t)

	resp, err := http.Post(srv.URL+"/api/instances/ghost/recreate",
		"application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	// Jobs slot must have been released — verify by checking
	// the package-level registry isn't holding the name.
	if jobs.Active("ghost") {
		t.Errorf("jobs.Active(ghost) = true after 404; jobs slot leaked")
	}
}

// TestRestart_InvalidName400 — same gate as every other instance
// endpoint. A malformed name must 400 before we touch jobs or
// registry.
func TestRestart_InvalidName400(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv, _ := restartMux(t)

	resp, err := http.Post(srv.URL+"/api/instances/UPPERCASE/recreate",
		"application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestRestart_AlreadyInFlight409 — a second restart POST while
// the first is still running must lose the jobs.Register race
// and surface as 409 INSTANCE_CREATING, NOT double-execute the
// down/up cycle. This is the idempotency contract from the
// completeness review.
func TestRestart_AlreadyInFlight409(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "busy", "0.6.4",
		map[string]int{"app_user_ui": 44440}, registry.StatusStopped)
	srv, _ := restartMux(t)

	// First POST claims the jobs slot. The goroutine will try to
	// reach docker and almost certainly fail, but that doesn't
	// matter for this test — what matters is the slot is held
	// while the goroutine runs.
	resp1, err := http.Post(srv.URL+"/api/instances/busy/recreate",
		"application/json", nil)
	if err != nil {
		t.Fatalf("first POST: %v", err)
	}
	_ = resp1.Body.Close()
	if resp1.StatusCode != http.StatusAccepted {
		t.Fatalf("first POST status = %d, want 202", resp1.StatusCode)
	}

	// Second POST must lose the race.
	resp2, err := http.Post(srv.URL+"/api/instances/busy/recreate",
		"application/json", nil)
	if err != nil {
		t.Fatalf("second POST: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("second POST status = %d, want 409", resp2.StatusCode)
	}
}

// TestProfilesFromComposeFiles — derivation helper used by the
// restart handler to re-apply the original profile set on the
// up-phase invocation.
func TestProfilesFromComposeFiles(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  []string
	}{
		{
			name:  "no overlays",
			files: []string{"/abs/path/docker-compose.yaml"},
			want:  nil,
		},
		{
			name: "observability overlay",
			files: []string{
				"/abs/path/docker-compose.yaml",
				"/abs/path/compose/observability.yaml",
			},
			want: []string{"observability"},
		},
		{
			name: "tokens-v2 overlay",
			files: []string{
				"/abs/path/docker-compose.yaml",
				"/abs/path/compose/tokens-v2.yaml",
			},
			want: []string{"tokens-v2"},
		},
		{
			name: "both overlays",
			files: []string{
				"/abs/path/docker-compose.yaml",
				"/abs/path/compose/observability.yaml",
				"/abs/path/compose/tokens-v2.yaml",
			},
			want: []string{"observability", "tokens-v2"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := profilesFromComposeFiles(tc.files)
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d (got=%v want=%v)",
					len(got), len(tc.want), got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
