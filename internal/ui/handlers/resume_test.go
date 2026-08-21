package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/stream"
)

// resumeMux mounts the instance handlers (including the new
// POST /api/instances/{name}/up). The hub IS required — the resume
// flow registers a buffer + spawns the same goroutine as create.
func resumeMux(t *testing.T) (*httptest.Server, *stream.Hub) {
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

// TestResume_StoppedInstance202 — the happy path the user-reported
// bug needs: instance is stopped (registry says so, containers are
// gone), and POST /up brings it back up. The recording stub also pins
// the no-silent-upgrade rule: resume reuses the recorded version.
func TestResume_StoppedInstance202(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "pebble", "0.6.4",
		map[string]int{"app_user_ui": 44440}, registry.StatusStopped)

	upOpts := make(chan *localnet.UpOptions, 1)
	origUp := runUp
	t.Cleanup(func() { runUp = origUp })
	runUp = func(_ context.Context, _ localnet.Progress, opts *localnet.UpOptions) int {
		upOpts <- opts
		return localnet.ExitSuccess
	}

	srv, _ := resumeMux(t)

	resp, err := http.Post(srv.URL+"/api/instances/pebble/up",
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

	select {
	case opts := <-upOpts:
		if opts.Name != "pebble" {
			t.Errorf("bring-up name = %q, want pebble", opts.Name)
		}
		if opts.Version != "0.6.4" {
			t.Errorf("bring-up version = %q, want 0.6.4", opts.Version)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resume never reached bring-up")
	}
}

// TestResume_UnknownInstance404 — a name that's not in the registry
// can't be "resumed". This catches typos and prevents the handler
// from quietly spawning an up-from-scratch for a name the user
// didn't explicitly create.
func TestResume_UnknownInstance404(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv, _ := resumeMux(t)

	resp, err := http.Post(srv.URL+"/api/instances/ghost/up",
		"application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestResume_AlreadyRunning409 — if the registry says running, the
// resume endpoint refuses. The user should be using /down to
// restart, not the resume verb. The 409 makes the distinction
// surface in the UI as an actionable error instead of silently
// duplicating work.
func TestResume_AlreadyRunning409(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo", "0.6.4",
		map[string]int{"app_user_ui": 44440}, registry.StatusRunning)
	srv, _ := resumeMux(t)

	resp, err := http.Post(srv.URL+"/api/instances/demo/up",
		"application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

// TestResume_InvalidName400 — same gate as every other instance
// endpoint. A malformed name (path-traversal, illegal chars) must
// 400 before we touch the registry.
func TestResume_InvalidName400(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv, _ := resumeMux(t)

	resp, err := http.Post(srv.URL+"/api/instances/UPPERCASE/up",
		"application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}
