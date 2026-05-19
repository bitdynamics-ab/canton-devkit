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
}

// RunLogs proxies to `docker compose logs`. SIGINT is forwarded to the
// child process so Ctrl-C cleanly terminates `--follow` streams.
func RunLogs(ctx context.Context, out io.Writer, errw io.Writer, opts *LogsOptions) int {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	state, err := registry.Read(opts.Name)
	if err == registry.ErrNotFound {
		_, _ = fmt.Fprintf(errw, "No instance named %q is registered.\n", opts.Name)
		return 3
	}
	if err != nil {
		_, _ = fmt.Fprintf(errw, "Failed to read state: %s\n", err)
		return ExitRuntimeFailure
	}

	env, envFiles, envErr := composeContext(state)
	if envErr != nil {
		_, _ = fmt.Fprintf(errw, "Warning: could not reconstruct compose context: %s\n", envErr)
	}

	args := []string{"compose", "-p", state.ComposeProject}
	for _, f := range state.ComposeFiles {
		args = append(args, "-f", f)
	}
	for _, ef := range envFiles {
		args = append(args, "--env-file", ef)
	}
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

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = out
	cmd.Stderr = errw
	cmd.Dir = state.ProjectDir
	if env != nil {
		cmd.Env = env
	}

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ExitSuccess // graceful Ctrl-C
		}
		_, _ = fmt.Fprintf(errw, "docker compose logs: %s\n", err)
		return ExitRuntimeFailure
	}
	return ExitSuccess
}
