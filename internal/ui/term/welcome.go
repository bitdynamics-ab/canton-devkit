package term

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// WelcomeScreen renders the post-`localnet up` "ready" surface — a brand
// lockup + endpoint cards + try-next cheat sheet. The whole screen is
// printed at once so the layout doesn't flicker when piped to less,
// and so non-TTY consumers (CI logs, `up | tee build.log`) capture a
// single coherent block.
//
// The renderer composes vertical bands separated by blank lines:
// lockup, one-line ✦ summary, primary CTA (exactly one — secondary
// actions belong in "Try next"), endpoint cards grouped by Category,
// try-next cheat sheet (keep ≤6 rows; glance-and-scan, not help),
// and a dim state-path footer.
//
// On non-TTY writers the same structural content is emitted without
// ANSI styling, OSC 8 hyperlinks, or the ASCII lockup (replaced by a
// one-line `=== canton-devkit ===` header), keeping
// `localnet up > log.txt` greppable.
type WelcomeScreen struct {
	// InstanceName — the registry-registered `--name` value.
	InstanceName string
	// SpliceVersion — the Splice tag this instance is running.
	SpliceVersion string
	// Elapsed — wall-clock time from "Starting Canton LocalNet" to
	// "ready". Optional; pass 0 to omit.
	Elapsed time.Duration
	// Endpoints — ordered list of services to surface. Caller is
	// responsible for grouping in the desired display order; the
	// renderer just walks the slice and inserts the section header
	// when the Category column changes.
	Endpoints []Endpoint
	// StatePath — absolute path to state.json (shown as dim footer).
	StatePath string
	// TryNext — ordered list of follow-up commands. Pass nil to
	// suppress the section.
	TryNext []NextStep
}

// Endpoint is one row in the endpoint cards section.
type Endpoint struct {
	Category string // section header — "WEB UIs", "INFRASTRUCTURE", etc.
	Label    string // human label — "Wallet (app-user)", "Postgres".
	URL      string // displayed URL — also wrapped in OSC 8 on TTY.
	External bool   // true → render `↗` glyph; false → omit (for sockets, paths).
}

// NextStep is one row in the "Try next" cheat sheet.
type NextStep struct {
	Label   string // short verb — "Status", "Env", "Live ACS".
	Command string // exact copy-pasteable command.
}

// canonicalLockup is the multi-line brand ASCII for `canton-devkit`,
// produced once via `figlet -f slant "canton-devkit"` and committed so
// the binary doesn't depend on figlet at runtime. Trailing whitespace
// trimmed line-by-line so terminals don't render long blank stripes.
const canonicalLockup = `            _                          _            _ _    _ _
   ___ __ _ _ __ | |_ ___  _ __        __| | _____   _| | _(_) |_
  / __/ _` + "`" + ` | '_ \| __/ _ \| '_ \ _____ / _` + "`" + ` |/ _ \ \ / / |/ / | __|
 | (_| (_| | | | | || (_) | | | |_____| (_| |  __/\ V /|   <| | |_
  \___\__,_|_| |_|\__\___/|_| |_|      \__,_|\___| \_/ |_|\_\_|\__|`

// Render writes the welcome screen to w. TTY-detection is delegated
// to ShouldColor(w) — same gate the rest of the term/ package uses, so
// `NO_COLOR=1` users get the plain variant without lockup ANSI.
func (s WelcomeScreen) Render(w io.Writer) {
	if !ShouldColor(w) {
		s.renderPlain(w)
		return
	}
	s.renderRich(w)
}

func (s WelcomeScreen) renderRich(w io.Writer) {
	// 1. Lockup — single colour over the whole multi-line block.
	for _, line := range strings.Split(canonicalLockup, "\n") {
		_, _ = fmt.Fprintln(w, Brandc(line))
	}
	_, _ = fmt.Fprintln(w)

	// 2. One-line summary.
	parts := []string{
		Brandc("✦"),
		Bold(fmt.Sprintf("%q is ready", s.InstanceName)),
	}
	if s.SpliceVersion != "" {
		parts = append(parts, Dimc("·"), Textc("Splice "+s.SpliceVersion))
	}
	if s.Elapsed > 0 {
		parts = append(parts, Dimc("·"), Dimc("ready in "+humanDuration(s.Elapsed)))
	}
	_, _ = fmt.Fprintln(w, "  "+strings.Join(parts, "  "))
	_, _ = fmt.Fprintln(w)

	// 3. Primary CTA — single brand-coloured action.
	_, _ = fmt.Fprintln(w,
		"  "+Brandc("→")+"  "+Bold("Open dashboard")+
			"      "+osc8("http://localhost:7777", "http://localhost:7777"))
	_, _ = fmt.Fprintln(w, "     "+Dimc("(canton-devkit localnet ui)"))
	_, _ = fmt.Fprintln(w)

	// 4. Endpoint cards (grouped by Category transitions).
	s.renderEndpoints(w)

	// 5. Try-next cheat sheet.
	if len(s.TryNext) > 0 {
		_, _ = fmt.Fprintln(w, "  "+Dimc("── ")+Bold("TRY NEXT")+" "+
			Dimc(strings.Repeat("─", maxTryNextHeaderPad)))
		labelW := 0
		for _, step := range s.TryNext {
			if n := VisibleLen(step.Label); n > labelW {
				labelW = n
			}
		}
		for _, step := range s.TryNext {
			pad := strings.Repeat(" ", labelW-VisibleLen(step.Label))
			_, _ = fmt.Fprintf(w, "  %s%s   %s\n",
				Textc(step.Label), pad, Dimc(step.Command))
		}
		_, _ = fmt.Fprintln(w)
	}

	// 6. State footer.
	if s.StatePath != "" {
		_, _ = fmt.Fprintln(w, "  "+Dimc("State: "+s.StatePath))
	}
}

