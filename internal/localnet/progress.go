package localnet

import (
	"fmt"
	"io"

	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
)

// Progress is the structured-event substrate the Web UI (BIT-163,
// webui-create.jsx) needs from `RunUp`. Today RunUp writes verbatim
// strings to two io.Writers (out, errw) — a terminal user sees a
// "Starting services..." line, the browser sees nothing structured.
//
// Goal: keep the CLI output byte-identical (TextProgress below
// reproduces the existing text exactly) while giving the Web UI a
// typed event stream — Step + status + percent + container chips —
// so it can render the rich progress modal the V2 mockup specifies.
//
// Migration plan (each step is its own PR for review traceability):
//
//	BIT-163a (this file) — define the interface + TextProgress + tests
//	BIT-163b           — refactor RunUp to take Progress; CLI caller
//	                     constructs TextProgress; byte-equivalent output
//	BIT-163c           — add an SSEProgress impl that publishes to the
//	                     per-instance topic on internal/ui/stream.Hub
//	BIT-163d           — async POST /api/instances spawns goroutine
//	                     with SSEProgress; UI subscribes to events
//
// Splitting this way keeps each PR small enough to audit. A single
// PR that did all four would touch 200+ lines across CLI, registry,
// UI, frontend — a review death trap.
//
// # Why an interface and not a callback channel
//
// The CLI needs ordered, synchronous output (a step's "starting…"
// line must paint before any sub-progress dots). A typed channel
// would let test code observe events without timing races, but it
// would also need a goroutine to forward into the terminal — extra
// complexity for the only common case (CLI). Methods on an interface
// are simpler: the CLI's impl writes directly; SSE's impl publishes
// to the hub; tests inject a fake.

// Step is the stable identifier for a phase of `localnet up`. The
// taxonomy mirrors the eight steps the webui-create.jsx mockup
// renders in its progress modal — frontend branches on these
// tokens to surface the right copy + icons.
//
// New steps belong here AND in the frontend's step→label map; a
// drift between them surfaces as an "unknown step" badge.
type Step string

