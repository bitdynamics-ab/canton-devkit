package token

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	localtoken "github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/spf13/cobra"
)

// seedInstanceWithLedgerPort writes a running instance whose app-user
// participant ledger port is captured, so resolveEndpoint (and `token ls`)
// resolve a live endpoint from --instance alone.
func seedInstanceWithLedgerPort(t *testing.T, name string, port int) {
	t.Helper()
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	s := registry.NewState(name, "0.6.4")
	s.ComposeProject = "canton-" + name
	s.DataDir = t.TempDir()
	s.ProjectDir = t.TempDir()
	s.Status = registry.StatusRunning
	s.Ports = map[string]int{"participant_ledger_app-user": port}
	if err := registry.Write(s); err != nil {
		t.Fatalf("seed %q: %v", name, err)
	}
}

// TestResolveEndpoint_ExplicitOverrideWins: an explicit --endpoint is
// returned verbatim and never re-resolved.
func TestResolveEndpoint_ExplicitOverrideWins(t *testing.T) {
	got, err := resolveEndpoint(&cobra.Command{}, "demo", "app-user", "myhost:6001")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "myhost:6001" {
		t.Errorf("explicit --endpoint must win, got %q", got)
	}
}

// TestResolveEndpoint_ResolvesFromInstance: an empty endpoint resolves the
// role's captured participant port.
func TestResolveEndpoint_ResolvesFromInstance(t *testing.T) {
	seedInstanceWithLedgerPort(t, "demo", 7501)
	got, err := resolveEndpoint(&cobra.Command{}, "demo", "app-user", "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "localhost:7501" {
		t.Errorf("want localhost:7501, got %q", got)
	}
}

// TestResolveEndpoint_MissingPortDiagnostic: no captured port prints the
// remediation on stderr and returns a (silent) error.
func TestResolveEndpoint_MissingPortDiagnostic(t *testing.T) {
	seedTokenInstance(t, "demo") // recorded token, no participant_ledger_* port
	var errBuf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&errBuf)
	got, err := resolveEndpoint(cmd, "demo", "app-user", "")
	if err == nil {
		t.Fatal("want an error when no ledger port is captured")
	}
	if got != "" {
		t.Errorf("want empty endpoint on failure, got %q", got)
	}
	if !strings.Contains(errBuf.String(), "no captured ledger port") {
		t.Errorf("want the missing-port diagnostic on stderr, got: %q", errBuf.String())
	}
}

// TestTokenLs_OfflineFallbackToRecorded: with no live ledger, `token ls`
// falls back to the recorded instrument list (the shared decision).
func TestTokenLs_OfflineFallbackToRecorded(t *testing.T) {
	seedTokenInstance(t, "demo") // recorded RTK, no ledger port
	cmd := buildList()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--instance", "demo"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("token ls: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "RTK") || !strings.Contains(s, "DECLARED") {
		t.Errorf("want the recorded RTK table with a DECLARED column, got:\n%s", s)
	}
}

// TestTokenLs_OfflineJSONUsesSharedResponse: JSON output decodes into the
// shared types.TokenListResponse; the offline path populates Tokens, not
// Instruments (mutually-exclusive keys).
func TestTokenLs_OfflineJSONUsesSharedResponse(t *testing.T) {
	seedTokenInstance(t, "demo")
	cmd := buildList()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--instance", "demo", "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("token ls --json: %v", err)
	}
	var resp types.TokenListResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decode shared response: %v\n%s", err, out.String())
	}
	if resp.Instruments != nil {
		t.Errorf("offline path must not set instruments, got %+v", resp.Instruments)
	}
	if resp.Tokens == nil || len(*resp.Tokens) != 1 || (*resp.Tokens)[0].Symbol != "RTK" {
		t.Errorf("want one recorded RTK token, got %+v", resp.Tokens)
	}
}

func TestTokenLs_EmptyOfflineJSONKeepsTokensArray(t *testing.T) {
	seedInstanceWithLedgerPort(t, "empty", 0)
	cmd := buildList()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--instance", "empty", "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("token ls --json: %v", err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, out.String())
	}
	if got := string(body["tokens"]); got != "[]" {
		t.Errorf("offline empty list must emit tokens:[], got %q in %s", got, out.String())
	}
	if _, ok := body["instruments"]; ok {
		t.Errorf("offline response must omit instruments: %s", out.String())
	}
}

// TestAllocationWithdraw_ResolvesEndpoint: withdraw now resolves the
// endpoint like the list path (was passing an empty endpoint → always
// ErrNeedsV2LocalNet even on a live instance). With no captured port it
// surfaces the shared missing-port diagnostic.
func TestAllocationWithdraw_ResolvesEndpoint(t *testing.T) {
	seedTokenInstance(t, "demo") // no ledger port
	cmd := buildAllocations()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"withdraw", "--instance", "demo", "--allocation", "abc"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("want an error when no ledger port is captured")
	}
	if !strings.Contains(errBuf.String(), "no captured ledger port") {
		t.Errorf("withdraw must resolve the endpoint and surface the missing-port diagnostic, got: %q", errBuf.String())
	}
}

func TestAllocationAction_PassesResolvedEndpointToRunner(t *testing.T) {
	seedInstanceWithLedgerPort(t, "demo", 7501)
	var got localtoken.AllocationActionOptions
	cmd := buildAllocationAction("withdraw", "test", func(_ context.Context, _ io.Writer, opts localtoken.AllocationActionOptions) error {
		got = opts
		return nil
	})
	cmd.SetArgs([]string{"--instance", "demo", "--allocation", "abc"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if got.Endpoint != "localhost:7501" {
		t.Errorf("runner endpoint = %q, want localhost:7501", got.Endpoint)
	}
}

func TestPartyList_AttemptsBestEffortEndpointResolution(t *testing.T) {
	seedInstanceWithLedgerPort(t, "demo", 0)
	prev := resolveLedgerEndpointFn
	called := false
	resolveLedgerEndpointFn = func(instance, role string) string {
		called = instance == "demo" && role == "app-user"
		return ""
	}
	t.Cleanup(func() { resolveLedgerEndpointFn = prev })

	cmd := buildPartyList()
	cmd.SetOut(io.Discard)
	cmd.SetArgs([]string{"--instance", "demo"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("party ls: %v", err)
	}
	if !called {
		t.Error("party ls did not attempt best-effort endpoint resolution")
	}
}
