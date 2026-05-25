package term

import (
	"fmt"
	"strings"
)

// StepKind selects the glyph + color a Step row renders with. The set
// mirrors terminal.jsx::Sym keys: check / cross / warn / pending /
// busy. Adding a new kind is one line here and one entry in Glyphs.
type StepKind int

const (
	StepCheck StepKind = iota
	StepCross
	StepWarn
	StepPending
	StepBusy
)

// Step renders one row inside a preflight / down / dar-upload list,
// matching the JSX Step({icon,label,detail,time}) component in
// terminal.jsx:
//
//	✓  Docker daemon                v25.0.5            0.2s
//
// label is the principal text; detail is dim trailing text (e.g. the
// version string); elapsed is the faint right-aligned time. Either
// detail or elapsed may be empty; they are simply omitted.
//
// We don't right-align elapsed with a hard column — terminal width is
// unknown at this layer and bubbletea's lipgloss would force us to
// know it. The mockup's right-alignment is visual polish; aligning
// with two spaces keeps the line scannable without that cost.
func Step(kind StepKind, label, detail, elapsed string) string {
	var icon string
	switch kind {
	case StepCheck:
		icon = Successc(Glyphs.Check)
	case StepCross:
		icon = Errorc(Glyphs.Cross)
	case StepWarn:
		icon = Warnc(Glyphs.Warn)
	case StepPending:
		icon = Dimc(Glyphs.Pending)
	case StepBusy:
		icon = Brandc(Glyphs.Busy)
	default:
		icon = " "
	}
	var b strings.Builder
	fmt.Fprintf(&b, " %s  %s", icon, Textc(label))
	if detail != "" {
		fmt.Fprintf(&b, "  %s", Dimc(detail))
	}
	if elapsed != "" {
		fmt.Fprintf(&b, "  %s", Faintc(elapsed))
	}
	return b.String()
}

// KV renders one "key: value" pair with a fixed-width dim key column.
// Mirrors terminal.jsx::KV. keyWidth is the left-column width in
// runes; pass 0 to default to 14 (the JSX default).
func KV(key string, value string, keyWidth int) string {
	if keyWidth <= 0 {
		keyWidth = 14
	}
	k := key
	if len(k) < keyWidth {
		k = k + strings.Repeat(" ", keyWidth-len(k))
	}
	return Dimc(k) + "  " + Textc(value)
}

// Prompt renders a zsh-ish prompt + command:
//
//	dev@devnet:~/canton-app ❯ dpm localnet up --name hubble
//
// Used to head a transcript-style screen. The actual command is
// printed bright; surrounding chrome (user@host:dir ❯) is colored.
// Mirrors terminal.jsx::Prompt.
func Prompt(user, host, dir, cmd string) string {
	if user == "" {
		user = "dev"
	}
	if host == "" {
		host = "devnet"
	}
	if dir == "" {
		dir = "~/canton-app"
	}
	return fmt.Sprintf("%s%s%s%s%s %s %s",
		Successc(user),
		Dimc("@"),
		Infoc(host),
		Dimc(":"),
		Magentac(dir),
		Dimc("❯"),
		Textc(cmd),
	)
}
