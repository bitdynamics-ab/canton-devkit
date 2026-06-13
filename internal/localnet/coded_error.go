package localnet

import "errors"

// CodedError wraps an error with a stable machine-readable code
// string. The HTTP layer (SSEProgress.FailStep, preflight handler)
// extracts the code via errors.As(err, &codedError) and stamps it
// on the wire so the frontend can switch on a known signal —
// "Docker isn't running", "port already in use", "host below
// memory floor" — rather than scraping freeform error text.
//
// Codes are stable strings — once added, they must keep the same
// meaning across versions. Adding a new code is non-breaking;
// renaming or repurposing one is breaking. Each code is paired
// with a Go-level remediation string in the constants block below;
// the frontend localizes its own copy.
type CodedError struct {
	Code  string
	Cause error
}

func (e *CodedError) Error() string {
	if e.Cause == nil {
		return e.Code
	}
	return e.Cause.Error()
}

func (e *CodedError) Unwrap() error { return e.Cause }

// WithCode wraps cause with the given code. Returns nil if cause
// is nil so callers can write `return WithCode(err, …)` without
// a manual nil-check.
func WithCode(cause error, code string) error {
	if cause == nil {
		return nil
	}
	return &CodedError{Code: code, Cause: cause}
}

// CodeOf returns the stable code for err if it is (or wraps) a
// CodedError. Returns "" if err has no code attached.
func CodeOf(err error) string {
	if err == nil {
		return ""
	}
	var ce *CodedError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return ""
}

// Known error codes. Frontend's CreateProgress reducer / preflight
// panel switch on these — keep the wire-stable.
const (
	// ErrCodePortsInUse: a stable-reuse port allocation failed
	// because the previously bound host port is busy. Remediation:
	// `lsof -i :<port>` or down the conflicting instance. See
	// internal/localnet/portblock.go ErrPortBusy.
	ErrCodePortsInUse = "PORTS_IN_USE"

	// ErrCodeDockerDown: docker daemon is unreachable. Surfaced
	// from preflight when `docker version` exits non-zero.
	// Remediation: start Docker Desktop / `systemctl start docker`.
	ErrCodeDockerDown = "DOCKER_DOWN"

	// ErrCodeDockerNotInstalled: `docker` not in PATH. Distinct
	// from DOCKER_DOWN — different remediation (install, not
	// start).
	ErrCodeDockerNotInstalled = "DOCKER_NOT_INSTALLED"

	// ErrCodeComposeMissing: docker compose v2 not installed or
	// is v1. Remediation: install compose v2 plugin.
	ErrCodeComposeMissing = "COMPOSE_V1_OR_MISSING"

	// ErrCodeMemoryLow: docker daemon's allocated memory is below
	// the chosen Splice version's floor. Remediation: raise
	// Docker Desktop memory (Preferences → Resources → Memory).
	ErrCodeMemoryLow = "DOCKER_MEMORY_LOW"

	// ErrCodeDiskLow: free disk space on the data dir is below
	// the global minimum. Remediation: free space, or move
	// CANTON_DEVKIT_REGISTRY to a roomier volume.
	ErrCodeDiskLow = "DISK_LOW"

	// ErrCodePreflightFailed: catch-all for a preflight gate
	// rejection that doesn't map to a more specific code. Use
	// only when no narrower code applies.
	ErrCodePreflightFailed = "PREFLIGHT_FAILED"

	// ErrCodeCantonOOM: wait_healthy failed and the canton
	// container's recent logs contain the JVM "-Xmx exceeds half
	// of the container's total memory" warning that precedes a
	// Linux OOM-kill. Covers the case where preflight
	// passed but the user later squeezed Docker's memory, or where
	// the cluster's actual heap consumption exceeds the catalogue
	// estimate. Remediation: raise Docker memory or pick a lighter
	// Splice version.
	ErrCodeCantonOOM = "CANTON_OOM"

	// ErrCodeContainerUnhealthy: wait_healthy failed but the cause
	// didn't match a more specific pattern. Used as a step up
	// from a bare wait_healthy timeout — the frontend can offer a
	// generic "see logs" affordance instead of suggesting nothing.
	ErrCodeContainerUnhealthy = "CONTAINER_UNHEALTHY"
)
