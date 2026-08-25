package localnet

import (
	"fmt"
	"io"

	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
)

// Progress abstracts `RunUp` progress reporting so the CLI can emit
// plain terminal text while the Web UI receives a typed event stream
// (Step + status + percent) for its progress modal.
//
// An interface (rather than a callback channel) because the CLI needs
// ordered, synchronous output: a step's "starting…" line must paint
// before any sub-progress dots. The CLI impl writes directly; SSE's
// impl publishes to the hub; tests inject a fake.

// Step is the stable identifier for a phase of `localnet up`. The
// frontend branches on these tokens to surface the right copy + icons.
// New steps belong here AND in the frontend's step→label map; a
// drift between them surfaces as an "unknown step" badge.
type Step string

const (
	// StepResolveVersion: 1 · "Resolve version + adapter" — splice
	// catalogue lookup, upstream fallback if AllowUncurated.
	StepResolveVersion Step = "resolve_version"
	// StepAcquireLock: 2 · "Acquire instance lock" — flock on
	// ~/.canton-devkit/localnet/.locks/<name>.lock.
	StepAcquireLock Step = "acquire_lock"
	// StepPreflight: 3 · "Run preflight checks" — docker daemon /
	// compose v2 / disk / memory thresholds.
	StepPreflight Step = "preflight"
	// StepFetchSplice: 4 · "Fetch Splice LocalNet" — cache hit OR
	// network download + content-sha verification.
	StepFetchSplice Step = "fetch_splice"
	// StepPersistState: 5 · "Persist state + write overlay" —
	// state.json (creating) + container-rename docker-compose
	// overlay.
	StepPersistState Step = "persist_state"
	// StepStartServices: 6 · "Starting services" — `docker compose
	// up -d --wait`. The longest single phase.
	StepStartServices Step = "start_services"
	// StepWaitHealthy: 7 · "Wait for services to become healthy" —
	// container --wait + adapter-specific readiness probes.
	StepWaitHealthy Step = "wait_healthy"
	// StepCaptureJWTs: 8 · "Capture JWTs · register endpoints" —
	// sign dev-secret tokens for sv/app-provider/app-user, write
	// running state.
	StepCaptureJWTs Step = "capture_jwts"
)

// Status is the lifecycle marker the Progress interface carries
// with each step event.
type Status string

const (
	StatusStepPending Status = "pending"
	StatusStepActive  Status = "active"
	StatusStepDone    Status = "done"
	StatusStepWarn    Status = "warn"
	StatusStepFail    Status = "fail"
)

// Progress is the interface RunUp uses instead of raw io.Writer
// calls. Implementations: TextProgress (CLI text), SSEProgress
// (typed events to the hub), test fakes.
//
// Method conventions:
//   - StartStep / FinishStep / FailStep are bookends; UpdateStep is
//     for in-flight sub-progress (percent or new detail string).
//   - Done is the final success marker; after Done no further calls
//     are valid. SSEProgress closes the per-instance topic on Done.
//   - Out / Err are escape hatches for already-formatted multi-line
//     blocks (the preflight report's table, docker compose log
//     forwarding) — text we don't want to classify into a step.
//     TextProgress returns the underlying writers; SSEProgress
//     wraps each Write into a typed "console" event so the browser
//     can render the same tail.
type Progress interface {
	StartStep(step Step, detail string)
	UpdateStep(step Step, detail string, percent int) // percent = -1 if N/A
	FinishStep(step Step, detail string)
	FailStep(step Step, summary string, cause error)
	Warn(message string)
	Done(detail string)
	Out() io.Writer
	Err() io.Writer
}

// TextProgress is the CLI-facing implementation. Every method
// produces a line (or block) of text on the appropriate stream.
//
// When stdout is a TTY, section headers and the final success box are
// rendered via the term primitives. When it is NOT a TTY (pipes, CI,
// bytes.Buffer test injection), output stays plain text so golden
// tests pass byte-for-byte, piping doesn't leak ANSI escapes, and CI
// logs stay greppable.
//
// The TTY check happens once at construction (via NewTextProgress),
// so callers that handcraft a TextProgress{OutW: …, ErrW: …} without
// setting tty get the plain path. Use NewTextProgress for the rich path.
type TextProgress struct {
	OutW io.Writer
	ErrW io.Writer
	// tty controls whether StartStep / Done emit the boxed,
	// glyph-prefixed rendering. False (default) keeps the plain
	// output the golden tests assert against.
	tty bool
}

// NewTextProgress constructs a TextProgress and auto-detects whether
// out is a TTY (false for non-*os.File writers such as bytes.Buffer),
// so interactive runs get the styled output and everything else gets
// the plain path.
func NewTextProgress(out, errw io.Writer) *TextProgress {
	return &TextProgress{
		OutW: out,
		ErrW: errw,
		tty:  term.IsTerminal(out),
	}
}

// stepLabel maps the typed Step to the human-readable phrase the CLI
// prints. The frontend keeps an equivalent map keyed by the same
// tokens — keep the two in sync.
var stepLabel = map[Step]string{
	StepResolveVersion: "Resolving version + adapter",
	StepAcquireLock:    "Acquiring instance lock",
	StepPreflight:      "Running preflight checks",
	StepFetchSplice:    "Fetching Splice LocalNet",
	StepPersistState:   "Persisting state + writing overlay",
	StepStartServices:  "Starting services",
	StepWaitHealthy:    "Waiting for services to become healthy",
	StepCaptureJWTs:    "Capturing JWTs · registering endpoints",
}

