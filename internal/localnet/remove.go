package localnet

import (
	"context"
	"fmt"
	"io"
	"os/signal"
	"sort"
	"syscall"

	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// RemoveOptions captures `localnet remove` flags. Cobra binds directly.
type RemoveOptions struct {
	// Name targets a single instance. The CLI layer rejects --name
	// together with --all as mutually exclusive; if both are set on
	// the struct directly (e.g. a programmatic caller), removeTargets
	// honours All and walks every instance.
	Name string
	// All removes every registered instance (walks the index).
	All bool
	// Force allows removing a RUNNING instance without asking — remove
	// tears it down (compose down --volumes) first.
	Force bool
	// ConfirmStop is asked, per instance, whether a RUNNING instance
	// may be torn down and removed. It is only consulted when Force is
	// false. Returning true proceeds exactly as Force would; false
	// leaves the instance untouched. A non-nil error means confirmation
	// could not be obtained (e.g. non-interactive stdin) and is
	// surfaced to the user.
	//
	// A nil ConfirmStop means the caller cannot prompt: a running
	// instance is then refused with a "run down first, or --force" hint.
	// The Web UI's DELETE handler relies on that refusal.
	ConfirmStop func(name string) (bool, error)
	// DryRun prints what WOULD be removed without touching anything.
	DryRun bool

	// NewRunner is a test-only seam mirroring DownOptions.NewRunner.
	// When nil, RunRemove constructs the real *docker.ComposeRunner.
	NewRunner func(projectName string, composeFiles, envFiles, env []string, workDir string, logw io.Writer) composeDowner
}

// RunRemove removes DevKit-managed state for one or all instances.
//
// Unlike `down` (which stops a running instance and is the normal
// lifecycle exit), `remove` is the recovery / housekeeping verb: it
// removes the registry entry, the per-instance data dir, and — for
// instances that still have docker resources — runs
// `docker compose down --volumes --remove-orphans` to reclaim
// lingering containers/volumes. It is the documented remedy for
// orphaned or corrupted instance state.
//
// Safety rules:
//   - A RUNNING instance is never torn down silently: with --force it
//     is, otherwise ConfirmStop must approve it. Callers that cannot
//     prompt (nil ConfirmStop) refuse it outright.
//   - --dry-run prints the plan and changes nothing.
//   - Orphaned entries (index row present, state.json gone) are
//     scrubbed idempotently.
//
// Exit code: ExitSuccess when every targeted instance was removed
// (or nothing matched). ExitUserError when a running instance was
// declined or refused. ExitRuntimeFailure on a teardown/delete
// error.
func RunRemove(ctx context.Context, out io.Writer, errw io.Writer, opts *RemoveOptions) int {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	targets, err := removeTargets(opts)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "%s\n", err)
		return ExitUserError
	}
	if len(targets) == 0 {
		if opts.All {
			_, _ = fmt.Fprintln(out, "No registered instances. Nothing to remove.")
		} else {
			_, _ = fmt.Fprintf(out, "No instance named %q is registered. Nothing to remove.\n", opts.Name)
		}
		return ExitSuccess
	}

	exit := ExitSuccess
	removed := 0
	for _, name := range targets {
		code := removeOne(ctx, out, errw, opts, name)
		switch code {
		case ExitSuccess:
			removed++
		case ExitUserError:
			// Refused (running, no --force). Keep going through the
			// rest of an --all sweep but remember the soft failure.
			if exit == ExitSuccess {
				exit = ExitUserError
			}
		default:
			// Hard failure trumps a soft refusal.
			exit = code
		}
	}

	if opts.DryRun {
		_, _ = fmt.Fprintf(out, "\nDry run — nothing was removed. %d instance(s) matched.\n", len(targets))
		return exit
	}
	_, _ = fmt.Fprintf(out, "\nRemoved %d of %d instance(s).\n", removed, len(targets))
	return exit
}

// removeTargets resolves the set of instance names to operate on.
func removeTargets(opts *RemoveOptions) ([]string, error) {
	if opts.All {
		idx, err := registry.ReadIndex()
		if err != nil {
			return nil, fmt.Errorf("read registry index: %w", err)
		}
		names := make([]string, 0, len(idx.Entries))
		for _, e := range idx.Entries {
			names = append(names, e.Name)
		}
		sort.Strings(names)
		return names, nil
	}
	if opts.Name == "" {
		return nil, fmt.Errorf("provide --name <instance> or --all")
	}
	if err := ValidateName(opts.Name); err != nil {
		return nil, err
	}
	return []string{opts.Name}, nil
}

// containerLister is the optional capability removeOne uses to confirm a
// project's containers are actually gone before it scrubs the registry.
// *docker.ComposeRunner implements it; removeOne type-asserts (rather
// than widening composeDowner) so down.go's runner and the existing test
// stubs don't have to implement it.
type containerLister interface {
	RemainingContainers(ctx context.Context) ([]string, error)
}

