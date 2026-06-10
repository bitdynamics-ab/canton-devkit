package localnet

import (
	"context"
	"fmt"
	"io"
	"os/signal"
	"syscall"

	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// composeDowner is the subset of *docker.ComposeRunner that RunDown
// drives. Extracted as an interface so tests can swap in a stub that
// returns a controlled error (or success) without touching Docker.
// *docker.ComposeRunner satisfies it implicitly.
type composeDowner interface {
	Down(ctx context.Context) error
}

// DownOptions captures `localnet down` flags. Cobra binds directly.
type DownOptions struct {
	Name      string
	KeepCache bool // documented, currently informational (cache is shared across instances)

	// NewRunner is a test-only seam. When nil, RunDown constructs the
	// real *docker.ComposeRunner. Tests inject a stub to assert the
	// compose-failure / interruption decision table without docker.
	NewRunner func(projectName string, composeFiles, envFiles, env []string, workDir string, logw io.Writer) composeDowner
}

// RunDown tears down a named instance. Idempotent — running against an
// already-stopped instance is a no-op with exit 0.
//
// Steps:
//  1. Acquire the per-instance lock (prevents concurrent up/down).
//  2. Load state.json. If missing, exit 0 with a hint.
//  3. Run `docker compose down --volumes --remove-orphans` with the
//     compose files recorded in state (no --env-file needed).
//  4. Set status=stopped and persist the registry entry so that a
//     subsequent `localnet clean` can discover the instance.
func RunDown(ctx context.Context, out io.Writer, errw io.Writer, opts *DownOptions) int {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	release, err := registry.Lock(opts.Name)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "%s\n", err)
		return ExitUserError
	}
	defer release()

	state, err := registry.Read(opts.Name)
	if err == registry.ErrNotFound {
		// state.json is missing — but an orphan index entry may still
		// exist (e.g. from a crashed `up` that wrote the index but not
		// the state, or a manual rm of the data dir). Delete handles
		// both: noop on missing dir, scrubs the index entry.
		if delErr := registry.Delete(opts.Name); delErr != nil {
			_, _ = fmt.Fprintf(errw, "Warning: could not scrub orphan registry entry: %s\n", delErr)
		}
		_, _ = fmt.Fprintf(out, "No instance named %q is registered. Nothing to do.\n", opts.Name)
		return ExitSuccess
	}
	if err != nil {
		_, _ = fmt.Fprintf(errw, "Failed to read state: %s\n", err)
		return ExitRuntimeFailure
	}

	_, _ = fmt.Fprintf(out, "Stopping Canton LocalNet %q (Splice %s)...\n",
		state.Name, state.SpliceVersion)

	// Persist the transitional `stopping` state BEFORE invoking compose.
	// If we crashed or were SIGKILLed mid-teardown without this,
	// `localnet status` would keep reporting `running` even though
	// containers are partially down. Failure to write the transitional
	// state is non-fatal.
	state.Status = registry.StatusStopping
	if err := registry.Write(state); err != nil {
		_, _ = fmt.Fprintf(errw, "Warning: could not persist stopping state: %s\n", err)
	}

	env, envFiles, err := composeContext(state)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "Warning: could not reconstruct compose context: %s\n", err)
		// Fall through — docker compose down often still works against
		// the project label even without all substitutions, just noisy.
	}

	var runner composeDowner
	if opts.NewRunner != nil {
		runner = opts.NewRunner(state.ComposeProject, state.ComposeFiles, envFiles, env, state.ProjectDir, out)
	} else {
		runner = &docker.ComposeRunner{
			ProjectName:  state.ComposeProject,
			ComposeFiles: state.ComposeFiles,
			EnvFiles:     envFiles,
			Env:          env,
			WorkDir:      state.ProjectDir,
			LogWriter:    out,
		}
	}
	if err := runner.Down(ctx); err != nil {
		// A compose-down failure must NOT silently fall through to
		// registry.Delete — doing so would scrub the retry metadata
		// while containers / volumes may still be running. Preserve
		// state as Failed, leave the data dir + registry entry intact
		// so the user can retry, and exit non-zero so scripts see the
		// failure.
		//
		// Interruption (Ctrl-C / SIGTERM) is a separate cell:
		// ExitTimeout rather than ExitRuntimeFailure, since the
		// underlying compose call may still be in flight on the host.
		if ctx.Err() != nil {
			_, _ = fmt.Fprintln(errw, "Interrupted during docker compose down — retry once the host is idle")
			state.Status = registry.StatusPartial
			_ = registry.Write(state)
			return ExitTimeout
		}
		_, _ = fmt.Fprintf(errw,
			"docker compose down failed: %s\nRegistry state preserved at %s. "+
				"Re-run `localnet down --name %s` once the host is healthy.\n",
			err, state.DataDir, state.Name)
		state.Status = registry.StatusFailed
		if werr := registry.Write(state); werr != nil {
			_, _ = fmt.Fprintf(errw, "Warning: could not persist failed state: %s\n", werr)
		}
		return ExitRuntimeFailure
	}

	state.Status = registry.StatusStopped
	if err := registry.Write(state); err != nil {
		_, _ = fmt.Fprintf(errw, "Warning: state update failed: %s\n", err)
	}

	_, _ = fmt.Fprintf(out, "Canton LocalNet %q stopped.\n", state.Name)
	return ExitSuccess
}
