package localnet

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/term"
	"github.com/spf13/cobra"
)

// buildDown wires `dpm localnet down --name <inst>`. The state
// machine it drives:
//
//	docker compose down → NO --volumes (preserves state)
//	StatusRunning → StatusStopping (BEFORE compose call)
//	  └─ compose ok → StatusStopped (registry entry preserved for `clean`)
//	  └─ compose err → StatusFailed (preserved for retry) exit 4
//	  └─ interrupted → StatusPartial (compose may still be in flight) exit 3
//
// The transitional StatusStopping is critical: if we SIGKILL during
// compose-down without it, `localnet status` keeps reporting
// `running` while containers are partially gone.
//
// Output: three Step rows + a brand-accented Box.
func buildDown() *cobra.Command {
	var (
		name  string
		force bool
	)
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Stop a Canton LocalNet instance (preserves volumes)",
		Long: `Stops the named LocalNet instance gracefully and detaches its
Docker networks. Volumes and the registry entry are preserved so
a follow-up

  dpm localnet up --name <name>

resumes from the same on-disk state.

To remove docker volumes (destructive — drops all ledger state),
use ` + "`dpm localnet clean --name <name>`" + ` after down.

If a normal down fails (e.g. a container is unhealthy or
restart-looping after an out-of-memory kill, or the cached compose
env is broken), pass --force: it tears the instance down by Docker
project label only — bypassing the compose files/env that the
normal path needs — and SIGKILLs containers that won't stop.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := localnet.ValidateName(name); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			return localnet.AsExitError(
				RunDown(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(),
					DownOptions{Name: name, Force: force}))
		},
	}
	cmd.Flags().StringVar(&name, "name", "",
		"Required. Identifier of the LocalNet instance to stop.")
	cmd.Flags().BoolVar(&force, "force", false,
		"Tear down by Docker project label only — resilient to an unhealthy/wedged instance a normal down can't remove.")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

// DownOptions carries the `localnet down` flags into RunDown. A
// stable shape so the Web UI handler (POST /api/instances/:name/down)
// can pass the same parameter.
type DownOptions struct {
	Name string
	// Force tears the instance down by Docker project label only,
	// bypassing the compose files/env the normal path needs — for
	// unsticking an instance a normal down can't remove (unhealthy,
	// OOM-restarting, or with a broken/missing compose env).
	Force bool
}

// stopperFn is the seam tests use to bypass docker. Production
// callers leave it nil; production builds a real
// docker.ComposeRunner and calls Stop(ctx, false). A test sets
// stopperFn to a fake that records the call and returns whatever
// the test wants.
//
// Modelled as a package var rather than a parameter on RunDown
// because RunDown is also called by the Web UI handler — neither
// caller wants a fake-runner argument in production. RunDown is
// reachable from concurrent goroutines (one per HTTP request), so
// the read of stopperFn is guarded by stopperMu to avoid a data
// race. Tests use installStopper (below) which swaps under the mutex
// and restores via t.Cleanup.
var (
	stopperMu sync.RWMutex
	stopperFn func(ctx context.Context, state *registry.State) error
)

// getStopper returns the active stopper under the read lock.
func getStopper() func(ctx context.Context, state *registry.State) error {
	stopperMu.RLock()
	defer stopperMu.RUnlock()
	return stopperFn
}

// installStopper is the test-only helper that swaps stopperFn under
// the write lock and registers the restore via t.Cleanup. Tests
// MUST use this rather than `stopperFn = ...` directly so the
// mutex contract holds.
func installStopper(t interface {
	Cleanup(func())
}, fn func(ctx context.Context, state *registry.State) error) {
	stopperMu.Lock()
	prev := stopperFn
	stopperFn = fn
	stopperMu.Unlock()
	t.Cleanup(func() {
		stopperMu.Lock()
		stopperFn = prev
		stopperMu.Unlock()
	})
}

// RunDown is exported so the Web UI handler can call the same code
// path without forking the implementation.
func RunDown(ctx context.Context, out io.Writer, errw io.Writer, opts DownOptions) int {
	ctx, stopSignal := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stopSignal()

	state, err := registry.Read(opts.Name)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			// A missing entry is the no-op idempotent case for
			// scripts. We also scrub any orphan index row
			// defensively.
			if delErr := registry.Delete(opts.Name); delErr != nil {
				_, _ = fmt.Fprintf(errw,
					"warning: could not scrub orphan registry entry: %s\n", delErr)
			}
			_, _ = fmt.Fprintln(out, term.Dimc(fmt.Sprintf(
				"No instance named %q is registered. Nothing to do.", opts.Name)))
			return localnet.ExitSuccess
		}
		_, _ = fmt.Fprintf(errw, "read registry state: %s\n", err)
		return localnet.ExitRuntimeFailure
	}

	// Lock-ordering matters here: reading state, branching on
	// StatusStopped, THEN acquiring the lock would be racy if a
	// concurrent `down` (or Web UI POST) flipped status between the
	// Read and the Lock. So: take the lock first, re-Read inside the
	// critical section, and branch on the authoritative state. The
	// early `state` Read above stays only to provide a friendly
	// ErrNotFound message before we even attempt to lock.
	release, err := registry.Lock(opts.Name)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "%s\n", err)
		return localnet.ExitUserError
	}
	defer release()

	// Re-read under the lock — between the pre-lock Read above
	// and now, a concurrent writer may have changed status.
	if fresh, rerr := registry.Read(opts.Name); rerr == nil {
		state = fresh
	}
	if state.Status == registry.StatusStopped {
		_, _ = fmt.Fprintln(out, term.Dimc(fmt.Sprintf(
			"LocalNet %q is already stopped.", opts.Name)))
		return localnet.ExitSuccess
	}

	_, _ = fmt.Fprintln(out, term.Prompt("", "", "", fmt.Sprintf(
		"dpm localnet down %s %s",
		term.Amberc("--name"), opts.Name)))
	_, _ = fmt.Fprintln(out)

	// Persist transitional state BEFORE compose. If we crash or
	// SIGKILL mid-teardown without this, `localnet status` keeps
	// reporting "running" while containers are partially down. A
	// write failure here is non-fatal.
	state.Status = registry.StatusStopping
	if werr := registry.Write(state); werr != nil {
		_, _ = fmt.Fprintf(errw, "warning: could not persist stopping state: %s\n", werr)
	}

	stop := getStopper()
	if stop == nil {
		if opts.Force {
			stop = defaultForceStop
		} else {
			stop = defaultStop
		}
	}

	start := time.Now()
	_, _ = fmt.Fprintln(out, term.Step(term.StepBusy,
		"Draining ledger", "in-flight commands", ""))

	if err := stop(ctx, state); err != nil {
		// Two distinct outcomes here:
		// ctx.Err() != nil → interrupted, exit 3, status partial.
		// compose may still be running on the host;
		// user retries when the host is idle.
		// ctx.Err() == nil → genuine compose failure, exit 4, status failed.
		// registry entry + data dir preserved so user
		// can retry.
		if ctx.Err() != nil {
			_, _ = fmt.Fprintln(errw, term.Errorc(
				"Interrupted during docker compose down — retry once the host is idle"))
			markStatus(state, registry.StatusPartial, errw)
			return localnet.ExitTimeout
		}
		_, _ = fmt.Fprintf(errw, "%s\n",
			term.Step(term.StepCross, "Compose down failed", err.Error(), ""))
		_, _ = fmt.Fprintln(errw, term.Dimc(fmt.Sprintf(
			"Registry state preserved at %s. Re-run `localnet down --name %s` "+
				"once the host is healthy.",
			state.DataDir, opts.Name)))
		markStatus(state, registry.StatusFailed, errw)
		return localnet.ExitRuntimeFailure
	}

	_, _ = fmt.Fprintln(out, term.Step(term.StepCheck,
		"Stopping services", state.ComposeProject, elapsedSince(start)))
	_, _ = fmt.Fprintln(out, term.Step(term.StepCheck,
		"Detaching networks · keeping volumes", "", ""))

	// Preserve the registry entry with status=stopped so that a
	// subsequent `localnet clean` can discover the compose project
	// and remove Docker volumes.
	state.Status = registry.StatusStopped
	if werr := registry.Write(state); werr != nil {
		_, _ = fmt.Fprintf(errw,
			"warning: services stopped but registry write failed: %s\n", werr)
	}

	// Deregister from the shared observability stack (#39); tear it down
	// if no instance still references it. Best-effort.
	_ = localnet.DeregisterInstanceTargets(opts.Name)
	_ = localnet.TeardownSharedStackIfIdle(ctx, io.Discard)

	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, term.Box(term.BoxBrand,
		fmt.Sprintf("%s Stopped LocalNet %s %s\n%s",
			term.Brandc("✦"),
			term.Bold("\""+opts.Name+"\""),
			term.Dimc("· state preserved."),
			term.Dimc(fmt.Sprintf("Run %s to resume, or %s to remove volumes.",
				term.Textc(fmt.Sprintf("localnet up --name %s", opts.Name)),
				term.Textc("localnet clean"))))))

	return localnet.ExitSuccess
}

// defaultStop is the production implementation that drives docker
// compose. Extracted so tests can swap stopperFn without exec'ing
// docker, and to keep RunDown's main path readable.
func defaultStop(ctx context.Context, st *registry.State) error {
	ce, err := localnet.ComposeEnvForInstance(st, nil)
	if err != nil {
		return fmt.Errorf("reconstruct compose env for %q: %w", st.Name, err)
	}
	runner := &docker.ComposeRunner{
		ProjectName:  st.ComposeProject,
		ComposeFiles: st.ComposeFiles,
		WorkDir:      st.ProjectDir,
		EnvFiles:     ce.EnvFiles,
		Env:          ce.Env,
		LogWriter:    io.Discard, // compose's logs are noisy; Step rows tell the user what's happening
		// Replay the up-time --profile set so `compose -f down` reaches
		// every profile-gated Splice service instead of skipping them all
		// and reporting a false success (down --force uses the label-only
		// ForceStop path, which is profile-agnostic).
		Profiles: localnet.ComposeProfilesFor(st),
	}
	return runner.Stop(ctx, false)
}

// defaultForceStop is the production stopper for `down --force`. It needs
// only the project name — ForceStop tears down by Docker project label
// without the -f compose files / env, so it works even when those are
// broken or a container is wedged.
func defaultForceStop(ctx context.Context, st *registry.State) error {
	runner := &docker.ComposeRunner{
		ProjectName: st.ComposeProject,
		LogWriter:   io.Discard,
	}
	return runner.ForceStop(ctx, false)
}

// markStatus persists `s` for `state`. A write failure is soft —
// compose may have already done its job and the user's containers
// state on disk is authoritative; we just couldn't record it.
func markStatus(state *registry.State, s registry.Status, errw io.Writer) {
	state.Status = s
	if err := registry.Write(state); err != nil {
		_, _ = fmt.Fprintf(errw, "warning: could not record %s state: %s\n", s, err)
	}
}

// elapsedSince formats a duration as the short "0.2s" / "1.4s" form
// the Step rows use. One decimal place for sub-second so fast
// operations don't render as "0s".
func elapsedSince(start time.Time) string {
	d := time.Since(start)
	if d < time.Second {
		return fmt.Sprintf("%.1fs", float64(d.Milliseconds())/1000)
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
