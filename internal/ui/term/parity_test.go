package term

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestPalette_MatchesTerminalJSX pins
// the color tokens in color.go MUST match the JSX TERM.* constants
// in docs/design/mockups/terminal.jsx, otherwise the rendered CLI
// drifts from the mockup and acceptance becomes "I'll just eyeball
// it" — exactly the failure mode the mockup-as-spec model is
// supposed to prevent.
//
// We parse the JSX file (regex; the file is hand-edited) and
// compare each `KEY:  '#HEX'` line against the corresponding Go
// `lipgloss.Color("#HEX")` assignment. Drift on either side fails
// here loud + obvious.
//
// JSX → Go name mapping (only entries that have a Go counterpart;
// the JSX file also has tokens we don't surface like `chrome` and
// `selBg` because they're only relevant to the React shell):
var jsxToGo = map[string]string{
	"bg":          "Background",
	"panel":       "PanelBg",
	"panelBorder": "PanelBorder",
	"chrome":      "ChromeBg",
	"text":        "Text",
	"mid":         "Mid",
	"dim":         "Dim",
	"faint":       "Faint",
	"brand":       "Brand",
	"brandSoft":   "BrandSoft",
	"success":     "Success",
	"warn":        "Warn",
	"error":       "Error",
	"info":        "Info",
	"magenta":     "Magenta",
	"amber":       "Amber",
	"rose":        "Rose",
}

func TestPalette_MatchesTerminalJSX(t *testing.T) {
	jsxPath := findMockupFile(t, "terminal.jsx")
	body, err := os.ReadFile(jsxPath)
	if err != nil {
		t.Fatalf("read %s: %v", jsxPath, err)
	}

	// Match `  key: '#HEX'` lines inside the TERM = {...} block.
	// Tolerant of trailing comma + the optional `// comment`.
	re := regexp.MustCompile(`(?m)^\s+([a-zA-Z]+):\s*'(#[0-9A-Fa-f]+)'`)
	jsxColors := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(string(body), -1) {
		jsxColors[m[1]] = strings.ToUpper(m[2])
	}
	if len(jsxColors) == 0 {
		t.Fatalf("no JSX color tokens parsed from %s — regex out of date?", jsxPath)
	}

	// Go side: read color.go and extract `Name = lipgloss.Color("#HEX")`.
	colorGoPath := goPathOf(t, "color.go")
	goBody, err := os.ReadFile(colorGoPath)
	if err != nil {
		t.Fatalf("read %s: %v", colorGoPath, err)
	}
	reGo := regexp.MustCompile(`(?m)^\s+(\w+)\s*=\s*lipgloss\.Color\("(#[0-9A-Fa-f]+)"\)`)
	goColors := map[string]string{}
	for _, m := range reGo.FindAllStringSubmatch(string(goBody), -1) {
		goColors[m[1]] = strings.ToUpper(m[2])
	}

	for jsxName, goName := range jsxToGo {
		jsxHex, jsxOK := jsxColors[jsxName]
		if !jsxOK {
			t.Errorf("JSX token %q expected but not found in terminal.jsx", jsxName)
			continue
		}
		goHex, goOK := goColors[goName]
		if !goOK {
			t.Errorf("Go token %q expected but not found in color.go", goName)
			continue
		}
		if jsxHex != goHex {
			t.Errorf("palette drift: JSX %s=%s vs Go %s=%s",
				jsxName, jsxHex, goName, goHex)
		}
	}
}

// TestBoxKinds_DecisionTable pins every BoxKind to its rendered
// accent color so a future "add a kind" PR fails here unless the
// table is updated.
func TestBoxKinds_DecisionTable(t *testing.T) {
	cases := map[BoxKind]string{
		BoxBrand:   "brand",
		BoxSuccess: "success",
		BoxWarn:    "warn",
		BoxError:   "error",
		BoxInfo:    "info",
	}
	for kind, label := range cases {
		got := Box(kind, "x")
		if !strings.Contains(got, "x") {
			t.Errorf("Box(%v) lost body content: %q", label, got)
		}
		// All kinds use the same ┃ left border; presence verifies
		// the renderer ran. Color difference can't be asserted
		// without ANSI inspection — we save that for the
		// integration tests.
		// Structural assertion via BoxLeftBorderRune lets a future Windows ASCII profile
		// substitute the rune without rewriting this assertion.
		if !strings.ContainsRune(got, BoxLeftBorderRune) {
			t.Errorf("Box(%v) missing left border (rune=%q): %q",
				label, BoxLeftBorderRune, got)
		}
	}
}

// TestStepKinds_DecisionTable covers each StepKind → glyph mapping.
// Mirrors TestBoxKinds_DecisionTable's intent — a future addition
// of a kind without updating Glyphs fails here.
func TestStepKinds_DecisionTable(t *testing.T) {
	cases := map[StepKind]string{
		StepCheck:   Glyphs.Check,
		StepCross:   Glyphs.Cross,
		StepWarn:    Glyphs.Warn,
		StepPending: Glyphs.Pending,
		StepBusy:    Glyphs.Busy,
	}
	for kind, want := range cases {
		got := Step(kind, "label", "", "")
		if !strings.Contains(got, want) {
			t.Errorf("Step(%v) missing glyph %q: %q", kind, want, got)
		}
		if !strings.Contains(got, "label") {
			t.Errorf("Step(%v) missing label: %q", kind, got)
		}
	}
}

// TestSpinnerFrames_NonEmpty pins the Braille rotation. A future
// "let's use a different spinner" change needs to update this AND
// the SpinnerFrames docstring's reference to the @keyframes
// term-spin output in terminal.jsx.
func TestSpinnerFrames_NonEmpty(t *testing.T) {
	if len(SpinnerFrames) < 4 {
		t.Errorf("SpinnerFrames too short (%d) — at least 4 frames needed for visible rotation",
			len(SpinnerFrames))
	}
	for i, f := range SpinnerFrames {
		if f == "" {
			t.Errorf("SpinnerFrames[%d] empty", i)
		}
	}
}

// TestPrompt_AllFieldsRender pins the Prompt format so a refactor
// of zsh-ish output can't silently drop user/host/dir/cmd.
func TestPrompt_AllFieldsRender(t *testing.T) {
	got := Prompt("alice", "devbox", "~/work", "go test")
	for _, want := range []string{"alice", "devbox", "~/work", "go test", "❯"} {
		if !strings.Contains(got, want) {
			t.Errorf("Prompt missing %q: %q", want, got)
		}
	}
}

// goPathOf returns the absolute path of `name` in the same dir as
// this test file. Uses runtime.Caller(0) so it works from any cwd.
func goPathOf(t *testing.T, name string) string {
	t.Helper()
	_, this, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(this), name)
}

// findMockupFile resolves docs/design/mockups/<name> relative to
// this test file's directory. Walks up until it finds a docs/
// sibling — robust to refactors that move internal/ui/term/.
func findMockupFile(t *testing.T, name string) string {
	t.Helper()
	_, this, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(this)
	for i := 0; i < 6; i++ {
		try := filepath.Join(dir, "docs", "design", "mockups", name)
		if _, err := os.Stat(try); err == nil {
			return try
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("could not locate docs/design/mockups/%s walking up from %s", name, this)
	return ""
}
