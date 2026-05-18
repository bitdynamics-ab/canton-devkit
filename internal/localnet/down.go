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

// DownOptions captures `localnet down` flags. Cobra binds directly.
type DownOptions struct {
	Name      string
	KeepData  bool
	KeepCache bool // documented, currently informational (cache is shared across instances)
}

// RunDown tears down a named instance. Idempotent — running against an
// already-stopped instance is a no-op with exit 0.
//
// Steps:
//  1. Acquire the per-instance lock (prevents concurrent up/down).
//  2. Load state.json. If missing, exit 0 with a hint.
//  3. Run `docker compose down --volumes --remove-orphans` with the
//     compose files recorded in state (no --env-file needed).
//  4. Remove the per-instance data dir (unless --keep-data).
//  5. Remove the entry from the registry index.
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

	runner := &docker.ComposeRunner{
		ProjectName:  state.ComposeProject,
		ComposeFiles: state.ComposeFiles,
		WorkDir:      state.ProjectDir,
		LogWriter:    out,
	}
	if err := runner.Down(ctx); err != nil {
		// Compose down failure on an already-cleaned project is
		// recoverable — fall through to data-dir cleanup. Surface the
		// error for visibility but don't fail the command.
		_, _ = fmt.Fprintf(errw, "Warning: docker compose down: %s\n", err)
	}

	if !opts.KeepData {
		if err := registry.Delete(state.Name); err != nil {
			_, _ = fmt.Fprintf(errw, "Warning: could not remove instance dir: %s\n", err)
			return ExitRuntimeFailure
		}
	} else {
		state.Status = registry.StatusStopped
		if err := registry.Write(state); err != nil {
			_, _ = fmt.Fprintf(errw, "Warning: state update failed: %s\n", err)
		}
		_, _ = fmt.Fprintf(out, "Kept instance data at %s\n", state.DataDir)
	}

	_, _ = fmt.Fprintf(out, "Canton LocalNet %q stopped.\n", state.Name)
	return ExitSuccess
}
