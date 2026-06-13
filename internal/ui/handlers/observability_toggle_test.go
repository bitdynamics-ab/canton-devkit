package handlers

import (
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
