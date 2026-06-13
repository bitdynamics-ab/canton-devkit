package localnet

import (
	"bytes"
	"strings"
	"testing"
)

// styledProgress builds a TextProgress with the tty flag forced
// on, so the styled rendering branches fire. Tests below
// then assert on the structural markers (Section underline, brand
// glyphs in the Done box) without depending on the exact lipgloss
// ANSI sequences.
//
// The plain-path tests in progress_test.go remain authoritative
// for the non-TTY output bytes; this file covers the styled
// branches that those tests intentionally skip.
func styledProgress(out, err *bytes.Buffer) *TextProgress {
	return &TextProgress{OutW: out, ErrW: err, tty: true}
}

// TestTextProgress_StartStep_Styled_RendersSection pins the
// expectation: visible-step StartStep emits a Section
// header (matching `┌─ preflight ────…` from ScreenUp). We assert
// on the structural marker (the `─` separator and the label),
// not the full ANSI byte sequence — the latter is owned by
// lipgloss + would shift across upgrades.
func TestTextProgress_StartStep_Styled_RendersSection(t *testing.T) {
	var out, errw bytes.Buffer
	p := styledProgress(&out, &errw)
	p.StartStep(StepPreflight, "")

	got := out.String()
	// The Section header uppercases the label.
	if !strings.Contains(got, "RUNNING PREFLIGHT CHECKS") {
		t.Errorf("styled StartStep missing uppercase section title; got:\n%s", got)
	}
	// And renders an underline separator (any run of ─ chars).
	if !strings.Contains(got, "─") {
		t.Errorf("styled StartStep missing Section underline separator; got:\n%s", got)
	}
}

// TestTextProgress_StartStep_Styled_NonVisibleSteps_NoEmit pins
// that the styled path honors textVisibleSteps just like the
// plain path. The 5 silent steps (resolve, lock, fetch, persist,
// capture_jwts) must NOT spew a Section header on the terminal —
// that'd add 5 extra noisy blocks to a happy `up`.
func TestTextProgress_StartStep_Styled_NonVisibleSteps_NoEmit(t *testing.T) {
	silent := []Step{
		StepResolveVersion, StepAcquireLock, StepFetchSplice,
		StepPersistState, StepCaptureJWTs,
	}
	for _, s := range silent {
		t.Run(string(s), func(t *testing.T) {
			var out, errw bytes.Buffer
			p := styledProgress(&out, &errw)
			p.StartStep(s, "")
			if got := out.String(); got != "" {
				t.Errorf("silent step %s emitted output in TTY mode:\n%s",
					s, got)
			}
		})
	}
}

// TestTextProgress_Done_Styled_RendersBox pins the success Box.
// ScreenUp ends with the `✦ LocalNet "<name>" is ready.` block;
// the styled Done must render that glyph + the detail text
// inside a box (asserted via the lipgloss border characters).
func TestTextProgress_Done_Styled_RendersBox(t *testing.T) {
	var out, errw bytes.Buffer
	p := styledProgress(&out, &errw)
	p.Done(`Canton LocalNet "demo" (Splice 0.6.4) is ready.`)

	got := out.String()
	for _, want := range []string{
		"✦",                                     // the brand glyph from the mockup
		`Canton LocalNet "demo" (Splice 0.6.4)`, // detail string verbatim
		"is ready",                              // detail tail
		"localnet env",                          // the secondary CTA line
		"localnet ui",                           // the alt CTA
	} {
		if !strings.Contains(got, want) {
			t.Errorf("styled Done missing %q; got:\n%s", want, got)
		}
	}
}

// TestTextProgress_Done_Styled_EmptyDetailNoop: matches the
// plain path's behavior — Done("") is a no-op so a callsite
// that opts out of the success line doesn't accidentally print
// an empty box. Same contract on both paths.
func TestTextProgress_Done_Styled_EmptyDetailNoop(t *testing.T) {
	var out, errw bytes.Buffer
	p := styledProgress(&out, &errw)
	p.Done("")
	if got := out.String(); got != "" {
		t.Errorf("styled Done(\"\") emitted output:\n%s", got)
	}
}

// TestTextProgress_NoTTY_BytesMatchPlain pins the regression
// guard: callers that build TextProgress with the literal struct
// (or via NewTextProgress against a bytes.Buffer) MUST get the
// historical plain output. This is what keeps every existing
// golden test from breaking under .
//
// Cheap belt-and-braces — the plain output is also exercised by
// the bulk of progress_test.go.
func TestTextProgress_NoTTY_BytesMatchPlain(t *testing.T) {
	var bufLit, bufCtor bytes.Buffer
	pLit := &TextProgress{OutW: &bufLit, ErrW: &bufLit}
	pCtor := NewTextProgress(&bufCtor, &bufCtor)
	pLit.StartStep(StepPreflight, "")
	pCtor.StartStep(StepPreflight, "")
	if bufLit.String() != bufCtor.String() {
		t.Errorf("NewTextProgress on a bytes.Buffer must match the literal-struct path\n  literal: %q\n  ctor:    %q",
			bufLit.String(), bufCtor.String())
	}
	// Sanity: both should be the plain "Running preflight checks...\n".
	if !strings.HasPrefix(bufLit.String(), "Running preflight checks") {
		t.Errorf("non-TTY StartStep didn't emit plain text; got %q", bufLit.String())
	}
}
