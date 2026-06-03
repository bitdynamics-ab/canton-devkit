package term

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// forcePlain swaps the renderer to an ASCII-profile one bound to
// io.Discard so every style call drops its color codes. Tests that
// want to assert byte-exact output set this up in a setup helper
// and restore the default at the end.
func forcePlain(t *testing.T) {
	t.Helper()
	prev := R()
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.Ascii)
	SetRenderer(r)
	t.Cleanup(func() { SetRenderer(prev) })
}

func TestStep_ContainsLabelAndDetail(t *testing.T) {
	forcePlain(t)
	got := Step(StepCheck, "Docker daemon", "v25.0.5", "0.2s")
	for _, want := range []string{"Docker daemon", "v25.0.5", "0.2s", Glyphs.Check} {
		if !strings.Contains(got, want) {
			t.Errorf("Step output missing %q: %q", want, got)
		}
	}
}

func TestStep_ElidesEmptyDetailAndElapsed(t *testing.T) {
	forcePlain(t)
	got := Step(StepWarn, "CPU", "", "")
	// Should still render the glyph + label exactly once, no double
	// spaces from missing detail/elapsed.
	if !strings.Contains(got, Glyphs.Warn) {
		t.Errorf("missing warn glyph: %q", got)
	}
	if !strings.Contains(got, "CPU") {
		t.Errorf("missing label: %q", got)
	}
	if strings.Contains(got, "   ") {
		t.Errorf("triple-space residue from missing detail/elapsed: %q", got)
	}
}

func TestKV_DefaultKeyWidth(t *testing.T) {
	forcePlain(t)
	got := KV("Name", "hubble", 0)
	// Default width is 14 → "Name" + 10 spaces + "  " separator + value
	if !strings.HasPrefix(got, "Name          ") {
		t.Errorf("KV did not pad to default width 14: %q", got)
	}
	if !strings.HasSuffix(got, "hubble") {
		t.Errorf("KV did not end with value: %q", got)
	}
}

func TestKV_CustomKeyWidth(t *testing.T) {
	forcePlain(t)
	got := KV("ID", "x", 4)
	if !strings.HasPrefix(got, "ID  ") {
		t.Errorf("KV did not pad to width 4: %q", got)
	}
}

func TestSection_HasUppercaseHeaderAndIndentsChildren(t *testing.T) {
	forcePlain(t)
	got := Section("services", "auto-refresh 2s", "row a\nrow b", 0)
	if !strings.Contains(got, "SERVICES") {
		t.Errorf("title should be uppercased: %q", got)
	}
	if !strings.Contains(got, "auto-refresh 2s") {
		t.Errorf("right hint missing: %q", got)
	}
	if !strings.Contains(got, "  row a") || !strings.Contains(got, "  row b") {
		t.Errorf("children should be indented two spaces: %q", got)
	}
}

func TestBox_RendersLeftAccentAndBody(t *testing.T) {
	forcePlain(t)
	got := Box(BoxBrand, "✦ ready.")
	if !strings.Contains(got, "ready.") {
		t.Errorf("body missing: %q", got)
	}
	// Structural assertion via BoxLeftBorderRune (PR #31 #9).
	if !strings.ContainsRune(got, BoxLeftBorderRune) {
		t.Errorf("expected left border (rune=%q), got %q", BoxLeftBorderRune, got)
	}
}

func TestTable_PadsColumns(t *testing.T) {
	forcePlain(t)
	got := Table(
		[]Column{{Label: "name"}, {Label: "state"}},
		[][]string{
			{"hubble", "running"},
			{"weiss-much-longer-name", "stopped"},
		},
	)
	// The "hubble" cell should be padded out so "running" aligns
	// with "stopped" on the next row.
	lines := strings.Split(got, "\n")
	if len(lines) < 4 {
		t.Fatalf("expected ≥4 lines (header, separator, 2 rows), got %d: %q", len(lines), got)
	}
	// Find the column position of "running" and "stopped"; they
	// should land at the same offset.
	pHubble := strings.Index(lines[2], "running")
	pWeiss := strings.Index(lines[3], "stopped")
	if pHubble != pWeiss {
		t.Errorf("columns misaligned: 'running' at %d, 'stopped' at %d\n%s", pHubble, pWeiss, got)
	}
}

