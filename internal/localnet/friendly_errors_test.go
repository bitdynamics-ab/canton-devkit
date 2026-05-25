package localnet

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestFriendlyError_ErrorIsMachineReadable pins the one-line form
// CI logs and scripts will grep. Code must appear verbatim, no
// ANSI noise, cause appended in parens if present.
func TestFriendlyError_ErrorIsMachineReadable(t *testing.T) {
	withCause := NewFriendly(ErrCodePortsInUse, "two ports busy",
		errors.New("bind: address in use"),
		"kill 88341", "use --ports …")
	got := withCause.Error()
	for _, want := range []string{"code PORTS_IN_USE", "two ports busy", "address in use"} {
		if !strings.Contains(got, want) {
			t.Errorf("Error() missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("Error() should not contain ANSI escapes: %q", got)
	}

	noCause := NewFriendly(ErrCodeDockerDown, "docker daemon unreachable", nil)
	if strings.Contains(noCause.Error(), "()") {
		t.Errorf("Error() with no cause should not emit empty parens: %q", noCause.Error())
	}
}

// TestFriendlyError_UnwrapWalksCause is the contract that scripts +
// existing log middleware depend on: errors.Is should match the
// original cause through the FriendlyError envelope. Without this,
// wrapping breaks callers that switch on sentinels (e.g.
// errors.Is(err, ctx.Canceled)).
func TestFriendlyError_UnwrapWalksCause(t *testing.T) {
	sentinel := errors.New("sentinel")
	wrapped := fmt.Errorf("layered: %w", sentinel)
	friendly := NewFriendly(ErrCodeFetchFail, "fetch failed", wrapped)

	if !errors.Is(friendly, sentinel) {
		t.Error("errors.Is should reach the original sentinel through FriendlyError")
	}
}

// TestAsFriendly_FindsThroughWrapping covers the symmetric path: a
// FriendlyError wrapped in another fmt.Errorf("%w", …) MUST still be
// findable by AsFriendly so the App.Run layer can render the box
// even when middleware added its own wrap on top.
func TestAsFriendly_FindsThroughWrapping(t *testing.T) {
	inner := NewFriendly(ErrCodeInstanceBusy, "instance busy", nil, "wait")
	outer := fmt.Errorf("RunDown: %w", inner)
	got := AsFriendly(outer)
	if got == nil {
		t.Fatal("AsFriendly should find the FriendlyError through %w wrap")
	}
	if got.Code != ErrCodeInstanceBusy {
		t.Errorf("Code = %q, want %q", got.Code, ErrCodeInstanceBusy)
	}
}

// TestAsFriendly_NonFriendlyReturnsNil makes sure a plain error
// surfaces as nil instead of a zero-value *FriendlyError pointer
// (which would render an empty box). The classic Go errors.As
// gotcha — easy to get wrong if the helper used `_ = errors.As(...)`.
func TestAsFriendly_NonFriendlyReturnsNil(t *testing.T) {
	if got := AsFriendly(errors.New("plain")); got != nil {
		t.Errorf("AsFriendly(plain) = %+v, want nil", got)
	}
	if got := AsFriendly(nil); got != nil {
		t.Errorf("AsFriendly(nil) = %+v, want nil", got)
	}
}

// TestRenderFriendly_ContainsCodeSummaryAndRemediation drives the
// boxed output through a buffer and asserts every visible element
// landed. We don't pin byte-exact output (lipgloss color codes
// depend on the renderer's profile), only substrings.
func TestRenderFriendly_ContainsCodeSummaryAndRemediation(t *testing.T) {
	err := NewFriendly(ErrCodePortsInUse,
		"Two ports DevKit needs are already bound by other processes.",
		nil,
		"Stop the conflicting processes  ·  kill 88341 88450",
		"Pick different ports  ·  dpm localnet up --ports wallet=4585",
		"Inspect host readiness  ·  dpm localnet doctor")
	var buf bytes.Buffer
	RenderFriendly(&buf, err)

	for _, want := range []string{
		"PORTS_IN_USE",
		"devkit.dev/e/PORTS_IN_USE",
		"already bound",
		"Try one of:",
		"1.", "2.", "3.",
		"kill 88341 88450",
		"dpm localnet doctor",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("Render output missing %q\nfull:\n%s", want, buf.String())
		}
	}
}

// TestRenderFriendly_NilIsNoOp is the defensive-coding guarantee:
// if a caller does `RenderFriendly(w, AsFriendly(err))` without
// checking the result, a nil pointer shouldn't panic or write
// anything.
func TestRenderFriendly_NilIsNoOp(t *testing.T) {
	var buf bytes.Buffer
	RenderFriendly(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("nil should not write anything, got %q", buf.String())
	}
}

// TestFriendlyExit_NonTTYFallsBackToMachineReadable is the contract
// that keeps CI logs grep-able: bytes.Buffer is not a TTY so
// ShouldColor returns false → no ANSI box, just the one-line
// Error() string. With no ExitCodeError in the chain, the fallback
// argument is the returned code.
func TestFriendlyExit_NonTTYFallsBackToMachineReadable(t *testing.T) {
	err := NewFriendly(ErrCodeDockerDown, "docker daemon unreachable", nil, "start docker")
	var buf bytes.Buffer
	code := FriendlyExit(&buf, err, 99)
	if code != 99 {
		t.Errorf("FriendlyExit returned %d, want 99 (fallback when no ExitCodeError)", code)
	}
	if strings.Contains(buf.String(), "┃") {
		t.Errorf("non-TTY output should not include box-drawing chars, got %q", buf.String())
	}
	if !strings.Contains(buf.String(), "code DOCKER_DOWN") {
		t.Errorf("non-TTY output missing machine code, got %q", buf.String())
	}
}

// TestFriendlyExit_NilErrorReturnsZero pins the success contract:
// nil err means "nothing to report" and the returned code is 0,
// regardless of `fallback`. (The previous behaviour returned
// fallback even for nil — making `return FriendlyExit(w, runErr,
// ExitUserError)` exit 1 on success — caught in review.)
func TestFriendlyExit_NilErrorReturnsZero(t *testing.T) {
	var buf bytes.Buffer
	if code := FriendlyExit(&buf, nil, 99); code != 0 {
		t.Errorf("nil err should return 0, got %d", code)
	}
	if buf.Len() != 0 {
		t.Errorf("nil err should not write, got %q", buf.String())
	}
}

// TestFriendlyExit_PreservesExitCodeError is the BIT-126 review pin:
// if the upstream orchestrator already returned an ExitCodeError
// (e.g. localnet.AsExitError(ExitPreflightFail)), FriendlyExit MUST
// surface that code and ignore `fallback`. Without this, every CLI
// command that returned via a friendly-error path would collapse
// to whatever generic code the caller passed in — losing the
// exit-code contract scripts depend on.
//
// CLAUDE.md: "ExitCodeError must not silently collapse through
// wrappers." This test is the artifact-grep that ensures it.
func TestFriendlyExit_PreservesExitCodeError(t *testing.T) {
	// A: bare ExitCodeError.
	var buf bytes.Buffer
	if code := FriendlyExit(&buf, ExitCodeError(ExitPreflightFail), 99); code != ExitPreflightFail {
		t.Errorf("bare ExitCodeError: got %d, want ExitPreflightFail (%d)", code, ExitPreflightFail)
	}

	// B: ExitCodeError wrapped in a FriendlyError. Both signals
	// must compose — box renders AND exit code is preserved.
	wrapped := fmt.Errorf("wrap: %w", ExitCodeError(ExitTimeout))
	friendly := NewFriendly(ErrCodeInstanceBusy, "instance busy", wrapped, "wait")
	buf.Reset()
	if code := FriendlyExit(&buf, friendly, 99); code != ExitTimeout {
		t.Errorf("wrapped ExitCodeError: got %d, want ExitTimeout (%d)", code, ExitTimeout)
	}

	// C: ExitCodeError wrapped in plain fmt.Errorf — also
	// recovered via errors.As walk through the cause chain.
	plain := fmt.Errorf("middleware: %w", ExitCodeError(ExitRuntimeFailure))
	buf.Reset()
	if code := FriendlyExit(&buf, plain, 99); code != ExitRuntimeFailure {
		t.Errorf("plain-wrapped ExitCodeError: got %d, want ExitRuntimeFailure (%d)", code, ExitRuntimeFailure)
	}
}
