package localnet

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// LogsOptions captures `localnet logs` flags.
type LogsOptions struct {
	Name     string
	Services []string
	Follow   bool
	Tail     string // "100" or "all"; passed through to docker compose
	Since    string // duration string, optional

	// RunFn is a test seam for the docker invocation. Production callers
	// leave it nil so RunLogs uses exec.CommandContext.
	RunFn func(ctx context.Context, args []string, dir string, env []string, out io.Writer, errw io.Writer) error
}

// RunLogs proxies to `docker compose logs`. SIGINT is forwarded to the
// child process so Ctrl-C cleanly terminates `--follow` streams.
func RunLogs(ctx context.Context, out io.Writer, errw io.Writer, opts *LogsOptions) int {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	state, err := registry.Read(opts.Name)
	if err == registry.ErrNotFound {
		_, _ = fmt.Fprintf(errw, "No instance named %q is registered.\n", opts.Name)
		return ExitUserError
	}
	if err != nil {
		_, _ = fmt.Fprintf(errw, "Failed to read state: %s\n", err)
		return ExitRuntimeFailure
	}

	// `logs` only needs the already-created containers for this Compose
	// project. Do not replay -f/--env-file here: doing so rebuilds the
	// active Compose model and can exclude profile-gated services unless
	// every profile from `up` is replayed exactly.
	args := []string{"compose", "-p", state.ComposeProject}
	args = append(args, "logs")
	if opts.Follow {
		args = append(args, "--follow")
	}
	if opts.Tail != "" {
		args = append(args, "--tail", opts.Tail)
	}
	if opts.Since != "" {
		args = append(args, "--since", opts.Since)
	}
	args = append(args, opts.Services...)

	run := opts.RunFn
	if run == nil {
		run = runDockerLogs
	}
	if err := run(ctx, args, "", nil, out, errw); err != nil {
		if ctx.Err() != nil {
			return ExitSuccess // graceful Ctrl-C
		}
		_, _ = fmt.Fprintf(errw, "docker compose logs: %s\n", err)
		return ExitRuntimeFailure
	}
	return ExitSuccess
}

func runDockerLogs(ctx context.Context, args []string, dir string, env []string, out io.Writer, errw io.Writer) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = out
	cmd.Stderr = errw
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	return cmd.Run()
}
