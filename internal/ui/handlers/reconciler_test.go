package handlers

import (
	"context"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// TestEvalStatus pins the BIT-177 decision table. Each row reads
// like a contract: "given the docker compose ps snapshot, what
// status should the dashboard show?" Failures here would silently
// re-introduce the bug the reconciler exists to fix.
func TestEvalStatus(t *testing.T) {
	healthy := ContainerHealth{State: "running", Health: "healthy"}
	noHealthcheckRunning := ContainerHealth{State: "running"} // nginx, swagger-ui style
	starting := ContainerHealth{State: "running", Health: "starting"}
	unhealthy := ContainerHealth{State: "running", Health: "unhealthy"}
	restarting := ContainerHealth{State: "restarting"}
	exited := ContainerHealth{State: "exited"}

	cases := []struct {
		name       string
		cached     registry.Status
		containers []ContainerHealth
		want       registry.Status
	}{
		{
			name:   "all healthy → running (the BIT-177 happy path)",
			cached: registry.StatusFailed,
			containers: []ContainerHealth{
				healthy, healthy, healthy,
			},
			want: registry.StatusRunning,
		},
		{
			name:       "no-healthcheck container is treated as healthy when running",
			cached:     registry.StatusFailed,
			containers: []ContainerHealth{healthy, noHealthcheckRunning, healthy},
			want:       registry.StatusRunning,
		},
		{
			name:       "any container restarting → partial",
			cached:     registry.StatusRunning,
			containers: []ContainerHealth{healthy, restarting, healthy},
			want:       registry.StatusPartial,
		},
		{
			name:       "any container exited → partial",
			cached:     registry.StatusRunning,
			containers: []ContainerHealth{healthy, exited},
			want:       registry.StatusPartial,
		},
		{
			name:       "any container unhealthy → partial",
			cached:     registry.StatusRunning,
			containers: []ContainerHealth{healthy, unhealthy},
			want:       registry.StatusPartial,
		},
		{
			name:       "mixed healthy + starting (no bad) → partial",
			cached:     registry.StatusFailed,
			containers: []ContainerHealth{healthy, starting},
			want:       registry.StatusPartial,
		},
		{
			name:       "no containers + cached running → stopped (compose down ran elsewhere)",
			cached:     registry.StatusRunning,
			containers: nil,
			want:       registry.StatusStopped,
		},
		{
			name:       "no containers + cached failed → unchanged",
			cached:     registry.StatusFailed,
			containers: nil,
			want:       registry.StatusFailed,
		},
		{
			name:       "no containers + cached stopped → unchanged",
			cached:     registry.StatusStopped,
			containers: nil,
			want:       registry.StatusStopped,
		},
		{
			name:       "no containers + cached partial → failed (nothing to be partial about)",
			cached:     registry.StatusPartial,
			containers: nil,
			want:       registry.StatusFailed,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := evalStatus(c.cached, c.containers)
			if got != c.want {
				t.Errorf("evalStatus(%v, %d containers) = %v; want %v",
					c.cached, len(c.containers), got, c.want)
			}
		})
	}
}

