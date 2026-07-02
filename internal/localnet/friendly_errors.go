package localnet

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
)

// Friendly-error wrapper: ErrorCode + Remediation list +
// RenderFriendly(), which produces a boxed callout with remediation
// steps when stderr is a TTY, and a one-line machine-readable form
// otherwise so CI logs stay grep-able.
//
// The wrapper is opt-in: a caller wraps a raw error with NewFriendly
// at the failure site; everything else keeps using plain fmt.Errorf
// and gets unchanged behaviour.

// ErrorCode is the stable identifier scripts can branch on. Known code
// constants live next to CodedError in coded_error.go; codes are NEVER
// renamed once shipped because users may have built alerts around them.
type ErrorCode = string

const (
	ErrCodeComposeFail  ErrorCode = "COMPOSE_FAILED"
	ErrCodeInstanceBusy ErrorCode = "INSTANCE_BUSY"
	ErrCodeNotFound     ErrorCode = "INSTANCE_NOT_FOUND"
	ErrCodeFetchFail    ErrorCode = "SPLICE_FETCH_FAILED"
)

// FriendlyError carries a code, a one-line summary, and an ordered
// list of remediation tips. Wraps a cause for errors.Is/As callers.
//
// Implements error so it can sit anywhere `error` is expected — the
// orchestrator just needs to know whether to call RenderFriendly()
// (TTY stderr) or err.Error() (everything else). FriendlyExit(errw,
// err, code) is the helper that does the right thing.
type FriendlyError struct {
	Code        ErrorCode
	Summary     string   // one line; appears next to the code
	Remediation []string // each entry one line; rendered as a numbered list
	Cause       error    // wrapped; nil if no underlying error
	// IncludeCause opts the cause into Error()'s one-line form.
	// Default false: Error() emits "error · code X · summary" only.
	//
	// Leaking the underlying cause by default risks exposing
	// paths, hostnames, port numbers, or
	// other system details through CI/log surfaces where the
	// FriendlyError was constructed to deliberately *replace* the
	// raw error with a curated message. Callers that DO want the
	// cause in the one-liner (debug logs, devkit doctor) set this
	// explicitly. The cause is always available via errors.Unwrap
	// / errors.As regardless of this flag.
	IncludeCause bool
}

// Error returns a one-line machine-readable form suitable for
// non-TTY stderr or wrapped log lines: `error · code PORTS_IN_USE ·
// summary…`. No ANSI codes, so grep/CI parsers see stable text.
//
// The cause is appended only when IncludeCause is set — see the
// field comment for why default-off is the safer posture.
func (f *FriendlyError) Error() string {
	if f.Cause != nil && f.IncludeCause {
		return fmt.Sprintf("error · code %s · %s (%s)", f.Code, f.Summary, f.Cause.Error())
	}
	return fmt.Sprintf("error · code %s · %s", f.Code, f.Summary)
}

// Unwrap supports errors.Is / errors.As on the cause chain. Callers
// that wrap a docker exec.ExitError can still type-check it through
// the FriendlyError envelope.
func (f *FriendlyError) Unwrap() error { return f.Cause }

// NewFriendly is the constructor. `summary` is the bright line shown
// inside the box; `remediation` is the bulleted "try one of:" list.
// Pass nil for cause if the failure was synthesised (e.g. our own
// port-scan returned a conflict — no upstream error to wrap).
func NewFriendly(code ErrorCode, summary string, cause error, remediation ...string) *FriendlyError {
	return &FriendlyError{
		Code:        code,
		Summary:     summary,
		Remediation: remediation,
		Cause:       cause,
	}
}

// AsFriendly is a typed errors.As — returns the *FriendlyError if
// err's chain contains one, nil otherwise. Lets `App.Run` decide
// whether to render the box without a type assertion in the call
// site.
func AsFriendly(err error) *FriendlyError {
	var f *FriendlyError
	if errors.As(err, &f) {
		return f
	}
	return nil
}

