package cli

import (
	"fmt"
	"io"
	"strings"
)

const appName = "canton-devkit"

var localnetCommands = map[string]string{
	"up":      "start a Canton LocalNet instance",
	"down":    "stop a Canton LocalNet instance",
	"restart": "restart a Canton LocalNet instance",
	"clean":   "remove Canton LocalNet state",
	"status":  "show Canton LocalNet status",
	"logs":    "show Canton LocalNet logs",
}

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
	if len(args) == 0 {
		_, _ = fmt.Fprint(a.out, rootHelp())
		return 0
	}

	switch args[0] {
	case "-h", "--help", "help":
		_, _ = fmt.Fprint(a.out, rootHelp())
		return 0
	case "-v", "--version", "version":
		_, _ = fmt.Fprintf(a.out, "%s %s\n", appName, a.version)
		return 0
	case "localnet":
		return a.runLocalnet(args[1:])
	default:
		_, _ = fmt.Fprintf(a.err, "unknown command %q\n\n%s", args[0], rootHelp())
		return 1
	}
}

func (a *App) runLocalnet(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		_, _ = fmt.Fprint(a.out, localnetHelp())
		return 0
	}

	command := args[0]
	description, ok := localnetCommands[command]
	if !ok {
		_, _ = fmt.Fprintf(a.err, "unknown localnet command %q\n\n%s", command, localnetHelp())
		return 1
	}

	_, _ = fmt.Fprintf(a.out, "localnet %s: %s is not implemented yet\n", command, description)
	return 0
}

func rootHelp() string {
	return strings.TrimLeft(`
canton-devkit manages Canton LocalNet developer environments.

Usage:
  canton-devkit [command]

Commands:
  localnet    Manage Canton LocalNet instances
  version     Print version information
  help        Show this help message

Options:
  -h, --help      Show help
  -v, --version   Print version information
`, "\n")
}

func localnetHelp() string {
	return strings.TrimLeft(`
Manage Canton LocalNet instances.

Usage:
  canton-devkit localnet [command]

Commands:
  up         Start a Canton LocalNet instance
  down       Stop a Canton LocalNet instance
  restart    Restart a Canton LocalNet instance
  clean      Remove Canton LocalNet state
  status     Show Canton LocalNet status
  logs       Show Canton LocalNet logs

Options:
  -h, --help      Show help
`, "\n")
}
