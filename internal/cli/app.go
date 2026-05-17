package cli

import "io"

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
	if err := root.Execute(); err != nil {
		return 1
	}
	return 0
}
