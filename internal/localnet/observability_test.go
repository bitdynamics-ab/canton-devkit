package localnet

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

func TestReadObservabilityState(t *testing.T) {
	cases := []struct {
		name     string
		ports    map[string]int
		wantProm bool
		wantGraf bool
		wantPP   int
		wantGP   int
	}{
		{"both off", map[string]int{}, false, false, 0, 0},
		{"prom only", map[string]int{"prometheus_ui": 19090}, true, false, 19090, 0},
		{"graf only", map[string]int{"grafana_ui": 13000}, false, true, 0, 13000},
		{"both on", map[string]int{"prometheus_ui": 19090, "grafana_ui": 13000}, true, true, 19090, 13000},
		{"zero port treated as off", map[string]int{"prometheus_ui": 0}, false, false, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ReadObservabilityState(&registry.State{Ports: tc.ports})
			if got.Prometheus != tc.wantProm || got.Grafana != tc.wantGraf ||
				got.PrometheusPort != tc.wantPP || got.GrafanaPort != tc.wantGP {
				t.Errorf("ReadObservabilityState = %+v, want prom=%v graf=%v pp=%d gp=%d",
					got, tc.wantProm, tc.wantGraf, tc.wantPP, tc.wantGP)
			}
		})
	}
	// Nil-safe.
	if s := ReadObservabilityState(nil); s.Prometheus || s.Grafana {
		t.Errorf("ReadObservabilityState(nil) = %+v, want zero", s)
	}
}

func TestSyncObservabilityProfiles(t *testing.T) {
	sortStrings := func(s []string) []string {
		out := append([]string(nil), s...)
		sort.Strings(out)
		return out
	}
	cases := []struct {
		name     string
		existing []string
		prom     bool
		graf     bool
		want     []string
	}{
		{"enable both from empty", nil, true, true,
			[]string{GrafanaProfileName, PrometheusProfileName}},
		{"disable both", []string{ObservabilityProfileName}, false, false, nil},
		{"umbrella collapses to prometheus when grafana disabled",
			[]string{ObservabilityProfileName}, true, false,
			[]string{PrometheusProfileName}},
		{"preserves non-observability profile (tokens-v2)",
			[]string{TokensV2ProfileName, ObservabilityProfileName}, true, true,
			[]string{GrafanaProfileName, PrometheusProfileName, TokensV2ProfileName}},
		{"tokens-v2 survives full observability disable",
			[]string{TokensV2ProfileName, PrometheusProfileName}, false, false,
			[]string{TokensV2ProfileName}},
		{"no duplicate when prometheus already present",
			[]string{PrometheusProfileName}, true, false,
			[]string{PrometheusProfileName}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := syncObservabilityProfiles(tc.existing, tc.prom, tc.graf)
			if !reflect.DeepEqual(sortStrings(got), sortStrings(tc.want)) {
				t.Errorf("syncObservabilityProfiles(%v, %v, %v) = %v, want %v",
					tc.existing, tc.prom, tc.graf, got, tc.want)
			}
		})
	}
}

// TestSetObservability_NoOpDisablePersists exercises the persist +
// profile-sync tail without docker: when the instance has no sidecars
// running and the caller asks for none, SetObservability touches no
// docker (no enable, no disable) and persists an empty profile set,
// returning a clean result. This proves the orchestration's
// state-management half independently of the exec half.
func TestSetObservability_NoOpDisablePersists(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	const name = "noop"
	seeded := registry.NewState(name, "0.6.4")
	seeded.Status = registry.StatusRunning
	seeded.Ports = map[string]int{"app_user_ui": 12345}
	seeded.Profiles = []string{ObservabilityProfileName}
	if err := registry.Write(seeded); err != nil {
		t.Fatalf("seed: %v", err)
	}

	state, err := registry.Read(name)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// current: both off (no prometheus_ui / grafana_ui ports). want: both
	// off → no docker calls at all.
	res, err := SetObservability(context.Background(), state, false, false, nil)
	if err != nil {
		t.Fatalf("SetObservability no-op: %v", err)
	}
	if res.Prometheus || res.Grafana {
		t.Errorf("res = %+v, want both off", res)
	}
	// Persisted: observability profiles dropped, ports absent.
	fresh, err := registry.Read(name)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if len(fresh.Profiles) != 0 {
		t.Errorf("expected observability profiles cleared on full disable, got %v", fresh.Profiles)
	}
	if _, ok := fresh.Ports["prometheus_ui"]; ok {
		t.Error("prometheus_ui should be absent after disable")
	}
}

// TestSetObservability_GrafanaWithoutPrometheusWarns: requesting Grafana
// without Prometheus surfaces the shared advisory (not an error). We use
// the both-off → graf-only-but-current-off path; enabling grafana would
// exec docker, so we instead verify the warning is computed on the
// result for the no-change case by starting from a state where grafana
// is already "on" (port present) so no docker enable fires.
func TestSetObservability_GrafanaWithoutPrometheusWarns(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

	const name = "grafonly"
	seeded := registry.NewState(name, "0.6.4")
	seeded.Status = registry.StatusRunning
	// grafana already on (port present), prometheus off → asking for
	// (graf=true, prom=false) is a no-op on docker but must carry the
	// warning.
	seeded.Ports = map[string]int{"grafana_ui": 13000}
	if err := registry.Write(seeded); err != nil {
		t.Fatalf("seed: %v", err)
	}
	state, _ := registry.Read(name)
	res, err := SetObservability(context.Background(), state, false, true, nil)
	if err != nil {
		t.Fatalf("SetObservability: %v", err)
	}
	if res.Warning != GrafanaWithoutPrometheusWarning {
		t.Errorf("expected grafana-without-prometheus warning, got %q", res.Warning)
	}
	if !res.Grafana || res.Prometheus {
		t.Errorf("res = %+v, want grafana on, prometheus off", res)
	}
}

func TestSetObservability_NilState(t *testing.T) {
	if _, err := SetObservability(context.Background(), nil, true, true, nil); err == nil {
		t.Fatal("expected error on nil state")
	}
}
