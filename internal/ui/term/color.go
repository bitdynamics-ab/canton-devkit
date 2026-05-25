// Package term renders the canton-devkit CLI surface — boxes, steps,
// sections, tables, prompts, spinners — to a terminal in a style that
// matches the JSX mockups under docs/design/mockups/.
//
// The package is a thin layer over charmbracelet/lipgloss: it owns the
// color palette and the small set of higher-level composite renderers
// (Section, Box, Step, KV, Table, Prompt) that the CLI commands reuse.
//
// Honor contract:
//   - NO_COLOR env, --no-color flag, and non-TTY stdout all force a
//     plain-text codepath. Output stays grep-able and CI-stable.
//   - Every primitive returns a string. Side-effect writes belong to
//     the caller so tests can assert on output without an io.Writer.
package term

import "github.com/charmbracelet/lipgloss"

// Palette mirrors the TERM.* token set in
// docs/design/mockups/terminal.jsx so the rendered CLI is visually
// identical to the JSX prototypes. Keep the two in lockstep: if a new
// token is added here, mirror it there (and vice-versa) before the
// next mockup review.
var (
	// Backgrounds + neutrals.
	Background     = lipgloss.Color("#0E1116")
	PanelBg        = lipgloss.Color("#13171E")
	PanelBorder    = lipgloss.Color("#1F2630")
	ChromeBg       = lipgloss.Color("#161B22")

	// Text scale, brightest to faintest.
	Text  = lipgloss.Color("#D6DAE0")
	Mid   = lipgloss.Color("#8B95A1")
	Dim   = lipgloss.Color("#6B7380")
	Faint = lipgloss.Color("#3B434F")

	// Brand + accent (CantonDevKit teal/mint).
	Brand     = lipgloss.Color("#5BD7C5")
	BrandSoft = lipgloss.Color("#2A6F66")

	// Semantic colors.
	Success = lipgloss.Color("#5EE2A0")
	Warn    = lipgloss.Color("#F5BF55")
	Error   = lipgloss.Color("#F47C7C")
	Info    = lipgloss.Color("#7CB5F7")
	Magenta = lipgloss.Color("#C4A8F5")
	Amber   = lipgloss.Color("#E8A14E")
	Rose    = lipgloss.Color("#F08FB5")
)

// Glyphs is the single source of truth for the Unicode marks used by
// Step rows, spinners and so on. Mirrors terminal.jsx::Sym.
//
// We keep the spinner frames here too rather than reaching for
// charmbracelet/bubbles/spinner — Step is a non-interactive renderer
// and pulling in a bubbletea spinner just for a static glyph would be
// over-engineering. The bubbletea Model in spinner.go handles the
// animated case.
var Glyphs = struct {
	Check, Cross, Warn, Arrow, Dot, Pending, Busy string
}{
	Check:   "✓",
	Cross:   "✗",
	Warn:    "!",
	Arrow:   "❯",
	Dot:     "•",
	Pending: "○",
	Busy:    "⠹", // single-frame placeholder; Spinner cycles ⠹⠸⠼⠴⠦⠧⠇⠏
}

// SpinnerFrames are the Braille rotation used by the animated Spinner.
// Matches the @keyframes term-spin output in terminal.jsx and the
// default charmbracelet/bubbles/spinner.Dot set.
var SpinnerFrames = []string{"⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
