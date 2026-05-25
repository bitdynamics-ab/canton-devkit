package localnet

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/signal"
	"syscall"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

// buildDown wires `dpm localnet down --name <inst>` — BIT-124.
//
// Semantics: stop containers + detach networks, but **preserve
// volumes** so a follow-up `up` against the same name resumes. The
// destructive wipe lives in `localnet clean` (separate command,
// confirmation prompt — implemented later).
//
// Output matches docs/design/mockups/screens-lifecycle.jsx
// (ScreenDown): three Step rows + a brand-accented Box confirming
// preservation. The "draining ledger" message is informational —
// docker compose itself handles graceful shutdown — but the visual
// step makes it obvious that down is doing more than a hard kill.
func buildDown() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Stop a Canton LocalNet instance (preserves volumes)",
		Long: `Stops the named LocalNet instance gracefully and detaches its
Docker networks. Volumes are preserved so a follow-up

  dpm localnet up --name <name>

resumes from the same on-disk state.

To remove volumes (destructive — drops all ledger state), use
` + "`dpm localnet clean --name <name>`" + ` instead.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := localnet.ValidateName(name); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			return localnet.AsExitError(
				RunDown(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), name))
		},
	}
	cmd.Flags().StringVar(&name, "name", "",
		"Required. Identifier of the LocalNet instance to stop.")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

// stopperFn is the seam tests use to bypass docker. Production
// callers leave it nil; the production path builds a real
// docker.ComposeRunner and calls Stop(ctx, false). A test sets
// stopperFn to a fake that records the call and returns whatever
// the test wants.
//
// Modelled as a package var rather than a parameter on RunDown
// because RunDown is also called by the future Web UI handler — and
// neither caller wants a fake-runner argument in production. Tests
// that swap the var must do so within a t.Cleanup-restored scope;
// `go test` serialises by default which is fine here.
var stopperFn func(ctx context.Context, state *registry.State) error

// RunDown is exported so the future Web UI handler
// (P2-03 POST /api/instances/:name/down) can call the same code
// path without forking the implementation. The CLI command is a
// thin shell that wires opts.
func RunDown(ctx context.Context, out io.Writer, errw io.Writer, name string) int {
	ctx, stopSignal := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stopSignal()

	state, err := registry.Read(name)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			_, _ = fmt.Fprintf(errw, "no LocalNet instance named %q\n", name)
			return localnet.ExitUserError
		}
		_, _ = fmt.Fprintf(errw, "read registry state: %s\n", err)
		return localnet.ExitRuntimeFailure
	}

	if state.Status == registry.StatusStopped {
		// No-op is the right answer — script callers wrapping
		// `down` should be able to call it idempotently without
		// special-casing "already down."
		_, _ = fmt.Fprintln(out, term.Dimc(fmt.Sprintf(
			"LocalNet %q is already stopped.", name)))
		return localnet.ExitSuccess
	}

	release, err := registry.Lock(name)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "%s\n", err)
		return localnet.ExitUserError
	}
	defer release()

	_, _ = fmt.Fprintln(out, term.Prompt("", "", "", fmt.Sprintf(
		"dpm localnet down %s %s",
		term.Amberc("--name"), name)))
	_, _ = fmt.Fprintln(out)

	stop := stopperFn
	if stop == nil {
		stop = defaultStop
	}

	start := time.Now()
	_, _ = fmt.Fprintln(out, term.Step(term.StepBusy,
		"Draining ledger", "in-flight commands", ""))

	if err := stop(ctx, state); err != nil {
		if ctx.Err() != nil {
			_, _ = fmt.Fprintln(errw,
				term.Errorc("Interrupted while stopping services"))
			markFailedStop(state, errw)
			return localnet.ExitTimeout
		}
		_, _ = fmt.Fprintf(errw, "%s\n",
			term.Step(term.StepCross, "Compose down failed", err.Error(), ""))
		markFailedStop(state, errw)
		return localnet.ExitRuntimeFailure
	}

	_, _ = fmt.Fprintln(out, term.Step(term.StepCheck,
		"Stopping services", state.ComposeProject, elapsedSince(start)))
	_, _ = fmt.Fprintln(out, term.Step(term.StepCheck,
		"Detaching networks · keeping volumes", "", ""))

	state.Status = registry.StatusStopped
	if err := registry.Write(state); err != nil {
		// Compose already stopped successfully; a state-write failure
		// is a soft warning rather than an error code — the user's
		// containers ARE stopped, we just couldn't record it.
		_, _ = fmt.Fprintf(errw,
			"warning: services stopped but registry write failed: %s\n", err)
	}

	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, term.Box(term.BoxBrand,
		fmt.Sprintf("%s Stopped LocalNet %s %s\n%s",
			term.Brandc("✦"),
			term.Bold("\""+name+"\""),
			term.Dimc("· state preserved."),
			term.Dimc(fmt.Sprintf("Run %s to resume, or %s to remove volumes.",
				term.Textc(fmt.Sprintf("localnet up --name %s", name)),
				term.Textc("localnet clean"))))))

	return localnet.ExitSuccess
}

// defaultStop is the production implementation that drives docker
// compose. Extracted so tests can swap stopperFn without exec'ing
// docker, and to keep RunDown's main path readable.
func defaultStop(ctx context.Context, st *registry.State) error {
	runner := &docker.ComposeRunner{
		ProjectName:  st.ComposeProject,
		ComposeFiles: st.ComposeFiles,
		WorkDir:      st.ProjectDir,
		LogWriter:    io.Discard, // compose's logs are noisy; Step rows tell the user what's happening
	}
	return runner.Stop(ctx, false)
}

// markFailedStop flips the registry status to "partial" so a later
// `status` shows the half-stopped state instead of pretending
// everything's fine. Mirrors the same pattern up.go uses on a
// mid-bring-up failure.
func markFailedStop(state *registry.State, errw io.Writer) {
	state.Status = registry.StatusPartial
	if err := registry.Write(state); err != nil {
		_, _ = fmt.Fprintf(errw, "warning: could not record failed stop: %s\n", err)
	}
}

// elapsedSince formats a duration as the short "0.2s" / "1.4s" form
// the mockup Step rows use. One decimal place for sub-second so fast
// operations don't render as "0s".
func elapsedSince(start time.Time) string {
	d := time.Since(start)
	if d < time.Second {
		return fmt.Sprintf("%.1fs", float64(d.Milliseconds())/1000)
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
