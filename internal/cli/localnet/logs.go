package localnet

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/containers"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// buildLogs wires `dpm localnet logs <service> [--name <inst>]` —
// BIT-145, the bubbletea log-tail TUI that matches ScreenLogs in
// docs/design/mockups/screens-lifecycle.jsx.
//
// Hotkeys (footer rendered at the bottom of the screen):
//
//	/   filter — type a substring; only matching lines display
//	g   grep   — type a regex; only matching lines display
//	c   clear  — drop any active filter/grep
//	G   bottom — jump to most recent line + resume follow
//	q   quit
//
// Follow is on by default when stdout is a TTY (the mockup shows
// `⠹ streaming…` at the bottom). Non-TTY callers (pipes, CI) get
// a one-shot tail in plain text so `dpm localnet logs splice |
// grep ERROR` works without TUI escape codes leaking through.
//
// The CLI mirror of the Web UI's ContainerLogsModal — same
// container-name resolution rules (service short-name OR full
// container name), same shared internal/localnet/containers
// package handling the docker exec.
func buildLogs() *cobra.Command {
	var (
		instance string
		tail     int
		since    string
		follow   bool
		noTUI    bool
	)
	cmd := &cobra.Command{
		Use:           "logs <service>",
		Short:         "Tail one service's logs with an interactive viewer",
		Long:          "Streams logs for the named service (e.g. `splice`, `canton`, `participant-alice`) belonging to a registered instance. Defaults to following live output in an interactive TUI; falls back to a plain tail when stdout is piped or --no-tui is set.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := resolveLogsInstance(instance)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			service := args[0]
			if !validServiceArg.MatchString(service) {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
					"invalid service name (alphanumeric + . _ - only)")
				return localnet.AsExitError(localnet.ExitUserError)
			}
			resolveCtx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			containerName, err := resolveContainerName(resolveCtx, state.ComposeProject, service)
			cancel()
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}

			useTUI := !noTUI && isatty.IsTerminal(os.Stdout.Fd())
			if !useTUI {
				return runLogsPlain(cmd, containerName, tail, since, follow)
			}
			return runLogsTUI(cmd, state.Name, service, containerName, tail, since, follow)
		},
	}
	cmd.Flags().StringVar(&instance, "name", "",
		"Instance to read from (defaults to the only registered instance, or errors when ambiguous)")
	cmd.Flags().IntVar(&tail, "tail", 200, "Initial lines to show before following (10-2000)")
	cmd.Flags().StringVar(&since, "since", "", "Show logs since duration, e.g. 5m, 30s, 2h")
	cmd.Flags().BoolVar(&follow, "follow", true, "Stream new lines as they appear (TUI: always on; plain: opt-in)")
	cmd.Flags().BoolVar(&noTUI, "no-tui", false, "Force plain text output even on a TTY")
	return cmd
}

// resolveLogsInstance picks the registry entry to read from.
// Explicit --name wins. With no flag and exactly one instance,
// auto-pick. With no flag and multiple instances, error so the
// user doesn't accidentally see the wrong logs.
func resolveLogsInstance(name string) (*registry.State, error) {
	if name != "" {
		return resolveInstance(name)
	}
	idx, err := registry.ReadIndex()
	if err != nil {
		return nil, fmt.Errorf("read registry index: %w", err)
	}
	if len(idx.Entries) == 0 {
		return nil, errors.New("no registered instances — run `dpm localnet up` first")
	}
	if len(idx.Entries) > 1 {
		names := make([]string, 0, len(idx.Entries))
		for _, e := range idx.Entries {
			names = append(names, e.Name)
		}
		return nil, fmt.Errorf("multiple instances registered (%s) — pass --name to pick one",
			strings.Join(names, ", "))
	}
	return registry.Read(idx.Entries[0].Name)
}

// runLogsPlain is the non-TTY path. Same control flow as
// `container logs` plus optional --follow streaming. Used in
// pipes and CI so the TUI escape codes never leak.
func runLogsPlain(cmd *cobra.Command, containerName string, tail int, since string, follow bool) error {
	if !follow {
		ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
		defer cancel()
		body, err := containers.Logs(ctx, containerName, containers.LogsOptions{
			Tail: tail, Since: since,
		})
		if body != "" {
			_, _ = fmt.Fprint(cmd.OutOrStdout(), body)
			if !strings.HasSuffix(body, "\n") {
				_, _ = fmt.Fprintln(cmd.OutOrStdout())
			}
		}
		if err != nil && body == "" {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
			return localnet.AsExitError(localnet.ExitRuntimeFailure)
		}
		return nil
	}
	lineCh, errCh := containers.Follow(cmd.Context(), containerName, containers.LogsOptions{
		Tail: tail, Since: since,
	})
	for line := range lineCh {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), line.Text)
	}
	if err := <-errCh; err != nil && cmd.Context().Err() == nil {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
		return localnet.AsExitError(localnet.ExitRuntimeFailure)
	}
	return nil
}

