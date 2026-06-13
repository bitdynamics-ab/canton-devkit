package localnet

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/assets"
)

// TestObservabilityComposeProfiles guards the per-component profile
// keys in assets/compose/observability.yaml. The bring-up path
// activates services via `--profile prometheus` and `--profile
// grafana`; if anyone drops the per-component keys from the compose
// YAML, those flags would become no-ops while existing tests still
// pass — this test catches that.
func TestObservabilityComposeProfiles(t *testing.T) {
	raw, err := fs.ReadFile(assets.FS, "compose/observability.yaml")
	if err != nil {
		t.Fatalf("read embedded observability.yaml: %v", err)
	}
	s := string(raw)
	// Cheap stanza-split: each service block ends at the next
	// 2-space-indented `<name>:` line. We only need to assert that
	// "prometheus" appears in prometheus's profiles list and ditto
	// for grafana — the YAML is small enough to look at as a string.
	promIdx := strings.Index(s, "\n  prometheus:\n")
	grafIdx := strings.Index(s, "\n  grafana:\n")
	if promIdx < 0 || grafIdx < 0 {
		t.Fatalf("could not find prometheus/grafana service blocks in compose")
	}
	if promIdx > grafIdx {
		promIdx, grafIdx = grafIdx, promIdx
	}
	promBlock := s[promIdx:grafIdx]
	grafBlock := s[grafIdx:]

	// Per-component checks. The umbrella `observability` is also
	// retained but isn't strictly required — its presence is
	// covered by TestExpandObservabilityProfiles's "umbrella +
	// override" case at the helper level.
	if !strings.Contains(promBlock, `"prometheus"`) {
		t.Errorf("prometheus service block missing per-component profile entry:\n%s", promBlock)
	}
	if !strings.Contains(grafBlock, `"grafana"`) {
		t.Errorf("grafana service block missing per-component profile entry:\n%s", grafBlock)
	}
	// Legacy umbrella must remain so existing users' `--profile observability` keeps working.
	if !strings.Contains(promBlock, `"observability"`) ||
		!strings.Contains(grafBlock, `"observability"`) {
		t.Errorf("legacy `observability` umbrella profile dropped from one or both service blocks")
	}
}
