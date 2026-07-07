package cli

import (
	"os"
	"strings"
	"unicode/utf8"

	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

// narrowFallbackCols is the width below which the help abandons its
// boxed/sectioned layout for a plain per-command listing: the boxed
// layout needs ~45 visible cols plus slack.
const narrowFallbackCols = 50

// helpDefaultCols is the assumed terminal width when COLUMNS is unset
// (e.g. piped output).
const helpDefaultCols = 80

// helpCols resolves the terminal width for the help template from
// $COLUMNS, falling back to helpDefaultCols if absent or unparseable.
// Kept private rather than in internal/ui/term: pulling golang.org/x/term
// just to measure one command's layout isn't worth the dependency.
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

// applyHelp swaps Cobra's default help on the `localnet` subcommand for
// a custom ASCII-box + sectioned listing. Only the `localnet` subgroup is
// overridden — the root command keeps Cobra's canonical help.
//
// Cobra inherits help templates and help funcs from parent to child, so
// each direct child is pinned to its currently-inherited default first;
// otherwise `localnet up --help` would render the parent's section
// listing instead of `up`'s own flags help.
//
// Render is lazy (per-call HelpFunc) rather than baked at install time,
// so NO_COLOR set after startup (a real CI pattern) is honored.
func applyHelp(localnet *cobra.Command) {
	for _, child := range localnet.Commands() {
		child.SetHelpTemplate(child.HelpTemplate())
		child.SetHelpFunc(child.HelpFunc())
	}
	localnet.SetHelpFunc(func(c *cobra.Command, _ []string) {
		_, _ = c.OutOrStdout().Write([]byte(renderLocalnetHelp()))
	})
}

// renderLocalnetHelp renders per --help invocation (cheap, so no caching
// — caching would break the lazy-palette behavior). Terminals narrower
// than narrowFallbackCols get a plain listing; a clipped box is worse.
func renderLocalnetHelp() string {
	if helpCols() < narrowFallbackCols {
		return renderNarrowHelp()
	}
	return renderBoxedHelp()
}

// renderNarrowHelp is the narrow-terminal fallback: a bold title line
// plus one line per command — no boxes, no sections, no padding math.
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

// helpCategories returns the canonical hand-list shared by the boxed
// and narrow renderers. Hand-listed (not scraped from cobra) because
// the listing groups by intent, not alphabetically. It must stay in
// sync with the commands wired in localnet.Build();
// TestLocalnetHelp_MatchesWiredCommandSet enforces both directions.
func helpCategories() []helpCategory {
	return []helpCategory{
		{
			Title: "lifecycle",
			Commands: []helpRow{
				{"up", "create and start a named LocalNet with preflight checks and readiness wait"},
				{"start", "start a stopped LocalNet (creates it if it doesn't exist)"},
				{"stop", "gracefully stop a running LocalNet, keeping containers (docker compose stop)"},
				{"down", "stop services and remove runtime containers"},
				{"restart", "restart services in place without removing containers"},
				{"pause", "freeze a running LocalNet (docker compose pause)"},
				{"resume", "resume a paused LocalNet (docker compose unpause)"},
				{"clean", "remove instance state, containers, and volumes"},
				{"snapshot", "capture a database dump and registry state to a tarball"},
				{"restore", "restore a database dump and registry state from a tarball"},
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
				{"token", "create, mint, transfer, burn, and check token balances"},
				{"metrics", "inspect observability endpoints and metrics state"},
				{"observability", "enable, disable, or inspect Prometheus + Grafana sidecars"},
				{"skills", "browse and install AI-agent skill docs for DevKit workflows"},
			},
		},
	}
}

func renderBoxedHelp() string {
	categories := helpCategories()

	var b strings.Builder

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

// boxGlyphs is the rune set for one box-drawing pass: Unicode by
// default, pure ASCII when the terminal likely can't render
// U+2500-range glyphs (CI logs with LANG=C and many Windows terminals
// print them as garbage).
type boxGlyphs struct {
	tl, tr, bl, br, h, v string
}

var (
	unicodeBox = boxGlyphs{tl: "┌", tr: "┐", bl: "└", br: "┘", h: "─", v: "│"}
	asciiBox   = boxGlyphs{tl: "+", tr: "+", bl: "+", br: "+", h: "-", v: "|"}
)

// pickBoxGlyphs returns asciiBox when color is off or when LANG/LC_ALL
// signals a non-UTF-8 locale — either suggests the terminal can't
// render Unicode box glyphs.
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

// dynamicBox renders a box-drawing frame around `lines`, auto-sized to
// the widest line plus 3 cells of padding on each side, so a longer
// title can't overflow the frame.
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

// helpCategory is one bucket ("lifecycle" / "developing").
type helpCategory struct {
	Title    string
	Commands []helpRow
}

// helpRow is one (command, description) pair.
type helpRow struct {
	name, desc string
}

// nameWidth is the visible-cell width the command-name column is
// padded to so rows in the same section align.
const nameWidth = 12

// renderHelpRow pads via term.VisibleLen (runes after stripping ANSI)
// rather than len(name): a multi-byte name would otherwise over-count
// bytes and misalign the column.
func renderHelpRow(name, desc string) string {
	pad := nameWidth - term.VisibleLen(name)
	if pad < 1 {
		pad = 1
	}
	return term.Brandc(name) + strings.Repeat(" ", pad) + term.Textc(desc)
}
