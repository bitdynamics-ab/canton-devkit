package cli

import (
	"bytes"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/telemetry"
)

// TestTelemetry_MetaCommandsExcludedFromAdoption pins the signal-quality
// rule: meta commands (`version`, `telemetry …`, `help`) must NOT record
// adoption context (os/arch/ci/llm_agent/install), while real localnet
// commands must. Otherwise auditing the telemetry config would inflate
// the adoption picture.
func TestTelemetry_MetaCommandsExcludedFromAdoption(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_TELEMETRY_DIR", t.TempDir())
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	t.Setenv("DPM_TELEMETRY", "on")
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CI", "") // so dpm/install isn't suppressed by the CI gate

	var o, e bytes.Buffer

	// --- meta command: must record NOTHING ---
	New(&o, &e, "1.0", "").WithTelemetry().Run([]string{"version"})
	agg, _ := telemetry.PreviewCurrentPeriod()
	for _, chart := range []string{"dpm/os", "dpm/arch", "dpm/install", "dpm/command"} {
		if _, ok := agg.Counters[chart]; ok {
			t.Errorf("meta command `version` recorded %s — it must not count toward adoption", chart)
		}
	}

	// --- also exercise `telemetry status` (a meta subcommand) ---
	New(&o, &e, "1.0", "").WithTelemetry().Run([]string{"telemetry", "status"})
	agg, _ = telemetry.PreviewCurrentPeriod()
	if _, ok := agg.Counters["dpm/os"]; ok {
		t.Error("`telemetry status` recorded dpm/os — meta commands must not count")
	}

	// --- real localnet command: context IS recorded ---
	New(&o, &e, "1.0", "").WithTelemetry().Run([]string{"localnet", "list"})
	agg, _ = telemetry.PreviewCurrentPeriod()
	if len(agg.Counters["dpm/os"]) == 0 {
		t.Error("`localnet list` did not record dpm/os — real commands should count context")
	}
	if agg.Counters["dpm/command"]["list"] != 1 {
		t.Errorf("`localnet list` should record dpm/command list=1, got %v", agg.Counters["dpm/command"])
	}
}
