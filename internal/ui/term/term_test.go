package term

import (
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
	got := Section("services", "auto-refresh 2s", "row a\nrow b")
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
	// The left accent character should appear at the start of the line.
	if !strings.Contains(got, "┃") {
		t.Errorf("expected ┃ left border, got %q", got)
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
	if got := visibleLen(raw); got != 5 {
		t.Errorf("visibleLen(%q) = %d, want 5", raw, got)
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
