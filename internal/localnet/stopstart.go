package localnet

import (
	"context"
	"fmt"
	"io"
	"os/signal"
	"syscall"

	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/containers"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// composeStopper is the subset of *docker.ComposeRunner that RunStop
// drives. Extracted for the test seam. *docker.ComposeRunner satisfies
// it.
type composeStopper interface {
	StopContainers(ctx context.Context) error
}

// composeStarter is the subset of *docker.ComposeRunner that RunStart's
// fast path drives. *docker.ComposeRunner satisfies it.
type composeStarter interface {
	Start(ctx context.Context) error
	WaitForHealthy(ctx context.Context) error
}

// StopOptions captures `localnet stop` flags.
type StopOptions struct {
	Name string

	// NewRunner is a test-only seam. When nil, the real
	// *docker.ComposeRunner is built.
	NewRunner func(projectName, workDir string, logw io.Writer) composeStopper
}

// StartOptions captures `localnet start` flags.
type StartOptions struct {
	Name string
	// SkipWait skips the post-start readiness poll on the fast
	// compose-start path. Ignored on the `up` fallback path (RunUp
	// always waits).
	SkipWait bool

	// NewRunner is a test-only seam for the fast compose-start path.
	// When nil, the real *docker.ComposeRunner is built.
	NewRunner func(projectName, workDir string, logw io.Writer) composeStarter

	// listContainers is a test-only seam over containers.List, used to
	// decide fast-start vs up-fallback without a live docker daemon.
	// When nil, the real containers.List is used.
	listContainers func(ctx context.Context, project string) ([]containers.Info, error)

	// runUp is a test-only seam over RunUp for the fallback path. When
	// nil, the real RunUp is called.
	runUp func(ctx context.Context, prog Progress, opts *UpOptions) int
}

// RunStop gracefully stops a running (or paused) instance via
// `docker compose stop` (SIGTERM→SIGKILL). Unlike RunDown it does NOT
// remove containers, networks, or volumes — the stopped containers can
// be restarted cheaply with RunStart (no image pull, no re-create).
// Ledger state is preserved. Resource-wise it frees CPU and memory
// (the processes exit), unlike pause which only freezes them.
//
// Exit: ExitSuccess on stop, ExitUserError when the instance isn't
// registered or isn't running/paused, ExitRuntimeFailure on a compose
// error, ExitTimeout on interruption.
func RunStop(ctx context.Context, out, errw io.Writer, opts *StopOptions) int {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := ValidateName(opts.Name); err != nil {
		_, _ = fmt.Fprintf(errw, "%s\n", err)
		return ExitUserError
	}
	release, err := registry.Lock(opts.Name)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "%s\n", err)
		return ExitUserError
	}
	defer release()

	state, err := registry.Read(opts.Name)
	if err == registry.ErrNotFound {
		_, _ = fmt.Fprintf(errw,
			"No instance named %q is registered. Run `localnet up %s` first.\n",
			opts.Name, opts.Name)
		return ExitUserError
	}
	if err != nil {
		_, _ = fmt.Fprintf(errw, "Failed to read state: %s\n", err)
		return ExitRuntimeFailure
	}
	if state.Status == registry.StatusStopped {
		_, _ = fmt.Fprintf(out, "Canton LocalNet %q is already stopped.\n", state.Name)
		return ExitSuccess
	}
	if state.Status != registry.StatusRunning && state.Status != registry.StatusPaused {
		_, _ = fmt.Fprintf(errw,
			"Instance %q is %s, not running — cannot stop it. Use `localnet down %s` to tear it down.\n",
			state.Name, state.Status, state.Name)
		return ExitUserError
	}

	_, _ = fmt.Fprintf(out, "Stopping Canton LocalNet %q...\n", state.Name)

	// Persist transitional state BEFORE compose so a crash mid-stop
	// doesn't leave `status` reporting running. A write failure is soft.
	state.Status = registry.StatusStopping
	if werr := registry.Write(state); werr != nil {
		_, _ = fmt.Fprintf(errw, "Warning: could not persist stopping state: %s\n", werr)
	}

	var runner composeStopper
	if opts.NewRunner != nil {
		runner = opts.NewRunner(state.ComposeProject, state.ProjectDir, out)
	} else {
		// StopContainers is `-p`-only (profile-agnostic), so the runner
		// needs only the project name + a workdir for the chdir.
		runner = &docker.ComposeRunner{
			ProjectName: state.ComposeProject,
			WorkDir:     state.ProjectDir,
			LogWriter:   out,
		}
	}

	if err := runner.StopContainers(ctx); err != nil {
		if ctx.Err() != nil {
			_, _ = fmt.Fprintln(errw, "Interrupted during docker compose stop — retry once the host is idle")
			state.Status = registry.StatusPartial
			_ = registry.Write(state)
			return ExitTimeout
		}
		_, _ = fmt.Fprintf(errw, "%s\n", err)
		state.Status = registry.StatusFailed
		_ = registry.Write(state)
		return ExitRuntimeFailure
	}

	state.Status = registry.StatusStopped
	if werr := registry.Write(state); werr != nil {
		_, _ = fmt.Fprintf(errw, "Warning: services stopped but registry write failed: %s\n", werr)
	}
	_, _ = fmt.Fprintf(out,
		"Canton LocalNet %q stopped (containers preserved). Run `localnet start %s` to resume.\n",
		state.Name, state.Name)
	return ExitSuccess
}

