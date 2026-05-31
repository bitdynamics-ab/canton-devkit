package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/telemetry"
)

const appName = "canton-devkit"

// App owns the CLI dependencies so tests can capture output without touching
// process-global stdout, stderr, or os.Exit.
type App struct {
	out       io.Writer
	err       io.Writer
	version   string
	telemetry bool // opt-in at construction; OFF by default so tests never phone home
}

func New(out io.Writer, err io.Writer, version string) *App {
	return &App{out: out, err: err, version: version}
}

// WithTelemetry enables the anonymous, aggregate, opt-out usage telemetry
// for this App. main() turns it on for the real binary; tests leave it
// off so they never touch the real ~/.canton-devkit or the network.
func (a *App) WithTelemetry() *App {
	a.telemetry = true
	return a
}

func (a *App) Run(args []string) int {
	if a.telemetry {
		telemetry.MaybeFirstRunNotice(a.err)
	}
	start := time.Now()

	root := a.buildRoot()
	root.SetArgs(args)
	// ExecuteC returns the command that actually ran so we can record its
	// PATH (never its args/flag values) for telemetry.
	cmd, err := root.ExecuteC()

	code := 0
	if err != nil {
		// Subcommands that need a specific process exit code (e.g. 2 for
		// preflight failure, 3 for timeout) wrap it in localnet.ExitCodeError
		// via localnet.AsExitError. Extract and propagate without printing —
		// those subcommands own their stderr already. Without this, Cobra's
		// default would print the wrapped error string and force exit 1,
		// collapsing every non-zero outcome into the same code.
		var ece localnet.ExitCodeError
		if errors.As(err, &ece) {
			code = int(ece)
		} else {
			_, _ = fmt.Fprintln(a.err, err)
			code = 1
		}
	}

	if a.telemetry {
		// Record ONLY the command path (e.g. "localnet token mint") — never
		// arguments or flag values, which could carry instance names, party
		// ids, or paths. Best-effort; never blocks or fails the command.
		path := commandPath(cmd)
		telemetry.RecordCommand(a.version, path, code, time.Since(start))
	}
	return code
}

// commandPath returns the safe, PII-free command path for telemetry:
// the cobra command chain with the binary name stripped (e.g.
// "localnet token mint"). Returns "localnet" when cmd is nil.
func commandPath(cmd interface{ CommandPath() string }) string {
	if cmd == nil {
		return "localnet"
	}
	p := cmd.CommandPath() // "canton-devkit localnet token mint"
	return strings.TrimSpace(strings.TrimPrefix(p, appName))
}
