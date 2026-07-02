package term

import "strings"

// Column describes one column of a Table. Width is in runes; pass 0
// to auto-size to the widest cell in that column. Align is "left" or
// "right" (anything else is treated as left). Label is rendered in
// the brand-colored uppercase header row.
type Column struct {
	Label string
	Width int
	Align string
}

// Table renders a header row + body rows in the dense, uppercased
// header style of the mockups (terminal.jsx::Table). Cells are
// plain strings — color the cell content before passing it in if you
// want per-cell coloring.
//
// Cells are NOT word-wrapped: a cell wider than its column overflows
// to the right, since wrapping mid-row would scramble the table.
// Callers that need wrapping should split the content into separate
// rows.
func Table(cols []Column, rows [][]string) string {
	// Resolve auto-widths by scanning each column for its widest cell
	// (including the header). We strip ANSI escape sequences before
	// measuring so the colored cells don't get extra padding for
	// invisible bytes.
	widths := make([]int, len(cols))
	for i, c := range cols {
		if c.Width > 0 {
			widths[i] = c.Width
			continue
		}
		widths[i] = VisibleLen(c.Label)
	}
	for _, row := range rows {
		for i := range cols {
			if cols[i].Width > 0 {
				continue
			}
			if i >= len(row) {
				continue
			}
			if n := VisibleLen(row[i]); n > widths[i] {
				widths[i] = n
			}
		}
	}

	var b strings.Builder
	// Header.
	headStyle := S().Foreground(Brand).Bold(true)
	for i, c := range cols {
		if i > 0 {
			b.WriteString("  ")
		}
		b.WriteString(headStyle.Render(padCell(strings.ToUpper(c.Label), widths[i], c.Align)))
	}
	b.WriteString("\n")
	// Faint separator under the header. Total visible width is the
	// sum of widths plus the two-space gutter between columns.
	total := 0
	for i, w := range widths {
		if i > 0 {
			total += 2
		}
		total += w
	}
	if total > 0 {
		b.WriteString(Faintc(strings.Repeat("─", total)))
		b.WriteString("\n")
	}
	// Body.
	for r, row := range rows {
		for i := range cols {
			if i > 0 {
				b.WriteString("  ")
			}
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			b.WriteString(padCell(cell, widths[i], cols[i].Align))
		}
		if r < len(rows)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// padCell pads s to width w. Right-align if align=="right",
// otherwise left-align. Padding uses spaces; we measure visible width
// so ANSI-colored cells pad correctly.
func padCell(s string, w int, align string) string {
	pad := w - VisibleLen(s)
	if pad <= 0 {
		return s
	}
	if align == "right" {
		return strings.Repeat(" ", pad) + s
	}
	return s + strings.Repeat(" ", pad)
}

// VisibleLen returns the rune-count of s with ANSI CSI escape
// sequences (color codes) excluded. lipgloss output wraps cells in
// `\x1b[...m` runs; counting raw bytes overstates the visible width
// and inflates table padding.
//
// Three-state machine — outside, after ESC (expecting `[`), inside
// CSI params (consume until a final byte 0x40-0x7e). This is enough
// for SGR (color) sequences which is all lipgloss emits; non-CSI
// escapes (OSC, DCS) are not handled but lipgloss doesn't produce
// them.
func VisibleLen(s string) int {
	const (
		stOut int = iota
		stAfterEsc
		stInCSI
	)
	n, st := 0, stOut
	for _, r := range s {
		switch st {
		case stOut:
			if r == 0x1b {
				st = stAfterEsc
				continue
			}
			n++
		case stAfterEsc:
			// Expect `[` (CSI introducer). Anything else means the
			// escape was a non-CSI sequence we don't model; treat
			// that next rune as visible so we don't silently swallow
			// real content if a non-lipgloss producer slips in.
			if r == '[' {
				st = stInCSI
			} else {
				st = stOut
				n++
			}
		case stInCSI:
			// Final byte is 0x40-0x7e. Parameter bytes 0x30-0x3f
			// and intermediates 0x20-0x2f are consumed silently.
			if r >= '@' && r <= '~' {
				st = stOut
			}
		}
	}
	return n
}
