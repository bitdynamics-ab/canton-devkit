package localnet

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestUsePerInstanceOverlay(t *testing.T) {
	cases := []struct {
		mode            ObservabilityMode
		sharedReachable bool
		want            bool
	}{
		{ObservabilityModePerInstance, true, true},
		{ObservabilityModePerInstance, false, true},
		{ObservabilityModeShared, true, false},
		{ObservabilityModeShared, false, false},
		{ObservabilityModeAuto, true, false},
		{ObservabilityModeAuto, false, true},
		{"", true, false},
		{"", false, true},
	}
	for _, c := range cases {
		if got := usePerInstanceOverlay(c.mode, c.sharedReachable); got != c.want {
			t.Errorf("usePerInstanceOverlay(%q, %v) = %v, want %v",
				c.mode, c.sharedReachable, got, c.want)
		}
	}
}

func TestValidateObservabilityMode(t *testing.T) {
	for _, m := range []ObservabilityMode{"", "auto", "shared", "per-instance"} {
		if err := ValidateObservabilityMode(m); err != nil {
			t.Errorf("ValidateObservabilityMode(%q) = %v, want nil", m, err)
		}
	}
	for _, m := range []ObservabilityMode{"bogus", "Shared", "perinstance"} {
		if err := ValidateObservabilityMode(m); err == nil {
			t.Errorf("ValidateObservabilityMode(%q) = nil, want error", m)
		}
	}
}

func TestNormalizeObservabilityMode(t *testing.T) {
	if got := normalizeObservabilityMode(""); got != ObservabilityModeAuto {
		t.Errorf("normalizeObservabilityMode(\"\") = %q, want %q", got, ObservabilityModeAuto)
	}
	if got := normalizeObservabilityMode(ObservabilityModeShared); got != ObservabilityModeShared {
		t.Errorf("normalizeObservabilityMode(shared) = %q, want shared", got)
	}
}

func TestSharedPrometheusHealthy(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer bad.Close()

	if !httpOK(context.Background(), ok.URL) {
		t.Error("httpOK on 200 = false")
	}
	if httpOK(context.Background(), bad.URL) {
		t.Error("httpOK on 503 = true")
	}
	if !sharedPrometheusHealthy(context.Background(), ok.URL) {
		t.Error("healthy server reported unhealthy")
	}
	// Bound the retry with a short parent deadline so the unhealthy path is fast.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if sharedPrometheusHealthy(ctx, bad.URL) {
		t.Error("unhealthy server reported healthy")
	}
}