// runLogsTUI runs the bubbletea program.
func runLogsTUI(cmd *cobra.Command, instance, service, containerName string, tail int, since string, follow bool) error {
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()
	model := newLogsModel(ctx, instance, service, containerName, tail, since, follow)
	prog := tea.NewProgram(model,
		tea.WithContext(ctx),
		tea.WithAltScreen(),
	)
	finalModel, err := prog.Run()
	if err != nil {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "TUI error:", err)
		return localnet.AsExitError(localnet.ExitRuntimeFailure)
	}
	if m, ok := finalModel.(*logsModel); ok && m.err != nil {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), m.err)
		return localnet.AsExitError(localnet.ExitRuntimeFailure)
	}
	return nil
}

// ── bubbletea model ──────────────────────────────────────────────

type logLine struct {
	text   string
	stream string
}

type streamMsg logLine
type streamClosedMsg struct{ err error }

type inputMode int

const (
	modeNone inputMode = iota
	modeFilter
	modeGrep
)

type logsModel struct {
	instance      string
	service       string
	containerName string

	lineCh <-chan containers.FollowLine
	errCh  <-chan error

	lines    []logLine
	maxLines int

	width, height int
	scroll        int
	autoFollow    bool

	mode     inputMode
	queryRaw string
	queryRE  *regexp.Regexp
	queryErr string

	streamClosed bool
	err          error

	spinnerFrames []string
	spinnerIdx    int
}

const (
	maxLogLines = 5000
	footerLines = 2
)

func newLogsModel(ctx context.Context, instance, service, containerName string, tail int, since string, follow bool) *logsModel {
	opts := containers.LogsOptions{Tail: tail, Since: since}
	var lineCh <-chan containers.FollowLine
	var errCh <-chan error
	if follow {
		lineCh, errCh = containers.Follow(ctx, containerName, opts)
	} else {
		oneCh := make(chan containers.FollowLine, 64)
		closedCh := make(chan error, 1)
		go func() {
			body, err := containers.Logs(ctx, containerName, opts)
			for _, l := range strings.Split(body, "\n") {
				if l == "" {
					continue
				}
				oneCh <- containers.FollowLine{Text: l, Stream: "stdout"}
			}
			close(oneCh)
			closedCh <- err
			close(closedCh)
		}()
		lineCh = oneCh
		errCh = closedCh
	}
	return &logsModel{
		instance:      instance,
		service:       service,
		containerName: containerName,
		lineCh:        lineCh,
		errCh:         errCh,
		lines:         make([]logLine, 0, 256),
		maxLines:      maxLogLines,
		autoFollow:    true,
		spinnerFrames: []string{"⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
	}
}

func (m *logsModel) Init() tea.Cmd {
	return tea.Batch(waitForLine(m.lineCh, m.errCh), tickSpinner())
}

func waitForLine(lineCh <-chan containers.FollowLine, errCh <-chan error) tea.Cmd {
	return func() tea.Msg {
		l, ok := <-lineCh
		if !ok {
			select {
			case err := <-errCh:
				return streamClosedMsg{err: err}
			default:
				return streamClosedMsg{}
			}
		}
		return streamMsg{text: l.Text, stream: l.Stream}
	}
}

type spinnerTickMsg struct{}

func tickSpinner() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

func (m *logsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case streamMsg:
		m.lines = append(m.lines, logLine{text: msg.text, stream: msg.stream})
		if len(m.lines) > m.maxLines {
			m.lines = m.lines[len(m.lines)-m.maxLines:]
		}
		return m, waitForLine(m.lineCh, m.errCh)

	case streamClosedMsg:
		m.streamClosed = true
		m.err = msg.err
		return m, nil

	case spinnerTickMsg:
		m.spinnerIdx = (m.spinnerIdx + 1) % len(m.spinnerFrames)
		if m.streamClosed {
			return m, nil
		}
		return m, tickSpinner()

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *logsModel) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mode != modeNone {
		switch k.Type {
		case tea.KeyEnter:
			m.compileQuery()
			m.mode = modeNone
			m.scroll = 0
			m.autoFollow = true
			return m, nil
		case tea.KeyEsc:
			m.mode = modeNone
			m.queryRaw = ""
			m.queryRE = nil
			m.queryErr = ""
			return m, nil
		case tea.KeyBackspace, tea.KeyDelete:
			if len(m.queryRaw) > 0 {
				m.queryRaw = m.queryRaw[:len(m.queryRaw)-1]
			}
			return m, nil
		case tea.KeyRunes, tea.KeySpace:
			m.queryRaw += string(k.Runes)
			return m, nil
		}
		return m, nil
	}

	switch k.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "/":
		m.mode = modeFilter
		m.queryRaw = ""
		m.queryErr = ""
		return m, nil
	case "g":
		m.mode = modeGrep
		m.queryRaw = ""
		m.queryErr = ""
		return m, nil
	case "c":
		m.queryRaw = ""
		m.queryRE = nil
		m.queryErr = ""
		return m, nil
	case "G", "end":
		m.scroll = 0
		m.autoFollow = true
		return m, nil
	case "up", "k":
		m.scroll++
		m.autoFollow = false
		return m, nil
	case "down", "j":
		if m.scroll > 0 {
			m.scroll--
		}
		if m.scroll == 0 {
			m.autoFollow = true
		}
		return m, nil
	case "pgup":
		m.scroll += m.viewportRows()
		m.autoFollow = false
		return m, nil
	case "pgdown":
		m.scroll -= m.viewportRows()
		if m.scroll < 0 {
			m.scroll = 0
			m.autoFollow = true
		}
		return m, nil
	}
	return m, nil
}

