package handlers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/stream"
)

func serveRemoveHandler(t *testing.T, runRemove runRemoveFunc) *httptest.Server {
	t.Helper()
	hub := stream.New()
	t.Cleanup(hub.Close)
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/instances/{name}", handleRemoveInstanceWithRunner(hub, runRemove))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// DELETE must use the shared localnet removal service. RunRemove's own tests
// pin that this path invokes compose down with removeVolumes=true before it
// deletes registry state.
func TestRemoveInstance_DelegatesToLocalnetRemove(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "old-test", "0.6.12", nil, registry.StatusStopped)

	called := false
	srv := serveRemoveHandler(t, func(_ context.Context, _, _ io.Writer, opts *localnet.RemoveOptions) int {
		called = true
		if opts.Name != "old-test" {
			t.Errorf("remove name = %q, want old-test", opts.Name)
		}
		if opts.Force {
			t.Error("Web UI remove must not force-delete a running instance")
		}
		return localnet.ExitSuccess
	})

	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/instances/old-test", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if !called {
		t.Fatal("DELETE did not delegate to localnet.RunRemove")
	}
}

func TestRemoveInstance_RunningInstanceRefusedBeforeRemoval(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "live-test", "0.6.12", nil, registry.StatusRunning)

	srv := serveRemoveHandler(t, func(context.Context, io.Writer, io.Writer, *localnet.RemoveOptions) int {
		t.Fatal("remove service must not be called for a running instance")
		return localnet.ExitRuntimeFailure
	})

	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/instances/live-test", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	if _, err := registry.Read("live-test"); err != nil {
		t.Fatalf("running instance state was changed: %v", err)
	}
}
