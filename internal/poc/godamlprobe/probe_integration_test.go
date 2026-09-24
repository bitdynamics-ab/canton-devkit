//go:build integration

package godamlprobe_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/poc/godamlprobe"
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

// TestProbe_AgainstLocalNet exercises go-daml against a running LocalNet
// participant. Skips cleanly when the endpoint is not reachable so
// developers without a booted instance are not blocked.
//
//	go test -tags=integration ./internal/poc/godamlprobe/...
//
// Override the endpoint with CANTON_DEVKIT_TEST_LEDGER_ENDPOINT when the
// registry-mapped host port is not localhost:5001.
func TestProbe_AgainstLocalNet(t *testing.T) {
	endpoint := os.Getenv("CANTON_DEVKIT_TEST_LEDGER_ENDPOINT")
	if endpoint == "" {
		endpoint = "localhost:5001"
	}

	token, err := splice.SignToken(splice.CredentialInputs{
		Role:     splice.RoleAppProvider,
		User:     "ledger-api-user",
		Audience: "https://canton.network.global",
	})
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := godamlprobe.Probe(ctx, godamlprobe.Options{
		Endpoint: endpoint,
		Token:    token,
	})
	if err != nil {
		if strings.Contains(err.Error(), "connection refused") ||
			strings.Contains(err.Error(), "Unavailable") ||
			strings.Contains(err.Error(), "dial") {
			t.Skipf("LocalNet not reachable at %s — run `localnet up` first: %v", endpoint, err)
		}
		t.Fatalf("Probe: %v", err)
	}

	if res.Endpoint != endpoint {
		t.Errorf("Endpoint = %q, want %q", res.Endpoint, endpoint)
	}
	if res.LedgerAPIVersion == "" {
		t.Error("LedgerAPIVersion is empty; expected a non-empty participant version string")
	}
	if res.Offset < 0 {
		t.Errorf("Offset = %d, want >= 0", res.Offset)
	}
}
