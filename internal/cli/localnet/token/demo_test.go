package token

import (
	"bytes"
	"errors"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/token"
)

// TestDemoCmd_RequiresInstance pins that the demo verb refuses to run
// without --instance (cobra required-flag guard).
func TestDemoCmd_RequiresInstance(t *testing.T) {
	cmd := buildDemo()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("want error when --instance is missing")
	}
}

// TestDemoCmd_NeedsV2WithoutEndpoint pins that with no discoverable V2
// endpoint the command surfaces ErrNeedsV2LocalNet (the "start a V2
// instance first" signal) rather than a half-provisioned token. The
// endpoint auto-discovery resolves to "" for an unregistered instance.
func TestDemoCmd_NeedsV2WithoutEndpoint(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	cmd := buildDemo()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetArgs([]string{"--instance", "nope"})
	err := cmd.Execute()
	if !errors.Is(err, token.ErrNeedsV2LocalNet) {
		t.Fatalf("want ErrNeedsV2LocalNet, got %v", err)
	}
}
