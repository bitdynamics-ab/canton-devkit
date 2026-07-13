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

// composePauser is the subset of *docker.ComposeRunner that RunPause /
// RunResume drive. Extracted for the test seam. *docker.ComposeRunner
// satisfies it.
type composePauser interface {
	Pause(ctx context.Context) error
	Unpause(ctx context.Context) error
}

// PauseOptions captures `localnet pause` / `localnet resume` flags.
type PauseOptions struct {
	Name string

	// NewRunner is a test-only seam. When nil, the real
	// *docker.ComposeRunner is built.
	NewRunner func(projectName string, composeFiles, envFiles, env []string, workDir string, logw io.Writer) composePauser
}

// RunPause freezes a running instance via `docker compose pause`
// (SIGSTOP): containers hold their in-memory state and ports but stop
// consuming CPU. Reverse with RunResume — no boot cost, no port
// re-capture (the apps never stopped). The natural "I'm stepping away,
// free my CPU but keep my ledger" control.
//
// Exit: ExitSuccess on pause, ExitUserError when the instance isn't
// registered or isn't running, ExitRuntimeFailure on a compose error.
func RunPause(ctx context.Context, out, errw io.Writer, opts *PauseOptions) int {
	return runPauseTransition(ctx, out, errw, opts, transitionPause)
}

// RunResume unfreezes a paused instance via `docker compose unpause`
// (SIGCONT). The inverse of RunPause.
func RunResume(ctx context.Context, out, errw io.Writer, opts *PauseOptions) int {
	return runPauseTransition(ctx, out, errw, opts, transitionResume)
}

type pauseTransition struct {
	verb       string          // "Pausing" / "Resuming"
	noun       string          // "pause" / "resume"
	done       string          // "paused" / "resumed"
	requireOld registry.Status // status the instance must be in
	newStatus  registry.Status // status to persist on success
	apply      func(ctx context.Context, r composePauser) error
}

var (
	transitionPause = pauseTransition{
		verb: "Pausing", noun: "pause", done: "paused",
		requireOld: registry.StatusRunning, newStatus: registry.StatusPaused,
		apply: func(ctx context.Context, r composePauser) error { return r.Pause(ctx) },
	}
	transitionResume = pauseTransition{
		verb: "Resuming", noun: "resume", done: "resumed",
		requireOld: registry.StatusPaused, newStatus: registry.StatusRunning,
		apply: func(ctx context.Context, r composePauser) error { return r.Unpause(ctx) },
	}
)

func runPauseTransition(ctx context.Context, out, errw io.Writer, opts *PauseOptions, t pauseTransition) int {
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
	if state.Status != t.requireOld {
		_, _ = fmt.Fprintf(errw,
			"Instance %q is %s, not %s — cannot %s it.\n",
			state.Name, state.Status, t.requireOld, t.noun)
		return ExitUserError
	}

	_, _ = fmt.Fprintf(out, "%s Canton LocalNet %q...\n", t.verb, state.Name)

	env, envFiles, ctxErr := composeContext(state)
	if ctxErr != nil {
		_, _ = fmt.Fprintf(errw, "Warning: could not reconstruct compose context: %s\n", ctxErr)
	}

	var runner composePauser
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
			// Every Splice service is profile-gated; replay the enabled
			// set or `pause`/`unpause` targets zero services.
			Profiles: composeProfiles(state),
		}
	}

	if err := t.apply(ctx, runner); err != nil {
		if ctx.Err() != nil {
			_, _ = fmt.Fprintln(errw, "Interrupted — retry once the host is idle")
			return ExitTimeout
		}
		_, _ = fmt.Fprintf(errw, "%s\n", err)
		return ExitRuntimeFailure
	}

	state.Status = t.newStatus
	if err := registry.Write(state); err != nil {
		_, _ = fmt.Fprintf(errw, "Warning: could not persist state: %s\n", err)
	}
	_, _ = fmt.Fprintf(out, "Canton LocalNet %q %s.\n", state.Name, t.done)
	return ExitSuccess
}
