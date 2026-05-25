package localnet

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// TestTextProgress_StartStepOutput pins the exact byte sequence
// TextProgress.StartStep produces for each visible step. The
// BIT-163b refactor must keep CLI output byte-identical; this test
// catches drift introduced when the refactor migrates a
// fmt.Fprintf callsite over to progress.StartStep.
//
// "Visible" here = the three steps the existing CLI emits a header
// line for (preflight / start_services / wait_healthy). The five
// silent steps are covered by TestTextProgress_StartStepSilentSteps
// below.
func TestTextProgress_StartStepOutput(t *testing.T) {
	cases := []struct {
		name   string
		step   Step
		detail string
		want   string
	}{
		{
			name:   "preflight, no detail",
			step:   StepPreflight,
			detail: "",
			want:   "Running preflight checks...\n",
		},
		{
			name:   "start_services, no detail",
			step:   StepStartServices,
			detail: "",
			want:   "Starting services...\n",
		},
		{
			name:   "wait_healthy, no detail",
			step:   StepWaitHealthy,
			detail: "",
			want:   "Waiting for services to become healthy...\n",
		},
		{
			name:   "preflight with detail (hypothetical future use)",
			step:   StepPreflight,
			detail: "skipping disk check",
			want:   "Running preflight checks (skipping disk check)...\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errw bytes.Buffer
			p := &TextProgress{OutW: &out, ErrW: &errw}
			p.StartStep(tc.step, tc.detail)
			if got := out.String(); got != tc.want {
				t.Errorf("out = %q, want %q", got, tc.want)
			}
			if errw.Len() != 0 {
				t.Errorf("stderr should be empty, got %q", errw.String())
			}
		})
	}
}

// TestTextProgress_StartStepSilentSteps pins the "silent" contract
// for the five steps the existing CLI doesn't print a header for.
// These steps still happen — and SSEProgress (BIT-163c) will emit
// typed events for them — but TextProgress must produce zero bytes
// or BIT-163b's refactor would accidentally add new lines to
// `localnet up` output.
//
// Adding a step to textVisibleSteps is a deliberate CLI behaviour
// change; this test fails loudly if someone slips one in.
func TestTextProgress_StartStepSilentSteps(t *testing.T) {
	silent := []Step{
		StepResolveVersion,
		StepAcquireLock,
		StepFetchSplice,
		StepPersistState,
		StepCaptureJWTs,
	}
	for _, s := range silent {
		t.Run(string(s), func(t *testing.T) {
			var out, errw bytes.Buffer
			p := &TextProgress{OutW: &out, ErrW: &errw}
			p.StartStep(s, "any detail")
			if out.Len() != 0 {
				t.Errorf("Step %q is supposed to be silent in CLI; got %q",
					s, out.String())
			}
			if errw.Len() != 0 {
				t.Errorf("Step %q stderr should be empty; got %q",
					s, errw.String())
			}
		})
	}
}

// TestTextProgress_FailStep pins the failure-line shape — the
// existing up.go writes "Failed to X: <cause>" or just "X" when
// cause is nil. The future SSEProgress impl needs to know the
// expected separator so the typed event carries the same prose.
func TestTextProgress_FailStep(t *testing.T) {
	cases := []struct {
		name    string
		summary string
		cause   error
		want    string
	}{
		{
			name:    "with cause",
			summary: "Failed to start services",
			cause:   errors.New("compose exit 1"),
			want:    "Failed to start services: compose exit 1\n",
		},
		{
			name:    "nil cause",
			summary: "Preflight failed. Address the items above and re-run.",
			cause:   nil,
			want:    "Preflight failed. Address the items above and re-run.\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errw bytes.Buffer
			p := &TextProgress{OutW: &out, ErrW: &errw}
			p.FailStep(StepStartServices, tc.summary, tc.cause)
			if got := errw.String(); got != tc.want {
				t.Errorf("stderr = %q, want %q", got, tc.want)
			}
			if out.Len() != 0 {
				t.Errorf("stdout should be empty, got %q", out.String())
			}
		})
	}
}

// TestTextProgress_DoneEmitsSurroundingBlankLines pins the success
// marker shape — the existing up.go writes a leading newline before
// "ready" and a trailing newline before the endpoint block. SSE
// consumers don't render whitespace the same way, but for CLI byte
// parity we must keep both newlines.
func TestTextProgress_Done(t *testing.T) {
	var out, errw bytes.Buffer
	p := &TextProgress{OutW: &out, ErrW: &errw}
	p.Done(`Canton LocalNet "demo" (Splice 0.4.12) is ready.`)
	want := "\nCanton LocalNet \"demo\" (Splice 0.4.12) is ready.\n\n"
	if got := out.String(); got != want {
		t.Errorf("out = %q, want %q", got, want)
	}
}

// TestTextProgress_DoneEmptyDetailIsNoop — calling Done with no
// detail shouldn't print anything. SSEProgress will use this path
// to publish a "done" event without text.
func TestTextProgress_DoneEmptyDetailIsNoop(t *testing.T) {
	var out, errw bytes.Buffer
	p := &TextProgress{OutW: &out, ErrW: &errw}
	p.Done("")
	if out.Len() != 0 || errw.Len() != 0 {
		t.Errorf("expected silence; out=%q errw=%q", out.String(), errw.String())
	}
}

