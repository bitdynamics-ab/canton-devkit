package localnet

import (
	"context"
	"fmt"
	"io"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/containers"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// listProjectContainers is a test seam over containers.List. The
// unknown-service validation branch needs live project membership,
// which normally requires a running docker daemon; tests override
// this to script the known-service set.
var listProjectContainers = containers.List

// composeRestarter is the subset of *docker.ComposeRunner that
// RunRestart drives. Extracted for the test seam (mirrors
// DownOptions.NewRunner). *docker.ComposeRunner satisfies it.
type composeRestarter interface {
	Restart(ctx context.Context, services ...string) error
	WaitForHealthy(ctx context.Context) error
}

// RestartOptions captures `localnet restart` flags.
type RestartOptions struct {
	Name string
	// Services, when non-empty, restarts only those compose services
	// (e.g. "canton", "splice"). Empty restarts the whole instance.
	Services []string
	// SkipWait skips the post-restart readiness poll (useful for a
	// single non-critical service bounce).
	SkipWait bool

	// NewRunner is a test-only seam. When nil, RunRestart builds the
	// real *docker.ComposeRunner.
	NewRunner func(projectName string, composeFiles, envFiles, env []string, workDir string, logw io.Writer) composeRestarter
}

// RunRestart bounces a running instance (or specific services) in
// place via `docker compose restart`, then re-runs the readiness
// wait and re-captures Canton's ephemeral gRPC ports.
//
// Why re-capture ports: Splice's compose publishes the participant
// admin/ledger/json gRPC ports on EPHEMERAL host ports (no fixed
// host-side bind). Docker re-assigns those on every container
// restart, so the registry's cached participant_* ports would point
// at dead host ports after a restart — breaking the Web UI Explorer,
// DAR Manager, and `contracts`/`tx` CLI. The fixed UI ports
// (app_user_ui, etc.) are explicit binds and survive a restart, so
// only the Canton gRPC ports need re-capture here.
//
// Exit: ExitSuccess on a healthy restart, ExitUserError when the
// instance isn't registered, ExitTimeout on interruption /
// readiness timeout, ExitRuntimeFailure on a compose error.
func RunRestart(ctx context.Context, out io.Writer, errw io.Writer, opts *RestartOptions) int {
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

	// Validate requested services against the live compose project
	// (mirrors internal/cli/localnet/container.go's resolveContainerName):
	// a typo'd `--service cantn` gets a friendly "known services: [...]"
	// instead of a raw compose error. Best-effort: if the docker probe
	// fails (daemon hiccup), fall through and let compose surface the
	// error rather than blocking.
	if len(opts.Services) > 0 {
		if known, perr := listProjectContainers(ctx, state.ComposeProject); perr == nil && len(known) > 0 {
			valid := make(map[string]bool, len(known))
			for _, c := range known {
				if c.Service != "" {
					valid[c.Service] = true
				}
			}
			for _, svc := range opts.Services {
				if !valid[svc] {
					names := make([]string, 0, len(valid))
					for s := range valid {
						names = append(names, s)
					}
					sort.Strings(names)
					_, _ = fmt.Fprintf(errw,
						"unknown service %q for instance %q. Known services: %s\n",
						svc, state.Name, strings.Join(names, ", "))
					return ExitUserError
				}
			}
		}
	}

	target := "instance"
	if len(opts.Services) > 0 {
		target = fmt.Sprintf("service(s) %v", opts.Services)
	}
	_, _ = fmt.Fprintf(out, "Restarting %s for Canton LocalNet %q...\n", target, state.Name)

	env, envFiles, ctxErr := composeContext(state)
	if ctxErr != nil {
		_, _ = fmt.Fprintf(errw, "Warning: could not reconstruct compose context: %s\n", ctxErr)
	}

	var runner composeRestarter
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
			// set or `restart` targets zero services. See composeProfiles.
			Profiles: composeProfiles(state),
		}
	}

	if err := runner.Restart(ctx, opts.Services...); err != nil {
		if ctx.Err() != nil {
			_, _ = fmt.Fprintln(errw, "Interrupted during docker compose restart — retry once the host is idle")
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
			// NOTE: we intentionally do NOT re-capture Canton ports
			// on the readiness-timeout path. The containers aren't
			// healthy yet, so a CaptureCantonPorts now could read
			// half-bound or still-shifting ephemeral ports. The
			// background reconciler (internal/ui/handlers/reconciler.go)
			// re-captures once the instance reaches `running`, so the
			// registry self-heals without us persisting bad ports here.
			_, _ = fmt.Fprintf(errw, "Services did not become healthy after restart: %s\n", err)
			state.Status = registry.StatusPartial
			_ = registry.Write(state)
			return ExitTimeout
		}
	}

	// Re-capture Canton's ephemeral gRPC ports (see godoc). Best-
	// effort: an empty result (docker unavailable in tests) is a
	// no-op merge that leaves the cached ports untouched.
	for key, port := range CaptureCantonPorts(ctx, state.ComposeProject) {
		state.Ports[key] = port
	}
	state.Status = registry.StatusRunning
	if err := registry.Write(state); err != nil {
		_, _ = fmt.Fprintf(errw, "Warning: could not persist post-restart state: %s\n", err)
	}

	_, _ = fmt.Fprintf(out, "Canton LocalNet %q restarted.\n", state.Name)
	return ExitSuccess
}