func TestVisibleLen_StripsANSI(t *testing.T) {
	// Manually inject a CSI escape so the test doesn't depend on
	// renderer state.
	raw := "\x1b[31mhello\x1b[0m"
	if got := VisibleLen(raw); got != 5 {
		t.Errorf("VisibleLen(%q) = %d, want 5", raw, got)
	}
}

func TestShouldColor_HonorsNO_COLOR(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	// File arg type but we just want the env branch to trigger.
	if ShouldColor(nil) {
		t.Error("NO_COLOR should disable color")
	}
}

func TestShouldColor_AppOverride(t *testing.T) {
	// t.Setenv saves+restores the variable around the test, so the
	// explicit Unsetenv here is the right way to clear NO_COLOR that
	// may have leaked from another test's t.Setenv("NO_COLOR", "1").
	_ = os.Unsetenv("NO_COLOR")
	t.Setenv("CANTON_DEVKIT_NO_COLOR", "1")
	if ShouldColor(nil) {
		t.Error("CANTON_DEVKIT_NO_COLOR should disable color")
	}
}

// TestShouldColor_NonFileWriter pins the gate behaviour for an
// io.Writer that isn't *os.File (e.g. bytes.Buffer used in tests
// or io.Discard). MUST return false — anything else risks ANSI
// codes leaking into captured output. Reviewer pin on PR #31 #4.
func TestShouldColor_NonFileWriter(t *testing.T) {
	_ = os.Unsetenv("NO_COLOR")
	_ = os.Unsetenv("CANTON_DEVKIT_NO_COLOR")
	var buf bytes.Buffer
	if ShouldColor(&buf) {
		t.Error("ShouldColor on a non-file writer must return false")
	}
}

// ── Spinner contract (PR #31 #4 + #8) ──────────────────────────

// TestSpinner_UpdateAdvancesFrameOnTick pins the rotation:
// sending a SpinnerTickMsg must increment the frame counter on
// the RETURNED Spinner (value receiver).
func TestSpinner_UpdateAdvancesFrameOnTick(t *testing.T) {
	s := Spinner{Label: "x"}
	if s.frame != 0 {
		t.Fatalf("initial frame = %d, want 0", s.frame)
	}
	s2, _ := s.Update(SpinnerTickMsg{})
	if s2.frame != 1 {
		t.Errorf("after one tick, frame = %d, want 1", s2.frame)
	}
}

// TestSpinner_UpdateWrapsAtFrameEnd pins the modulo: at the last
// frame, one more tick wraps to 0 (rotation, not overflow).
func TestSpinner_UpdateWrapsAtFrameEnd(t *testing.T) {
	s := Spinner{Label: "x"}
	s.frame = len(SpinnerFrames) - 1
	s2, _ := s.Update(SpinnerTickMsg{})
	if s2.frame != 0 {
		t.Errorf("wrap-around frame = %d, want 0", s2.frame)
	}
}

// TestSpinner_UpdateIgnoresUnknownMsg pins the embedded-Model
// safety contract: unknown messages must pass through unchanged
// so a parent Model's own dispatch isn't perturbed.
func TestSpinner_UpdateIgnoresUnknownMsg(t *testing.T) {
	s := Spinner{Label: "x", frame: 3}
	s2, cmd := s.Update("not a tick — embedded parent's msg")
	if s2.frame != 3 {
		t.Errorf("unknown msg changed frame: %d → %d", s.frame, s2.frame)
	}
	if cmd != nil {
		t.Errorf("unknown msg returned a non-nil tea.Cmd")
	}
}

// TestSpinner_ReassignmentContract pins the value-receiver
// contract from the godoc Example block. Without re-assignment,
// the caller's spinner instance does NOT advance. Reviewer pin
// on PR #31 #8.
func TestSpinner_ReassignmentContract(t *testing.T) {
	s := Spinner{Label: "x"}
	// Drop the return values twice — simulates a careless
	// caller who forgot to reassign.
	_, _ = s.Update(SpinnerTickMsg{})
	_, _ = s.Update(SpinnerTickMsg{})
	if s.frame != 0 {
		t.Errorf("dropped return advanced caller's frame to %d — value receiver contract broken", s.frame)
	}
	// Now do it correctly.
	s, _ = s.Update(SpinnerTickMsg{})
	if s.frame != 1 {
		t.Errorf("after reassignment, frame = %d, want 1", s.frame)
	}
}

