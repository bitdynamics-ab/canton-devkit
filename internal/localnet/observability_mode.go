package localnet

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ObservabilityMode selects how the Prometheus + Grafana sidecars are
// provisioned when an observability profile is enabled.
type ObservabilityMode string

const (
	// ObservabilityModeAuto uses the host-shared stack and skips the
	// per-instance overlay when the shared stack is reachable. Default.
	ObservabilityModeAuto ObservabilityMode = "auto"
	// ObservabilityModeShared forces shared-only: never materialize the
	// per-instance overlay.
	ObservabilityModeShared ObservabilityMode = "shared"
	// ObservabilityModePerInstance forces the per-instance overlay.
	ObservabilityModePerInstance ObservabilityMode = "per-instance"
)

// ObservabilityModes lists the accepted --observability-mode values.
func ObservabilityModes() []string {
	return []string{
		string(ObservabilityModeAuto),
		string(ObservabilityModeShared),
		string(ObservabilityModePerInstance),
	}
}

func normalizeObservabilityMode(m ObservabilityMode) ObservabilityMode {
	if m == "" {
		return ObservabilityModeAuto
	}
	return m
}

// ValidateObservabilityMode rejects an unknown --observability-mode value.
func ValidateObservabilityMode(m ObservabilityMode) error {
	switch normalizeObservabilityMode(m) {
	case ObservabilityModeAuto, ObservabilityModeShared, ObservabilityModePerInstance:
		return nil
	default:
		return fmt.Errorf("invalid observability mode %q (want one of: %s)",
			m, strings.Join(ObservabilityModes(), ", "))
	}
}

// usePerInstanceOverlay reports whether to materialize the per-instance
// Prometheus + Grafana overlay: per-instance always does, shared never does,
// and auto keeps it only as a fallback when the shared stack is unreachable.
func usePerInstanceOverlay(mode ObservabilityMode, sharedReachable bool) bool {
	switch normalizeObservabilityMode(mode) {
	case ObservabilityModePerInstance:
		return true
	case ObservabilityModeShared:
		return false
	default:
		return !sharedReachable
	}
}

const (
	sharedHealthTimeout      = 12 * time.Second
	sharedHealthPollInterval = time.Second
)

// sharedStackReachable brings the host-shared observability stack up
// (idempotent) and reports whether its Prometheus is actually serving. A stack
// that is up but corrupt/unhealthy is treated as unreachable so auto falls back
// to the per-instance overlay rather than binding to a broken data source.
func sharedStackReachable(ctx context.Context, logw io.Writer) bool {
	if _, err := EnsureSharedStack(ctx, logw); err != nil {
		return false
	}
	host, port, err := SharedPrometheusEndpoint(ctx)
	if err != nil {
		return false
	}
	return sharedPrometheusHealthy(ctx, fmt.Sprintf("http://%s:%d/-/healthy", host, port))
}

// sharedPrometheusHealthy polls a Prometheus readiness URL, retrying briefly so
// a just-started stack isn't misread as unhealthy.
func sharedPrometheusHealthy(ctx context.Context, url string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, sharedHealthTimeout)
	defer cancel()
	for {
		if httpOK(probeCtx, url) {
			return true
		}
		select {
		case <-probeCtx.Done():
			return false
		case <-time.After(sharedHealthPollInterval):
		}
	}
}

func httpOK(ctx context.Context, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == http.StatusOK
}
