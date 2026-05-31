package localnet

import (
	"bytes"
	"strings"
	"testing"
)

// runTelemetry runs `localnet telemetry <args...>` against a sandboxed
// telemetry dir so the test never touches the real ~/.canton-devkit.
func runTelemetry(t *testing.T, args ...string) string {
	t.Helper()
	t.Setenv("CANTON_DEVKIT_TELEMETRY_DIR", t.TempDir())
	cmd := buildTelemetry()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("telemetry %v: %v", args, err)
	}
	return out.String()
}

func TestTelemetryOffThenStatus(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_TELEMETRY_DIR", t.TempDir())
	// off
	off := buildTelemetry()
	var ob bytes.Buffer
	off.SetOut(&ob)
	off.SetErr(&ob)
	off.SetArgs([]string{"off"})
	if err := off.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ob.String(), "disabled") {
		t.Errorf("off output: %q", ob.String())
	}
	// status reflects disabled
	st := buildTelemetry()
	var sb bytes.Buffer
	st.SetOut(&sb)
	st.SetErr(&sb)
	st.SetArgs([]string{"status"})
	if err := st.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), "disabled") {
		t.Errorf("status should show disabled: %q", sb.String())
	}
}

func TestTelemetryStatusJSON(t *testing.T) {
	out := runTelemetry(t, "status", "--format", "json")
	for _, k := range []string{"enabled", "install_id", "endpoint", "queued_events"} {
		if !strings.Contains(out, k) {
			t.Errorf("status json missing %q: %s", k, out)
		}
	}
}
