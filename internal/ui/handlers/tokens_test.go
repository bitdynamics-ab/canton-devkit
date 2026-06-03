package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// tokensSrv mounts MountTokens on a fresh mux and returns an
// httptest.Server. Test wide enough to exercise Go 1.22's pattern
// matching (path values), which raw HandlerFunc invocation skips.
func tokensSrv(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	MountTokens(mux, nil)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func seedForTokens(t *testing.T, name string) {
	t.Helper()
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	s := registry.NewState(name, "0.6.4")
	s.ComposeProject = "canton-" + name
	s.DataDir = t.TempDir()
	s.ProjectDir = t.TempDir()
	s.Status = registry.StatusRunning
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// TestTokens_CreateThenListAndDetail walks the read+write path end-
// to-end: POST create → GET list contains the new ref → GET
// /{symbol} returns the same body. Pins the cross-handler contract.
func TestTokens_CreateThenListAndDetail(t *testing.T) {
	seedForTokens(t, "demo")
	srv := tokensSrv(t)

	body := strings.NewReader(`{
	  "name": "Retail Token",
	  "symbol": "RTK",
	  "decimals": 6,
	  "initial_supply": "1000000",
	  "issuer": "alice::abc"
	}`)
	resp, err := http.Post(srv.URL+"/api/tokens?instance=demo", "application/json", body)
	if err != nil {
		t.Fatalf("POST create: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// GET list
	resp, err = http.Get(srv.URL + "/api/tokens?instance=demo")
	if err != nil {
		t.Fatalf("GET list: %v", err)
	}
	var list struct {
		SchemaVersion int                 `json:"schema_version"`
		Tokens        []registry.TokenRef `json:"tokens"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	_ = resp.Body.Close()
	if len(list.Tokens) != 1 || list.Tokens[0].Symbol != "RTK" {
		t.Errorf("list tokens drift: %+v", list)
	}

	// GET detail
	resp, err = http.Get(srv.URL + "/api/tokens/RTK?instance=demo")
	if err != nil {
		t.Fatalf("GET detail: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", resp.StatusCode)
	}
}

// TestTokens_CreateDuplicateIsConflict pins the symbol-collision
// mapping: ErrSymbolInUse → 409. The frontend uses this code to
// surface a focused "pick a different symbol" message.
func TestTokens_CreateDuplicateIsConflict(t *testing.T) {
	seedForTokens(t, "demo")
	srv := tokensSrv(t)
	body := `{"name":"x","symbol":"RTK","decimals":6,"initial_supply":"1","issuer":"a"}`
	resp, _ := http.Post(srv.URL+"/api/tokens?instance=demo", "application/json", strings.NewReader(body))
	_ = resp.Body.Close()

	resp, err := http.Post(srv.URL+"/api/tokens?instance=demo", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST dup: %v", err)
	}
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("dup status = %d, want 409", resp.StatusCode)
	}
}

// TestTokens_MintNeedsV2 pins the 412 Precondition Failed mapping for
// ErrNeedsV2LocalNet — the frontend uses this to render a "bring up
// the V2 LocalNet" remediation pill rather than a generic 500.
func TestTokens_MintNeedsV2(t *testing.T) {
	seedForTokens(t, "demo")
	srv := tokensSrv(t)
	// Create first so the symbol resolves.
	create := `{"name":"x","symbol":"RTK","decimals":6,"initial_supply":"1","issuer":"a"}`
	cr, _ := http.Post(srv.URL+"/api/tokens?instance=demo", "application/json", strings.NewReader(create))
	_ = cr.Body.Close()

	mint := `{"to":"bob","amount":"5"}`
	resp, err := http.Post(srv.URL+"/api/tokens/RTK/mint?instance=demo", "application/json", strings.NewReader(mint))
	if err != nil {
		t.Fatalf("POST mint: %v", err)
	}
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Errorf("mint status = %d, want 412 (needs V2 LocalNet)", resp.StatusCode)
	}
	out := new(bytes.Buffer)
	_, _ = out.ReadFrom(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(out.String(), "NEEDS_V2_LOCALNET") {
		t.Errorf("body should carry NEEDS_V2_LOCALNET code; got %s", out.String())
	}
}

// TestTokens_MissingInstanceIs400 covers the shared
// instanceFromQuery guard.
func TestTokens_MissingInstanceIs400(t *testing.T) {
	srv := tokensSrv(t)
	resp, err := http.Get(srv.URL + "/api/tokens")
	if err != nil {
		t.Fatalf("GET no instance: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestMapTokenError_DefaultIs400WithCause pins main's policy: a
// non-gRPC orchestration error is user-actionable (bad amount,
// unknown party, malformed instrument id), so it surfaces as 400
// with the cause string — `writeError`'s 5xx-only redaction would
// hide context the user needs. gRPC paths (Unavailable, Internal,
// InvalidArgument) are exercised in their dedicated mint tests
// where sanitize400 masks party-id fingerprints on the 400 branch.
func TestMapTokenError_DefaultIs400WithCause(t *testing.T) {
	cause := errors.New("amount must be a non-negative decimal")
	rec := httptest.NewRecorder()
	mapTokenError(rec, cause, "mint")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), ErrCodeInvalidRequest) {
		t.Errorf("body should carry INVALID_REQUEST code; got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "non-negative decimal") {
		t.Errorf("body should carry the cause string; got %s", rec.Body.String())
	}
}

// TestMapTokenError_InvalidArgumentSanitizesPartyID pins main's
// sanitize400: a gRPC InvalidArgument whose message embeds a fully-
// qualified party id has the fingerprint masked before reaching the
// client. The readable hint stays so the error remains actionable.
func TestMapTokenError_InvalidArgumentSanitizesPartyID(t *testing.T) {
	st := status.New(codes.InvalidArgument, "party alice::abc123def456 not authorized")
	rec := httptest.NewRecorder()
	mapTokenError(rec, st.Err(), "mint")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "abc123def456") {
		t.Errorf("body leaks party-id fingerprint: %s", body)
	}
	if !strings.Contains(body, "alice::") {
		t.Errorf("body should keep readable hint: %s", body)
	}
}

// TestMapTokenError_NeedsV2Maps412 pins that the V2-needed sentinel
// still produces the dedicated remediation code even after A1's
// default-branch tightening.
func TestMapTokenError_NeedsV2Maps412(t *testing.T) {
	rec := httptest.NewRecorder()
	mapTokenError(rec, token.ErrNeedsV2LocalNet, "balance")
	if rec.Code != http.StatusPreconditionFailed {
		t.Errorf("status = %d, want 412", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "NEEDS_V2_LOCALNET") {
		t.Errorf("body should carry NEEDS_V2_LOCALNET code; got %s", rec.Body.String())
	}
}
