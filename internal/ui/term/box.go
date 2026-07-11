package term

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// BoxKind selects the accent color of a Box. Mirrors the
// `accent={TERM.brand|warn|error|success}` prop set in the JSX Box
// component (terminal.jsx::Box).
type BoxKind int

const (
	BoxBrand BoxKind = iota
	BoxSuccess
	BoxWarn
	BoxError
	BoxInfo
)

// BoxLeftBorderRune is the glyph Box uses for its left accent bar.
// Exported so tests can assert presence structurally instead of
// hard-coding the literal "┃" — a future ASCII profile can
// substitute "|" without breaking tests.
const BoxLeftBorderRune = '┃'

// Box renders a left-accented, padded block — the "READY" /
// "BREAKING" callout style from the mockups:
//
//	┃  ✓  LocalNet "hubble" is ready.
//	┃     Run `dpm localnet env hubble` to export config.
//
// The right edge is left open so content flows naturally and the
// renderer never needs to know the terminal width.
func Box(kind BoxKind, body string) string {
	var accent lipgloss.Color
	switch kind {
	case BoxBrand:
		accent = Brand
	case BoxSuccess:
		accent = Success
	case BoxWarn:
		accent = Warn
	case BoxError:
		accent = Error
	case BoxInfo:
		accent = Info
	default:
		accent = Dim
	}
	style := S().
		BorderStyle(lipgloss.Border{Left: "┃"}).
		BorderLeft(true).
		BorderForeground(accent).
		PaddingLeft(2).
		PaddingRight(0).
		PaddingTop(0).
		PaddingBottom(0)
	// Trim trailing newlines so callers can chain Box() with
	// fmt.Fprintln without doubling the gap.
	return style.Render(strings.TrimRight(body, "\n"))
}
