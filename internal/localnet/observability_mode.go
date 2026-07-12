package localnet

import (
	"context"
	"fmt"
	"io"
	"strings"
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

// sharedStackReachable brings the host-shared observability stack up
// (idempotent) and reports whether it is running.
func sharedStackReachable(ctx context.Context, logw io.Writer) bool {
	_, err := EnsureSharedStack(ctx, logw)
	return err == nil
}
