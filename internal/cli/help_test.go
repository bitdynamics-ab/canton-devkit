package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/cli/localnet"
)

// TestLocalnetHelp_RendersMockupShape verifies the help body contains
// the distinctive elements of the custom layout:
//
// - ASCII box header
// - "lifecycle" + "developing" section titles (uppercased by Section)
// - a usage line
// - a footer mentioning command-specific flags
//
// We don't pin byte-exact output because lipgloss-rendered colors are
// terminal-profile-dependent; substring assertions are robust.
func TestLocalnetHelp_RendersMockupShape(t *testing.T) {
	var out, errBuf bytes.Buffer
	app := New(&out, &errBuf, "test", "")
	code := app.Run([]string{"localnet", "--help"})
	if code != 0 {
		t.Fatalf("help returned %d, want 0; stderr=%q", code, errBuf.String())
	}
	body := out.String()

	for _, want := range []string{
		"canton-devkit",
		"localnet",
		"LIFECYCLE",
		"DEVELOPING",
		"Usage",
		"--name",
		"--format=json",
		"NO_COLOR=1",
		"up", "start", "stop", "down", "status", "logs", "snapshot", "dar",
		"restart", "remove", "skills", "token",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("help body missing %q\nfull:\n%s", want, body)
		}
	}
}

// TestLocalnetHelp_MatchesWiredCommandSet pins the hand-written
// helpCategories() list to the live cobra tree in BOTH directions:
//
//   - every visible command registered in localnet.Build() must have
//     a help row, or users can't discover it from --help;
//   - every help row must correspond to a registered command, so the
//     help never advertises commands that aren't wired yet.
//
// Cobra's auto-added help/completion builtins and hidden commands are
// exempt — they're not part of the curated surface.
func TestLocalnetHelp_MatchesWiredCommandSet(t *testing.T) {
	wired := make(map[string]bool)
	for _, cmd := range localnet.Build(false).Commands() {
		if cmd.Hidden || cmd.Name() == "help" || cmd.Name() == "completion" {
			continue
		}
		wired[cmd.Name()] = true
	}
	if len(wired) == 0 {
		t.Fatal("localnet.Build() registered no visible commands — enumeration broken?")
	}

	listed := make(map[string]bool)
	for _, cat := range helpCategories() {
		for _, row := range cat.Commands {
			// Rows name top-level verbs; a future multi-word row
			// (e.g. "container restart") counts under its first token.
			name := strings.Fields(row.name)[0]
			if listed[name] {
				t.Errorf("help row %q listed more than once", name)
			}
			listed[name] = true
		}
	}

	for name := range wired {
		if !listed[name] {
			t.Errorf("command %q is wired in localnet.Build() but missing from helpCategories() — users can't discover it from --help", name)
		}
	}
	for name := range listed {
		if !wired[name] {
			t.Errorf("helpCategories() advertises %q but no such command is registered in localnet.Build()", name)
		}
	}
}

// TestLocalnetHelp_AdvertisedCommandsAreWired makes the hand-written
// category list prove its own safety: every listed row must resolve to
// an actual Cobra command, and it must not be one of the placeholder
// DisableFlagParsing stubs.
func TestLocalnetHelp_AdvertisedCommandsAreWired(t *testing.T) {
	var out, errBuf bytes.Buffer
	root := New(&out, &errBuf, "test", "").buildRoot()
	for _, cat := range helpCategories() {
		for _, row := range cat.Commands {
			args := append([]string{"localnet"}, strings.Fields(row.name)...)
			cmd, _, err := root.Find(args)
			if err != nil {
				t.Errorf("advertised command %q does not resolve: %v", row.name, err)
				continue
			}
			if cmd == nil || cmd.CommandPath() != appName+" localnet "+row.name {
				t.Errorf("advertised command %q resolved to %q", row.name, cmd.CommandPath())
				continue
			}
			if cmd.DisableFlagParsing {
				t.Errorf("advertised command %q is still a placeholder stub", row.name)
			}
		}
	}
}

// TestRenderHelpRow_AlignsAcrossLengths makes sure the fixed-width
// name column produces aligned output regardless of command-name
// length. Without this, a long name like "snapshot" would shift its
// description right and the two-column layout breaks visually.
func TestRenderHelpRow_AlignsAcrossLengths(t *testing.T) {
	short := renderHelpRow("up", "x")
	longer := renderHelpRow("doctor", "y")
	posShort := strings.LastIndex(short, "x")
	posLong := strings.LastIndex(longer, "y")
	if posShort != posLong {
		t.Errorf("description columns misaligned:\n  short %q (pos=%d)\n  long  %q (pos=%d)",
			short, posShort, longer, posLong)
	}
}