// TestTextProgress_WarnPrefix — every warning carries a "warning:"
// prefix on stderr. Mirrors the existing dev-secret + JWT-capture
// warning lines in up.go so the refactor doesn't drop the prefix.
func TestTextProgress_WarnPrefix(t *testing.T) {
	var out, errw bytes.Buffer
	p := &TextProgress{OutW: &out, ErrW: &errw}
	p.Warn("using uncurated Splice tag x; not tested by DevKit")
	want := "warning: using uncurated Splice tag x; not tested by DevKit\n"
	if got := errw.String(); got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// TestTextProgress_UpdateAndFinishAreNoops — the CLI doesn't print
// mid-step progress or per-step completion lines today (the next
// step's StartStep IS the implicit "previous step done" signal).
// Pinning this contract ensures BIT-163b doesn't accidentally add
// "% complete" spam to the terminal.
func TestTextProgress_UpdateAndFinishAreNoops(t *testing.T) {
	var out, errw bytes.Buffer
	p := &TextProgress{OutW: &out, ErrW: &errw}
	p.UpdateStep(StepStartServices, "11/15 containers up", 71)
	p.FinishStep(StepStartServices, "all healthy")
	if out.Len() != 0 || errw.Len() != 0 {
		t.Errorf("expected CLI silence on UpdateStep/FinishStep; out=%q errw=%q",
			out.String(), errw.String())
	}
}

// TestTextProgress_OutErrEscapeHatches — the verbatim Out/Err
// writers must round-trip exact bytes for already-formatted blocks
// (preflight report tables, docker compose log forwarding,
// endpoint listings). Anything that mangles those would break the
// CLI byte-parity contract.
func TestTextProgress_OutErrEscapeHatches(t *testing.T) {
	var out, errw bytes.Buffer
	p := &TextProgress{OutW: &out, ErrW: &errw}
	blob := "  HOST            STATUS\n  app-provider    ok\n"
	if _, err := io.WriteString(p.Out(), blob); err != nil {
		t.Fatalf("Out write: %v", err)
	}
	if _, err := io.WriteString(p.Err(), "stderr blob\n"); err != nil {
		t.Fatalf("Err write: %v", err)
	}
	if out.String() != blob {
		t.Errorf("Out round-trip mismatch:\ngot:  %q\nwant: %q", out.String(), blob)
	}
	if errw.String() != "stderr blob\n" {
		t.Errorf("Err round-trip mismatch: %q", errw.String())
	}
}

// TestNopProgress_DoesNothing — RunUp's tests use NopProgress when
// they don't care about output. Verify every method is silent and
// the Out/Err writers don't error.
func TestNopProgress_DoesNothing(t *testing.T) {
	p := NopProgress{}
	p.StartStep(StepPreflight, "x")
	p.UpdateStep(StepPreflight, "x", 50)
	p.FinishStep(StepPreflight, "x")
	p.FailStep(StepPreflight, "x", errors.New("y"))
	p.Warn("x")
	p.Done("x")
	if _, err := io.WriteString(p.Out(), "x"); err != nil {
		t.Errorf("Out should accept writes silently: %v", err)
	}
	if _, err := io.WriteString(p.Err(), "x"); err != nil {
		t.Errorf("Err should accept writes silently: %v", err)
	}
}

// TestStepConstants_FrontendParity is a forward-looking pin: every
// Step constant declared here must show up in the frontend's
// step→label map (webui-create.jsx::stepLabels) once that exists.
//
// Today the frontend lives at docs/design/mockups/webui-create.jsx
// (a JSX mockup, not a TS module). After BIT-163b ships the
// SSEProgress impl and the frontend gains a real step renderer,
// this test should grep the .ts/.tsx source for each token and
// fail if a constant lacks a corresponding entry.
//
// Until then, this test pins the enum membership against the
// labels map so an internal drift (new constant added without
// updating stepLabel) is caught locally.
func TestStepConstants_AllHaveLabels(t *testing.T) {
	want := []Step{
		StepResolveVersion,
		StepAcquireLock,
		StepPreflight,
		StepFetchSplice,
		StepPersistState,
		StepStartServices,
		StepWaitHealthy,
		StepCaptureJWTs,
	}
	for _, s := range want {
		if _, ok := stepLabel[s]; !ok {
			t.Errorf("Step %q has no label entry in stepLabel; add one to keep CLI output structured", s)
		}
	}
	if len(stepLabel) != len(want) {
		// Any extra entry in stepLabel that doesn't have a
		// corresponding constant is a typo or stale label.
		var extras []string
		known := make(map[Step]bool, len(want))
		for _, s := range want {
			known[s] = true
		}
		for k := range stepLabel {
			if !known[k] {
				extras = append(extras, string(k))
			}
		}
		t.Errorf("stepLabel has extras not in the Step constants: %s", strings.Join(extras, ", "))
	}
}
