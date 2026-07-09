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
	commit  string
	// telemetry, when true, installs the real counter sink. Off by default
	// so tests never touch the real config dir or the network; main() turns
	// it on.
	telemetry bool
	// mode records whether the binary was launched directly or via DPM,
	// resolved from args in Run. Defaults to Direct.
	mode InvocationMode
}

func New(out io.Writer, err io.Writer, version, commit string) *App {
	return &App{out: out, err: err, version: version, commit: commit}
}

// versionString renders the version, appending the short git commit hash in
// parentheses when one is known.
func (a *App) versionString() string {
	if a.commit == "" {
		return a.version
	}
	return fmt.Sprintf("%s (%s)", a.version, a.commit)
}

// WithTelemetry enables the anonymous, aggregate, opt-out usage telemetry
// for this App (counter-based, weekly aggregates). Tests leave it off.
func (a *App) WithTelemetry() *App {
	a.telemetry = true
	return a
}

func (a *App) Run(args []string) int {
	// Resolve direct-vs-DPM from a leading marker DPM prepends via the
	// component manifest, then strip it so Cobra only sees real args. Done
	// first so help/examples and flag visibility reflect the mode.
	a.mode, args = detectMode(args)

	// Install the real sink only when telemetry is on AND effectively
	// enabled; otherwise the package's no-op sink stays installed, so even
	// a stray Inc records nothing.
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
		// preflight failure) wrap it in localnet.ExitCodeError. Propagate
		// without printing — those subcommands own their stderr already.
		var ece localnet.ExitCodeError
		if errors.As(err, &ece) {
			code = int(ece)
		} else {
			_, _ = fmt.Fprintln(a.err, err)
			code = 1
		}
	}

	if a.telemetry && telemetry.IsEnabled() {
		// Counters are gated on a REAL localnet verb: meta commands
		// (`version`, `telemetry …`, `help`, completion) must not count
		// toward adoption — auditing the telemetry config repeatedly would
		// otherwise inflate the adoption picture. The verb is a closed
		// allow-list bucket, never args or flags.
		verb := localnetVerb(cmd)
		if verb != "" {
			telemetry.Inc("dpm/command", verb)
			outcome := "ok"
			if code != 0 {
				outcome = "fail"
			}
			telemetry.Inc("dpm/command_exit", verb+"/"+outcome)
			// The verb only says "token"; record which token subcommand
			// ran (create / mint / transfer / burn / balance).
			if verb == "token" {
				if act := tokenAction(cmd); act != "" {
					telemetry.Inc("dpm/token_action", act)
				}
			}
			telemetry.RecordContext()
			// dpm/install: once per machine (first non-CI run) — the
			// privacy-preserving device-count proxy. No-op on later runs.
			telemetry.RecordInstallOnce()
		}
		// Persist + run the upload window regardless, so `telemetry flush`
		// (a meta command) can still ship already-recorded counters.
		telemetry.Persist()
	}
	return code
}

// localnetVerb extracts the first localnet subcommand (e.g. "token" for
// `localnet token mint`) — the bucket for the dpm/command counter. Returns
// "" for root/version/telemetry/help. Derived from the cobra command chain,
// never from raw args, so no flag value can leak.
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

// tokenAction returns the direct subcommand of `localnet token` (the
// bucket for dpm/token_action), or "" when the command isn't under
// `localnet token`. Derived from the cobra command chain, never raw args,
// so no flag or instrument name can leak.
func tokenAction(cmd interface{ CommandPath() string }) string {
	if cmd == nil {
		return ""
	}
	parts := strings.Fields(cmd.CommandPath()) // [...,"localnet","token","mint"]
	for i, p := range parts {
		if p == "token" && i+1 < len(parts) {
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
