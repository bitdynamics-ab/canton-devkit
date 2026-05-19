package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
)

const appName = "canton-devkit"

// App owns the CLI dependencies so tests can capture output without touching
// process-global stdout, stderr, or os.Exit.
type App struct {
	out     io.Writer
	err     io.Writer
	version string
}

func New(out io.Writer, err io.Writer, version string) *App {
	return &App{out: out, err: err, version: version}
}

func (a *App) Run(args []string) int {
	root := a.buildRoot()
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		return 0
	}
	// Subcommands that need a specific process exit code (e.g. 2 for
	// preflight failure, 3 for timeout) wrap it in localnet.ExitCodeError
	// via localnet.AsExitError. Extract and propagate without printing —
	// those subcommands own their stderr already. Without this, Cobra's
	// default would print the wrapped error string and force exit 1,
	// collapsing every non-zero outcome into the same code.
	var ece localnet.ExitCodeError
	if errors.As(err, &ece) {
		return int(ece)
	}
	_, _ = fmt.Fprintln(a.err, err)
	return 1
}
