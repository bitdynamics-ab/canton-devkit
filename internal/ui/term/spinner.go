package term

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Spinner is a self-contained bubbletea Model rendering a single
// rotating ⠹ glyph followed by a label. Plug it into a parent Model
// for in-progress steps in long-running commands. Hand-rolled rather
// than charmbracelet/bubbles/spinner because all we need is "advance
// a frame on a tick" — this keeps the dependency surface small.
type Spinner struct {
	Frames []string // defaults to SpinnerFrames if zero-value
	Label  string
	frame  int
}

// spinnerTickMsg is private — only Spinner.Update produces and
// consumes it. Using a tagged struct keeps the bubbletea Update
// switch unambiguous when a Spinner is embedded inside a larger Model.
type spinnerTickMsg struct{}

// Init returns the first tick. Callers embed this in their parent
// Init: `return tea.Batch(otherCmds, spinner.Init())`.
func (s Spinner) Init() tea.Cmd {
	return s.tick()
}

// SpinnerTickMsg is exported so tests can construct one and drive
// Update without waiting on the real 80 ms timer.
type SpinnerTickMsg = spinnerTickMsg

// Update advances the frame on every tick and schedules the next one.
// Unknown messages pass through unchanged — embedding parents stay
// free to dispatch on their own message types.
//
// Value receiver — the frame only advances on the RETURNED copy, so
// callers MUST reassign:
//
//	m.spinner, cmd = m.spinner.Update(msg)
func (s Spinner) Update(msg tea.Msg) (Spinner, tea.Cmd) {
	if _, ok := msg.(spinnerTickMsg); !ok {
		return s, nil
	}
	frames := s.Frames
	if len(frames) == 0 {
		frames = SpinnerFrames
	}
	s.frame = (s.frame + 1) % len(frames)
	return s, s.tick()
}

// View renders one frame: e.g. `⠹ indexing… 62%`. Color is the brand
// accent so the spinner reads as a primary in-progress signal.
func (s Spinner) View() string {
	frames := s.Frames
	if len(frames) == 0 {
		frames = SpinnerFrames
	}
	return Brandc(frames[s.frame%len(frames)]) + " " + Dimc(s.Label)
}

func (s Spinner) tick() tea.Cmd {
	// 80ms ≈ 12.5fps. Fast enough to feel smooth, slow enough that we
	// don't burn the parent's render budget on a static screen.
	return tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}
