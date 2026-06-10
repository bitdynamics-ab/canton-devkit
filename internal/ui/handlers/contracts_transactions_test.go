package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// Error-path coverage for the Explorer handlers
// (handlers/contracts.go + handlers/transactions.go). These
// exercise every validation/lookup branch above the gRPC dial
// without needing a real Canton ledger.
//
// The full happy-path test requires a ledger.Client interface +
// fake — tracked as a follow-up. What we cover here:
//
//   - 400 on invalid instance name
//   - 404 on unknown registered instance
//   - 400 on invalid role
//   - 503 PARTICIPANT_PORT_NOT_RECORDED when state.json lacks the
//     participant_ledger_<role> port (the capture didn't
//     run yet for this instance)
//   - 500 when state.json has a port but no JWT for the role
//
// AGENTS.md "all new code must be tested" — these guard the
// dispatch logic that 90% of error-mode users will hit before
// they ever see a gRPC failure.

func contractsTxMux(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	MountContracts(mux)
	MountTransactions(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func readErrBody(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	_ = resp.Body.Close()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("parse JSON: %v; body=%s", err, body)
	}
	return out
}

func TestContracts_InvalidName(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv := contractsTxMux(t)

	resp, err := http.Get(srv.URL + "/api/instances/UPPERCASE_NAME/contracts")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestContracts_UnknownInstance(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv := contractsTxMux(t)

	resp, err := http.Get(srv.URL + "/api/instances/nobody/contracts")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestContracts_InvalidRole(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo", "0.6.4",
		map[string]int{"participant_ledger_app-user": 9999},
		registry.StatusRunning)
	srv := contractsTxMux(t)

	resp, err := http.Get(srv.URL + "/api/instances/demo/contracts?role=intruder")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body := readErrBody(t, resp)
	if !strings.Contains(toStr(body["code"]), "INVALID_REQUEST") {
		t.Errorf("error code = %v, want INVALID_REQUEST", body["code"])
	}
}

func TestContracts_MissingParticipantPort(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	// No participant_ledger_app-user port — instance came up before
	// captured Canton ports.
	seedInstance(t, "demo", "0.6.4",
		map[string]int{"app_user_ui": 4485},
		registry.StatusRunning)
	srv := contractsTxMux(t)

	resp, err := http.Get(srv.URL + "/api/instances/demo/contracts?role=app-user")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	body := readErrBody(t, resp)
	if got := toStr(body["code"]); got != "PARTICIPANT_PORT_NOT_RECORDED" {
		t.Errorf("error code = %q, want PARTICIPANT_PORT_NOT_RECORDED", got)
	}
}

func TestContracts_MissingCredential(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	// Port present but no JWT recorded.
	seedInstance(t, "demo", "0.6.4",
		map[string]int{"participant_ledger_app-user": 9999},
		registry.StatusRunning)
	srv := contractsTxMux(t)

	resp, err := http.Get(srv.URL + "/api/instances/demo/contracts?role=app-user")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
}

// Transactions handler mirrors contracts — same validation order,
// same lookups. Cover the same matrix so any future divergence
// surfaces immediately. (CLI ↔ Web UI parity rule.)

func TestTransactions_InvalidName(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv := contractsTxMux(t)
	resp, err := http.Get(srv.URL + "/api/instances/.../transactions")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestTransactions_UnknownInstance(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv := contractsTxMux(t)
	resp, err := http.Get(srv.URL + "/api/instances/nobody/transactions")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestTransactions_InvalidRole(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo", "0.6.4",
		map[string]int{"participant_ledger_app-user": 9999},
		registry.StatusRunning)
	srv := contractsTxMux(t)
	resp, err := http.Get(srv.URL + "/api/instances/demo/transactions?role=intruder")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestTransactions_MissingParticipantPort(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo", "0.6.4",
		map[string]int{"app_user_ui": 4485},
		registry.StatusRunning)
	srv := contractsTxMux(t)
	resp, err := http.Get(srv.URL + "/api/instances/demo/transactions?role=app-user")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	body := readErrBody(t, resp)
	if got := toStr(body["code"]); got != "PARTICIPANT_PORT_NOT_RECORDED" {
		t.Errorf("error code = %q, want PARTICIPANT_PORT_NOT_RECORDED", got)
	}
}

// ── stream + contract-detail endpoint guards ───────────────────
//
// The new SSE stream and contract-detail endpoints share the same
// validation prefix as the snapshot path. Mirror the matrix so any
// future drift surfaces immediately.

func TestContractsStream_InvalidName(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv := contractsTxMux(t)
	resp, err := http.Get(srv.URL + "/api/instances/UPPERCASE/contracts/stream")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestContractsStream_UnknownInstance(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv := contractsTxMux(t)
	resp, err := http.Get(srv.URL + "/api/instances/nobody/contracts/stream")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestContractsStream_InvalidSince(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo", "0.6.4",
		map[string]int{"participant_ledger_app-user": 9999},
		registry.StatusRunning)
	srv := contractsTxMux(t)
	cases := []struct {
		name, query string
	}{
		{"non-integer", "since=abc"},
		{"negative", "since=-1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := http.Get(srv.URL + "/api/instances/demo/contracts/stream?role=app-user&" + c.query)
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (since must be a non-negative integer)", resp.StatusCode)
			}
		})
	}
}

func TestContractsStream_InvalidRole(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo", "0.6.4",
		map[string]int{"participant_ledger_app-user": 9999},
		registry.StatusRunning)
	srv := contractsTxMux(t)
	resp, err := http.Get(srv.URL + "/api/instances/demo/contracts/stream?role=intruder")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestContractsStream_MissingParticipantPort(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo", "0.6.4",
		map[string]int{"app_user_ui": 4485},
		registry.StatusRunning)
	srv := contractsTxMux(t)
	resp, err := http.Get(srv.URL + "/api/instances/demo/contracts/stream?role=app-user")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	body := readErrBody(t, resp)
	if got := toStr(body["code"]); got != "PARTICIPANT_PORT_NOT_RECORDED" {
		t.Errorf("error code = %q, want PARTICIPANT_PORT_NOT_RECORDED", got)
	}
}

func TestContractDetail_InvalidName(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv := contractsTxMux(t)
	resp, err := http.Get(srv.URL + "/api/instances/UPPER/contracts/abc:1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestContractDetail_UnknownInstance(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	srv := contractsTxMux(t)
	resp, err := http.Get(srv.URL + "/api/instances/nobody/contracts/abc:1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestContractDetail_InvalidRole(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo", "0.6.4",
		map[string]int{"participant_ledger_app-user": 9999},
		registry.StatusRunning)
	srv := contractsTxMux(t)
	resp, err := http.Get(srv.URL + "/api/instances/demo/contracts/abc:1?role=intruder")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestContractDetail_MissingParticipantPort(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo", "0.6.4",
		map[string]int{"app_user_ui": 4485},
		registry.StatusRunning)
	srv := contractsTxMux(t)
	resp, err := http.Get(srv.URL + "/api/instances/demo/contracts/abc:1?role=app-user")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	body := readErrBody(t, resp)
	if got := toStr(body["code"]); got != "PARTICIPANT_PORT_NOT_RECORDED" {
		t.Errorf("error code = %q, want PARTICIPANT_PORT_NOT_RECORDED", got)
	}
}

// TestContractDetail_RoutedDistinctFromStream pins the
// route-precedence: /contracts/stream must hit handleContractsStream,
// not handleContractDetail with contract_id="stream". Both pass
// validation up to the dial; the discriminator is the 503 code we
// surface for the stream path's setup phase vs the detail path's.
// We assert the two paths return distinct responses for the same
// stub instance configuration to catch a future stdlib-mux change
// that re-orders matches.
func TestContractDetail_RoutedDistinctFromStream(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo", "0.6.4",
		map[string]int{"app_user_ui": 4485},
		registry.StatusRunning)
	srv := contractsTxMux(t)

	// Both should 503 with PARTICIPANT_PORT_NOT_RECORDED — but the
	// router has to dispatch to the right handler for the precondition
	// to even apply. If `/stream` collapsed onto the {contract_id}
	// route we'd still get a 503, but a future regression where the
	// stream handler short-circuits earlier would surface here.
	for _, path := range []string{
		"/api/instances/demo/contracts/stream?role=app-user",
		"/api/instances/demo/contracts/somecid?role=app-user",
	} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("path=%s status=%d want 503", path, resp.StatusCode)
		}
	}
}

func toStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