// RunStart brings an instance to `running`, whatever its current state:
//
//   - not registered            → full bring-up (RunUp, defaults)
//   - stopped, containers exist  → fast `docker compose start` + wait
//   - stopped, containers gone   → RunUp reusing recorded version/profiles
//   - failed/partial             → RunUp reusing recorded version/profiles
//   - paused                     → error (use `localnet resume`)
//   - running                    → idempotent no-op success
//
// The fast path re-captures Canton's ephemeral gRPC ports afterward
// (Docker re-assigns them on start, same as restart). prog is used only
// on the RunUp fallback paths; the fast path writes plain lines to out.
func RunStart(ctx context.Context, prog Progress, out, errw io.Writer, opts *StartOptions) int {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := ValidateName(opts.Name); err != nil {
		_, _ = fmt.Fprintf(errw, "%s\n", err)
		return ExitUserError
	}

	runUp := opts.runUp
	if runUp == nil {
		runUp = RunUp
	}

	// Not registered → nothing to fast-start; do a full bring-up so
	// `start <new-name>` just works. Take no lock here — RunUp acquires
	// its own.
	state, err := registry.Read(opts.Name)
	if err == registry.ErrNotFound {
		_, _ = fmt.Fprintf(out,
			"No instance named %q yet — running localnet up...\n", opts.Name)
		return runUp(ctx, prog, &UpOptions{Name: opts.Name})
	}
	if err != nil {
		_, _ = fmt.Fprintf(errw, "Failed to read state: %s\n", err)
		return ExitRuntimeFailure
	}

	if state.Status == registry.StatusRunning {
		_, _ = fmt.Fprintf(out, "Canton LocalNet %q is already running.\n", state.Name)
		return ExitSuccess
	}
	if state.Status == registry.StatusPaused {
		_, _ = fmt.Fprintf(errw,
			"Instance %q is paused, not stopped. Run `localnet resume %s` (or `unpause`) to continue it.\n",
			state.Name, state.Name)
		return ExitUserError
	}

	// stopped / failed / partial: decide fast-start vs up-fallback by
	// probing whether the containers still exist. If a `down` (or a
	// direct `docker compose down`) removed them, `compose start` can't
	// work — fall back to a full bring-up reusing the recorded config.
	list := opts.listContainers
	if list == nil {
		list = containers.List
	}
	existing, listErr := list(ctx, state.ComposeProject)
	if listErr != nil || len(existing) == 0 {
		_, _ = fmt.Fprintf(out,
			"Containers for %q were removed — running localnet up to recreate them...\n",
			state.Name)
		return runUp(ctx, prog, &UpOptions{
			Name:     state.Name,
			Version:  state.SpliceVersion,
			Profiles: composeProfiles(state),
		})
	}

	return fastStart(ctx, out, errw, opts, state)
}

// fastStart runs the cheap `docker compose start` path: the containers
// already exist and only need their processes restarted.
func fastStart(ctx context.Context, out, errw io.Writer, opts *StartOptions, state *registry.State) int {
	release, err := registry.Lock(opts.Name)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "%s\n", err)
		return ExitUserError
	}
	defer release()

	// Re-read under the lock in case a concurrent writer changed state
	// between the pre-lock Read and now.
	if fresh, rerr := registry.Read(opts.Name); rerr == nil {
		state = fresh
	}

	_, _ = fmt.Fprintf(out, "Starting Canton LocalNet %q...\n", state.Name)

	var runner composeStarter
	if opts.NewRunner != nil {
		runner = opts.NewRunner(state.ComposeProject, state.ProjectDir, out)
	} else {
		runner = &docker.ComposeRunner{
			ProjectName: state.ComposeProject,
			WorkDir:     state.ProjectDir,
			LogWriter:   out,
		}
	}

	if err := runner.Start(ctx); err != nil {
		if ctx.Err() != nil {
			_, _ = fmt.Fprintln(errw, "Interrupted during docker compose start — retry once the host is idle")
			return ExitTimeout
		}
		_, _ = fmt.Fprintf(errw,
			"%s\nIf containers were removed, run `localnet up %s` instead.\n",
			err, state.Name)
		return ExitRuntimeFailure
	}

	if !opts.SkipWait {
		if err := runner.WaitForHealthy(ctx); err != nil {
			if ctx.Err() != nil {
				_, _ = fmt.Fprintln(errw, "Interrupted while waiting for services to become healthy")
				return ExitTimeout
			}
			// Containers aren't healthy yet; leave port re-capture to
			// the background reconciler once the instance reaches
			// running (see restart.go for the same reasoning).
			_, _ = fmt.Fprintf(errw, "Services did not become healthy after start: %s\n", err)
			state.Status = registry.StatusPartial
			_ = registry.Write(state)
			return ExitTimeout
		}
	}

	// Re-capture Canton's ephemeral gRPC ports — Docker may re-assign
	// them on start. Best-effort merge (empty result is a no-op).
	for key, port := range CaptureCantonPorts(ctx, state.ComposeProject) {
		state.Ports[key] = port
	}
	state.Status = registry.StatusRunning
	if err := registry.Write(state); err != nil {
		_, _ = fmt.Fprintf(errw, "Warning: could not persist post-start state: %s\n", err)
	}

	_, _ = fmt.Fprintf(out, "Canton LocalNet %q started.\n", state.Name)
	return ExitSuccess
}