// TestDynamicBox_AutoSizesToContent pins that the box auto-sizes to
// the widest line: a fixed-width box would silently truncate any
// title longer than its hardcoded width.
func TestDynamicBox_AutoSizesToContent(t *testing.T) {
	short := dynamicBox([]string{"abc"})
	long := dynamicBox([]string{"this title is significantly longer than 36 characters"})

	// Top/bottom bars equal in length, and the long box's bar
	// must exceed the short box's bar by the difference in
	// content widths.
	if len(short[0]) >= len(long[0]) {
		t.Errorf("long box should be wider than short box:\n  short=%d %q\n  long =%d %q",
			len(short[0]), short[0], len(long[0]), long[0])
	}
	// Body lines must be padded to the same width as the top bar
	// (every line in the box has identical visible-cell length).
	for _, line := range long {
		if utf8RuneCount(line) != utf8RuneCount(long[0]) {
			t.Errorf("dynamic-box line width drift: top=%q line=%q",
				long[0], line)
		}
	}
}

// TestLocalnetHelp_LazyRenderRespectsLatePaletteChange covers the
// "ANSI cached at install time" hazard: if NO_COLOR is set after
// process start (e.g. a wrapper script that rewrites env between
// subcommand invocations), the help output must honor it. Caching the
// render at applyHelp time would ignore later env changes.
func TestLocalnetHelp_LazyRenderRespectsLatePaletteChange(t *testing.T) {
	// First call with default profile to ensure the lazy path
	// is exercised at least once.
	var out1, errBuf1 bytes.Buffer
	_ = New(&out1, &errBuf1, "test", "").Run([]string{"localnet", "--help"})

	t.Setenv("NO_COLOR", "1")
	var out2, errBuf2 bytes.Buffer
	_ = New(&out2, &errBuf2, "test", "").Run([]string{"localnet", "--help"})

	// The first output may or may not contain ANSI depending on the
	// test environment's TTY detection; we only pin that after
	// NO_COLOR is set the output has no ANSI CSI sequences. (A render
	// cached at install time would make out2 equal out1 byte-for-byte
	// regardless of NO_COLOR.)
	if strings.Contains(out2.String(), "\x1b[") {
		t.Errorf("NO_COLOR=1 should produce ANSI-free output, got:\n%s", out2.String())
	}
}

// TestLocalnetHelp_NarrowTerminalFallsBackToSingleLine pins width
// adaptation: COLUMNS under narrowFallbackCols must produce the plain
// fallback (no box, no "Usage ", no Section titles) — the boxed layout
// wraps to garbage at narrow widths.
func TestLocalnetHelp_NarrowTerminalFallsBackToSingleLine(t *testing.T) {
	t.Setenv("COLUMNS", "30")
	var out, errBuf bytes.Buffer
	app := New(&out, &errBuf, "test", "")
	if code := app.Run([]string{"localnet", "--help"}); code != 0 {
		t.Fatalf("help returned %d, stderr=%q", code, errBuf.String())
	}
	body := out.String()
	if strings.Contains(body, "Usage  ") {
		t.Errorf("narrow help should omit boxed Usage line, got:\n%s", body)
	}
	if strings.Contains(body, "LIFECYCLE") {
		t.Errorf("narrow help should omit Section titles, got:\n%s", body)
	}
	// Should still list commands.
	if !strings.Contains(body, "up") || !strings.Contains(body, "doctor") {
		t.Errorf("narrow help should still list commands, got:\n%s", body)
	}
}

// TestLocalnetHelp_WideTerminalClampsBoxToMax verifies that a generous
// COLUMNS (>= narrowFallbackCols) renders the boxed layout — the
// inverse of the narrow case.
func TestLocalnetHelp_WideTerminalClampsBoxToMax(t *testing.T) {
	t.Setenv("COLUMNS", "120")
	var out, errBuf bytes.Buffer
	app := New(&out, &errBuf, "test", "")
	_ = app.Run([]string{"localnet", "--help"})
	body := out.String()
	if !strings.Contains(body, "LIFECYCLE") {
		t.Errorf("wide help should render Section titles, got:\n%s", body)
	}
}

