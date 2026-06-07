package term

import (
	"fmt"
	"strings"
)

// Section renders a brand-colored, uppercased, underline-separated
// header followed by indented children — matches the JSX Section
// component:
//
//	SERVICES                    auto-refresh 2s
//	─────────────────────────────────────────────
//	  ✓  canton-domain ...
//	  ✓  participant-alice ...
//
// right is optional dim trailing text on the header row (the
// "auto-refresh 2s" hint in the mockup). Pass "" to omit.
// children is a pre-rendered block — typically the join of
// several Step/KV/Table lines.
//
// width controls the underline length in cells. Pass 0 for the
// auto-size default (VisibleLen(title)+len(right)+4, capped at 80).
// the previous fixed-60-rune separator
// silently truncated for long titles or wasted space for short
// ones. TestSection_SeparatorMatchesWidthArg locks this in.
func Section(title, right, children string, width int) string {
	var head strings.Builder
	head.WriteString(S().
		Foreground(Brand).
		Bold(true).
		Render(strings.ToUpper(title)))
	if right != "" {
		head.WriteString("  ")
		head.WriteString(Dimc(right))
	}
	if width == 0 {
		width = VisibleLen(title) + len(right) + 4
		if width > 80 {
			width = 80
		}
		if width < 20 {
			width = 20
		}
	}
	sep := S().Foreground(Faint).Render(strings.Repeat("─", width))
	return fmt.Sprintf("%s\n%s\n%s", head.String(), sep, indent(children, 2))
}

// indent prefixes every non-empty line in s with n spaces. Used by
// Section to set off children visually under the header.
func indent(s string, n int) string {
	if s == "" {
		return ""
	}
	pad := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l == "" {
			continue
		}
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}