func (m *logsModel) compileQuery() {
	if m.queryRaw == "" {
		m.queryRE = nil
		m.queryErr = ""
		return
	}
	if m.mode == modeGrep {
		re, err := regexp.Compile(m.queryRaw)
		if err != nil {
			m.queryErr = err.Error()
			m.queryRE = nil
			return
		}
		m.queryRE = re
		m.queryErr = ""
		return
	}
	m.queryRE = regexp.MustCompile("(?i)" + regexp.QuoteMeta(m.queryRaw))
	m.queryErr = ""
}

func (m *logsModel) viewportRows() int {
	rows := m.height - footerLines - 1
	if rows < 1 {
		rows = 1
	}
	return rows
}

func (m *logsModel) filteredLines() []logLine {
	if m.queryRE == nil {
		return m.lines
	}
	out := make([]logLine, 0, len(m.lines))
	for _, l := range m.lines {
		if m.queryRE.MatchString(l.text) {
			out = append(out, l)
		}
	}
	return out
}

// ── view ─────────────────────────────────────────────────────────

var (
	logsHeaderStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#6BD3FF")).Bold(true)
	logsDimStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#888"))
	logsHotkeyStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#EAEAEA")).Bold(true)
	logsBarStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#333"))
	logsErrLineStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B"))
	logsStreamingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#6BD3FF"))
	logsQueryStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFC857"))
	logsErrInlineStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B")).Bold(true)
)

func (m *logsModel) View() string {
	if m.width == 0 || m.height == 0 {
		return "starting…"
	}
	var b strings.Builder

	header := fmt.Sprintf("canton-devkit · localnet logs %s · %s",
		m.service, m.instance)
	b.WriteString(logsHeaderStyle.Render(header))
	b.WriteString("\n")

	view := m.filteredLines()
	maxRows := m.viewportRows()
	start := len(view) - maxRows - m.scroll
	if start < 0 {
		start = 0
	}
	end := start + maxRows
	if end > len(view) {
		end = len(view)
	}
	for _, l := range view[start:end] {
		style := lipgloss.NewStyle()
		if l.stream == "stderr" {
			style = logsErrLineStyle
		}
		line := l.text
		if len(line) > m.width {
			line = line[:m.width-1] + "…"
		}
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}
	for i := end - start; i < maxRows; i++ {
		b.WriteString("\n")
	}

	b.WriteString(m.renderFooter(len(view)))
	return b.String()
}

func (m *logsModel) renderFooter(visibleCount int) string {
	var status string
	if m.streamClosed {
		if m.err != nil {
			status = logsErrInlineStyle.Render("✕ ") +
				logsDimStyle.Render(fmt.Sprintf("stream closed (%s)", firstLogLine(m.err.Error())))
		} else {
			status = logsDimStyle.Render("✓ stream ended")
		}
	} else {
		spin := logsStreamingStyle.Render(m.spinnerFrames[m.spinnerIdx])
		mode := "follow"
		if !m.autoFollow {
			mode = fmt.Sprintf("paused @ +%d", m.scroll)
		}
		status = spin + " " + logsDimStyle.Render(fmt.Sprintf(
			"streaming · %d lines · %s", visibleCount, mode))
	}

	var keys string
	if m.mode != modeNone {
		label := "/ filter"
		if m.mode == modeGrep {
			label = "g grep"
		}
		keys = logsQueryStyle.Render(label+": ") + m.queryRaw + logsQueryStyle.Render("█")
		if m.queryErr != "" {
			keys += "  " + logsErrInlineStyle.Render("regex error: "+m.queryErr)
		}
	} else {
		hk := func(k, label string) string {
			return logsHotkeyStyle.Render(k) + " " + logsDimStyle.Render(label)
		}
		parts := []string{
			hk("/", "filter"),
			hk("g", "grep"),
			hk("c", "clear"),
			hk("↑↓", "scroll"),
			hk("G", "bottom"),
			hk("q", "quit"),
		}
		if m.queryRE != nil {
			parts = append([]string{logsQueryStyle.Render(fmt.Sprintf("[match: %s]", m.queryRaw))}, parts...)
		}
		keys = strings.Join(parts, "   ")
	}

	bar := logsBarStyle.Render(strings.Repeat("─", m.width))
	return bar + "\n" + status + "    " + keys
}

func firstLogLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