// textVisibleSteps is the allowlist of steps TextProgress renders a
// header line for. The other five steps (resolve, lock, fetch,
// persist, capture JWTs) stay silent in the CLI — either the
// underlying call writes its own progress (splice.Fetch streams
// download dots into Out()) or the step is fast enough not to warrant
// a line. The orchestrator issues StartStep for ALL steps
// unconditionally; TextProgress filters to the visible ones, while
// SSEProgress emits every event.
//
// Adding a step here is a deliberate CLI behaviour change — it adds a
// new line to `localnet up` output.
var textVisibleSteps = map[Step]bool{
	StepPreflight:     true,
	StepStartServices: true,
	StepWaitHealthy:   true,
}

// StartStep prints the step's header line — but ONLY for steps in
// textVisibleSteps. detail is appended in parentheses when non-empty
// so the user sees context (e.g. the resolved splice tag).
func (t *TextProgress) StartStep(step Step, detail string) {
	if !textVisibleSteps[step] {
		return
	}
	label := labelFor(step)
	if !t.tty {
		// Plain path — byte-stable output for non-TTY callers
		// (tests, pipes, CI).
		if detail == "" {
			_, _ = fmt.Fprintf(t.OutW, "%s...\n", label)
			return
		}
		_, _ = fmt.Fprintf(t.OutW, "%s (%s)...\n", label, detail)
		return
	}
	// Styled path — section header with the same per-step label the
	// plain path uses, so a user flipping between modes sees the same
	// vocabulary.
	body := term.Dimc("(running…)")
	_, _ = fmt.Fprintln(t.OutW)
	_, _ = fmt.Fprintln(t.OutW, term.Section(label, detail, body, 0))
}

// UpdateStep is a no-op in the CLI — printing a line every percent
// tick would spam the terminal. Mid-step progress is rendered by
// SSEProgress only.
func (t *TextProgress) UpdateStep(_ Step, _ string, _ int) {}

// FinishStep is a no-op for the CLI: step completion is signalled
// implicitly by starting the next step. SSEProgress emits a typed
// `step.done` event.
func (t *TextProgress) FinishStep(_ Step, _ string) {}

// FailStep writes a one-line failure to stderr. If cause is non-nil,
// its message follows the summary so the user sees both "what we were
// doing" and "why it broke".
func (t *TextProgress) FailStep(_ Step, summary string, cause error) {
	if cause == nil {
		_, _ = fmt.Fprintln(t.ErrW, summary)
		return
	}
	_, _ = fmt.Fprintf(t.ErrW, "%s: %s\n", summary, cause)
}

// Warn writes a "warning: ..." line to stderr.
func (t *TextProgress) Warn(message string) {
	_, _ = fmt.Fprintf(t.ErrW, "warning: %s\n", message)
}

// Done is the success marker. detail carries the full ready-line;
// the endpoint listing goes through Out() as a verbatim block. On a
// TTY the ready-line is rendered inside a brand-accented Box.
func (t *TextProgress) Done(detail string) {
	if detail == "" {
		return
	}
	if !t.tty {
		_, _ = fmt.Fprintf(t.OutW, "\n%s\n\n", detail)
		return
	}
	body := term.Brandc("✦ ") + term.Textc(detail) + "\n" +
		term.Dimc("Run ") + term.Textc("dpm localnet env") +
		term.Dimc(" to export config, or ") + term.Textc("dpm localnet ui") +
		term.Dimc(" to open the dashboard.")
	_, _ = fmt.Fprintln(t.OutW)
	_, _ = fmt.Fprintln(t.OutW, term.Box(term.BoxBrand, body))
	_, _ = fmt.Fprintln(t.OutW)
}

// Out returns the underlying stdout writer — for already-formatted
// blocks (preflight report table, endpoint listing, compose log
// forwarding) that don't fit the typed step model.
func (t *TextProgress) Out() io.Writer { return t.OutW }

// Err returns the underlying stderr writer — same rationale.
func (t *TextProgress) Err() io.Writer { return t.ErrW }

// labelFor returns the human label for a step. Unknown steps fall
// back to the raw token so a new step added without updating the
// map still prints something usable (the test catches the
// omission).
func labelFor(step Step) string {
	if l, ok := stepLabel[step]; ok {
		return l
	}
	return string(step)
}

// NopProgress discards every event. Used by tests that exercise
// RunUp's exit codes without caring about the output. RunUp must
// tolerate a NopProgress without panicking — Out() and Err() return
// io.Discard so any verbatim block writes don't crash.
type NopProgress struct{}

func (NopProgress) StartStep(Step, string)       {}
func (NopProgress) UpdateStep(Step, string, int) {}
func (NopProgress) FinishStep(Step, string)      {}
func (NopProgress) FailStep(Step, string, error) {}
func (NopProgress) Warn(string)                  {}
func (NopProgress) Done(string)                  {}
func (NopProgress) Out() io.Writer               { return io.Discard }
func (NopProgress) Err() io.Writer               { return io.Discard }