func (s WelcomeScreen) renderEndpoints(w io.Writer) {
	if len(s.Endpoints) == 0 {
		return
	}
	// First pass: max label width across all endpoints, for column
	// alignment.
	labelW := 0
	for _, e := range s.Endpoints {
		if n := VisibleLen(e.Label); n > labelW {
			labelW = n
		}
	}
	currentCat := ""
	for _, e := range s.Endpoints {
		if e.Category != currentCat {
			if currentCat != "" {
				_, _ = fmt.Fprintln(w) // blank line between groups
			}
			currentCat = e.Category
			_, _ = fmt.Fprintln(w, "  "+Dimc("── ")+Bold(e.Category)+" "+
				Dimc(strings.Repeat("─", maxSectionHeaderPad-len(e.Category))))
		}
		pad := strings.Repeat(" ", labelW-VisibleLen(e.Label))
		glyph := "  "
		if e.External {
			glyph = Brandc("↗") + " "
		}
		_, _ = fmt.Fprintf(w, "  %s%s  %s  %s\n",
			Textc(e.Label), pad, glyph, osc8(e.URL, e.URL))
	}
	_, _ = fmt.Fprintln(w)
}

func (s WelcomeScreen) renderPlain(w io.Writer) {
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "=== canton-devkit ===")
	_, _ = fmt.Fprintln(w)
	summary := fmt.Sprintf(`"%s" is ready`, s.InstanceName)
	if s.SpliceVersion != "" {
		summary += " · Splice " + s.SpliceVersion
	}
	if s.Elapsed > 0 {
		summary += " · ready in " + humanDuration(s.Elapsed)
	}
	_, _ = fmt.Fprintln(w, summary)
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Open dashboard: http://localhost:7777")
	_, _ = fmt.Fprintln(w, "                (canton-devkit localnet ui)")
	_, _ = fmt.Fprintln(w)
	if len(s.Endpoints) > 0 {
		currentCat := ""
		labelW := 0
		for _, e := range s.Endpoints {
			if n := len(e.Label); n > labelW {
				labelW = n
			}
		}
		for _, e := range s.Endpoints {
			if e.Category != currentCat {
				if currentCat != "" {
					_, _ = fmt.Fprintln(w)
				}
				currentCat = e.Category
				_, _ = fmt.Fprintln(w, "["+e.Category+"]")
			}
			_, _ = fmt.Fprintf(w, "  %-*s  %s\n", labelW, e.Label, e.URL)
		}
		_, _ = fmt.Fprintln(w)
	}
	if len(s.TryNext) > 0 {
		_, _ = fmt.Fprintln(w, "[TRY NEXT]")
		labelW := 0
		for _, step := range s.TryNext {
			if n := len(step.Label); n > labelW {
				labelW = n
			}
		}
		for _, step := range s.TryNext {
			_, _ = fmt.Fprintf(w, "  %-*s   %s\n", labelW, step.Label, step.Command)
		}
		_, _ = fmt.Fprintln(w)
	}
	if s.StatePath != "" {
		_, _ = fmt.Fprintln(w, "State: "+s.StatePath)
	}
}

// osc8 wraps text in an OSC 8 hyperlink escape sequence. Modern
// terminals render this as a clickable link; terminals that don't
// support it print the text verbatim because the escape sequences are
// non-printing bytes, so we emit unconditionally on the rich path.
//
// Spec: https://gist.github.com/egmontkob/eb114294efbcd5adb1944c9f3cb5feda
func osc8(url, text string) string {
	return "\x1b]8;;" + url + "\x07" + text + "\x1b]8;;\x07"
}

// humanDuration formats a Duration as a compact two-unit string:
// `1m 47s`, `12s`, `2h 5m`.
func humanDuration(d time.Duration) string {
	if d < time.Second {
		return "<1s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		m := int(d.Minutes())
		s := int(d.Seconds()) - m*60
		if s == 0 {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%dm %ds", m, s)
	}
	h := int(d.Hours())
	m := int(d.Minutes()) - h*60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}

// Padding constants — tuned so headers reach approximately column 72
// regardless of the longest section title. Centralised so any future
// glyph or section change shifts both sections in lockstep.
const (
	maxSectionHeaderPad = 68
	maxTryNextHeaderPad = 60
)
