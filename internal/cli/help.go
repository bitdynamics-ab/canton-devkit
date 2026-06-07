package cli

import (
	"os"
	"strings"
	"unicode/utf8"

	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

// narrowFallbackCols is the width below which the help template
// abandons its boxed/sectioned layout and prints a single-line
// summary. The 50-col threshold matches the JSX ScreenHelp's minimum
// visible content width (45 cols box + 5 cols slack).
const narrowFallbackCols = 50

// helpDefaultCols is the assumed terminal width when COLUMNS is
// unset and we can't measure (e.g. piped output). 80 cols is the
// long-standing CLI default and matches the JSX mockup.
const helpDefaultCols = 80

// helpCols resolves the terminal width for the help-template only.
// We read $COLUMNS (test-controllable, also set by `tput`) and fall
// back to helpDefaultCols if absent or unparseable. We deliberately
// keep this private to the cli package rather than putting it in
// internal/ui/term — pulling golang.org/x/term just to measure one
// command's layout adds dependency surface for no other consumer.
func helpCols() int {
	v := os.Getenv("COLUMNS")
	if v == "" {
		return helpDefaultCols
	}
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return helpDefaultCols
		}
		n = n*10 + int(c-'0')
	}
	if n <= 0 {
		return helpDefaultCols
	}
	return n
}

// applyHelp swaps Cobra's default help template on the `localnet`
// subcommand for the ASCII-box + sectioned listing in
// docs/design/mockups/screens-tokens-help.jsx (ScreenHelp).
// Implements + the review-fix surfaced on PR #35.
//
// We only override the `localnet` subgroup (not the root) because:
// - The root command's help is overwhelmingly Cobra-canonical
// ("Usage: canton-devkit [command] …" etc.) and most users
// reach it via `--version`, not via reading help.
// - The ScreenHelp mockup is specifically the `dpm localnet
// --help` shape: lifecycle vs developing sections, dimmed
// trailing flags hint.
//
// Cobra inherits help templates from parent to child unless the
// child has its own — so before overriding `localnet`'s template
// we explicitly pin each direct child to the inherited default.
// Without this, `localnet up --help` would render the parent's
// section listing instead of `up`'s detailed flags help.
//
// PR #35 review pin: render is LAZY (per-call HelpFunc) rather
// than baked at install time. The original cut cached the ANSI
// once during init, which meant NO_COLOR or --no-color set after
// startup (a real CI pattern) was ignored. The lazy func calls
// the term package on every invocation so palette overrides take
// effect.
//
// PR #35 review pin: box width is DYNAMIC. The original cut
// hardcoded 42 columns — adding a new title or a longer subtitle
// would silently overflow the box. dynamicBox sizes to the
// widest title/body line plus padding.
//
// Categories are intentionally hand-listed rather than scraped
// from the cobra tree — the mockup groups by intent, not
// alphabetically. New commands need one new line each in
// `categories` below; that's the right friction.
func applyHelp(localnet *cobra.Command) {
	// Pin each child's BOTH template + helpFunc so the parent's
	// overrides below don't bleed down via cobra's parent-walk.
	// Both fields are inherited — without the explicit pin,
	// `localnet up --help` would render the parent's section
	// listing instead of `up`'s own detailed flags help
	// (TestRunIsArgvOnly is the canary that caught this).
	//
	// We capture each child's currently-inherited HelpFunc and
	// re-bind it directly so the parent override below doesn't
	// reach the child via the walk-up.
	for _, child := range localnet.Commands() {
		child.SetHelpTemplate(child.HelpTemplate())
		fn := child.HelpFunc() // currently-inherited (cobra default)
		child.SetHelpFunc(fn)  // pin it to the child explicitly
	}
	// Parent: lazy render via SetHelpFunc so palette state at
	// INVOCATION time (not init time) drives the output.
	localnet.SetHelpFunc(func(c *cobra.Command, _ []string) {
		out := c.OutOrStdout()
		_, _ = out.Write([]byte(renderLocalnetHelp(out)))
	})
}

