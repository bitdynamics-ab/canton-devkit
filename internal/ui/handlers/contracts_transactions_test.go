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

// Yellow Y1 — error-path coverage for the Explorer handlers
// (handlers/contracts.go + handlers/transactions.go). These
// exercise every validation/lookup branch above the gRPC dial
// without needing a real Canton ledger.
//
// The full happy-path test requires a ledger.Client interface +
// fake — tracked as a follow-up (file a Linear ticket once this
// PR lands). What we cover here:
//
//   - 400 on invalid instance name
//   - 404 on unknown registered instance
//   - 400 on invalid role
//   - 503 PARTICIPANT_PORT_NOT_RECORDED when state.json lacks the
//     participant_ledger_<role> port (the BIT-190 capture didn't
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
	defer resp.Body.Close()
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
	defer resp.Body.Close()
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
	defer resp.Body.Close()
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
	// BIT-190 captured Canton ports.
	seedInstance(t, "demo", "0.6.4",
		map[string]int{"app_user_ui": 4485},
		registry.StatusRunning)
	srv := contractsTxMux(t)

	resp, err := http.Get(srv.URL + "/api/instances/demo/contracts?role=app-user")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
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
	defer resp.Body.Close()
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
	defer resp.Body.Close()
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
	defer resp.Body.Close()
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
	defer resp.Body.Close()
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
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	body := readErrBody(t, resp)
	if got := toStr(body["code"]); got != "PARTICIPANT_PORT_NOT_RECORDED" {
		t.Errorf("error code = %q, want PARTICIPANT_PORT_NOT_RECORDED", got)
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
