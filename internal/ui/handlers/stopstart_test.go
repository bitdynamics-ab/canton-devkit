package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/containers"
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

// Without stubbing, the bring-up branch really boots Canton, and the
// containers it leaves behind push every later run down the fast-start
// branch instead.
func stubStartPath(t *testing.T, existing []containers.Info) chan *localnet.UpOptions {
	t.Helper()
	upOpts := make(chan *localnet.UpOptions, 1)
	origList, origUp, origStart := listContainers, runUp, runStart
	t.Cleanup(func() { listContainers, runUp, runStart = origList, origUp, origStart })

	listContainers = func(context.Context, string) ([]containers.Info, error) {
		return existing, nil
	}
	runUp = func(_ context.Context, _ localnet.Progress, opts *localnet.UpOptions) int {
		upOpts <- opts
		return localnet.ExitSuccess
	}
	runStart = func(context.Context, localnet.Progress, io.Writer, io.Writer, *localnet.StartOptions) int {
		t.Error("fast-start ran; with no containers the handler must bring up instead")
		return localnet.ExitSuccess
	}
	return upOpts
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
	state, err := registry.Read("pebble")
	if err != nil {
		t.Fatalf("read seeded state: %v", err)
	}
	state.Profiles = []string{"sv", "app-provider", "app-user", "swagger-ui", "prometheus"}
	if err := registry.Write(state); err != nil {
		t.Fatalf("write seeded profiles: %v", err)
	}
	upOpts := stubStartPath(t, nil)
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
	var body struct {
		Instance  string `json:"instance"`
		EventsURL string `json:"events_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.EventsURL != "/api/instances/pebble/events" {
		t.Errorf("events_url = %q", body.EventsURL)
	}

	select {
	case opts := <-upOpts:
		if opts.Name != "pebble" || opts.Version != "0.6.4" {
			t.Errorf("bring-up opts = %q/%q, want pebble/0.6.4", opts.Name, opts.Version)
		}
		if len(opts.Profiles) != 5 {
			t.Errorf("bring-up profiles = %v, want the 5 seeded", opts.Profiles)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bring-up never ran")
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