// renderLocalnetHelp is called per --help invocation. Cheap (one
// builder, ~30 writes) so caching would buy us nothing measurable
// while costing the lazy-palette correctness.
//
// w is the destination writer — used only to derive the terminal
// width (via $COLUMNS or fallback) for box layout. Reviewer pin
// (PR #35 #1): on narrow terminals (<50 cols) the boxed layout
// overflows and wraps ugly; we fall back to a single-line summary
// in that case.
func renderLocalnetHelp(w interface {
	Write([]byte) (int, error)
}) string {
	_ = w
	cols := helpCols()
	if cols < narrowFallbackCols {
		return renderNarrowHelp()
	}
	return renderBoxedHelp()
}

// renderNarrowHelp is the <50-col fallback: a single bold title
// line + one line per command. No boxes, no sections, no padding
// math — terminals this narrow can't render the JSX mockup
// faithfully and a clipped box is worse than a plain list.
func renderNarrowHelp() string {
	var b strings.Builder
	b.WriteString(term.Brandc(helpTitle()))
	b.WriteString("\n")
	for _, cat := range helpCategories() {
		for _, row := range cat.Commands {
			b.WriteString(term.Brandc(row.name))
			b.WriteString("  ")
			b.WriteString(term.Textc(row.desc))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// helpCategories returns the canonical hand-list shared by the
// boxed and narrow renderers. Hand-listed (not scraped from cobra)
// because the mockup groups by intent, and unshipped commands
// (snapshot, dar, etc.) must NOT appear until they land.
func helpCategories() []helpCategory {
	return []helpCategory{
		{
			Title: "lifecycle",
			Commands: []helpRow{
				{"up", "start a named LocalNet with preflight checks and readiness wait"},
				{"down", "stop services and remove runtime containers"},
				{"snapshot", "capture docker volumes and registry state to a tarball"},
				{"restore", "restore docker volumes and registry state from a tarball"},
				{"status", "services, endpoints, identities, version"},
				{"logs", "tail or follow service logs with filters"},
				{"env", "emit .env values for ledger/json/admin/wallet/scan"},
				{"creds", "show local ledger API credentials"},
				{"list", "discover devkit-managed LocalNets on this host"},
				{"doctor", "check docker, ports, resources, host prereqs"},
				{"refresh", "reconcile registry state with docker reality"},
			},
		},
		{
			Title: "developing",
			Commands: []helpRow{
				{"versions", "list available Splice versions (curated + upstream)"},
				{"ui", "launch the browser-based LocalNet dashboard"},
				{"container", "inspect, restart, and tail individual containers"},
				{"contracts", "query active contracts from a participant endpoint"},
				{"tx", "inspect transactions from a participant endpoint"},
				{"dar", "upload, list, download, diff, and watch DAR files"},
				{"metrics", "inspect observability endpoints and metrics state"},
			},
		},
	}
}

func renderBoxedHelp() string {
	categories := helpCategories()

	var b strings.Builder

	// Dynamic-width ASCII box (review pin: hardcoded 42 was a bug
	// waiting to happen for any title edit).
	boxBody := []string{
		helpTitle(),
		"manage Canton LocalNets like a normal",
		"process, not a Docker compose project",
	}
	for _, line := range dynamicBox(boxBody) {
		b.WriteString(term.Brandc(line))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(term.Dimc("Usage  "))
	b.WriteString(term.Textc(appName + " localnet "))
	b.WriteString(term.Brandc("<command>"))
	b.WriteString(term.Dimc(" [flags]"))
	b.WriteString("\n\n")

	for i, cat := range categories {
		rows := make([]string, 0, len(cat.Commands))
		for _, row := range cat.Commands {
			rows = append(rows, renderHelpRow(row.name, row.desc))
		}
		b.WriteString(renderHelpSection(cat.Title, strings.Join(rows, "\n")))
		if i < len(categories)-1 {
			b.WriteString("\n\n")
		}
	}

	b.WriteString("\n\n")
	b.WriteString(term.Dimc("Common flags: "))
	b.WriteString(term.Textc("--name"))
	b.WriteString(term.Dimc(" selects an instance; many commands support "))
	b.WriteString(term.Textc("--format=json"))
	b.WriteString(term.Dimc(". Set "))
	b.WriteString(term.Textc("NO_COLOR=1"))
	b.WriteString(term.Dimc(" for plain output"))
	b.WriteString(term.Dimc(". Run "))
	b.WriteString(term.Textc(appName + " localnet <cmd> --help"))
	b.WriteString(term.Dimc(" for command-specific help."))
	b.WriteString("\n")
	return b.String()
}

// boxGlyphs is the rune set used for one box-drawing pass. We pick
// either Unicode (default, matches the JSX mockup) or pure ASCII
// (LANG=C, NO_COLOR, or non-TTY where the user has signalled the
// renderer can't safely emit U+2500-range glyphs).
//
// Reviewer pin (PR #35 #2b): the previous implementation always
// wrote Unicode box-drawing characters. CI logs with LANG=C and
// many Windows terminals print these as `???` or `~~`, which makes
// the help box look broken. ASCII fallback keeps the structure
// readable even when fancy glyphs can't render.
type boxGlyphs struct {
	tl, tr, bl, br, h, v string
}

var (
	unicodeBox = boxGlyphs{tl: "┌", tr: "┐", bl: "└", br: "┘", h: "─", v: "│"}
	asciiBox   = boxGlyphs{tl: "+", tr: "+", bl: "+", br: "+", h: "-", v: "|"}
)

// pickBoxGlyphs returns asciiBox when color is off OR when LANG/
// LC_ALL signals a C / POSIX locale (no UTF-8). Either signal
// suggests the terminal probably can't render U+2500-range glyphs.
func pickBoxGlyphs() boxGlyphs {
	if !term.ShouldColor(os.Stderr) || localeForcesASCII() {
		return asciiBox
	}
	return unicodeBox
}

func localeForcesASCII() bool {
	for _, k := range []string{"LC_ALL", "LANG"} {
		v := os.Getenv(k)
		if v == "" {
			continue
		}
		if v == "C" || v == "POSIX" {
			return true
		}
		if !strings.Contains(strings.ToUpper(v), "UTF-8") &&
			!strings.Contains(strings.ToUpper(v), "UTF8") {
			return true
		}
	}
	return false
}

func helpTitle() string {
	if localeForcesASCII() {
		return "canton-devkit - localnet"
	}
	return "canton-devkit · localnet"
}

func renderHelpSection(title, children string) string {
	if !localeForcesASCII() {
		return term.Section(title, "", children, 0)
	}
	header := term.Brandc(strings.ToUpper(title))
	width := term.VisibleLen(title) + 4
	if width < 20 {
		width = 20
	}
	if width > 80 {
		width = 80
	}
	return header + "\n" + term.Faintc(strings.Repeat("-", width)) + "\n" + indentHelp(children, 2)
}

func indentHelp(s string, n int) string {
	if s == "" {
		return ""
	}
	pad := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = pad + line
		}
	}
	return strings.Join(lines, "\n")
}

// dynamicBox renders a box-drawing frame around `lines`, auto-sized
// to the widest line + 6 cells of padding (3 on each side, matching
// the mockup's visual breathing room). Returns the 2 frame lines +
// N body lines + 1 frame line, in order. Glyph set is chosen by
// pickBoxGlyphs (Unicode normally, ASCII when the terminal can't
// render U+2500-range).
func dynamicBox(lines []string) []string {
	g := pickBoxGlyphs()
	inner := 0
	for _, l := range lines {
		if w := utf8.RuneCountInString(l); w > inner {
			inner = w
		}
	}
	inner += 6
	bar := strings.Repeat(g.h, inner)
	out := []string{g.tl + bar + g.tr}
	for _, l := range lines {
		pad := inner - utf8.RuneCountInString(l) - 3
		if pad < 1 {
			pad = 1
		}
		out = append(out, g.v+"   "+l+strings.Repeat(" ", pad)+g.v)
	}
	out = append(out, g.bl+bar+g.br)
	return out
}

// helpCategory is one bucket in the mockup ("lifecycle" / "developing").
type helpCategory struct {
	Title    string
	Commands []helpRow
}

// helpRow is one (command, description) pair.
type helpRow struct {
	name, desc string
}

// renderHelpRow pads the command name out to nameWidth visible
// cells so two rows in the same section align. Reviewer pin
// (PR #35 #3): `len(name)` counted bytes, which for any multi-byte
// name (e.g. an i18n alias) over-counted and produced a
// short-padded — and visually misaligned — column. We now use
// term.VisibleLen which counts runes after stripping ANSI.
const nameWidth = 12

func renderHelpRow(name, desc string) string {
	pad := nameWidth - term.VisibleLen(name)
	if pad < 1 {
		pad = 1
	}
	return term.Brandc(name) + strings.Repeat(" ", pad) + term.Textc(desc)
}
