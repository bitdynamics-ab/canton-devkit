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

// Run executes the root Cobra command. Exit code semantics:
//   - nil error → 0
//   - localnet.ExitCodeError(n) → n (no error printed; subcommand owns stderr)
//   - any other error → 1, with the message printed to stderr
func (a *App) Run(args []string) int {
	root := a.buildRoot()
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		var code localnet.ExitCodeError
		if errors.As(err, &code) {
			return int(code)
		}
		_, _ = fmt.Fprintln(a.err, err)
		return 1
	}
	return 0
}