// TestRefreshCantonPorts pins the BIT-221 fix: when canton has been
// recreated and ephemeral host ports drifted, the reconciler must
// rewrite state.Ports with the live capture, regardless of whether
// the high-level status changed.
//
// The motivating bug: v2-canton restarts (clean exit 0 → docker
// recreates → new ports) while the reconciler still sees the same
// "all-healthy" snapshot before AND after. Status doesn't flip;
// without this fix Explorer / DAR / contracts 502 against the closed
// old port for the entire 15s reconcile interval (or forever if the
// container never goes unhealthy in between).
func TestRefreshCantonPorts(t *testing.T) {
	t.Run("stale canton ports overwritten with live capture", func(t *testing.T) {
		state := &registry.State{
			ComposeProject: "canton-v2",
			Ports: map[string]int{
				"participant_admin_app-user":      58953, // stale
				"participant_ledger_app-user":     58955, // stale
				"participant_json_app-user":       58956,
				"app_user_ui":                     65320, // NOT a canton port — must not be touched
				"participant_admin_app-provider":  58954,
				"participant_ledger_app-provider": 58952,
				"participant_json_app-provider":   58957,
				"participant_admin_sv":            58950,
				"participant_ledger_sv":           58949,
				"participant_json_sv":             58951,
			},
		}

		origCapture := captureCantonPorts
		t.Cleanup(func() { captureCantonPorts = origCapture })
		captureCantonPorts = func(_ context.Context, project string) map[string]int {
			if project != "canton-v2" {
				t.Errorf("captureCantonPorts got project %q, want canton-v2", project)
			}
			return map[string]int{
				"participant_admin_app-user":      61252,
				"participant_ledger_app-user":     61247,
				"participant_json_app-user":       61246,
				"participant_admin_app-provider":  61253,
				"participant_ledger_app-provider": 61248,
				"participant_json_app-provider":   61249,
				"participant_admin_sv":            61251,
				"participant_ledger_sv":           61244,
				"participant_json_sv":             61245,
			}
		}

		changed := refreshCantonPorts(context.Background(), state, "v2")
		if !changed {
			t.Fatal("refreshCantonPorts returned false on a drifted port set")
		}
		if state.Ports["participant_admin_app-user"] != 61252 {
			t.Errorf("admin app-user = %d, want 61252", state.Ports["participant_admin_app-user"])
		}
		if state.Ports["participant_ledger_app-user"] != 61247 {
			t.Errorf("ledger app-user = %d, want 61247", state.Ports["participant_ledger_app-user"])
		}
		if state.Ports["app_user_ui"] != 65320 {
			t.Errorf("app_user_ui = %d, want 65320 (must not be touched)", state.Ports["app_user_ui"])
		}
	})

	t.Run("no diff returns false", func(t *testing.T) {
		state := &registry.State{
			ComposeProject: "canton-v2",
			Ports: map[string]int{
				"participant_admin_app-user":  61252,
				"participant_ledger_app-user": 61247,
			},
		}
		origCapture := captureCantonPorts
		t.Cleanup(func() { captureCantonPorts = origCapture })
		captureCantonPorts = func(context.Context, string) map[string]int {
			return map[string]int{
				"participant_admin_app-user":  61252,
				"participant_ledger_app-user": 61247,
			}
		}
		if refreshCantonPorts(context.Background(), state, "v2") {
			t.Error("refreshCantonPorts returned true when nothing diffed")
		}
	})

	t.Run("probe failure (empty map) leaves cached ports alone", func(t *testing.T) {
		state := &registry.State{
			ComposeProject: "canton-v2",
			Ports: map[string]int{
				"participant_admin_app-user": 99999,
			},
		}
		origCapture := captureCantonPorts
		t.Cleanup(func() { captureCantonPorts = origCapture })
		captureCantonPorts = func(context.Context, string) map[string]int { return nil }
		if refreshCantonPorts(context.Background(), state, "v2") {
			t.Error("refreshCantonPorts returned true on empty live capture")
		}
		if state.Ports["participant_admin_app-user"] != 99999 {
			t.Errorf("port was clobbered to %d on probe failure", state.Ports["participant_admin_app-user"])
		}
	})

	t.Run("partial probe keeps cached for missing keys", func(t *testing.T) {
		state := &registry.State{
			ComposeProject: "canton-v2",
			Ports: map[string]int{
				"participant_admin_app-user":  58953,
				"participant_ledger_app-user": 58955,
			},
		}
		origCapture := captureCantonPorts
		t.Cleanup(func() { captureCantonPorts = origCapture })
		captureCantonPorts = func(context.Context, string) map[string]int {
			// Only admin probe succeeded — the contract from
			// canton_ports.go is "missing key = I don't know,
			// leave cached value alone."
			return map[string]int{"participant_admin_app-user": 61252}
		}
		if !refreshCantonPorts(context.Background(), state, "v2") {
			t.Fatal("admin drifted but refresh returned false")
		}
		if state.Ports["participant_admin_app-user"] != 61252 {
			t.Errorf("admin = %d, want 61252", state.Ports["participant_admin_app-user"])
		}
		if state.Ports["participant_ledger_app-user"] != 58955 {
			t.Errorf("ledger clobbered to %d; want cached 58955 to survive", state.Ports["participant_ledger_app-user"])
		}
	})
}
