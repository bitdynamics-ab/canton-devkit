package handlers

import (
	"net/http"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// TestHandlePendingTransfersList_NoEndpointIs503 pins the offline branch:
// with no recorded participant port the list handler returns 503 (not 500),
// and — since the GET is wired — never 404/405.
func TestHandlePendingTransfersList_NoEndpointIs503(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DPM_REGISTRY_DIR", dir)
	t.Setenv("XDG_DATA_HOME", dir)
	const inst = "wire-transfers-list"
	if err := registry.Write(&registry.State{SchemaVersion: 1, Name: inst}); err != nil {
		t.Fatalf("registry.Write: %v", err)
	}
	srv := tokensSrv(t)
	resp, err := http.Get(srv.URL + "/api/tokens/transfers?instance=" + inst)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		t.Fatalf("transfers-list route not wired: status %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}
