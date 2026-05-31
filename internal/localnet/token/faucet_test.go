package token

import (
	"context"
	"errors"
	"testing"
)

// TestRunFaucet_RequiresFields — validation precedes any ledger dial.
func TestRunFaucet_RequiresFields(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	// Missing amount.
	err := RunFaucet(context.Background(), nil, FaucetOptions{
		Instance: "demo", Instrument: "RTK", To: "bob",
	})
	if err == nil {
		t.Fatal("want a validation error for missing amount")
	}
}

// TestRunFaucet_DefaultsSourceAndThreadsToTransfer — with no source and
// no endpoint, RunFaucet should default the source (so the From field is
// populated) and reach RunTransfer, which returns ErrNeedsV2LocalNet for
// the no-endpoint case. A missing-From validation error instead would
// mean the source default didn't apply.
func TestRunFaucet_DefaultsSourceAndThreadsToTransfer(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedInstance(t, "demo")
	err := RunFaucet(context.Background(), nil, FaucetOptions{
		Instance: "demo", Instrument: "RTK", To: "bob", Amount: "5", Role: "app-user",
		// Source intentionally empty, Endpoint empty.
	})
	if !errors.Is(err, ErrNeedsV2LocalNet) {
		t.Fatalf("want ErrNeedsV2LocalNet (source defaulted, threaded to transfer), got %v", err)
	}
}
