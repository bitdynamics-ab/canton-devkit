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
// Exported as a constant so tests can assert presence structurally
// (`strings.Count(out, string(BoxLeftBorderRune))`) instead of hard-
// coding the literal "┃" — lets a future Windows ASCII profile
// substitute "|" without breaking tests. Reviewer pin on PR #31 #9.
const BoxLeftBorderRune = '┃'

// Box renders a left-accented, padded block — the "READY" /
// "BREAKING" callout style from the mockups. Equivalent to:
//
//	┃  ✓  LocalNet "hubble" is ready.
//	┃     Run `dpm localnet env --name hubble` to export config.
//
// The left bar uses a single-cell border so the box doesn't waste
// horizontal space on a wide terminal. Per the JSX Box pattern, we
// keep the right edge open — content flows naturally and we don't
// have to compute terminal width.
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
	// lipgloss will pad each line of body to its longest line so the
	// border is consistent — that's what we want for a multi-line
	// box. Trim trailing newlines so callers can chain Box() with
	// fmt.Fprintln without doubling the gap.
	return style.Render(strings.TrimRight(body, "\n"))
}
