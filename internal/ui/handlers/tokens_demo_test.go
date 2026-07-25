package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// TestTokens_DemoProvisionsLiveToken pins that POST /api/tokens/demo
// resolves the live endpoint from the instance's captured port, threads
// instance/role through to RunDemo, and returns the DemoResult.
func TestTokens_DemoProvisionsLiveToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DPM_REGISTRY_DIR", dir)
	t.Setenv("XDG_DATA_HOME", dir)
	const inst = "demo-wire"
	st := &registry.State{
		SchemaVersion: 1,
		Name:          inst,
		Ports:         map[string]int{"participant_ledger_app-user": 13902},
	}
	if err := registry.Write(st); err != nil {
		t.Fatalf("registry.Write: %v", err)
	}

	var captured token.DemoOptions
	prev := runTokenDemo
	runTokenDemo = func(_ context.Context, _ io.Writer, opts token.DemoOptions) (*token.DemoResult, error) {
		captured = opts
		return &token.DemoResult{
			Token:  registry.TokenRef{Symbol: "DEMO", Status: "on-ledger"},
			Issuer: registry.PartyRef{Alias: "demo-issuer", PartyID: "iss::pid"},
			Holder: &registry.PartyRef{Alias: "demo-holder", PartyID: "hld::pid"},
			Seeded: true,
		}, nil
	}
	defer func() { runTokenDemo = prev }()

	srv := tokensSrv(t)
	resp, err := http.Post(srv.URL+"/api/tokens/demo?instance="+inst, "application/json",
		bytes.NewBufferString(`{"symbol":"DEMO"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	if captured.Instance != inst {
		t.Errorf("opts not threaded through: %+v", captured)
	}
	if captured.Endpoint == "" {
		t.Errorf("endpoint not resolved from the captured participant port")
	}
	var out token.DemoResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Token.Symbol != "DEMO" || out.Holder == nil || !out.Seeded {
		t.Errorf("result body wrong: %+v", out)
	}
}

// TestTokens_DemoNeedsV2Is412 pins that without a live V2 endpoint the
// demo returns 412 NEEDS_V2_LOCALNET — the signal the UI uses to disable
// the "Launch demo token" button with a "start a V2 instance" hint —
// rather than producing an un-mintable stub. Exercises the real RunDemo
// guard (no runTokenDemo override); seedForTokens pins the resolver to "".
func TestTokens_DemoNeedsV2Is412(t *testing.T) {
	seedForTokens(t, "demo")
	srv := tokensSrv(t)
	resp, err := http.Post(srv.URL+"/api/tokens/demo?instance=demo", "application/json",
		strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412", resp.StatusCode)
	}
	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(resp.Body)
	if !strings.Contains(body.String(), "NEEDS_V2_LOCALNET") {
		t.Errorf("body should carry NEEDS_V2_LOCALNET code; got %s", body.String())
	}
}
