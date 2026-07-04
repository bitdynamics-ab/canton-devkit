package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/stream"
)

// stopStartMux mounts the instance handlers with a hub (required for
// the /start bring-up fallback path).
func stopStartMux(t *testing.T) *httptest.Server {
	t.Helper()
	jobsReset()
	hub := stream.New()
	t.Cleanup(func() { hub.Close() })
	mux := http.NewServeMux()
	MountInstances(mux, hub)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestStop_InvalidName400(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv := stopStartMux(t)
	resp, err := http.Post(srv.URL+"/api/instances/UPPER/stop", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestStop_UnknownInstance400(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv := stopStartMux(t)
	// RunStop surfaces an unregistered instance as ExitUserError → 400.
	resp, err := http.Post(srv.URL+"/api/instances/ghost/stop", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestStop_PausedInstance400(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "frozen", "0.6.4",
		map[string]int{"app_user_ui": 44440}, registry.StatusPaused)
	srv := stopStartMux(t)
	resp, err := http.Post(srv.URL+"/api/instances/frozen/stop", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestStart_UnknownInstance404(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv := stopStartMux(t)
	resp, err := http.Post(srv.URL+"/api/instances/ghost/start", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestStart_AlreadyRunning204(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo", "0.6.4",
		map[string]int{"app_user_ui": 44440}, registry.StatusRunning)
	srv := stopStartMux(t)
	resp, err := http.Post(srv.URL+"/api/instances/demo/start", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
}

func TestStart_Paused409(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "frozen", "0.6.4",
		map[string]int{"app_user_ui": 44440}, registry.StatusPaused)
	srv := stopStartMux(t)
	resp, err := http.Post(srv.URL+"/api/instances/frozen/start", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

// TestStart_StoppedNoContainers202 — a stopped instance whose compose
// project no longer has any containers (containers.List returns empty,
// no error, when the project is absent) falls back to a full bring-up,
// which is async: 202 + events_url.
func TestStart_StoppedNoContainers202(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "pebble", "0.6.4",
		map[string]int{"app_user_ui": 44440}, registry.StatusStopped)
	srv := stopStartMux(t)
	resp, err := http.Post(srv.URL+"/api/instances/pebble/start", "application/json", nil)
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
}

// TestStartStub503WithoutHub — without a hub the /start route is
// stubbed 503 (the bring-up fallback needs SSE).
func TestStartStub503WithoutHub(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	mux := http.NewServeMux()
	MountInstances(mux, nil)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	resp, err := http.Post(srv.URL+"/api/instances/demo/start", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}