// TestLocalnetHelp_AsciiBoxWhenNoColor: with NO_COLOR set, the help
// box must use ASCII glyphs (+, -, |) instead of Unicode (┌, ─, │) —
// CI logs with LANG=C render U+2500 as garbage and the box looks
// broken.
func TestLocalnetHelp_AsciiBoxWhenNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("COLUMNS", "120") // force boxed renderer
	var out, errBuf bytes.Buffer
	app := New(&out, &errBuf, "test", "")
	_ = app.Run([]string{"localnet", "--help"})
	body := out.String()
	// Scope the assertion to the top header (the dynamicBox output)
	// — the first 6 lines. Section separators below render their
	// own Unicode rule which is a separate concern (term.Section).
	header := strings.Join(strings.SplitN(body, "\n", 7)[:6], "\n")
	if strings.ContainsAny(header, "┌┐└┘│") {
		t.Errorf("NO_COLOR help box should use ASCII corners, got Unicode:\n%s", header)
	}
	if !strings.Contains(header, "+-") || !strings.Contains(header, "-+") {
		t.Errorf("ASCII box should contain +/- corners, got:\n%s", header)
	}
}

// TestLocalnetHelp_AsciiBoxWhenCLocale pins the locale branch of the
// box-glyph fallback. NO_COLOR is not the only unsafe terminal: LANG=C
// / LC_ALL=C frequently renders U+2500 box glyphs as garbage.
func TestLocalnetHelp_AsciiBoxWhenCLocale(t *testing.T) {
	t.Setenv("LC_ALL", "C")
	t.Setenv("LANG", "C")
	t.Setenv("COLUMNS", "120") // force boxed renderer
	var out, errBuf bytes.Buffer
	app := New(&out, &errBuf, "test", "")
	_ = app.Run([]string{"localnet", "--help"})
	body := out.String()
	header := strings.Join(strings.SplitN(body, "\n", 7)[:6], "\n")
	if strings.ContainsAny(header, "┌┐└┘│") {
		t.Errorf("LANG=C help box should use ASCII corners, got Unicode:\n%s", header)
	}
	if strings.ContainsAny(body, "─·") {
		t.Errorf("LANG=C help should be fully ASCII-safe, got:\n%s", body)
	}
	if !strings.Contains(header, "+-") || !strings.Contains(header, "-+") {
		t.Errorf("ASCII box should contain +/- corners, got:\n%s", header)
	}
}

// TestRenderHelpRow_AlignsWithMultiByteName: renderHelpRow must use
// rune width, not byte length. A multi-byte command name (e.g. an
// i18n alias like "日本") would otherwise count 6 bytes for 2 runes
// of width and shift the description left, misaligning the column.
func TestRenderHelpRow_AlignsWithMultiByteName(t *testing.T) {
	ascii := renderHelpRow("up", "x")
	multi := renderHelpRow("日本", "y")
	posAscii := strings.LastIndex(ascii, "x")
	posMulti := strings.LastIndex(multi, "y")
	// Byte positions will differ (multi has 6 bytes for the name vs
	// 2), but the count of trailing-space padding before the desc
	// should reflect rune-width alignment. Easy check: strip ANSI +
	// count runes before the desc letter.
	runesBefore := func(s, marker string) int {
		idx := strings.Index(s, marker)
		if idx < 0 {
			return -1
		}
		return len([]rune(s[:idx]))
	}
	if runesBefore(ascii, "x") != runesBefore(multi, "y") {
		t.Errorf("name column not rune-aligned:\n  ascii %q (runes=%d, pos=%d)\n  multi %q (runes=%d, pos=%d)",
			ascii, runesBefore(ascii, "x"), posAscii,
			multi, runesBefore(multi, "y"), posMulti)
	}
}

// TestLocalnetSubcommandHelp_UsesCobraDefault: the help override is
// scoped to the `localnet` subgroup ONLY — child commands (`localnet
// up --help`, etc.) must fall through to cobra's default
// flag-listing template. If the parent override bled down, child
// help would render the section listing instead of the command's own
// --help flags block.
func TestLocalnetSubcommandHelp_UsesCobraDefault(t *testing.T) {
	var out, errBuf bytes.Buffer
	app := New(&out, &errBuf, "test", "")
	code := app.Run([]string{"localnet", "up", "--help"})
	if code != 0 {
		t.Fatalf("up --help returned %d, stderr=%q", code, errBuf.String())
	}
	body := out.String()
	if strings.Contains(body, "LIFECYCLE") {
		t.Errorf("`localnet up --help` should NOT show parent's LIFECYCLE section, got:\n%s", body)
	}
	// Cobra's default flag block uses "Usage:" with a colon (not
	// "Usage " with two spaces like our custom template).
	if !strings.Contains(body, "Usage:") {
		t.Errorf("`localnet up --help` should use cobra's default template (with `Usage:`), got:\n%s", body)
	}
}

// utf8RuneCount avoids importing unicode/utf8 for one call.
func utf8RuneCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
