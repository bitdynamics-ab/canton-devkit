package localnet

import (
	"bytes"
	"context"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// TestDown_ForceFlagExposed pins that `localnet down` offers --force.
func TestDown_ForceFlagExposed(t *testing.T) {
	if buildDown().Flags().Lookup("force") == nil {
		t.Fatal("`localnet down` is missing the --force flag")
	}
}

// TestDown_ForceReachesStopper: a forced down still drives the teardown
// (the seam is shared; production swaps in the label-only force stopper).
func TestDown_ForceReachesStopper(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	seedDownInstance(t, "demo", registry.StatusRunning)

	called := false
	installFakeStopper(t, func(context.Context, *registry.State) error {
		called = true
		return nil
	})

	var out, errBuf bytes.Buffer
	code := RunDown(context.Background(), &out, &errBuf, DownOptions{Name: "demo", Force: true})
	if code != localnet.ExitSuccess {
		t.Fatalf("code = %d, want ExitSuccess; stderr=%q", code, errBuf.String())
	}
	if !called {
		t.Error("forced down did not reach the stopper")
	}
}