const (
	// StepResolveVersion: 1 · "Resolve version + adapter" — splice
	// catalogue lookup, upstream fallback if AllowUncurated.
	StepResolveVersion Step = "resolve_version"
	// StepAcquireLock: 2 · "Acquire instance lock" — flock on
	// ~/.canton-devkit/localnet/<name>/.lock.
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
	// up -d --wait`. The longest single phase; the V2 mockup shows
	// per-container chips updating during this step.
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
// with each step event. Mirrors the icon set in the mockup
// (✓ / ⠹ / ○ / ! / ✕).
type Status string

const (
	StatusStepPending Status = "pending"
	StatusStepActive  Status = "active"
	StatusStepDone    Status = "done"
	StatusStepWarn    Status = "warn"
	StatusStepFail    Status = "fail"
)

// Progress is the interface RunUp will use (BIT-163b) instead of
// raw io.Writer calls. Implementations:
//
//	TextProgress  — wraps two io.Writers, produces today's CLI bytes
//	SSEProgress   — (future) publishes typed events to the hub
//	captureProgress — (test seam) records all calls for assertion
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
// BIT-122 re-skin: when stdout is a TTY, we render section
// headers + final success box via the term primitives that match
// ScreenUp in docs/design/mockups/screens-lifecycle.jsx. When
// stdout is NOT a TTY (pipes, CI, bytes.Buffer test injection),
// we keep the historical plain-text output so:
//   - existing golden tests still pass byte-for-byte,
//   - piping (`dpm localnet up | tee log.txt`) doesn't leak
//     ANSI escapes into the log file,
//   - CI logs stay greppable.
//
// The TTY check happens once at construction (via NewTextProgress)
// so callers that handcraft a TextProgress{OutW: …, ErrW: …}
// without setting tty get the plain path — matches the prior
// behavior. Use NewTextProgress for the rich path.
type TextProgress struct {
	OutW io.Writer
	ErrW io.Writer
	// tty controls whether StartStep / Done emit the boxed,
	// glyph-prefixed rendering from BIT-121's term primitives.
	// False (default) keeps the historical plain output so the
	// suite of golden tests built against the unstyled bytes
	// keeps passing.
	tty bool
}

// NewTextProgress constructs a TextProgress and auto-detects
// whether out is a TTY. CLI entrypoints (`dpm localnet up`)
// should use this constructor so an interactive run gets the
// styled output. Tests + non-TTY callers can keep using the
// literal `&TextProgress{OutW: buf, ErrW: buf}` form — the
// plain path stays the default.
func NewTextProgress(out, errw io.Writer) *TextProgress {
	return &TextProgress{
		OutW: out,
		ErrW: errw,
		tty:  isTTY(out),
	}
}

// isTTY indirects through the term package so this file doesn't
// have to import isatty directly. Returns false when out isn't an
// *os.File (e.g. bytes.Buffer in tests) — that's the desired
// "fall back to plain" path.
func isTTY(out io.Writer) bool {
	return term.IsTerminal(out)
}

// stepLabel maps the typed Step to the human-readable phrase the
// CLI prints. Kept as a map (not switch) so adding a new step is a
// one-line edit; the compiler-checked Step type protects against
// typos.
//
// Frontend has the same map keyed by the SAME tokens
// (webui-create.jsx::stepLabels). A drift test (BIT-163c) will
// verify cross-language parity once SSEProgress lands.
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
// header line for. Mirrors the three lines today's up.go emits
// directly:
//
//	"Running preflight checks..."
//	"Starting services..."
//	"Waiting for services to become healthy..."
//
// The other five steps (resolve, lock, fetch, persist, capture
// JWTs) happen silently in the CLI today — either because the
// underlying call writes its own progress (splice.Fetch streams
// download dots into Out()) or because they're fast enough to not
// warrant a line. Keeping the allowlist here means BIT-163b's
// refactor can issue StartStep for ALL steps unconditionally;
// TextProgress filters to the visible ones, and the upcoming
// SSEProgress impl emits every event.
//
// Adding a step here is a deliberate CLI behaviour change — it'll
// add a new line to `localnet up` output. Discuss before doing it.
var textVisibleSteps = map[Step]bool{
	StepPreflight:     true,
	StepStartServices: true,
	StepWaitHealthy:   true,
}

// StartStep prints the step's header line — but ONLY for steps in
// textVisibleSteps. The five silent steps (resolve, lock, fetch,
// persist, capture_jwts) happen invisibly in the CLI today; the
// orchestrator calls StartStep unconditionally and TextProgress
// filters. SSEProgress (future) emits an event for every call.
//
// detail is appended in parentheses when non-empty so the user
// sees context (e.g. the resolved splice tag).
func (t *TextProgress) StartStep(step Step, detail string) {
	if !textVisibleSteps[step] {
		return
	}
	label := labelFor(step)
	if !t.tty {
		// Plain path — historical byte-stable output for non-TTY
		// callers (tests, pipes, CI).
		if detail == "" {
			_, _ = fmt.Fprintf(t.OutW, "%s...\n", label)
			return
		}
		_, _ = fmt.Fprintf(t.OutW, "%s (%s)...\n", label, detail)
		return
	}
	// BIT-122 styled path — mockup-aligned section header. Maps
	// to the `┌─ preflight ────…` / `┌─ services ─────…` block
	// headers in ScreenUp; we surface the same per-step label
	// the plain path uses for the section title so a user
	// flipping between modes sees the same vocabulary.
	right := ""
	if detail != "" {
		right = detail
	}
	body := term.Dimc("(running…)")
	_, _ = fmt.Fprintln(t.OutW)
	_, _ = fmt.Fprintln(t.OutW, term.Section(label, right, body, 0))
}

// UpdateStep is a no-op in the CLI: the existing up.go doesn't
// print mid-step progress. The mockup's "11/15 containers up" line
// is rendered by SSEProgress only. We deliberately do NOT print
// here — adding a line every percent tick would spam the terminal
// during a clean run.
func (t *TextProgress) UpdateStep(_ Step, _ string, _ int) {}

// FinishStep is also a no-op for the CLI today: today's up.go
// signals step completion implicitly by starting the next step.
// SSEProgress emits a typed `step.done` event.
func (t *TextProgress) FinishStep(_ Step, _ string) {}

// FailStep writes a one-line failure to stderr. If cause is
// non-nil, its message follows the summary so the user sees both
// "what we were doing" and "why it broke" — same pattern as the
// existing fmt.Fprintf(errw, "Failed to ...: %s\n", err) sites
// scattered through up.go today.
func (t *TextProgress) FailStep(_ Step, summary string, cause error) {
	if cause == nil {
		_, _ = fmt.Fprintln(t.ErrW, summary)
		return
	}
	_, _ = fmt.Fprintf(t.ErrW, "%s: %s\n", summary, cause)
}

// Warn writes a "warning: ..." line to stderr. Mirrors the existing
// dev-secret warning + JWT capture warnings in up.go.
func (t *TextProgress) Warn(message string) {
	_, _ = fmt.Fprintf(t.ErrW, "warning: %s\n", message)
}

// Done is the success marker. Today's up.go ends with:
//
//	"Canton LocalNet "<name>" (Splice <tag>) is ready."
//
// followed by the endpoint listing. detail carries the full ready-
// line; endpoint listing goes through Out() as a verbatim block.
//
// BIT-122 styled path on a TTY: render the ready-line inside a
// brand-accented Box matching the `✦ LocalNet "<name>" is ready.`
// block in ScreenUp.
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
