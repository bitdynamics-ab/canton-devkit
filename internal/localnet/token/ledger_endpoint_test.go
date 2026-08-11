package token

import (
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

func TestResolveLedgerEndpointRequiresRunningInstance(t *testing.T) {
	tests := []struct {
		name   string
		status registry.Status
		want   string
	}{
		{name: "running", status: registry.StatusRunning, want: "localhost:42002"},
		{name: "stopped", status: registry.StatusStopped},
		{name: "paused", status: registry.StatusPaused},
		{name: "failed", status: registry.StatusFailed},
		{name: "creating", status: registry.StatusCreating},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
			s := registry.NewState("demo", "0.6.12")
			s.Status = tt.status
			s.Ports["participant_ledger_"+DefaultRole] = 42002
			if err := registry.Write(s); err != nil {
				t.Fatalf("seed registry: %v", err)
			}

			if got := ResolveLedgerEndpoint("demo", ""); got != tt.want {
				t.Fatalf("ResolveLedgerEndpoint() = %q, want %q", got, tt.want)
			}
		})
	}
}
