package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTelemetry_RecordInstallSurface_APTFlushesImmediately(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_TELEMETRY_DIR", t.TempDir())
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	t.Setenv("DPM_TELEMETRY", "on")
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CI", "")

	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	t.Setenv("CANTON_DEVKIT_TELEMETRY_ENDPOINT", srv.URL)

	var out, err bytes.Buffer
	code := New(&out, &err, "1.0", "").WithTelemetry().Run([]string{"telemetry", "_record-install-surface", "apt"})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%s", code, err.String())
	}
	if got == nil {
		t.Fatal("collector saw no install-surface payload")
	}
	counters, _ := got["counters"].(map[string]any)
	surfaces, _ := counters["dpm/install_surface"].(map[string]any)
	if n, _ := surfaces["apt"].(float64); int(n) != 1 {
		t.Fatalf("apt surface count = %v, want 1", surfaces["apt"])
	}
	if _, ok := counters["dpm/command"]; ok {
		t.Fatal("hidden telemetry helper should not emit dpm/command")
	}
}

func TestTelemetry_RecordInstallSurface_APTHonorsTelemetryOptOut(t *testing.T) {
	t.Setenv("CANTON_DEVKIT_TELEMETRY_DIR", t.TempDir())
	t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
	t.Setenv("DPM_TELEMETRY", "off")
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CI", "")

	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	t.Setenv("CANTON_DEVKIT_TELEMETRY_ENDPOINT", srv.URL)

	var out, err bytes.Buffer
	code := New(&out, &err, "1.0", "").WithTelemetry().Run([]string{"telemetry", "_record-install-surface", "apt"})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%s", code, err.String())
	}
	if hit {
		t.Fatal("telemetry opt-out still allowed an install-surface ping")
	}
}
