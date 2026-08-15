package term

import (
	"io"
	"os"
	"sync"

	"github.com/charmbracelet/lipgloss"
	isatty "github.com/mattn/go-isatty"
)

// Renderer is the package-wide lipgloss renderer; it caches the
// color-profile probe (TTY check, NO_COLOR, terminal capabilities)
// behind the scenes. Tests and dry-runs can swap it via SetRenderer.
//
// We hold a single instance because lipgloss.Style values cache the
// profile they were built against — if we created a fresh renderer per
// call, every style would re-probe. The mutex protects swaps in
// tests; production callers never write.
var (
	rendererMu sync.RWMutex
	renderer   = lipgloss.DefaultRenderer()
)

// SetRenderer overrides the package-wide renderer. Tests use this with
// lipgloss.NewRenderer(io.Discard, lipgloss.WithColorProfile(termenv.Ascii))
// to force plain-text output and assert byte-exact strings.
func SetRenderer(r *lipgloss.Renderer) {
	rendererMu.Lock()
	defer rendererMu.Unlock()
	renderer = r
}

// R returns the active renderer. All style constructors below go
// through R so a test SetRenderer call takes effect immediately
// without callers re-building styles.
func R() *lipgloss.Renderer {
	rendererMu.RLock()
	defer rendererMu.RUnlock()
	return renderer
}

// ShouldColor reports whether color output is appropriate for w. The
// rules, in order:
//
//  1. NO_COLOR env (any value) → false. (https://no-color.org)
//  2. CANTON_DEVKIT_NO_COLOR env → false. App-specific override the
//     user can set in CI scripts without affecting other tools.
//  3. w is not a *os.File or its fd is not a TTY → false.
//  4. otherwise → true.
//
// Callers wire this into a --no-color CLI flag plus the
// initialization path: at startup, if !ShouldColor(os.Stdout), call
// SetRenderer with an ASCII-profile renderer so every later style call
// produces plain text.
func ShouldColor(w io.Writer) bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	if _, ok := os.LookupEnv("CANTON_DEVKIT_NO_COLOR"); ok {
		return false
	}
	return IsTerminal(w)
}

// IsTerminal reports whether w is a TTY. Separate from ShouldColor
// so callers that want STRUCTURE (e.g. boxed FriendlyError output)
// but not COLOR — the "TTY but NO_COLOR=1" case — can gate on
// IsTerminal while the per-token Brandc/Errc/etc. still respect
// ShouldColor for the ANSI sequences themselves. Conflating the two
// would turn the Box off whenever color was off, which hides useful
// structure from CI logs that captured a TTY without color.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return isTTY(f)
}

// CanPrompt reports whether r can answer an interactive prompt. A
// non-*os.File reader (test buffer, in-process pipe) is assumed to be
// scripted and answerable; a real file — including a character device
// like /dev/null, which no TTY check based on os.ModeCharDevice alone
// would catch — is not.
func CanPrompt(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return true
	}
	return isTTY(f)
}

func isTTY(f *os.File) bool {
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

// ── reusable style constructors (no behavior, just composition) ──

// S is the entry point for ad-hoc styles. Equivalent to
// term.R().NewStyle(); prefer this so test renderer swaps land.
func S() lipgloss.Style { return R().NewStyle() }

// Color-only styles for inline tokens. Kept tiny — anything more
// elaborate (padding, borders) belongs in the composite renderers in
// box.go / section.go / table.go.
func Brandc(s string) string   { return S().Foreground(Brand).Render(s) }
func Dimc(s string) string     { return S().Foreground(Dim).Render(s) }
func Faintc(s string) string   { return S().Foreground(Faint).Render(s) }
func Textc(s string) string    { return S().Foreground(Text).Render(s) }
func Successc(s string) string { return S().Foreground(Success).Render(s) }
func Warnc(s string) string    { return S().Foreground(Warn).Render(s) }
func Errorc(s string) string   { return S().Foreground(Error).Render(s) }
func Infoc(s string) string    { return S().Foreground(Info).Render(s) }
func Magentac(s string) string { return S().Foreground(Magenta).Render(s) }
func Amberc(s string) string   { return S().Foreground(Amber).Render(s) }
func Rosec(s string) string    { return S().Foreground(Rose).Render(s) }

// Bold renders s in bold white — used by Prompt and KV value
// highlighting. Mirrors C.bold in terminal.jsx.
func Bold(s string) string {
	return S().Bold(true).Foreground(lipgloss.Color("#EEF1F4")).Render(s)
}
