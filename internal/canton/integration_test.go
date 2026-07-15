//go:build integration

// Package canton's integration tests exercise the ledger gRPC client and
// the registry HTTP client against a real LocalNet brought up by
// `dpm localnet up dev` (or `canton-devkit localnet up dev`).
//
// Run with:
//
//	go test -tags=integration ./internal/canton/...
//
// The tests SKIP cleanly if the expected LocalNet isn't reachable, so a
// developer who hasn't started one doesn't see a confusing failure.
// CI runs them on a job that boots LocalNet first; default `go test` does
// NOT run them.
//
// What we verify (the "would-fail-without-fix" set):
//   - Ledger gRPC dial + auth + GetLedgerEnd round-trip against a real
//     participant. Catches dazl-client / Canton protobuf drift — if the
//     v2 wire shape changes incompatibly the test fails on response decode.
//   - ListKnownParties returns at least the LocalNet's known roles. Catches
//     admin-subpackage stub wiring regressions.
//   - Registry GET /api/scan/v0/dso returns a non-empty dso_party_id.
//     Catches Splice scan-app routing changes between Splice versions.
package canton

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/ledger"
	"github.com/bitdynamics-ab/canton-devkit/internal/canton/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// localnetParticipantEndpoint is the gRPC endpoint to hit. Overridable
// via env so a developer with non-default ports can still run the
// tests. The Splice docker-compose convention assigns participant
// ledger-API gRPC to container port 4901 (app-user participant) which
// the canton-devkit registry maps to a host-allocated ephemeral port.
//
// To discover the actual host port:
//
//	docker port <project>-canton 4901
//
// Then export it, e.g.:
//
//	export CANTON_DEVKIT_TEST_LEDGER_ENDPOINT=localhost:57894
func localnetParticipantEndpoint() string {
	if v := os.Getenv("CANTON_DEVKIT_TEST_LEDGER_ENDPOINT"); v != "" {
		return v
	}
	return "localhost:5001"
}

// localnetScanBaseURL is the Splice scan HTTP root.
//
// CRITICAL — the URL scheme/host matters for nginx routing on the SV
// tenant. The /api/scan/* path only matches when the request Host
// header is the instance-scoped `scan.<instance>.localhost` (see
// assets/nginx/sv.conf — there are multiple `server_name` blocks under
// one listen port; only the scan block proxies to the splice scan app,
// and DevKit serves only the instance-scoped name). This harness runs
// against `localnet up dev`, so the host is `scan.dev.localhost`.
// Hitting `http://localhost:<sv-port>/api/scan/v0/dso` returns the
// sv-html static index (200 with HTML body) because the default
// server_name block serves the SV UI assets.
//
// The Go http.Client uses the URL's host as the Host header by
// default, so the right form is:
//
//	export CANTON_DEVKIT_TEST_SCAN_URL=http://scan.dev.localhost:<sv-port>
//
// `scan.dev.localhost` resolves to 127.0.0.1 in Go's resolver. To find
// the SV UI host port on a running instance:
//
//	docker port <project>-nginx <SV_UI_PORT-from-compose-env>
//
// or simply read state.Ports["sv_ui"] from the canton-devkit registry.
func localnetScanBaseURL() string {
	if v := os.Getenv("CANTON_DEVKIT_TEST_SCAN_URL"); v != "" {
		return v
	}
	return "http://scan.dev.localhost:4000"
}

// devLocalNetTokenSource returns a TokenSource that signs JWTs with the
// LocalNet "unsafe" secret for the given role. This is the test-side
// equivalent of what RunUp's captureCredentials does, just inlined so
// the integration tests don't depend on a registry.State being present.
func devLocalNetTokenSource(t *testing.T, role splice.Role) ledger.TokenSource {
	t.Helper()
	// The audience/user names match splice's env/<role>-auth-on.env;
	// for app-user role they're "ledger-api-user" / "https://daml.com/jwt/aud/participant/...".
	// Since these vary by Splice release, we keep the values opaque
	// here — the test only needs *some* valid LocalNet JWT — and use
	// "ledger-api-user" + audience derived from the role name as a
	// well-known LocalNet convention.
	return ledger.TokenSourceFunc(func(context.Context) (string, error) {
		in := splice.CredentialInputs{
			Role:     role,
			User:     "ledger-api-user",
			Audience: "https://canton.network.global",
		}
		return splice.SignToken(in)
	})
}

// TestIntegration_LedgerEnd is the gRPC smoke. If this passes, the
// whole ledger/ package's wiring (dial + auth interceptor + decoder)
// works against a real Canton.
func TestIntegration_LedgerEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, err := ledger.Dial(ctx, ledger.DialOptions{
		Endpoint:  localnetParticipantEndpoint(),
		Token:     devLocalNetTokenSource(t, splice.RoleAppUser),
		PlainText: true, // LocalNet participant speaks plaintext gRPC
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	end, err := c.LedgerEnd(ctx)
	if err != nil {
		// Skip rather than fail if LocalNet isn't up: the test is
		// behind an integration build tag, but a developer who set
		// the tag and didn't start LocalNet should get a friendly
		// "no participant reachable, skipping" message, not a red
		// CI line.
		if strings.Contains(err.Error(), "connection refused") ||
			strings.Contains(err.Error(), "Unavailable") {
			t.Skipf("LocalNet not reachable at %s — run `localnet up dev` first: %v",
				localnetParticipantEndpoint(), err)
		}
		t.Fatalf("LedgerEnd: %v", err)
	}
	if end.Offset < 0 {
		t.Errorf("Offset = %d, want >= 0", end.Offset)
	}
}

// TestIntegration_ListKnownParties — admin-subpackage smoke. A
// well-formed `localnet up` instance always has at least 3 parties
// allocated (the per-role LocalNet identities). If this returns zero,
// either the auth header is wrong or the participant initialised
// without parties — both regressions worth catching.
func TestIntegration_ListKnownParties(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, err := ledger.Dial(ctx, ledger.DialOptions{
		Endpoint:  localnetParticipantEndpoint(),
		Token:     devLocalNetTokenSource(t, splice.RoleAppUser),
		PlainText: true, // LocalNet participant speaks plaintext gRPC
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	resp, err := c.ListKnownParties(ctx)
	if err != nil {
		if strings.Contains(err.Error(), "connection refused") {
			t.Skipf("LocalNet not reachable; run `localnet up dev`: %v", err)
		}
		t.Fatalf("ListKnownParties: %v", err)
	}
	if len(resp.GetPartyDetails()) == 0 {
		t.Errorf("expected at least 1 party on a real LocalNet, got 0 — auth or init regression")
	}
}

// TestIntegration_ScanDsoInfo — registry HTTP smoke. The scan app's DSO
// endpoint is the canonical "is Splice talking?" probe.
func TestIntegration_ScanDsoInfo(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, err := registry.Dial(registry.DialOptions{
		BaseURL: localnetScanBaseURL(),
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	dso, err := c.GetDsoInfo(ctx)
	if err != nil {
		if strings.Contains(err.Error(), "connection refused") {
			t.Skipf("Splice scan app not reachable at %s — run `localnet up dev`: %v",
				localnetScanBaseURL(), err)
		}
		t.Fatalf("GetDsoInfo: %v", err)
	}
	if dso.DsoParty == "" {
		t.Errorf("DsoParty is empty — scan app responded but DSO not initialised?")
	}
	if !strings.HasPrefix(dso.DsoParty, "DSO::") {
		t.Errorf("DsoParty = %q, want prefix DSO:: — Splice convention changed?", dso.DsoParty)
	}
}