// RenderFriendly writes the boxed ScreenError-style callout to w.
// Layout:
//
//	┃ error · code PORTS_IN_USE · devkit.dev/e/PORTS_IN_USE
//	┃ Two ports DevKit needs are already bound by other processes.
//	┃
//	┃ Try one of:
//	┃   1. Stop the conflicting processes   ·  kill 88341 88450
//	┃   2. Pick different ports             ·  dpm localnet up --ports …
//	┃   3. Inspect host readiness           ·  dpm localnet doctor
//
// term.Box owns the left accent bar; we own the body assembly.
// Numbering is rendered with brand-colored "1." prefixes; the
// remediation strings can contain a "  ·  " separator to split a
// label from its example command — we don't parse them, we trust
// the caller to write them readably.
func RenderFriendly(w io.Writer, f *FriendlyError) {
	if f == nil {
		return
	}
	var body strings.Builder
	body.WriteString(term.Errorc("error"))
	body.WriteString(term.Dimc(" · code "))
	body.WriteString(term.Textc(string(f.Code)))
	body.WriteString(term.Dimc(" · "))
	body.WriteString(term.Faintc(fmt.Sprintf("devkit.dev/e/%s", f.Code)))
	body.WriteString("\n")
	body.WriteString(term.Textc(f.Summary))
	if len(f.Remediation) > 0 {
		body.WriteString("\n\n")
		body.WriteString(term.Dimc("Try one of:"))
		body.WriteString("\n")
		for i, r := range f.Remediation {
			body.WriteString("  ")
			body.WriteString(term.Brandc(fmt.Sprintf("%d.", i+1)))
			body.WriteString(" ")
			body.WriteString(term.Textc(r))
			if i < len(f.Remediation)-1 {
				body.WriteString("\n")
			}
		}
	}
	_, _ = fmt.Fprintln(w, term.Box(term.BoxError, body.String()))
}

// FriendlyExit is the one-call helper for orchestrators. If err is a
// FriendlyError and `errw` is a TTY, render the box; otherwise print
// the machine-readable Error() form.
//
// Exit-code precedence (invariant: an ExitCodeError must not silently
// collapse through wrappers):
//
//  1. If err's chain carries an ExitCodeError, that wins. This
//     preserves the exit-code contract every Run* function in this
//     package establishes — e.g. RunUp returns
//     localnet.AsExitError(ExitPreflightFail) and any later wrapper
//     (including FriendlyExit) must surface 2, not whatever
//     `fallback` happened to be passed in.
//  2. Otherwise the `fallback` argument is used. Callers pass the
//     code they want when wrapping a non-ExitCodeError (e.g.
//     ExitUserError for an upstream validation error).
//
// Returns the chosen code so the caller can write
// `return localnet.FriendlyExit(errw, err, ExitUserError)` and
// trust that an upstream ExitTimeout still surfaces as 3.
//
// Box gating delegates to term.IsTerminal (NOT term.ShouldColor):
// ShouldColor would conflate "is a TTY" with "should emit ANSI",
// stripping the box STRUCTURE for a NO_COLOR=1 user on a real
// terminal. The box's value is the left ┃ accent + the indented
// remediation list — both work without color — so the box is gated
// on TTY only, and the per-token Errorc/Brandc/etc. inside
// RenderFriendly respect ShouldColor for the ANSI sequences.
func FriendlyExit(errw io.Writer, err error, fallback int) int {
	if err == nil {
		return 0 // success regardless of fallback; nil means nothing to report
	}
	code := fallback
	var ece ExitCodeError
	if errors.As(err, &ece) {
		code = int(ece)
	}
	f := AsFriendly(err)
	if f != nil && term.IsTerminal(errw) {
		RenderFriendly(errw, f)
		return code
	}
	_, _ = fmt.Fprintln(errw, err.Error())
	return code
}
