package localnet

import (
	"errors"
	"fmt"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
)

// TestCodedError_RoundTrip pins the WithCode/CodeOf contract:
// wrapping preserves both the code AND the wrapped error so
// `errors.Is` and the Error() text still work for downstream
// callers that don't know about CodedError.
func TestCodedError_RoundTrip(t *testing.T) {
	base := errors.New("docker daemon refused connection")

	wrapped := WithCode(base, ErrCodeDockerDown)
	if got := CodeOf(wrapped); got != ErrCodeDockerDown {
		t.Errorf("CodeOf(wrapped) = %q, want %q", got, ErrCodeDockerDown)
	}
	if !errors.Is(wrapped, base) {
		t.Error("errors.Is(wrapped, base) = false; want true (Unwrap broken?)")
	}
	if got := wrapped.Error(); got != base.Error() {
		t.Errorf("wrapped.Error() = %q, want %q (CodedError must surface the cause's text)",
			got, base.Error())
	}
}

// TestWithCode_NilCause: WithCode(nil, …) must return nil so
// callers can write `return WithCode(maybeErr, code)` without a
// nil check. A non-nil return would convert "no error" into
// "error with code" — a bug factory.
func TestWithCode_NilCause(t *testing.T) {
	if got := WithCode(nil, ErrCodeDockerDown); got != nil {
		t.Errorf("WithCode(nil, code) = %v, want nil", got)
	}
}

// TestCodeOf_NoCode: a plain error that isn't a CodedError must
// return "" — distinguishes "we recognized the failure mode" from
// "we don't know what this is". The HTTP/SSE layers switch on
// empty vs non-empty.
func TestCodeOf_NoCode(t *testing.T) {
	if got := CodeOf(errors.New("plain")); got != "" {
		t.Errorf("CodeOf(plain error) = %q, want \"\"", got)
	}
	if got := CodeOf(nil); got != "" {
		t.Errorf("CodeOf(nil) = %q, want \"\"", got)
	}
}

// TestCodeOf_DeepWrap: errors.As is supposed to traverse the
// Unwrap chain. Pin that CodeOf works on a deeply-wrapped chain
// — fmt.Errorf with %w is the common shape RunUp produces.
func TestCodeOf_DeepWrap(t *testing.T) {
	base := errors.New("port 5011 in use")
	coded := WithCode(base, ErrCodePortsInUse)
	wrappedAgain := fmt.Errorf("during start: %w", coded)
	wrappedTwice := fmt.Errorf("during bring-up: %w", wrappedAgain)

	if got := CodeOf(wrappedTwice); got != ErrCodePortsInUse {
		t.Errorf("CodeOf(2x-wrapped) = %q, want %q (errors.As traversal broken?)",
			got, ErrCodePortsInUse)
	}
}

// TestPreflightCodeFromReport pins the priority order. The
// concern was that the SSE-side helper and the HTTP-side
// helper could drift; this test plus the dedupe (handlers/
// preflight.go is now just an alias for this function) keeps them
// in lockstep.
func TestPreflightCodeFromReport(t *testing.T) {
	mk := func(results ...docker.CheckResult) *docker.Report {
		return &docker.Report{Results: results}
	}
	fail := func(name string) docker.CheckResult {
		return docker.CheckResult{Name: name, Status: docker.StatusFail}
	}
	pass := func(name string) docker.CheckResult {
		return docker.CheckResult{Name: name, Status: docker.StatusOK}
	}

	cases := []struct {
		name   string
		report *docker.Report
		want   string
	}{
		{
			name: "Docker CLI missing wins over everything else",
			report: mk(
				fail("Docker CLI"),
				fail("Docker memory"),
				fail("Disk space"),
			),
			want: ErrCodeDockerNotInstalled,
		},
		{
			name: "Docker daemon down beats memory/disk/compose",
			report: mk(
				pass("Docker CLI"),
				fail("Docker daemon"),
				fail("Docker memory"),
				fail("Disk space"),
			),
			want: ErrCodeDockerDown,
		},
		{
			name: "Compose v1 beats memory/disk",
			report: mk(
				pass("Docker CLI"),
				pass("Docker daemon"),
				fail("Docker Compose v2"),
				fail("Docker memory"),
			),
			want: ErrCodeComposeMissing,
		},
		{
			name: "Memory beats disk",
			report: mk(
				pass("Docker CLI"),
				fail("Docker memory"),
				fail("Disk space"),
			),
			want: ErrCodeMemoryLow,
		},
		{
			name:   "Disk alone",
			report: mk(pass("Docker CLI"), fail("Disk space")),
			want:   ErrCodeDiskLow,
		},
		{
			name:   "Unknown failing check name → generic catch-all",
			report: mk(docker.CheckResult{Name: "Future check", Status: docker.StatusFail}),
			want:   ErrCodePreflightFailed,
		},
		{
			name:   "Empty report → catch-all (defensive — caller shouldn't call this on OK)",
			report: mk(),
			want:   ErrCodePreflightFailed,
		},
		{
			name:   "All passing → catch-all (same as empty — defensive)",
			report: mk(pass("Docker CLI"), pass("Docker daemon")),
			want:   ErrCodePreflightFailed,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := PreflightCodeFromReport(c.report)
			if got != c.want {
				t.Errorf("PreflightCodeFromReport() = %q, want %q",
					got, c.want)
			}
		})
	}
}
