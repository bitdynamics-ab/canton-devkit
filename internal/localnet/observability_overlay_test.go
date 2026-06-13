package localnet

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/assets"
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
	path, err := MaterializeObservabilityOverlay(dataDir, projectDir, nil)
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

// TestMaterializeObservabilityOverlay_PreservesDashboardEdits is the
// regression for the inverted Y5 logic: a hand-edited Grafana dashboard
// (the exact workload the comment claims to protect) must survive a
// re-materialise, with the drift reported on the warn writer.
func TestMaterializeObservabilityOverlay_PreservesDashboardEdits(t *testing.T) {
	dataDir := t.TempDir()
	projectDir := t.TempDir()
	if _, err := MaterializeObservabilityOverlay(dataDir, projectDir, nil); err != nil {
		t.Fatalf("first materialise: %v", err)
	}

	dashboard := filepath.Join(dataDir, "observability", "grafana", "dashboards", "canton-localnet.json")
	edited := []byte(`{"title":"my custom panels"}`)
	if err := os.WriteFile(dashboard, edited, 0o644); err != nil {
		t.Fatalf("simulate operator edit: %v", err)
	}

	var warn bytes.Buffer
	if _, err := MaterializeObservabilityOverlay(dataDir, projectDir, &warn); err != nil {
		t.Fatalf("re-materialise: %v", err)
	}

	got, err := os.ReadFile(dashboard)
	if err != nil {
		t.Fatalf("read dashboard after re-materialise: %v", err)
	}
	if !bytes.Equal(got, edited) {
		t.Errorf("hand-edited dashboard was clobbered:\n got: %q\nwant: %q", got, edited)
	}
	if !strings.Contains(warn.String(), dashboard) {
		t.Errorf("expected drift notice mentioning %q, got %q", dashboard, warn.String())
	}
}

// TestMaterializeObservabilityOverlay_RefreshesComposeOverlay pins the
// one deliberate exception to edit-preservation: the compose overlay
// carries per-instance absolute mount paths that must track dataDir, so
// it is always rewritten from the baseline (and never reported as
// operator drift). Materialising into a second dataDir must produce a
// compose file pointing at the SECOND dataDir, not the first.
func TestMaterializeObservabilityOverlay_RefreshesComposeOverlay(t *testing.T) {
	projectDir := t.TempDir()

	dataDir1 := t.TempDir()
	if _, err := MaterializeObservabilityOverlay(dataDir1, projectDir, nil); err != nil {
		t.Fatalf("materialise into dataDir1: %v", err)
	}

	// Stage the embedded compose overlay (pre-rewrite, relative mounts)
	// into a fresh dataDir, then materialise — the function must rewrite
	// the mounts to dataDir2 absolutes rather than preserve the staged
	// relative form as if it were an operator edit.
	dataDir2 := t.TempDir()
	embedded, err := fs.ReadFile(assets.FS, "compose/observability.yaml")
	if err != nil {
		t.Fatalf("read embedded overlay: %v", err)
	}
	stageDir := filepath.Join(dataDir2, "observability", "compose")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		t.Fatalf("stage dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "observability.yaml"), embedded, 0o644); err != nil {
		t.Fatalf("stage overlay: %v", err)
	}

	var warn bytes.Buffer
	path, err := MaterializeObservabilityOverlay(dataDir2, projectDir, &warn)
	if err != nil {
		t.Fatalf("materialise into dataDir2: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read overlay: %v", err)
	}
	root2 := filepath.ToSlash(filepath.Join(dataDir2, "observability"))
	if !strings.Contains(string(body), root2) {
		t.Errorf("compose overlay was not refreshed for the new dataDir; want a mount under %q\n---\n%s", root2, body)
	}
	root1 := filepath.ToSlash(filepath.Join(dataDir1, "observability"))
	if strings.Contains(string(body), root1) {
		t.Errorf("compose overlay leaked the previous dataDir %q", root1)
	}
	if strings.Contains(warn.String(), "observability.yaml") {
		t.Errorf("compose overlay must not be reported as operator drift; got %q", warn.String())
	}
}
