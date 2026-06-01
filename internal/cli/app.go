package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/telemetry"
)

const appName = "canton-devkit"

// App owns the CLI dependencies so tests can capture output without touching
// process-global stdout, stderr, or os.Exit.
type App struct {
	out     io.Writer
	err     io.Writer
	version string
	// telemetry, when true, installs the real counter sink and records the
	// per-invocation counters. OFF by default so tests never touch the real
	// config dir or the network. main() turns it on.
	telemetry bool
}

func New(out io.Writer, err io.Writer, version string) *App {
	return &App{out: out, err: err, version: version}
}

// WithTelemetry enables the anonymous, aggregate, opt-out usage telemetry
// for this App (counter-based, weekly aggregates). Tests leave it off.
func (a *App) WithTelemetry() *App {
	a.telemetry = true
	return a
}

func (a *App) Run(args []string) int {
	// Install the real sink only when telemetry is on AND effectively
	// enabled. Disabled / DO_NOT_TRACK / off-by-config → the package's
	// no-op sink stays installed, so even a stray Inc records nothing.
	if a.telemetry && telemetry.IsEnabled() {
		telemetry.Install(telemetry.NewSink(telemetry.Channel()))
		// One-time opt-out notice — only for an interactive, operational
		// invocation (not version/help/telemetry, not CI/pipes).
		telemetry.MaybeNotice(a.err, firstVerb(args), interactive())
	}

	root := a.buildRoot()
	root.SetArgs(args)
	cmd, err := root.ExecuteC()

	code := 0
	if err != nil {
		// Subcommands that need a specific process exit code (e.g. 2 for
		// preflight failure, 3 for timeout) wrap it in localnet.ExitCodeError
		// via localnet.AsExitError. Extract and propagate without printing —
		// those subcommands own their stderr already.
		var ece localnet.ExitCodeError
		if errors.As(err, &ece) {
			code = int(ece)
		} else {
			_, _ = fmt.Fprintln(a.err, err)
			code = 1
		}
	}

	if a.telemetry && telemetry.IsEnabled() {
		// Record aggregate counters for this run — the verb (a closed
		// allow-list bucket, never args/flags), its ok/fail outcome, and
		// the cheap context counters (channel/os/arch/ci/llm_agent). Deep
		// code (preflight/doctor) adds docker_engine / compose / doctor_fail
		// via telemetry.Inc directly. Then persist + run the weekly upload.
		verb := localnetVerb(cmd)
		if verb != "" {
			telemetry.Inc("dpm/command", verb)
			outcome := "ok"
			if code != 0 {
				outcome = "fail"
			}
			telemetry.Inc("dpm/command_exit", verb+"/"+outcome)
		}
		telemetry.RecordContext()
		telemetry.Persist()
	}
	return code
}

// localnetVerb extracts the first localnet subcommand (e.g. "token" for
// `localnet token mint`, "up" for `localnet up`) — the bucket for the
// dpm/command counter. Returns "" for root/version/telemetry/help so
// those don't pollute the command counter. Derived from the cobra command
// chain, never from raw args, so no flag value can leak.
func localnetVerb(cmd interface{ CommandPath() string }) string {
	if cmd == nil {
		return ""
	}
	parts := strings.Fields(cmd.CommandPath()) // ["canton-devkit","localnet","token","mint"]
	for i, p := range parts {
		if p == "localnet" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// firstVerb returns the first localnet subcommand from raw args, for the
// notice gating (which runs before cobra parses). "" when not a localnet
// invocation.
func firstVerb(args []string) string {
	seenLocalnet := false
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if !seenLocalnet {
			if a == "localnet" {
				seenLocalnet = true
			}
			continue
		}
		return a
	}
	return ""
}

// interactive reports whether the process is attached to a terminal (and
// not in CI) — the gate for the one-time notice.
func interactive() bool {
	if v := os.Getenv("CI"); v != "" && v != "0" && v != "false" {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
