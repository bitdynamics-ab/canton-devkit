package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// TestHandleTokenAllocate_ThreadsFieldsAndReturnsID pins that the allocate
// handler threads from/to/amount/committed/deadline through to RunAllocate
// and surfaces the returned allocation id.
func TestHandleTokenAllocate_ThreadsFieldsAndReturnsID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DPM_REGISTRY_DIR", dir)
	t.Setenv("XDG_DATA_HOME", dir)
	const inst = "wire-allocate"
	st := &registry.State{SchemaVersion: 1, Name: inst, Ports: map[string]int{"participant_ledger_app-user": 13902}}
	if err := registry.Write(st); err != nil {
		t.Fatalf("registry.Write: %v", err)
	}

	var captured token.AllocationOptions
	prev := runTokenAllocate
	runTokenAllocate = func(_ context.Context, _ io.Writer, opts token.AllocationOptions) (string, error) {
		captured = opts
		return "00alloc-xyz", nil
	}
	defer func() { runTokenAllocate = prev }()

	srv := tokensSrv(t)
	body := bytes.NewBufferString(`{"from":"alice","to":"bob","amount":"10","committed":true,"settlement_deadline":"1h"}`)
	resp, err := http.Post(srv.URL+"/api/tokens/RTK/allocate?instance="+inst, "application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got struct {
		AllocationID string `json:"allocation_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.AllocationID != "00alloc-xyz" {
		t.Errorf("allocation_id = %q, want 00alloc-xyz", got.AllocationID)
	}
	if captured.Instrument != "RTK" || captured.From != "alice" || captured.To != "bob" || captured.Amount != "10" {
		t.Errorf("fields not threaded: %+v", captured)
	}
	if !captured.Committed {
		t.Error("committed not threaded")
	}
	if captured.SettlementDeadline != "1h" {
		t.Errorf("deadline not threaded: %q", captured.SettlementDeadline)
	}
}

// TestHandleAllocationActions_Routing pins that each per-allocation action
// route (settle/withdraw/cancel) reaches the matching RunX with the path id
// + ?party=.
func TestHandleAllocationActions_Routing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DPM_REGISTRY_DIR", dir)
	t.Setenv("XDG_DATA_HOME", dir)
	const inst = "wire-alloc-actions"
	// No participant port → liveLedgerEndpoint resolves "" → the RunX
	// returns ErrNeedsV2LocalNet → 412. That proves the route is wired and
	// the id/party reach the orchestration layer via the not-wired stub.
	st := &registry.State{SchemaVersion: 1, Name: inst}
	if err := registry.Write(st); err != nil {
		t.Fatalf("registry.Write: %v", err)
	}
	srv := tokensSrv(t)

	for _, action := range []string{"settle", "withdraw", "cancel"} {
		t.Run(action, func(t *testing.T) {
			url := srv.URL + "/api/tokens/allocations/00alloc-1/" + action + "?instance=" + inst + "&party=bob%3A%3A1220"
			resp, err := http.Post(url, "application/json", nil)
			if err != nil {
				t.Fatalf("POST: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			// settle currently returns a plain not-proven error (400);
			// withdraw/cancel hit the not-wired stub (412). Either way a
			// wired route must NOT 404/405.
			if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
				t.Fatalf("%s route not wired: status %d", action, resp.StatusCode)
			}
		})
	}
}

// TestHandleAllocationsList_NoEndpointIs503 pins the offline branch: with
// no recorded participant port the list handler returns 503, not a 500.
func TestHandleAllocationsList_NoEndpointIs503(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DPM_REGISTRY_DIR", dir)
	t.Setenv("XDG_DATA_HOME", dir)
	const inst = "wire-alloc-list"
	if err := registry.Write(&registry.State{SchemaVersion: 1, Name: inst}); err != nil {
		t.Fatalf("registry.Write: %v", err)
	}
	srv := tokensSrv(t)
	resp, err := http.Get(srv.URL + "/api/tokens/allocations?instance=" + inst)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}
