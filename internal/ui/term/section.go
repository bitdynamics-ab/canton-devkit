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
// right is optional dim trailing text that sits flush-right on the
// header row (the "auto-refresh 2s" hint in the mockup). Pass "" to
// omit. children is a pre-rendered block — typically the join of
// several Step/KV/Table lines.
func Section(title, right, children string) string {
	var head strings.Builder
	head.WriteString(S().
		Foreground(Brand).
		Bold(true).
		Render(strings.ToUpper(title)))
	if right != "" {
		head.WriteString("  ")
		head.WriteString(Dimc(right))
	}
	// Width-agnostic underline: we use a fixed-character separator
	// rather than padding to terminal width because the renderer
	// doesn't know the destination width and we'd rather under-fill
	// than overflow (which wraps badly in narrow terminals).
	sep := S().Foreground(Faint).Render(strings.Repeat("─", 60))
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
