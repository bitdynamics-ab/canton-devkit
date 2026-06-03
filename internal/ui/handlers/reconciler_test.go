package handlers

import (
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
