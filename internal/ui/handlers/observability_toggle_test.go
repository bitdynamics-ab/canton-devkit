package handlers

import (
	"errors"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
)

// TestObservabilityRequestResolveTargets covers the JSON-body shape
// the per-component toggle accepts. The matrix:
//   - per-component flags set independently
//   - legacy `enabled` synonym expands to both
//   - per-component flags WIN when both are sent (so a client can
//     migrate field-by-field without ambiguity)
//   - empty body is rejected by the handler (ok=false here)
func TestObservabilityRequestResolveTargets(t *testing.T) {
	tt := true
	ff := false
	cases := []struct {
		name     string
		req      observabilityToggleRequest
		wantProm bool
		wantGraf bool
		wantOk   bool
	}{
		{"empty", observabilityToggleRequest{}, false, false, false},
		{"prometheus only true", observabilityToggleRequest{Prometheus: &tt}, true, false, true},
		{"grafana only true", observabilityToggleRequest{Grafana: &tt}, false, true, true},
		{"both per-component", observabilityToggleRequest{Prometheus: &tt, Grafana: &tt}, true, true, true},
		{"legacy enabled true", observabilityToggleRequest{Enabled: &tt}, true, true, true},
		{"legacy enabled false", observabilityToggleRequest{Enabled: &ff}, false, false, true},
		{"per-component overrides legacy", observabilityToggleRequest{Enabled: &tt, Prometheus: &ff}, false, true, true},
		{"only one component flipped via legacy", observabilityToggleRequest{Enabled: &ff, Grafana: &tt}, false, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, g, ok := tc.req.resolveTargets()
			if p != tc.wantProm || g != tc.wantGraf || ok != tc.wantOk {
				t.Errorf("resolveTargets() = (%v,%v,%v); want (%v,%v,%v)",
					p, g, ok, tc.wantProm, tc.wantGraf, tc.wantOk)
			}
		})
	}
}

// TestFailedObservabilityServiceNamesComponent guards the
// OBSERVABILITY_TOGGLE_FAIL remediation hint: it must name the sidecar
// that actually failed. Before the fix the hint hardcoded
// `<project>-prometheus` even when only Grafana failed; the component is
// now derived from the wrapped SetObservability error.
func TestFailedObservabilityServiceNamesComponent(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"grafana-only failure", errors.New("enable grafana: docker compose up: exit status 1"), "grafana"},
		{"disable grafana failure", errors.New("disable grafana: docker compose stop: exit status 1"), "grafana"},
		{"prometheus failure", errors.New("enable prometheus: docker compose up: exit status 1"), "prometheus"},
		{"non-component error falls back", errors.New("persist observability toggle: disk full"), "prometheus"},
		{"nil error falls back", nil, "prometheus"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := failedObservabilityService(tc.err); got != tc.want {
				t.Errorf("failedObservabilityService(%v) = %q; want %q", tc.err, got, tc.want)
			}
		})
	}

	// The hint string the handler builds must carry the resolved
	// component so an operator tails the right container.
	grafErr := errors.New("enable grafana: docker compose up: exit status 1")
	hint := "docker logs proj-" + failedObservabilityService(grafErr)
	if !strings.Contains(hint, "-grafana") {
		t.Errorf("grafana-only remediation hint = %q; want it to name -grafana", hint)
	}
}

// TestAllowedProfilesIncludesPerComponent guards the HTTP allowlist:
// `prometheus` and `grafana` must be accepted as standalone
// `--profile` strings on POST /api/instances so the Create modal can
// flip each side independently (CLI ↔ UI parity).
func TestAllowedProfilesIncludesPerComponent(t *testing.T) {
	for _, p := range []string{
		localnet.PrometheusProfileName,
		localnet.GrafanaProfileName,
	} {
		if !allowedProfiles[p] {
			t.Errorf("profile %q must be accepted by the HTTP surface", p)
		}
	}
}