// ── Prompt defaults (PR #31 #4) ────────────────────────────────

// TestPrompt_DefaultsWhenFieldsEmpty pins the L83-92 default
// substitution contract — empty user/host/dir must be replaced
// with dev / devnet / ~/canton-app respectively.
func TestPrompt_DefaultsWhenFieldsEmpty(t *testing.T) {
	forcePlain(t)
	got := Prompt("", "", "", "ls")
	for _, want := range []string{"dev", "devnet", "~/canton-app", "ls"} {
		if !strings.Contains(got, want) {
			t.Errorf("default-fill failed: missing %q in %q", want, got)
		}
	}
}

// ── KV rune-count (PR #31 #6) ──────────────────────────────────

// TestKV_MultiByteKeyAlignment pins the rune-count fix: "日本"
// (6 bytes / 2 runes) and "abcd" (4 runes) must align in the
// value column when padded to the same key width. Without the
// fix, len() over-pads "日本" by 4 spaces because bytes ≠ runes.
func TestKV_MultiByteKeyAlignment(t *testing.T) {
	forcePlain(t)
	jp := KV("日本", "value", 4)
	en := KV("abcd", "value", 4)
	// Find where "value" starts in each — must be at the same offset.
	jpPos := strings.Index(jp, "value")
	enPos := strings.Index(en, "value")
	// Strings differ in BYTE position because the multi-byte key
	// has more bytes; what we actually want is the same RUNE
	// position. Convert via rune slice.
	jpRunePos := utf8RuneIndexOfSubstring(jp, "value")
	enRunePos := utf8RuneIndexOfSubstring(en, "value")
	if jpRunePos != enRunePos {
		t.Errorf("multi-byte key misaligned: 日本 puts value at rune %d (byte %d), abcd at rune %d (byte %d)",
			jpRunePos, jpPos, enRunePos, enPos)
	}
}

// utf8RuneIndexOfSubstring returns the rune offset (not byte
// offset) where sub starts in s, or -1 if not found.
func utf8RuneIndexOfSubstring(s, sub string) int {
	byteIdx := strings.Index(s, sub)
	if byteIdx < 0 {
		return -1
	}
	// Count runes in the prefix.
	runeIdx := 0
	for i := range s {
		if i >= byteIdx {
			return runeIdx
		}
		runeIdx++
	}
	return runeIdx
}

// ── Section width arg (PR #31 #7) ──────────────────────────────

// TestSection_SeparatorMatchesWidthArg pins the separator-length
// contract: width=N produces exactly N "─" runes in the separator
// line; width=0 falls back to the auto-size.
func TestSection_SeparatorMatchesWidthArg(t *testing.T) {
	forcePlain(t)

	got40 := Section("svcs", "", "body", 40)
	if n := strings.Count(got40, "─"); n != 40 {
		t.Errorf("width=40: separator has %d runes, want 40", n)
	}

	// width=0 → auto-size. We assert the bounds (>=20, <=80) per
	// the documented contract, not a single number.
	got0 := Section("services", "auto-refresh 2s", "body", 0)
	n := strings.Count(got0, "─")
	if n < 20 || n > 80 {
		t.Errorf("width=0 auto-size: separator has %d runes, want 20..80", n)
	}
}

// ── Box border structural assertion (PR #31 #9) ────────────────

// TestBox_LeftBorderUsesExportedRune asserts presence
// structurally via BoxLeftBorderRune rather than the literal
// glyph. Lets a future Windows ASCII profile substitute the rune
// without breaking this test (or the Box decision-table tests
// that previously hard-coded "┃").
func TestBox_LeftBorderUsesExportedRune(t *testing.T) {
	forcePlain(t)
	got := Box(BoxBrand, "line1\nline2\nline3")
	if c := strings.Count(got, string(BoxLeftBorderRune)); c < 3 {
		t.Errorf("Box body has 3 lines → expected ≥3 border runes, got %d in %q",
			c, got)
	}
}
