package localnet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExpandObservabilityProfiles pins the mapping from the user-
// facing `--profile` strings to the (prometheus, grafana) booleans
// the bring-up path branches on. The umbrella `observability` MUST
// keep activating both — that's the legacy behaviour we preserve for
// existing scripts, docs, and registry state.
func TestExpandObservabilityProfiles(t *testing.T) {
	cases := []struct {
		name     string
		in       []string
		wantProm bool
		wantGraf bool
	}{
		{"empty", nil, false, false},
		{"prometheus only", []string{"prometheus"}, true, false},
		{"grafana only", []string{"grafana"}, false, true},
		{"both per-component", []string{"prometheus", "grafana"}, true, true},
		{"legacy umbrella", []string{"observability"}, true, true},
		{"umbrella + override is still both", []string{"observability", "prometheus"}, true, true},
		{"unknown profile is ignored", []string{"tokens-v2"}, false, false},
		{"mixed with unknown", []string{"tokens-v2", "grafana"}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotProm, gotGraf := ExpandObservabilityProfiles(tc.in)
			if gotProm != tc.wantProm || gotGraf != tc.wantGraf {
				t.Errorf("ExpandObservabilityProfiles(%v) = (%v,%v); want (%v,%v)",
					tc.in, gotProm, gotGraf, tc.wantProm, tc.wantGraf)
			}
		})
	}
}

// TestObservabilityProfileConstants pins the compose profile name
// strings. Changing them is a user-visible breakage (CLI flags +
// registered state) so the test exists to make the breakage loud.
func TestObservabilityProfileConstants(t *testing.T) {
	if ObservabilityProfileName != "observability" {
		t.Errorf("ObservabilityProfileName = %q; legacy users rely on this exact string", ObservabilityProfileName)
	}
	if PrometheusProfileName != "prometheus" {
		t.Errorf("PrometheusProfileName = %q; want %q", PrometheusProfileName, "prometheus")
	}
	if GrafanaProfileName != "grafana" {
		t.Errorf("GrafanaProfileName = %q; want %q", GrafanaProfileName, "grafana")
	}
}

func TestMaterializeObservabilityOverlay_RewritesVolumeMountsAbsolute(t *testing.T) {
	dataDir := t.TempDir()
	projectDir := t.TempDir()
	path, err := MaterializeObservabilityOverlay(dataDir, projectDir)
	if err != nil {
		t.Fatalf("MaterializeObservabilityOverlay: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	root := filepath.Join(dataDir, "observability")
	for _, want := range []string{
		filepath.ToSlash(filepath.Join(root, "compose", "prometheus.yml")) + ":/etc/prometheus/prometheus.yml:ro",
		filepath.ToSlash(filepath.Join(root, "grafana", "provisioning")) + ":/etc/grafana/provisioning:ro",
		filepath.ToSlash(filepath.Join(root, "grafana", "dashboards")) + ":/var/lib/grafana/dashboards:ro",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("materialized overlay missing absolute mount %q\n---\n%s\n---", want, s)
		}
	}
	for _, stale := range []string{"./prometheus.yml", "../grafana/provisioning", "../grafana/dashboards"} {
		if strings.Contains(s, stale) {
			t.Errorf("materialized overlay still has relative mount %q\n---\n%s\n---", stale, s)
		}
	}
}