// removeOne removes a single instance under its per-instance lock.
func removeOne(ctx context.Context, out io.Writer, errw io.Writer, opts *RemoveOptions, name string) int {
	indexed, err := removeIndexHasEntry(name)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "%s: read registry index: %s\n", name, err)
		return ExitRuntimeFailure
	}

	if opts.DryRun {
		return dryRunRemoveOne(out, errw, opts, name, indexed)
	}

	if !indexed {
		_, _ = fmt.Fprintf(out, "No instance named %q is registered. Nothing to remove.\n", name)
		return ExitSuccess
	}

	release, err := registry.Lock(name)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "%s: %s\n", name, err)
		return ExitUserError
	}
	defer release()

	state, err := registry.Read(name)
	if err == registry.ErrNotFound {
		// Orphan: index row present but state.json gone (crashed up,
		// manual rm, etc.). Scrub the index idempotently.
		if delErr := registry.Delete(name); delErr != nil {
			_, _ = fmt.Fprintf(errw, "%s: scrub orphan entry: %s\n", name, delErr)
			return ExitRuntimeFailure
		}
		_, _ = fmt.Fprintf(out, "Scrubbed orphan registry entry %q.\n", name)
		return ExitSuccess
	}
	if err != nil {
		_, _ = fmt.Fprintf(errw, "%s: read state: %s\n", name, err)
		return ExitRuntimeFailure
	}

	running := state.Status == registry.StatusRunning
	if running && !opts.Force {
		if opts.ConfirmStop == nil {
			_, _ = fmt.Fprintf(errw, "%s\n", runningRefusedMessage(name))
			return ExitUserError
		}
		ok, cerr := opts.ConfirmStop(name)
		if cerr != nil {
			_, _ = fmt.Fprintf(errw, "%s: %s\n", name, cerr)
			return ExitUserError
		}
		if !ok {
			_, _ = fmt.Fprintf(out, "Left %q running — nothing removed.\n", name)
			return ExitUserError
		}
	}

	// Best-effort teardown of any lingering docker resources. Even a
	// `stopped` instance can leave volumes behind if it was stopped
	// without --volumes; remove's contract is "reclaim everything",
	// so we always attempt compose down --volumes here. Failures are
	// non-fatal for a non-running instance (the containers may
	// already be gone) but fatal-ish for a running --force teardown.
	env, envFiles, ctxErr := composeContext(state)
	if ctxErr != nil {
		_, _ = fmt.Fprintf(errw, "%s: Warning: could not reconstruct compose context: %s\n", name, ctxErr)
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
			// Replay the up-time --profile set so the teardown reaches
			// every profile-gated Splice service (see down.go).
			Profiles: composeProfiles(state),
		}
	}
	// removeVolumes=true: `remove` is the destructive verb — it reclaims
	// the named ledger volumes (unlike `down`, which preserves them).
	if derr := runner.Stop(ctx, true); derr != nil {
		if running {
			// We were asked to --force a running instance and the
			// teardown failed — do NOT scrub state, or we'd orphan
			// live containers. Surface and bail.
			_, _ = fmt.Fprintf(errw,
				"%s: docker compose down failed during --force remove: %s\n"+
					"Registry state preserved; retry once the host is healthy.\n",
				name, derr)
			return ExitRuntimeFailure
		}
		// Non-running instance: the down failure is usually "no such
		// project" because containers are already gone — in which case
		// scrubbing the registry is exactly remove's job. But a down that
		// errored while containers REMAIN must not let remove orphan them
		// by deleting their only registry record. Verify docker is
		// actually clear first; if containers linger, keep the entry and
		// fail so the user can retry once the host is healthy.
		if lister, ok := runner.(containerLister); ok {
			if remaining, cerr := lister.RemainingContainers(ctx); cerr == nil && len(remaining) > 0 {
				_, _ = fmt.Fprintf(errw,
					"%s: docker compose down failed and %d container(s) still exist for project %q — "+
						"registry entry preserved so they aren't orphaned. Retry once docker is healthy: %s\n",
					name, len(remaining), state.ComposeProject, derr)
				return ExitRuntimeFailure
			}
		}
		// Containers confirmed gone (or unverifiable) — proceed to scrub.
		_, _ = fmt.Fprintf(errw,
			"%s: docker compose down reported an error (continuing — "+
				"containers already gone): %s\n", name, derr)
	}

	if err := registry.Delete(name); err != nil {
		_, _ = fmt.Fprintf(errw, "%s: remove instance state: %s\n", name, err)
		return ExitRuntimeFailure
	}

	// Deregister from the shared observability stack and tear it
	// down if this was the last instance referencing it — atomically under
	// the shared-stack lock so a concurrent `up` can't race the teardown.
	_ = DeregisterInstanceAndTeardownIfIdle(ctx, name, io.Discard)

	_, _ = fmt.Fprintf(out, "Removed %q.\n", name)
	return ExitSuccess
}

func runningRefusedMessage(name string) string {
	return fmt.Sprintf(
		"%s is running — refusing to remove. Run `localnet down %s` first, "+
			"or pass --force to tear it down and remove in one step.",
		name, name)
}

func removeIndexHasEntry(name string) (bool, error) {
	idx, err := registry.ReadIndex()
	if err != nil {
		return false, err
	}
	for _, e := range idx.Entries {
		if e.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func dryRunRemoveOne(out io.Writer, errw io.Writer, opts *RemoveOptions, name string, indexed bool) int {
	if !indexed {
		_, _ = fmt.Fprintf(out, "No instance named %q is registered. Nothing to remove.\n", name)
		return ExitSuccess
	}

	state, err := registry.Read(name)
	if err == registry.ErrNotFound {
		_, _ = fmt.Fprintf(out, "would scrub orphan registry entry %q (state.json missing)\n", name)
		return ExitSuccess
	}
	if err != nil {
		_, _ = fmt.Fprintf(errw, "%s: read state: %s\n", name, err)
		return ExitRuntimeFailure
	}

	running := state.Status == registry.StatusRunning
	if running && !opts.Force && opts.ConfirmStop == nil {
		_, _ = fmt.Fprintf(errw, "%s\n", runningRefusedMessage(name))
		return ExitUserError
	}

	extra := ""
	switch {
	case running && opts.Force:
		extra = " (running — would be torn down first)"
	case running:
		extra = " (running — would ask to tear it down first)"
	}
	_, _ = fmt.Fprintf(out,
		"would remove %q%s: state.json + data dir %s + docker resources for project %s\n",
		name, extra, state.DataDir, state.ComposeProject)
	return ExitSuccess
}
